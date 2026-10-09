package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Adnan-Dogar/telivoz-gateway/internal/auth"
	"github.com/jackc/pgx/v5"
)

// field kinds
const (
	kText     = "text"
	kInt      = "int"
	kNum      = "num"  // NUMERIC, passed as a decimal string
	kBool     = "bool"
	kRef      = "ref"  // nullable BIGINT foreign key (0 or null = NULL)
	kISO      = "iso"  // nullable 2-letter country code, upper-cased
	kTextArr  = "textarr"
	kTime     = "time"
	kPassword = "password" // stored as bcrypt hash in password_hash
	kSecret   = "secret"   // stored encrypted in password_enc
)

type field struct {
	name     string
	kind     string
	required bool
}

type resource struct {
	path       string
	table      string
	fields     []field
	search     []string // columns searched by ?q=
	hidden     []string // never returned
	selectFrom string   // FROM clause with alias t; default "<table> t"
	extraCols  string   // extra select columns
	canRead    func(p *auth.Principal) bool
	canWrite   func(p *auth.Principal) bool
	scopeSQL   func(sc scope) (string, []any) // condition on alias t, using $1.. placeholders
	reloads    bool
	afterWrite func(ctx context.Context, s *Server, id int64, in map[string]any, created bool) error
}

func staff(p *auth.Principal) bool    { return !p.IsClient() }
func managers(p *auth.Principal) bool { return p.CanManage() }
func anyone(p *auth.Principal) bool   { return true }
func adminOnly(p *auth.Principal) bool { return p.IsAdmin() }

func clientScope(col string) func(sc scope) (string, []any) {
	return func(sc scope) (string, []any) {
		if sc.allClients {
			return "true", nil
		}
		return "t." + col + " = ANY($1)", []any{sc.clientIDs}
	}
}

func vendorScope(col string) func(sc scope) (string, []any) {
	return func(sc scope) (string, []any) {
		if sc.allVendors {
			return "true", nil
		}
		return "t." + col + " = ANY($1)", []any{sc.vendorIDs}
	}
}

