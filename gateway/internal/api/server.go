// Package api serves the portal API (session cookie), the client HTTP API v1 (API key or basic auth), the
// legacy-compatible HTTP endpoint, and the built web portal.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Adnan-Dogar/telivoz-gateway/internal/auth"
	"github.com/Adnan-Dogar/telivoz-gateway/internal/engine"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Options struct {
	WebDir       string
	CookieSecure bool
	SessionTTL   time.Duration
}

type Server struct {
	db     *pgxpool.Pool
	eng    *engine.Engine
	cipher *auth.Cipher
	log    *slog.Logger
	opts   Options

	loginMu       sync.Mutex
	loginFailures map[string][]time.Time

	campaigns *campaignRunner
}

func New(db *pgxpool.Pool, eng *engine.Engine, cipher *auth.Cipher, log *slog.Logger, opts Options) *Server {
	if opts.SessionTTL == 0 {
		opts.SessionTTL = 12 * time.Hour
	}
	s := &Server{db: db, eng: eng, cipher: cipher, log: log, opts: opts, loginFailures: map[string][]time.Time{}}
	s.campaigns = &campaignRunner{s: s}
	return s
}

const sessionCookie = "tvz_session"

func (s *Server) Handler() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RealIP, middleware.Recoverer, s.securityHeaders)

	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if err := s.db.Ping(r.Context()); err != nil {
			http.Error(w, "database unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Write([]byte("ok"))
	})

	// Client HTTP API v1 and the legacy-compatible endpoint.
	r.Route("/api/v1", func(r chi.Router) {
		r.Post("/messages", s.v1Send)
		r.Get("/messages/{id}", s.v1Status)
		r.Get("/balance", s.v1Balance)
	})
	r.HandleFunc("/api/legacy", s.legacySend)

	// Portal API.
	r.Route("/api", func(r chi.Router) {
		r.Post("/auth/login", s.login)
		r.Group(func(r chi.Router) {
			r.Use(s.requireSession, s.csrf)
			r.Post("/auth/logout", s.logout)
			r.Get("/auth/me", s.me)
			r.Post("/auth/password", s.changePassword)

			r.Get("/stats/live", s.statsLive)
			r.Get("/stats/overview", s.statsOverview)
			r.Get("/stats/breakdown", s.statsBreakdown)

			r.Get("/messages", s.listMessages)
			r.Get("/messages/{id}", s.getMessage)
			r.Post("/messages/send", s.portalSend)

			r.Post("/campaigns", s.createCampaign)
			r.Get("/campaigns", s.listCampaigns)
			r.Get("/campaigns/{id}", s.getCampaign)

			r.Post("/clients/{id}/topup", s.topup)
			r.Get("/clients/{id}/ledger", s.clientLedger)
			r.Post("/accounts/{id}/api-keys", s.createAPIKey)
			r.Get("/accounts/{id}/api-keys", s.listAPIKeys)
			r.Delete("/api-keys/{id}", s.revokeAPIKey)
			r.Post("/connections/{id}/restart", s.restartConnection)
			r.Post("/routes/test", s.testRoute)
			r.Post("/rates/import", s.importRates)
			r.Get("/lookups", s.lookups)
			r.Get("/team", s.team)
			r.Get("/audit", s.auditLog)

			for _, res := range resources {
				res := res
				r.Get("/"+res.path, s.crudList(res))
				r.Post("/"+res.path, s.crudCreate(res))
				r.Get("/"+res.path+"/{id}", s.crudGet(res))
				r.Patch("/"+res.path+"/{id}", s.crudUpdate(res))
				r.Delete("/"+res.path+"/{id}", s.crudDelete(res))
			}
		})
	})

	r.Handle("/metrics", s.metricsHandler())
	r.NotFound(s.spa)
	return r
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}

