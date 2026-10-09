package api

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Adnan-Dogar/telivoz-gateway/internal/auth"
	"github.com/Adnan-Dogar/telivoz-gateway/internal/engine"
	"github.com/Adnan-Dogar/telivoz-gateway/internal/gsm"
	"github.com/Adnan-Dogar/telivoz-gateway/internal/routing"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ---- messages -------------------------------------------------------------------------------------------

const messageCols = `m.id, m.created_at, m.client_id, (SELECT name FROM clients c WHERE c.id = m.client_id) AS client_name,
	m.account_id, m.direction, m.source, m.destination, m.body, m.parts, m.country_iso, m.network_id,
	(SELECT name FROM networks n WHERE n.id = m.network_id) AS network_name, m.route_id, m.connection_id,
	(SELECT name FROM connections c WHERE c.id = m.connection_id) AS connection_name, m.status, m.attempts, m.price, m.cost,
	m.client_ref, m.error, m.dlr_status, m.dlr_error, m.sent_at, m.dlr_at, m.dlr_sent_at,
	EXTRACT(EPOCH FROM (m.dlr_at - m.created_at)) * 1000 AS dlr_ms`

func (s *Server) messageScope(r *http.Request, args []any) (string, []any) {
	sc := s.scopeFor(r.Context(), principal(r))
	if sc.allClients {
		return "true", args
	}
	args = append(args, sc.clientIDs)
	return fmt.Sprintf("m.client_id = ANY($%d)", len(args)), args
}

// listMessages searches messages. Filters: q (number, sender, id or text), status, client_id, connection_id,
// from, to. Results are newest first; pass before=<created_at of the last row> to page.
func (s *Server) listMessages(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	from, to := period(r)
	if q.Get("from") == "" {
		from = to.Add(-7 * 24 * time.Hour)
	}
	args := []any{from, to}
	cond := "m.created_at >= $1 AND m.created_at < $2"
	if before := queryTime(r, "before", time.Time{}); !before.IsZero() {
		args = append(args, before)
		cond += fmt.Sprintf(" AND m.created_at < $%d", len(args))
	}
	var sc string
	sc, args = s.messageScope(r, args)
	cond += " AND " + sc
	if term := strings.TrimSpace(q.Get("q")); term != "" {
		if id, err := uuid.Parse(term); err == nil {
			args = append(args, id)
			cond += fmt.Sprintf(" AND m.id = $%d", len(args))
		} else if n := routing.NormalizeNumber(term); len(n) >= 5 && len(n) == len(strings.TrimLeft(strings.TrimPrefix(term, "+"), " ")) {
			args = append(args, n)
			cond += fmt.Sprintf(" AND m.destination = $%d", len(args))
		} else {
			args = append(args, "%"+term+"%")
			cond += fmt.Sprintf(" AND (m.source ILIKE $%[1]d OR m.body ILIKE $%[1]d OR m.client_ref ILIKE $%[1]d)", len(args))
		}
	}
	for _, f := range []string{"status", "client_id", "connection_id", "direction"} {
		if v := q.Get(f); v != "" {
			args = append(args, v)
			cond += fmt.Sprintf(" AND m.%s::text = $%d", f, len(args))
		}
	}
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	var raw []byte
	err := s.db.QueryRow(r.Context(), fmt.Sprintf(`SELECT COALESCE(jsonb_agg(to_jsonb(x) ORDER BY x.created_at DESC), '[]')
		FROM (SELECT %s FROM messages m WHERE %s ORDER BY m.created_at DESC LIMIT %d) x`, messageCols, cond, limit), args...).Scan(&raw)
	if err != nil {
		s.dbError(w, err)
		return
	}
	writeRaw(w, http.StatusOK, raw)
}

func (s *Server) getMessage(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid", "invalid message id")
		return
	}
	args := []any{id}
	var sc string
	sc, args = s.messageScope(r, args)
	var raw []byte
	err = s.db.QueryRow(r.Context(), fmt.Sprintf(`SELECT to_jsonb(x) || jsonb_build_object(
			'ledger', (SELECT COALESCE(jsonb_agg(to_jsonb(l) ORDER BY l.id), '[]') FROM ledger l WHERE l.message_id = x.id),
			'route_name', (SELECT name FROM routes r WHERE r.id = x.route_id))
		FROM (SELECT %s FROM messages m WHERE m.id = $1 AND %s) x`, messageCols, sc), args...).Scan(&raw)
	if err != nil {
		s.dbError(w, err)
		return
	}
	writeRaw(w, http.StatusOK, raw)
}