var resources = []*resource{
	{
		path: "clients", table: "clients",
		fields: []field{{"name", kText, true}, {"email", kText, false}, {"phone", kText, false}, {"country_iso", kISO, false},
			{"currency", kText, false}, {"billing_type", kText, false}, {"credit_limit", kNum, false}, {"owner_id", kRef, false},
			{"parent_id", kRef, false}, {"dlr_webhook_url", kText, false}, {"dlr_format", kText, false}, {"status", kText, false}},
		search:    []string{"name", "email"},
		extraCols: `, (SELECT balance FROM balances b WHERE b.client_id = t.id) AS balance, (SELECT name FROM users u WHERE u.id = t.owner_id) AS owner_name`,
		canRead:   anyone, canWrite: managers, scopeSQL: clientScope("id"),
		afterWrite: func(ctx context.Context, s *Server, id int64, _ map[string]any, created bool) error {
			if created {
				_, err := s.db.Exec(ctx, `INSERT INTO balances (client_id) VALUES ($1) ON CONFLICT DO NOTHING`, id)
				return err
			}
			return nil
		},
	},
	{
		path: "accounts", table: "accounts",
		fields: []field{{"client_id", kRef, true}, {"kind", kText, true}, {"username", kText, true}, {"password", kPassword, false},
			{"allowed_ips", kTextArr, false}, {"tps", kInt, false}, {"max_binds", kInt, false}, {"status", kText, false}},
		search: []string{"username"}, hidden: []string{"password_hash"},
		extraCols: `, (SELECT name FROM clients c WHERE c.id = t.client_id) AS client_name`,
		canRead:   anyone, canWrite: managers, scopeSQL: clientScope("client_id"),
	},
	{
		path: "vendors", table: "vendors",
		fields: []field{{"name", kText, true}, {"email", kText, false}, {"currency", kText, false}, {"owner_id", kRef, false}, {"status", kText, false}},
		search: []string{"name"}, canRead: staff, canWrite: managers, scopeSQL: vendorScope("id"), reloads: true,
	},
	{
		path: "connections", table: "connections",
		fields: []field{{"vendor_id", kRef, true}, {"name", kText, true}, {"host", kText, true}, {"port", kInt, true},
			{"system_id", kText, true}, {"password", kSecret, false}, {"system_type", kText, false}, {"bind_mode", kText, false},
			{"binds", kInt, false}, {"tps", kInt, false}, {"window_size", kInt, false}, {"source_ton", kInt, false},
			{"source_npi", kInt, false}, {"dest_ton", kInt, false}, {"dest_npi", kInt, false}, {"dlr_id_format", kText, false},
			{"max_attempts", kInt, false}, {"status", kText, false}},
		search: []string{"name", "host", "system_id"}, hidden: []string{"password_enc"},
		extraCols: `, (SELECT name FROM vendors v WHERE v.id = t.vendor_id) AS vendor_name`,
		canRead:   staff, canWrite: managers, scopeSQL: vendorScope("vendor_id"), reloads: true,
	},
	{
		path: "client-rates", table: "client_rates",
		fields: []field{{"client_id", kRef, true}, {"country_iso", kISO, true}, {"network_id", kRef, false}, {"price", kNum, true},
			{"effective_from", kTime, false}},
		search:    []string{"country_iso"},
		extraCols: `, (SELECT name FROM networks n WHERE n.id = t.network_id) AS network_name, (SELECT name FROM clients c WHERE c.id = t.client_id) AS client_name`,
		canRead:   anyone, canWrite: managers, scopeSQL: clientScope("client_id"), reloads: true,
	},
	{
		path: "vendor-rates", table: "vendor_rates",
		fields: []field{{"connection_id", kRef, true}, {"country_iso", kISO, true}, {"network_id", kRef, false}, {"price", kNum, true},
			{"effective_from", kTime, false}},
		search:    []string{"country_iso"},
		extraCols: `, (SELECT name FROM networks n WHERE n.id = t.network_id) AS network_name, (SELECT name FROM connections c WHERE c.id = t.connection_id) AS connection_name`,
		canRead:   staff, canWrite: managers, reloads: true,
		scopeSQL: func(sc scope) (string, []any) {
			if sc.allVendors {
				return "true", nil
			}
			return "t.connection_id IN (SELECT id FROM connections WHERE vendor_id = ANY($1))", []any{sc.vendorIDs}
		},
	},
	{
		path: "routes", table: "routes",
		fields: []field{{"name", kText, true}, {"priority", kInt, false}, {"client_id", kRef, false}, {"account_id", kRef, false},
			{"country_iso", kISO, false}, {"network_id", kRef, false}, {"sender_match", kText, false}, {"sender_pattern", kText, false},
			{"policy", kText, false}, {"allow_loss", kBool, false}, {"status", kText, false}},
		search: []string{"name", "sender_pattern"},
		extraCols: `, (SELECT COALESCE(jsonb_agg(jsonb_build_object('connection_id', rt.connection_id, 'position', rt.position,
			'weight', rt.weight, 'name', c.name) ORDER BY rt.position), '[]') FROM route_targets rt JOIN connections c ON c.id = rt.connection_id
			WHERE rt.route_id = t.id) AS targets,
			(SELECT name FROM clients c WHERE c.id = t.client_id) AS client_name,
			(SELECT username FROM accounts a WHERE a.id = t.account_id) AS account_name,
			(SELECT name || ' (' || mcc || '-' || mnc || ')' FROM networks n WHERE n.id = t.network_id) AS network_name`,
		canRead: staff, canWrite: managers, reloads: true,
		afterWrite: func(ctx context.Context, s *Server, id int64, in map[string]any, _ bool) error {
			raw, ok := in["targets"]
			if !ok {
				return nil
			}
			list, _ := raw.([]any)
			return pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
				if _, err := tx.Exec(ctx, `DELETE FROM route_targets WHERE route_id = $1`, id); err != nil {
					return err
				}
				for i, item := range list {
					m, _ := item.(map[string]any)
					conn := toInt(m["connection_id"])
					weight := toInt(m["weight"])
					if _, ok := m["weight"]; !ok {
						weight = 100
					}
					if _, err := tx.Exec(ctx, `INSERT INTO route_targets (route_id, connection_id, position, weight)
						VALUES ($1, $2, $3, $4)`, id, conn, i, weight); err != nil {
						return err
					}
				}
				return nil
			})
		},
	},
	{
		path: "content-rules", table: "content_rules",
		fields: []field{{"name", kText, true}, {"priority", kInt, false}, {"client_id", kRef, false}, {"country_iso", kISO, false},
			{"sender_pattern", kText, false}, {"text_pattern", kText, false}, {"action", kText, true}, {"find", kText, false},
			{"replace_with", kText, false}, {"status", kText, false}},
		search: []string{"name"}, canRead: staff, canWrite: managers, reloads: true,
	},
	{
		path: "users", table: "users",
		fields: []field{{"email", kText, true}, {"name", kText, false}, {"password", kPassword, false}, {"role", kText, true},
			{"manager_id", kRef, false}, {"client_id", kRef, false}, {"status", kText, false}},
		search: []string{"email", "name"}, hidden: []string{"password_hash"},
		extraCols: `, (SELECT name FROM users m WHERE m.id = t.manager_id) AS manager_name, (SELECT name FROM clients c WHERE c.id = t.client_id) AS client_name`,
		canRead:   staff, canWrite: adminOnly,
	},
	{
		path: "networks", table: "networks",
		fields: []field{{"country_iso", kISO, true}, {"mcc", kText, true}, {"mnc", kText, true}, {"name", kText, false}},
		search: []string{"name", "mcc", "country_iso"},
		extraCols: `, (SELECT count(*) FROM number_prefixes p WHERE p.network_id = t.id) AS prefixes`,
		canRead:   staff, canWrite: adminOnly, reloads: true,
	},
}

