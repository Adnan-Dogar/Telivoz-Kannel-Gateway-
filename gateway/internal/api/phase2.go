package api

import (
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Adnan-Dogar/telivoz-gateway/internal/auth"
	"github.com/Adnan-Dogar/telivoz-gateway/internal/routing"
	"github.com/Adnan-Dogar/telivoz-gateway/internal/sheet"
)

// ---- two-factor login -----------------------------------------------------------------------------------

// totpSetup creates a new secret (not active until confirmed with a code).
func (s *Server) totpSetup(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	var enabled bool
	_ = s.db.QueryRow(r.Context(), `SELECT totp_enabled FROM users WHERE id = $1`, p.UserID).Scan(&enabled)
	if enabled {
		writeError(w, http.StatusConflict, "already_enabled", "two-factor login is already on; turn it off first")
		return
	}
	secret := auth.NewTOTPSecret()
	if _, err := s.db.Exec(r.Context(), `UPDATE users SET totp_secret_enc = $2 WHERE id = $1`, p.UserID, s.cipher.Encrypt(secret)); err != nil {
		s.dbError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"secret": secret, "otpauth_url": auth.TOTPURL(secret, p.Email, "Telivoz")})
}

func (s *Server) userSecret(r *http.Request) (string, bool, error) {
	var enc string
	var enabled bool
	if err := s.db.QueryRow(r.Context(), `SELECT totp_secret_enc, totp_enabled FROM users WHERE id = $1`, principal(r).UserID).Scan(&enc, &enabled); err != nil {
		return "", false, err
	}
	secret, err := s.cipher.Decrypt(enc)
	return secret, enabled, err
}

func (s *Server) totpEnable(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Code string `json:"code"`
	}
	_ = readJSON(r, &in)
	secret, _, err := s.userSecret(r)
	if err != nil || secret == "" {
		writeError(w, http.StatusBadRequest, "no_setup", "start the setup first")
		return
	}
	if !auth.VerifyTOTP(secret, in.Code, time.Now()) {
		writeError(w, http.StatusBadRequest, "invalid_code", "wrong code; check the time on your phone and try again")
		return
	}
	if _, err := s.db.Exec(r.Context(), `UPDATE users SET totp_enabled = true WHERE id = $1`, principal(r).UserID); err != nil {
		s.dbError(w, err)
		return
	}
	s.audit(r, "enable_2fa", "users", strconv.FormatInt(principal(r).UserID, 10), nil)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) totpDisable(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Code string `json:"code"`
	}
	_ = readJSON(r, &in)
	secret, enabled, err := s.userSecret(r)
	if err != nil || !enabled || !auth.VerifyTOTP(secret, in.Code, time.Now()) {
		writeError(w, http.StatusBadRequest, "invalid_code", "enter a valid code from your authenticator app")
		return
	}
	if _, err := s.db.Exec(r.Context(), `UPDATE users SET totp_enabled = false, totp_secret_enc = '' WHERE id = $1`, principal(r).UserID); err != nil {
		s.dbError(w, err)
		return
	}
	s.audit(r, "disable_2fa", "users", strconv.FormatInt(principal(r).UserID, 10), nil)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// totpReset lets an administrator turn off two-factor login for a user who lost their phone.
