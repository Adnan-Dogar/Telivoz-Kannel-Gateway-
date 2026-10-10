package api

import (
	"net"
	"net/http"
	"strings"
)

// branding returns the portal name, logo and colour for the domain the portal is opened on (a reseller's
// own domain), falling back to the default row (domain ”) and then to built-in values. Public: the login
// page needs it before anyone signs in.
func (s *Server) branding(w http.ResponseWriter, r *http.Request) {
	host := strings.ToLower(r.Host)
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	var raw []byte
	err := s.db.QueryRow(r.Context(), `SELECT jsonb_build_object('name', name, 'tagline', COALESCE(tagline, ''),
			'logo', COALESCE(logo, ''), 'primary_color', COALESCE(primary_color, ''), 'support_email', COALESCE(support_email, ''))
		FROM branding WHERE COALESCE(domain, '') IN ($1, '') ORDER BY COALESCE(domain, '') DESC LIMIT 1`, host).Scan(&raw)
	if err != nil {
		raw = []byte(`{"name":"Telivoz","tagline":"SMS Gateway","logo":"","primary_color":"","support_email":""}`)
	}
	w.Header().Set("Cache-Control", "public, max-age=300")
	writeRaw(w, http.StatusOK, raw)
}
