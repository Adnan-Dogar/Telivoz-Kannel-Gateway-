package api

import (
	"context"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/Adnan-Dogar/telivoz-gateway/internal/auth"
)

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// tooManyFailures limits password guessing: 10 failures per IP and email in 15 minutes.
func (s *Server) tooManyFailures(key string) bool {
	s.loginMu.Lock()
	defer s.loginMu.Unlock()
	cutoff := time.Now().Add(-15 * time.Minute)
	recent := s.loginFailures[key][:0]
	for _, t := range s.loginFailures[key] {
		if t.After(cutoff) {
			recent = append(recent, t)
		}
	}
	s.loginFailures[key] = recent
	return len(recent) >= 10
}

func (s *Server) recordFailure(key string) {
	s.loginMu.Lock()
	defer s.loginMu.Unlock()
	s.loginFailures[key] = append(s.loginFailures[key], time.Now())
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := readJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid", "invalid request")
		return
	}
	in.Email = strings.TrimSpace(strings.ToLower(in.Email))
	key := clientIP(r) + "|" + in.Email
	if s.tooManyFailures(key) {
		writeError(w, http.StatusTooManyRequests, "locked", "too many failed attempts, try again in 15 minutes")
		return
	}
	var id int64
	var hash, status string
	err := s.db.QueryRow(r.Context(), `SELECT id, password_hash, status FROM users WHERE lower(email) = $1`, in.Email).Scan(&id, &hash, &status)
	if err != nil || status != "active" || !auth.CheckPassword(hash, in.Password) {
		s.recordFailure(key)
		writeError(w, http.StatusUnauthorized, "invalid_login", "wrong email or password")
		return
	}
	token, tokenHash := auth.NewToken()
	if _, err := s.db.Exec(r.Context(), `INSERT INTO sessions (token_hash, user_id, expires_at, ip) VALUES ($1, $2, $3, $4)`,
		tokenHash, id, time.Now().Add(s.opts.SessionTTL), clientIP(r)); err != nil {
		s.dbError(w, err)
		return
	}
	_, _ = s.db.Exec(r.Context(), `UPDATE users SET last_login_at = now() WHERE id = $1`, id)
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: token, Path: "/", HttpOnly: true, Secure: s.opts.CookieSecure,
		SameSite: http.SameSiteStrictMode, Expires: time.Now().Add(s.opts.SessionTTL)})
	r = r.WithContext(context.WithValue(r.Context(), principalKey, &auth.Principal{UserID: id}))
	s.audit(r, "login", "user", "", map[string]string{"ip": clientIP(r)})
	s.writeMe(w, r, id)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		_, _ = s.db.Exec(r.Context(), `DELETE FROM sessions WHERE token_hash = $1`, auth.HashToken(c.Value))
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true})
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	s.writeMe(w, r, principal(r).UserID)
}

func (s *Server) writeMe(w http.ResponseWriter, r *http.Request, id int64) {
	var raw []byte
	err := s.db.QueryRow(r.Context(), `SELECT jsonb_build_object('id', u.id, 'email', u.email, 'name', u.name, 'role', u.role,
			'client_id', u.client_id, 'client_name', c.name)
		FROM users u LEFT JOIN clients c ON c.id = u.client_id WHERE u.id = $1`, id).Scan(&raw)
	if err != nil {
		s.dbError(w, err)
		return
	}
	writeRaw(w, http.StatusOK, raw)
}

func (s *Server) changePassword(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Current string `json:"current"`
		New     string `json:"new"`
	}
	if err := readJSON(r, &in); err != nil || len(in.New) < 8 {
		writeError(w, http.StatusBadRequest, "invalid", "the new password must have at least 8 characters")
		return
	}
	p := principal(r)
	var hash string
	if err := s.db.QueryRow(r.Context(), `SELECT password_hash FROM users WHERE id = $1`, p.UserID).Scan(&hash); err != nil {
		s.dbError(w, err)
		return
	}
	if !auth.CheckPassword(hash, in.Current) {
		writeError(w, http.StatusBadRequest, "invalid_login", "current password is wrong")
		return
	}
	newHash, _ := auth.HashPassword(in.New)
	if _, err := s.db.Exec(r.Context(), `UPDATE users SET password_hash = $2, updated_at = now() WHERE id = $1`, p.UserID, newHash); err != nil {
		s.dbError(w, err)
		return
	}
	s.audit(r, "change_password", "user", "", nil)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// ---- hierarchy scoping ----------------------------------------------------------------------------------

// teamIDs returns the user and everyone below them in the reporting tree.
func (s *Server) teamIDs(ctx context.Context, userID int64) []int64 {
	var ids []int64
	_ = s.db.QueryRow(ctx, `WITH RECURSIVE team AS (
			SELECT id FROM users WHERE id = $1
			UNION SELECT u.id FROM users u JOIN team t ON u.manager_id = t.id)
		SELECT array_agg(id) FROM team`, userID).Scan(&ids)
	return ids
}

// scope describes what a user may see. allClients=true means no client filter.
type scope struct {
	allClients bool
	clientIDs  []int64
	allVendors bool
	vendorIDs  []int64
}

func (s *Server) scopeFor(ctx context.Context, p *auth.Principal) scope {
	switch {
	case p.CanSeeAll():
		return scope{allClients: true, allVendors: true}
	case p.IsClient():
		return scope{clientIDs: []int64{p.ClientID}, vendorIDs: []int64{}}
	default:
		team := s.teamIDs(ctx, p.UserID)
		sc := scope{clientIDs: []int64{}, vendorIDs: []int64{}}
		_ = s.db.QueryRow(ctx, `SELECT COALESCE(array_agg(id), '{}') FROM clients WHERE owner_id = ANY($1)`, team).Scan(&sc.clientIDs)
		_ = s.db.QueryRow(ctx, `SELECT COALESCE(array_agg(id), '{}') FROM vendors WHERE owner_id = ANY($1)`, team).Scan(&sc.vendorIDs)
		return sc
	}
}

func (sc scope) canClient(id int64) bool {
	if sc.allClients {
		return true
	}
	for _, c := range sc.clientIDs {
		if c == id {
			return true
		}
	}
	return false
}

func (s *Server) team(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	var raw []byte
	err := s.db.QueryRow(r.Context(), `WITH RECURSIVE team AS (
			SELECT id, 0 AS depth FROM users WHERE id = $1
			UNION SELECT u.id, t.depth + 1 FROM users u JOIN team t ON u.manager_id = t.id)
		SELECT COALESCE(jsonb_agg(jsonb_build_object('id', u.id, 'name', u.name, 'email', u.email, 'role', u.role,
			'manager_id', u.manager_id, 'depth', t.depth,
			'clients', (SELECT count(*) FROM clients c WHERE c.owner_id = u.id)) ORDER BY t.depth, u.name), '[]')
		FROM team t JOIN users u ON u.id = t.id`, p.UserID).Scan(&raw)
	if err != nil {
		s.dbError(w, err)
		return
	}
	writeRaw(w, http.StatusOK, raw)
}