func toInt(v any) int64 {
	switch x := v.(type) {
	case json.Number:
		n, _ := x.Int64()
		return n
	case float64:
		return int64(x)
	case string:
		n, _ := strconv.ParseInt(x, 10, 64)
		return n
	case int64:
		return x
	}
	return 0
}

// convert turns a JSON value into a database argument and the column it is stored in.
func (s *Server) convert(f field, v any) (string, any, error) {
	if v == nil {
		if f.kind == kText {
			return f.name, "", nil
		}
		return f.name, nil, nil
	}
	switch f.kind {
	case kText:
		return f.name, strings.TrimSpace(fmt.Sprint(v)), nil
	case kInt:
		return f.name, toInt(v), nil
	case kNum:
		str := strings.TrimSpace(fmt.Sprint(v))
		if _, err := strconv.ParseFloat(str, 64); err != nil {
			return "", nil, fmt.Errorf("%s must be a number", f.name)
		}
		return f.name, str, nil
	case kBool:
		b, ok := v.(bool)
		if !ok {
			return "", nil, fmt.Errorf("%s must be true or false", f.name)
		}
		return f.name, b, nil
	case kRef:
		n := toInt(v)
		if n == 0 {
			return f.name, nil, nil
		}
		return f.name, n, nil
	case kISO:
		str := strings.ToUpper(strings.TrimSpace(fmt.Sprint(v)))
		if str == "" {
			return f.name, nil, nil
		}
		if len(str) != 2 {
			return "", nil, fmt.Errorf("%s must be a 2-letter country code", f.name)
		}
		return f.name, str, nil
	case kTextArr:
		var out []string
		switch x := v.(type) {
		case []any:
			for _, i := range x {
				if t := strings.TrimSpace(fmt.Sprint(i)); t != "" {
					out = append(out, t)
				}
			}
		case string:
			for _, t := range strings.FieldsFunc(x, func(r rune) bool { return r == ',' || r == ' ' || r == '\n' }) {
				out = append(out, t)
			}
		}
		if out == nil {
			out = []string{}
		}
		return f.name, out, nil
	case kTime:
		str := fmt.Sprint(v)
		if str == "" {
			return f.name, time.Now(), nil
		}
		for _, layout := range []string{time.RFC3339, "2006-01-02T15:04", "2006-01-02"} {
			if t, err := time.Parse(layout, str); err == nil {
				return f.name, t, nil
			}
		}
		return "", nil, fmt.Errorf("%s must be a date", f.name)
	case kPassword:
		pw := fmt.Sprint(v)
		if pw == "" {
			return "", nil, errSkip
		}
		if len(pw) < 6 {
			return "", nil, fmt.Errorf("password must have at least 6 characters")
		}
		h, err := hashPassword(pw)
		return "password_hash", h, err
	case kSecret:
		pw := fmt.Sprint(v)
		if pw == "" {
			return "", nil, errSkip
		}
		return "password_enc", s.cipher.Encrypt(pw), nil
	}
	return "", nil, fmt.Errorf("unsupported field %s", f.name)
}

var errSkip = errors.New("skip")

var hashPassword = auth.HashPassword

func (res *resource) from() string {
	if res.selectFrom != "" {
		return res.selectFrom
	}
	return res.table + " t"
}

func (res *resource) jsonExpr() string {
	expr := "to_jsonb(x)"
	for _, h := range res.hidden {
		expr += " - '" + h + "'"
	}
	return expr
}

// where builds the scope condition plus extra filters; returns SQL and args.
func (s *Server) where(r *http.Request, res *resource) (string, []any) {
	p := principal(r)
	conds := []string{}
	var args []any
	if res.scopeSQL != nil {
		cond, a := res.scopeSQL(s.scopeFor(r.Context(), p))
		conds = append(conds, cond)
		args = append(args, a...)
	}
	return strings.Join(append(conds, "true"), " AND "), args
}