func (s *Server) totpReset(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(r)
	if !ok || !principal(r).IsAdmin() {
		writeError(w, http.StatusForbidden, "forbidden", "not allowed")
		return
	}
	if _, err := s.db.Exec(r.Context(), `UPDATE users SET totp_enabled = false, totp_secret_enc = '' WHERE id = $1`, id); err != nil {
		s.dbError(w, err)
		return
	}
	// End the user's sessions so the change takes effect immediately.
	_, _ = s.db.Exec(r.Context(), `DELETE FROM sessions WHERE user_id = $1`, id)
	s.audit(r, "reset_2fa", "users", strconv.FormatInt(id, 10), nil)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// ---- blacklist bulk import ------------------------------------------------------------------------------

// importBlacklist adds numbers (one per line, or separated by commas/semicolons) for a client, or to the
// global list (admins only, client_id omitted).
func (s *Server) importBlacklist(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	clientID, _ := strconv.ParseInt(r.URL.Query().Get("client_id"), 10, 64)
	if p.IsClient() {
		clientID = p.ClientID
	}
	if clientID == 0 && !p.IsAdmin() {
		writeError(w, http.StatusForbidden, "forbidden", "only administrators can add numbers to the global blacklist")
		return
	}
	if clientID != 0 && !s.scopeFor(r.Context(), p).canClient(clientID) {
		writeError(w, http.StatusForbidden, "forbidden", "client not in your scope")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 50<<20))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid", "file too large (max 50 MB)")
		return
	}
	if sheet.IsXLSX(body) {
		rows, err := sheet.ReadXLSX(body)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid", "could not read the file: "+err.Error())
			return
		}
		var b strings.Builder
		for _, row := range rows {
			b.WriteString(strings.Join(row, ",") + "\n")
		}
		body = []byte(b.String())
	}
	seen := map[string]bool{}
	var numbers []string
	for _, f := range strings.FieldsFunc(string(body), func(c rune) bool { return c == '\n' || c == '\r' || c == ',' || c == ';' || c == '\t' }) {
		n := routing.NormalizeNumber(f)
		if len(n) >= 6 && len(n) <= 15 && !seen[n] {
			seen[n] = true
			numbers = append(numbers, n)
		}
	}
	reason := r.URL.Query().Get("reason")
	var cid any
	if clientID != 0 {
		cid = clientID
	}
	tag, err := s.db.Exec(r.Context(), `INSERT INTO blacklist (client_id, number, reason, created_by)
		SELECT $1, n, $3, $4 FROM unnest($2::text[]) AS n ON CONFLICT DO NOTHING`, cid, numbers, reason, p.UserID)
	if err != nil {
		s.dbError(w, err)
		return
	}
	s.audit(r, "import_blacklist", "blacklist", strconv.FormatInt(clientID, 10), map[string]int64{"added": tag.RowsAffected()})
	s.reload(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"added": tag.RowsAffected(), "valid": len(numbers)})
}

// ---- statements / invoices ------------------------------------------------------------------------------

// statement returns a monthly statement for a client: opening and closing balance, payments, usage by
// country with prices, and the total charged. ?month=YYYY-MM (default: last month).
func (s *Server) statement(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(r)
	if !ok || !s.scopeFor(r.Context(), principal(r)).canClient(id) {
		writeError(w, http.StatusNotFound, "not_found", "client not found")
		return
	}
	now := time.Now().UTC()
	start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, -1, 0)
	if m := r.URL.Query().Get("month"); m != "" {
		t, err := time.Parse("2006-01", m)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid", "month must be YYYY-MM")
			return
		}
		start = t
	}
	end := start.AddDate(0, 1, 0)
	var raw []byte
	err := s.db.QueryRow(r.Context(), `SELECT jsonb_build_object(
		'number', 'TVZ-' || to_char($2::timestamptz, 'YYYYMM') || '-' || lpad($1::bigint::text, 5, '0'),
		'period_start', $2::timestamptz, 'period_end', $3::timestamptz, 'issued_at', now(),
		'client', (SELECT jsonb_build_object('id', id, 'name', name, 'email', email, 'phone', phone, 'country_iso', country_iso,
			'currency', currency, 'billing_type', billing_type, 'credit_limit', credit_limit) FROM clients WHERE id = $1),
		'opening_balance', COALESCE((SELECT balance_after FROM ledger WHERE client_id = $1 AND created_at < $2 ORDER BY id DESC LIMIT 1), 0),
		'closing_balance', COALESCE((SELECT balance_after FROM ledger WHERE client_id = $1 AND created_at < $3 ORDER BY id DESC LIMIT 1), 0),
		'payments', COALESCE((SELECT jsonb_agg(jsonb_build_object('date', created_at, 'kind', kind, 'amount', amount, 'note', note) ORDER BY id)
			FROM ledger WHERE client_id = $1 AND created_at >= $2 AND created_at < $3 AND kind IN ('topup', 'adjustment', 'migration')), '[]'),
		'charged', COALESCE((SELECT -sum(amount) FROM ledger WHERE client_id = $1 AND created_at >= $2 AND created_at < $3 AND kind IN ('charge', 'refund')), 0),
		'usage', COALESCE((SELECT jsonb_agg(u ORDER BY u.amount DESC) FROM (
			SELECT COALESCE(c.name, m.country_iso, 'Other') AS country, count(*) AS messages, sum(m.parts) AS parts,
				count(*) FILTER (WHERE m.status = 'delivered') AS delivered, round(sum(m.price), 4) AS amount,
				CASE WHEN sum(m.parts) > 0 THEN round(sum(m.price) / sum(m.parts), 6) END AS unit_price
			FROM messages m LEFT JOIN countries c ON c.iso = m.country_iso
			WHERE m.client_id = $1 AND m.created_at >= $2 AND m.created_at < $3 AND m.direction = 'mt'
				AND m.status <> 'failed' -- failed messages are refunded
			GROUP BY 1) u), '[]'))`, id, start, end).Scan(&raw)
	if err != nil {
		s.dbError(w, err)
		return
	}
	writeRaw(w, http.StatusOK, raw)
}