// spa serves the built portal, falling back to index.html for client-side routes.
func (s *Server) spa(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		writeError(w, http.StatusNotFound, "not_found", "no such endpoint")
		return
	}
	clean := filepath.Clean("/" + r.URL.Path)
	path := filepath.Join(s.opts.WebDir, clean)
	if st, err := os.Stat(path); err == nil && !st.IsDir() {
		if strings.HasPrefix(clean, "/assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		http.ServeFile(w, r, path)
		return
	}
	index := filepath.Join(s.opts.WebDir, "index.html")
	if _, err := os.Stat(index); err != nil {
		http.Error(w, "portal not built (run the web build)", http.StatusNotFound)
		return
	}
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeFile(w, r, index)
}

// ---- JSON helpers -------------------------------------------------------------------------------------

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeRaw(w http.ResponseWriter, status int, raw []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(raw)
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]any{"error": code, "message": msg})
}

func readJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 2<<20))
	dec.UseNumber()
	return dec.Decode(v)
}

func (s *Server) dbError(w http.ResponseWriter, err error) {
	var pgErr interface{ SQLState() string }
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	if errors.As(err, &pgErr) {
		switch pgErr.SQLState() {
		case "23505":
			writeError(w, http.StatusConflict, "duplicate", "an item with these values already exists")
			return
		case "23503":
			writeError(w, http.StatusConflict, "in_use", "referenced item missing or still in use")
			return
		case "23514", "22P02", "22003", "23502":
			writeError(w, http.StatusBadRequest, "invalid", "invalid value: "+err.Error())
			return
		}
	}
	s.log.Error("database error", "err", err)
	writeError(w, http.StatusInternalServerError, "internal_error", "internal error")
}

func idParam(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	return id, err == nil && id > 0
}

func queryTime(r *http.Request, key string, def time.Time) time.Time {
	v := r.URL.Query().Get(key)
	if v == "" {
		return def
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04", "2006-01-02"} {
		if t, err := time.Parse(layout, v); err == nil {
			return t
		}
	}
	return def
}

// ---- sessions -----------------------------------------------------------------------------------------

type ctxKey int

const principalKey ctxKey = 1

func principal(r *http.Request) *auth.Principal {
	p, _ := r.Context().Value(principalKey).(*auth.Principal)
	return p
}

func (s *Server) requireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(sessionCookie)
		if err != nil || c.Value == "" {
			writeError(w, http.StatusUnauthorized, "unauthenticated", "please sign in")
			return
		}
		p := &auth.Principal{}
		var clientID *int64
		err = s.db.QueryRow(r.Context(), `SELECT u.id, u.email, u.name, u.role, u.client_id
			FROM sessions s JOIN users u ON u.id = s.user_id
			WHERE s.token_hash = $1 AND s.expires_at > now() AND u.status = 'active'`, auth.HashToken(c.Value)).
			Scan(&p.UserID, &p.Email, &p.Name, &p.Role, &clientID)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "unauthenticated", "session expired, please sign in again")
			return
		}
		if clientID != nil {
			p.ClientID = *clientID
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), principalKey, p)))
	})
}

// csrf: state-changing portal requests must carry a custom header, which browsers never add cross-site.
func (s *Server) csrf(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Header.Get("X-Requested-With") != "telivoz" {
			writeError(w, http.StatusForbidden, "csrf", "missing X-Requested-With header")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) audit(r *http.Request, action, entity, entityID string, details any) {
	p := principal(r)
	var uid *int64
	if p != nil {
		uid = &p.UserID
	}
	b, _ := json.Marshal(details)
	if _, err := s.db.Exec(context.Background(), `INSERT INTO audit_log (user_id, action, entity, entity_id, details)
		VALUES ($1, $2, $3, $4, $5)`, uid, action, entity, entityID, b); err != nil {
		s.log.Error("audit log failed", "err", err)
	}
}

// reload applies configuration changes to the running engine.
func (s *Server) reload(ctx context.Context) {
	if err := s.eng.Reload(ctx); err != nil {
		s.log.Error("reload after change failed", "err", err)
	}
}