func (s *Server) crudList(res *resource) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p := principal(r)
		if !res.canRead(p) {
			writeError(w, http.StatusForbidden, "forbidden", "not allowed")
			return
		}
		cond, args := s.where(r, res)
		q := r.URL.Query()
		if term := strings.TrimSpace(q.Get("q")); term != "" && len(res.search) > 0 {
			args = append(args, "%"+term+"%")
			var ors []string
			for _, c := range res.search {
				ors = append(ors, fmt.Sprintf("t.%s::text ILIKE $%d", c, len(args)))
			}
			cond += " AND (" + strings.Join(ors, " OR ") + ")"
		}
		for _, f := range append(res.fields, field{name: "id", kind: kInt}) {
			if v := q.Get(f.name); v != "" && f.kind != kPassword && f.kind != kSecret {
				args = append(args, v)
				cond += fmt.Sprintf(" AND t.%s::text = $%d", f.name, len(args))
			}
		}
		limit, _ := strconv.Atoi(q.Get("limit"))
		if limit <= 0 || limit > 1000 {
			limit = 200
		}
		offset, _ := strconv.Atoi(q.Get("offset"))
		sql := fmt.Sprintf(`SELECT jsonb_build_object('total', (SELECT count(*) FROM %[1]s WHERE %[2]s),
				'items', COALESCE((SELECT jsonb_agg(%[3]s) FROM (SELECT t.*%[4]s FROM %[1]s WHERE %[2]s
					ORDER BY t.id DESC LIMIT %[5]d OFFSET %[6]d) x), '[]'))`,
			res.from(), cond, res.jsonExpr(), res.extraCols, limit, offset)
		var raw []byte
		if err := s.db.QueryRow(r.Context(), sql, args...).Scan(&raw); err != nil {
			s.dbError(w, err)
			return
		}
		writeRaw(w, http.StatusOK, raw)
	}
}

func (s *Server) getOne(ctx context.Context, r *http.Request, res *resource, id int64) ([]byte, error) {
	cond, args := s.where(r, res)
	args = append(args, id)
	sql := fmt.Sprintf(`SELECT %s FROM (SELECT t.*%s FROM %s WHERE %s AND t.id = $%d) x`,
		res.jsonExpr(), res.extraCols, res.from(), cond, len(args))
	var raw []byte
	err := s.db.QueryRow(ctx, sql, args...).Scan(&raw)
	return raw, err
}

func (s *Server) crudGet(res *resource) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !res.canRead(principal(r)) {
			writeError(w, http.StatusForbidden, "forbidden", "not allowed")
			return
		}
		id, ok := idParam(r)
		if !ok {
			writeError(w, http.StatusBadRequest, "invalid", "invalid id")
			return
		}
		raw, err := s.getOne(r.Context(), r, res, id)
		if err != nil {
			s.dbError(w, err)
			return
		}
		writeRaw(w, http.StatusOK, raw)
	}
}

// checkRefs makes sure a scoped user only links items to clients/vendors they can see.
func (s *Server) checkRefs(r *http.Request, in map[string]any) error {
	sc := s.scopeFor(r.Context(), principal(r))
	if v, ok := in["client_id"]; ok && toInt(v) != 0 && !sc.canClient(toInt(v)) {
		return errors.New("client not in your scope")
	}
	if v, ok := in["vendor_id"]; ok && toInt(v) != 0 && !sc.allVendors {
		found := false
		for _, id := range sc.vendorIDs {
			found = found || id == toInt(v)
		}
		if !found {
			return errors.New("vendor not in your scope")
		}
	}
	return nil
}

