package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/Adnan-Dogar/telivoz-gateway/internal/auth"
	"github.com/Adnan-Dogar/telivoz-gateway/internal/engine"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// apiAccount authenticates a client HTTP request: "Authorization: Bearer <api key>" or HTTP basic auth with
// the account username and password.
func (s *Server) apiAccount(r *http.Request) (engine.Account, error) {
	ctx := r.Context()
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		key := strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
		var a engine.Account
		err := s.db.QueryRow(ctx, `UPDATE api_keys k SET last_used_at = now() FROM accounts a JOIN clients c ON c.id = a.client_id
			WHERE k.key_hash = $1 AND k.revoked_at IS NULL AND a.id = k.account_id AND a.status = 'active' AND c.status = 'active'
			RETURNING a.id, a.client_id, a.kind, a.username, a.tps, a.max_binds`, auth.HashToken(key)).
			Scan(&a.ID, &a.ClientID, &a.Kind, &a.Username, &a.TPS, &a.MaxBinds)
		if err != nil {
			return a, errors.New("invalid API key")
		}
		return a, nil
	}
	if user, pass, ok := r.BasicAuth(); ok {
		return s.passwordAccount(ctx, user, pass)
	}
	return engine.Account{}, errors.New("missing credentials")
}

func (s *Server) passwordAccount(ctx context.Context, user, pass string) (engine.Account, error) {
	a, hash, _, err := s.eng.LoadAccount(ctx, "http", user)
	if err != nil || !auth.CheckPassword(hash, pass) {
		return engine.Account{}, errors.New("invalid username or password")
	}
	return a, nil
}

type v1Message struct {
	To        string `json:"to"`
	From      string `json:"from"`
	Text      string `json:"text"`
	ClientRef string `json:"client_ref"`
}

// v1Send: POST /api/v1/messages with {"to","from","text","client_ref"} or {"messages":[...]} (max 1000).
func (s *Server) v1Send(w http.ResponseWriter, r *http.Request) {
	acc, err := s.apiAccount(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", err.Error())
		return
	}
	var in struct {
		v1Message
		Messages []v1Message `json:"messages"`
	}
	if err := readJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid", "invalid JSON body")
		return
	}
	if len(in.Messages) == 0 {
		m := in.v1Message
		if key := r.Header.Get("Idempotency-Key"); key != "" && m.ClientRef == "" {
			m.ClientRef = key
		}
		res, err := s.submitV1(r.Context(), acc, m)
		if err != nil {
			writeSubmitError(w, err)
			return
		}
		writeJSON(w, http.StatusAccepted, res)
		return
	}
	if len(in.Messages) > 1000 {
		writeError(w, http.StatusBadRequest, "too_many", "at most 1000 messages per request")
		return
	}
	results := make([]map[string]any, len(in.Messages))
	for i, m := range in.Messages {
		res, err := s.submitV1(r.Context(), acc, m)
		if err != nil {
			var se *engine.SubmitError
			code := "internal_error"
			if errors.As(err, &se) {
				code = se.Code
			}
			results[i] = map[string]any{"index": i, "error": code, "message": err.Error()}
			continue
		}
		res["index"] = i
		results[i] = res
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"results": results})
}

func (s *Server) submitV1(ctx context.Context, acc engine.Account, m v1Message) (map[string]any, error) {
	res, err := s.eng.Submit(ctx, engine.SubmitRequest{Account: acc, Source: m.From, Destination: m.To, Text: m.Text,
		ClientRef: m.ClientRef, WantsDLR: true})
	if err != nil {
		return nil, err
	}
	out := map[string]any{"id": res.MessageID, "status": "queued", "parts": res.Parts, "price": res.Price.String()}
	if res.Duplicate {
		out["duplicate"] = true
		delete(out, "parts")
		delete(out, "price")
	}
	return out, nil
}

func (s *Server) v1Status(w http.ResponseWriter, r *http.Request) {
	acc, err := s.apiAccount(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", err.Error())
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid", "invalid message id")
		return
	}
	var raw []byte
	err = s.db.QueryRow(r.Context(), `SELECT jsonb_build_object('id', id, 'to', destination, 'from', source, 'status', status,
			'dlr_status', dlr_status, 'error', dlr_error, 'parts', parts, 'price', price, 'client_ref', client_ref,
			'created_at', created_at, 'sent_at', sent_at, 'done_at', dlr_at)
		FROM messages WHERE id = $1 AND client_id = $2`, id, acc.ClientID).Scan(&raw)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "message not found")
		return
	}
	writeRaw(w, http.StatusOK, raw)
}

func (s *Server) v1Balance(w http.ResponseWriter, r *http.Request) {
	acc, err := s.apiAccount(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", err.Error())
		return
	}
	var raw []byte
	err = s.db.QueryRow(r.Context(), `SELECT jsonb_build_object('balance', b.balance, 'currency', c.currency,
			'credit_limit', c.credit_limit, 'billing_type', c.billing_type)
		FROM clients c JOIN balances b ON b.client_id = c.id WHERE c.id = $1`, acc.ClientID).Scan(&raw)
	if err != nil {
		s.dbError(w, err)
		return
	}
	writeRaw(w, http.StatusOK, raw)
}

// legacySend accepts the old portal's HTTP API (GET or POST form parameters username, password, to, from,
// message, messageid, action=balance) and answers in its JSON format, so existing HTTP clients can switch to
// the new gateway by changing only the URL.
func (s *Server) legacySend(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	get := func(k string) string { return strings.TrimSpace(r.Form.Get(k)) }
	legacy := func(status int, payload map[string]any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		payload["status"] = status
		_ = jsonEncode(w, payload)
	}
	acc, err := s.passwordAccount(r.Context(), get("username"), get("password"))
	if err != nil {
		legacy(403, map[string]any{"description": "Unable to authenticate"})
		return
	}
	if get("action") == "balance" {
		var balance float64
		_ = s.db.QueryRow(r.Context(), `SELECT balance FROM balances WHERE client_id = $1`, acc.ClientID).Scan(&balance)
		legacy(1, map[string]any{"statusCode": 200, "balance": balance, "description": "Balance fetched successfully"})
		return
	}
	for _, p := range []string{"to", "message"} {
		if get(p) == "" {
			legacy(0, map[string]any{"statusCode": 405, "description": "invalid param: " + p})
			return
		}
	}
	res, err := s.eng.Submit(r.Context(), engine.SubmitRequest{Account: acc, Source: get("from"), Destination: get("to"),
		Text: r.Form.Get("message"), ClientRef: get("messageid"), WantsDLR: true})
	if err != nil {
		var se *engine.SubmitError
		code, desc := 500, "Unable to send SMS. Internal Error"
		if errors.As(err, &se) {
			switch se.Code {
			case "insufficient_balance":
				code, desc = 406, "Insufficient balance"
			case "invalid_destination":
				code, desc = 405, "invalid param: to"
			case "throttled":
				code, desc = 429, "Too many requests"
			default:
				desc = "Unable to send SMS. " + se.Message
			}
		}
		legacy(code, map[string]any{"description": desc})
		return
	}
	ref := res.MessageID.String()
	shown := get("messageid")
	if shown == "" {
		shown = ref
	}
	legacy(1, map[string]any{"statusCode": 200, "description": fmt.Sprintf("Success [%s]", shown), "reference": ref})
}