// accountFor loads an HTTP account the user may send with.
func (s *Server) accountFor(r *http.Request, accountID int64) (engine.Account, error) {
	var a engine.Account
	err := s.db.QueryRow(r.Context(), `SELECT a.id, a.client_id, a.kind, a.username, a.tps, a.max_binds FROM accounts a
		JOIN clients c ON c.id = a.client_id WHERE a.id = $1 AND a.status = 'active' AND c.status = 'active'`, accountID).
		Scan(&a.ID, &a.ClientID, &a.Kind, &a.Username, &a.TPS, &a.MaxBinds)
	if err != nil {
		return a, errors.New("account not found or disabled")
	}
	if !s.scopeFor(r.Context(), principal(r)).canClient(a.ClientID) {
		return a, errors.New("account not in your scope")
	}
	return a, nil
}

// portalSend sends one message from the portal through one of the client's accounts.
func (s *Server) portalSend(w http.ResponseWriter, r *http.Request) {
	var in struct {
		AccountID int64  `json:"account_id"`
		From      string `json:"from"`
		To        string `json:"to"`
		Text      string `json:"text"`
	}
	if err := readJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid", "invalid request")
		return
	}
	acc, err := s.accountFor(r, in.AccountID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid", err.Error())
		return
	}
	res, err := s.eng.Submit(r.Context(), engine.SubmitRequest{Account: acc, Source: in.From, Destination: in.To, Text: in.Text, WantsDLR: true})
	if err != nil {
		writeSubmitError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": res.MessageID, "parts": res.Parts, "price": res.Price.String()})
}

func writeSubmitError(w http.ResponseWriter, err error) {
	var se *engine.SubmitError
	if errors.As(err, &se) {
		status := http.StatusUnprocessableEntity
		switch se.Code {
		case "throttled":
			status = http.StatusTooManyRequests
		case "insufficient_balance":
			status = http.StatusPaymentRequired
		case "internal_error":
			status = http.StatusInternalServerError
		}
		writeError(w, status, se.Code, se.Message)
		return
	}
	writeError(w, http.StatusInternalServerError, "internal_error", "internal error")
}

// ---- balances -------------------------------------------------------------------------------------------

func (s *Server) topup(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	if !(p.IsAdmin() || p.Role == "finance" || p.Role == "manager") {
		writeError(w, http.StatusForbidden, "forbidden", "not allowed")
		return
	}
	id, ok := idParam(r)
	if !ok || !s.scopeFor(r.Context(), p).canClient(id) {
		writeError(w, http.StatusNotFound, "not_found", "client not found")
		return
	}
	var in struct {
		Amount string `json:"amount"`
		Kind   string `json:"kind"`
		Note   string `json:"note"`
	}
	if err := readJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid", "invalid request")
		return
	}
	amount, err := routing.ParseMicros(in.Amount)
	if err != nil || amount == 0 {
		writeError(w, http.StatusBadRequest, "invalid", "amount must be a non-zero number")
		return
	}
	kind := "topup"
	if in.Kind == "adjustment" || amount < 0 {
		kind = "adjustment"
	}
	var balance string
	err = pgx.BeginFunc(r.Context(), s.db, func(tx pgx.Tx) error {
		if err := tx.QueryRow(r.Context(), `INSERT INTO balances (client_id, balance) VALUES ($1, $2::numeric)
			ON CONFLICT (client_id) DO UPDATE SET balance = balances.balance + EXCLUDED.balance, updated_at = now()
			RETURNING balance::text`, id, amount.String()).Scan(&balance); err != nil {
			return err
		}
		_, err := tx.Exec(r.Context(), `INSERT INTO ledger (client_id, kind, amount, balance_after, note, created_by)
			VALUES ($1, $2, $3::numeric, $4::numeric, $5, $6)`, id, kind, amount.String(), balance, in.Note, p.UserID)
		return err
	})
	if err != nil {
		s.dbError(w, err)
		return
	}
	s.audit(r, kind, "clients", strconv.FormatInt(id, 10), map[string]string{"amount": amount.String(), "note": in.Note})
	writeJSON(w, http.StatusOK, map[string]string{"balance": balance})
}