func (s *Server) crudCreate(res *resource) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !res.canWrite(principal(r)) {
			writeError(w, http.StatusForbidden, "forbidden", "not allowed")
			return
		}
		var in map[string]any
		if err := readJSON(r, &in); err != nil {
			writeError(w, http.StatusBadRequest, "invalid", "invalid JSON")
			return
		}
		if err := s.checkRefs(r, in); err != nil {
			writeError(w, http.StatusForbidden, "forbidden", err.Error())
			return
		}
		var cols, ph []string
		var args []any
		for _, f := range res.fields {
			v, ok := in[f.name]
			if !ok || v == nil || v == "" {
				if f.required {
					writeError(w, http.StatusBadRequest, "invalid", f.name+" is required")
					return
				}
				continue
			}
			col, val, err := s.convert(f, v)
			if errors.Is(err, errSkip) {
				continue
			}
			if err != nil {
				writeError(w, http.StatusBadRequest, "invalid", err.Error())
				return
			}
			cols = append(cols, col)
			args = append(args, val)
			ph = append(ph, fmt.Sprintf("$%d", len(args)))
		}
		if res.table == "accounts" && !hasCol(cols, "password_hash") {
			writeError(w, http.StatusBadRequest, "invalid", "password is required")
			return
		}
		var id int64
		err := s.db.QueryRow(r.Context(), fmt.Sprintf(`INSERT INTO %s (%s) VALUES (%s) RETURNING id`,
			res.table, strings.Join(cols, ", "), strings.Join(ph, ", ")), args...).Scan(&id)
		if err != nil {
			s.dbError(w, err)
			return
		}
		if res.afterWrite != nil {
			if err := res.afterWrite(r.Context(), s, id, in, true); err != nil {
				s.dbError(w, err)
				return
			}
		}
		s.audit(r, "create", res.table, strconv.FormatInt(id, 10), redact(in))
		if res.reloads {
			s.reload(r.Context())
		}
		raw, err := s.getOne(r.Context(), r, res, id)
		if err != nil {
			s.dbError(w, err)
			return
		}
		writeRaw(w, http.StatusCreated, raw)
	}
}

func hasCol(cols []string, c string) bool {
	for _, x := range cols {
		if x == c {
			return true
		}
	}
	return false
}

func redact(in map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range in {
		if strings.Contains(k, "password") {
			v = "***"
		}
		out[k] = v
	}
	return out
}

func (s *Server) crudUpdate(res *resource) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !res.canWrite(principal(r)) {
			writeError(w, http.StatusForbidden, "forbidden", "not allowed")
			return
		}
		id, ok := idParam(r)
		if !ok {
			writeError(w, http.StatusBadRequest, "invalid", "invalid id")
			return
		}
		if _, err := s.getOne(r.Context(), r, res, id); err != nil {
			s.dbError(w, err)
			return
		}
		var in map[string]any
		if err := readJSON(r, &in); err != nil {
			writeError(w, http.StatusBadRequest, "invalid", "invalid JSON")
			return
		}
		if err := s.checkRefs(r, in); err != nil {
			writeError(w, http.StatusForbidden, "forbidden", err.Error())
			return
		}
		var sets []string
		var args []any
		for _, f := range res.fields {
			v, ok := in[f.name]
			if !ok {
				continue
			}
			col, val, err := s.convert(f, v)
			if errors.Is(err, errSkip) {
				continue
			}
			if err != nil {
				writeError(w, http.StatusBadRequest, "invalid", err.Error())
				return
			}
			args = append(args, val)
			sets = append(sets, fmt.Sprintf("%s = $%d", col, len(args)))
		}
		if len(sets) > 0 {
			if res.table != "networks" && res.table != "client_rates" && res.table != "vendor_rates" {
				sets = append(sets, "updated_at = now()")
			}
			args = append(args, id)
			if _, err := s.db.Exec(r.Context(), fmt.Sprintf(`UPDATE %s SET %s WHERE id = $%d`, res.table,
				strings.Join(sets, ", "), len(args)), args...); err != nil {
				s.dbError(w, err)
				return
			}
		}
		if res.afterWrite != nil {
			if err := res.afterWrite(r.Context(), s, id, in, false); err != nil {
				s.dbError(w, err)
				return
			}
		}
		s.audit(r, "update", res.table, strconv.FormatInt(id, 10), redact(in))
		if res.reloads {
			s.reload(r.Context())
		}
		raw, err := s.getOne(r.Context(), r, res, id)
		if err != nil {
			s.dbError(w, err)
			return
		}
		writeRaw(w, http.StatusOK, raw)
	}
}

func (s *Server) crudDelete(res *resource) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !res.canWrite(principal(r)) {
			writeError(w, http.StatusForbidden, "forbidden", "not allowed")
			return
		}
		id, ok := idParam(r)
		if !ok {
			writeError(w, http.StatusBadRequest, "invalid", "invalid id")
			return
		}
		if _, err := s.getOne(r.Context(), r, res, id); err != nil {
			s.dbError(w, err)
			return
		}
		if _, err := s.db.Exec(r.Context(), fmt.Sprintf(`DELETE FROM %s WHERE id = $1`, res.table), id); err != nil {
			s.dbError(w, err)
			return
		}
		s.audit(r, "delete", res.table, strconv.FormatInt(id, 10), nil)
		if res.reloads {
			s.reload(r.Context())
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}
}