func (s *Server) clientLedger(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(r)
	if !ok || !s.scopeFor(r.Context(), principal(r)).canClient(id) {
		writeError(w, http.StatusNotFound, "not_found", "client not found")
		return
	}
	var raw []byte
	err := s.db.QueryRow(r.Context(), `SELECT COALESCE(jsonb_agg(to_jsonb(x) ORDER BY x.id DESC), '[]') FROM (
		SELECT l.*, (SELECT name FROM users u WHERE u.id = l.created_by) AS created_by_name FROM ledger l
		WHERE l.client_id = $1 AND (l.kind <> 'charge' OR $2) ORDER BY l.id DESC LIMIT 300) x`,
		id, r.URL.Query().Get("charges") == "1").Scan(&raw)
	if err != nil {
		s.dbError(w, err)
		return
	}
	writeRaw(w, http.StatusOK, raw)
}

// ---- API keys -------------------------------------------------------------------------------------------

func (s *Server) accountInScope(r *http.Request, id int64) bool {
	var clientID int64
	if err := s.db.QueryRow(r.Context(), `SELECT client_id FROM accounts WHERE id = $1 AND kind = 'http'`, id).Scan(&clientID); err != nil {
		return false
	}
	return s.scopeFor(r.Context(), principal(r)).canClient(clientID)
}

func (s *Server) createAPIKey(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(r)
	if !ok || !s.accountInScope(r, id) {
		writeError(w, http.StatusNotFound, "not_found", "HTTP account not found")
		return
	}
	var in struct {
		Name string `json:"name"`
	}
	_ = readJSON(r, &in)
	key, prefix, hash := auth.NewAPIKey()
	var keyID int64
	if err := s.db.QueryRow(r.Context(), `INSERT INTO api_keys (account_id, name, prefix, key_hash) VALUES ($1, $2, $3, $4) RETURNING id`,
		id, in.Name, prefix, hash).Scan(&keyID); err != nil {
		s.dbError(w, err)
		return
	}
	s.audit(r, "create", "api_keys", strconv.FormatInt(keyID, 10), map[string]string{"prefix": prefix})
	writeJSON(w, http.StatusCreated, map[string]any{"id": keyID, "key": key, "prefix": prefix,
		"note": "Copy this key now. It is shown only once."})
}

func (s *Server) listAPIKeys(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(r)
	if !ok || !s.accountInScope(r, id) {
		writeError(w, http.StatusNotFound, "not_found", "HTTP account not found")
		return
	}
	var raw []byte
	err := s.db.QueryRow(r.Context(), `SELECT COALESCE(jsonb_agg(jsonb_build_object('id', id, 'name', name, 'prefix', prefix,
		'created_at', created_at, 'last_used_at', last_used_at, 'revoked_at', revoked_at) ORDER BY id DESC), '[]')
		FROM api_keys WHERE account_id = $1`, id).Scan(&raw)
	if err != nil {
		s.dbError(w, err)
		return
	}
	writeRaw(w, http.StatusOK, raw)
}

func (s *Server) revokeAPIKey(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(r)
	var accountID int64
	if ok {
		_ = s.db.QueryRow(r.Context(), `SELECT account_id FROM api_keys WHERE id = $1`, id).Scan(&accountID)
	}
	if !ok || !s.accountInScope(r, accountID) {
		writeError(w, http.StatusNotFound, "not_found", "key not found")
		return
	}
	if _, err := s.db.Exec(r.Context(), `UPDATE api_keys SET revoked_at = now() WHERE id = $1`, id); err != nil {
		s.dbError(w, err)
		return
	}
	s.audit(r, "revoke", "api_keys", strconv.FormatInt(id, 10), nil)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// ---- operations -----------------------------------------------------------------------------------------

func (s *Server) restartConnection(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	id, ok := idParam(r)
	if !ok || !(p.CanManage() || p.Role == "noc") {
		writeError(w, http.StatusForbidden, "forbidden", "not allowed")
		return
	}
	if err := s.eng.RestartConnection(r.Context(), id); err != nil {
		s.dbError(w, err)
		return
	}
	s.audit(r, "restart", "connections", strconv.FormatInt(id, 10), nil)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// testRoute shows how a message would be priced and routed, without sending it.
func (s *Server) testRoute(w http.ResponseWriter, r *http.Request) {
	if principal(r).IsClient() {
		writeError(w, http.StatusForbidden, "forbidden", "not allowed")
		return
	}
	var in struct {
		ClientID    int64  `json:"client_id"`
		AccountID   int64  `json:"account_id"`
		Sender      string `json:"sender"`
		Destination string `json:"destination"`
		Text        string `json:"text"`
	}
	if err := readJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid", "invalid request")
		return
	}
	snap := s.eng.Snapshot()
	d := snap.Lookup(in.Destination)
	out := map[string]any{"number": d.Number, "country_iso": d.CountryISO, "network_id": d.NetworkID}
	if n, ok := snap.Network(d.NetworkID); ok {
		out["network"] = fmt.Sprintf("%s (%s-%s)", n.Name, n.MCC, n.MNC)
	}
	content, applied, err := snap.ApplyContent(in.ClientID, d.CountryISO, routing.Content{Sender: in.Sender, Text: in.Text})
	out["content"] = map[string]any{"sender": content.Sender, "text": content.Text, "rules": applied, "blocked": err != nil,
		"parts": gsm.Parts(content.Text)}
	price, ok := snap.ClientPrice(in.ClientID, d)
	if !ok {
		out["error"] = "no client rate for this destination"
		writeJSON(w, http.StatusOK, out)
		return
	}
	out["price"] = price.String()
	plan, err := snap.Select(routing.Request{ClientID: in.ClientID, AccountID: in.AccountID, Sender: content.Sender, Dest: d, Price: price})
	if err != nil {
		out["error"] = err.Error()
		writeJSON(w, http.StatusOK, out)
		return
	}
	var conns []map[string]any
	for _, id := range plan.Connections {
		c, _ := snap.Connection(id)
		cost, known := snap.VendorCost(id, d)
		item := map[string]any{"id": id, "name": c.Name}
		if known {
			item["cost"] = cost.String()
			item["margin"] = (price - cost).String()
		}
		conns = append(conns, item)
	}
	out["route"] = map[string]any{"id": plan.RouteID, "name": plan.RouteName, "connections": conns}
	writeJSON(w, http.StatusOK, out)
}

// importRates loads a CSV of rates for one client or one connection.
// Columns: country_iso, mcc, mnc, price[, effective_from]. Empty mcc/mnc means the whole country.
func (s *Server) importRates(w http.ResponseWriter, r *http.Request) {
	if !principal(r).CanManage() {
		writeError(w, http.StatusForbidden, "forbidden", "not allowed")
		return
	}
	q := r.URL.Query()
	target := q.Get("target") // client or connection
	ownerID, _ := strconv.ParseInt(q.Get("id"), 10, 64)
	table, ownerCol := "client_rates", "client_id"
	if target == "connection" {
		table, ownerCol = "vendor_rates", "connection_id"
	} else if target != "client" {
		writeError(w, http.StatusBadRequest, "invalid", "target must be client or connection")
		return
	}
	if target == "client" && !s.scopeFor(r.Context(), principal(r)).canClient(ownerID) {
		writeError(w, http.StatusForbidden, "forbidden", "client not in your scope")
		return
	}
	cr := csv.NewReader(http.MaxBytesReader(w, r.Body, 20<<20))
	cr.FieldsPerRecord = -1
	cr.TrimLeadingSpace = true
	imported, skipped := 0, []string{}
	err := pgx.BeginFunc(r.Context(), s.db, func(tx pgx.Tx) error {
		line := 0
		for {
			rec, err := cr.Read()
			if errors.Is(err, io.EOF) {
				return nil
			}
			line++
			if err != nil {
				return fmt.Errorf("line %d: %w", line, err)
			}
			if len(rec) < 4 || strings.EqualFold(strings.TrimSpace(rec[0]), "country_iso") {
				continue
			}
			iso := strings.ToUpper(strings.TrimSpace(rec[0]))
			mcc, mnc := strings.TrimSpace(rec[1]), strings.TrimSpace(rec[2])
			price, perr := routing.ParseMicros(rec[3])
			if len(iso) != 2 || perr != nil {
				skipped = append(skipped, fmt.Sprintf("line %d: invalid country or price", line))
				continue
			}
			eff := time.Now()
			if len(rec) > 4 && strings.TrimSpace(rec[4]) != "" {
				if t, err := time.Parse("2006-01-02", strings.TrimSpace(rec[4])); err == nil {
					eff = t
				}
			}
			var networkID *int64
			if mcc != "" && mnc != "" {
				var nid int64
				if err := tx.QueryRow(r.Context(), `SELECT id FROM networks WHERE mcc = $1 AND mnc = $2`, mcc, mnc).Scan(&nid); err != nil {
					skipped = append(skipped, fmt.Sprintf("line %d: unknown network %s-%s", line, mcc, mnc))
					continue
				}
				networkID = &nid
			}
			if _, err := tx.Exec(r.Context(), fmt.Sprintf(`INSERT INTO %s (%s, country_iso, network_id, price, effective_from)
				VALUES ($1, $2, $3, $4::numeric, $5)
				ON CONFLICT (%s, country_iso, COALESCE(network_id, 0), effective_from) DO UPDATE SET price = EXCLUDED.price`,
				table, ownerCol, ownerCol), ownerID, iso, networkID, price.String(), eff); err != nil {
				return fmt.Errorf("line %d: %w", line, err)
			}
			imported++
		}
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid", err.Error())
		return
	}
	s.audit(r, "import_rates", table, strconv.FormatInt(ownerID, 10), map[string]int{"imported": imported})
	s.reload(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"imported": imported, "skipped": skipped})
}

// lookups returns small lists for form dropdowns.
func (s *Server) lookups(w http.ResponseWriter, r *http.Request) {
	sc := s.scopeFor(r.Context(), principal(r))
	clientCond, vendorCond := "true", "true"
	args := []any{}
	if !sc.allClients {
		args = append(args, sc.clientIDs)
		clientCond = "id = ANY($1)"
	}
	if !sc.allVendors {
		args = append(args, sc.vendorIDs)
		vendorCond = fmt.Sprintf("vendor_id = ANY($%d)", len(args))
	}
	var raw []byte
	err := s.db.QueryRow(r.Context(), fmt.Sprintf(`SELECT jsonb_build_object(
		'countries', (SELECT COALESCE(jsonb_agg(jsonb_build_object('iso', iso, 'name', name, 'dial_code', dial_code) ORDER BY name), '[]') FROM countries),
		'clients', (SELECT COALESCE(jsonb_agg(jsonb_build_object('id', id, 'name', name) ORDER BY name), '[]') FROM clients WHERE %s),
		'accounts', (SELECT COALESCE(jsonb_agg(jsonb_build_object('id', id, 'username', username, 'kind', kind, 'client_id', client_id) ORDER BY username), '[]') FROM accounts WHERE client_id IN (SELECT id FROM clients WHERE %s)),
		'connections', (SELECT COALESCE(jsonb_agg(jsonb_build_object('id', id, 'name', name, 'vendor_id', vendor_id) ORDER BY name), '[]') FROM connections WHERE %s),
		'vendors', (SELECT COALESCE(jsonb_agg(jsonb_build_object('id', id, 'name', name) ORDER BY name), '[]') FROM vendors WHERE %s),
		'users', (SELECT COALESCE(jsonb_agg(jsonb_build_object('id', id, 'name', name, 'role', role) ORDER BY name), '[]') FROM users WHERE role <> 'client' AND %s))`,
		clientCond, clientCond, vendorCond, strings.Replace(vendorCond, "vendor_id", "id", 1), map[bool]string{true: "false", false: "true"}[principal(r).IsClient()]), args...).Scan(&raw)
	if err != nil {
		s.dbError(w, err)
		return
	}
	writeRaw(w, http.StatusOK, raw)
}

func (s *Server) auditLog(w http.ResponseWriter, r *http.Request) {
	if !principal(r).IsAdmin() {
		writeError(w, http.StatusForbidden, "forbidden", "not allowed")
		return
	}
	var raw []byte
	err := s.db.QueryRow(r.Context(), `SELECT COALESCE(jsonb_agg(to_jsonb(x) ORDER BY x.id DESC), '[]') FROM (
		SELECT a.*, u.name AS user_name, u.email AS user_email FROM audit_log a LEFT JOIN users u ON u.id = a.user_id
		ORDER BY a.id DESC LIMIT 500) x`).Scan(&raw)
	if err != nil {
		s.dbError(w, err)
		return
	}
	writeRaw(w, http.StatusOK, raw)
}

var _ = context.Background
