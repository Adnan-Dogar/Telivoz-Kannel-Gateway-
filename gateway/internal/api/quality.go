package api

import (
	"net/http"

	"github.com/Adnan-Dogar/telivoz-gateway/internal/routing"
)

// vendorQuality lists each vendor connection's delivery quality per country over the last 24 hours, with the
// score that the "quality" and "balanced" route policies use.
func (s *Server) vendorQuality(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	if p.IsClient() {
		writeError(w, http.StatusForbidden, "forbidden", "not allowed")
		return
	}
	sc := s.scopeFor(r.Context(), p)
	rows, err := s.db.Query(r.Context(), `SELECT s.connection_id, cn.name, s.country_iso, COALESCE(co.name, s.country_iso),
			sum(s.sent)::bigint, sum(s.delivered)::bigint, sum(s.delivered + s.undelivered)::bigint, sum(s.failed)::bigint,
			COALESCE(sum(s.dlr_latency_ms_sum) / NULLIF(sum(s.dlr_latency_count), 0), 0)::bigint,
			COALESCE(sum(s.cost) / NULLIF(sum(s.sent), 0), 0)::float8
		FROM stats_hourly s JOIN connections cn ON cn.id = s.connection_id LEFT JOIN countries co ON co.iso = s.country_iso
		WHERE s.hour >= now() - interval '24 hours' AND s.connection_id <> 0 AND s.country_iso <> ''
		  AND ($1 OR cn.vendor_id = ANY($2))
		GROUP BY 1, 2, 3, 4 ORDER BY 3, 1`, sc.allVendors, sc.vendorIDs)
	if err != nil {
		s.dbError(w, err)
		return
	}
	defer rows.Close()
	type row struct {
		ConnectionID   int64   `json:"connection_id"`
		ConnectionName string  `json:"connection_name"`
		CountryISO     string  `json:"country_iso"`
		Country        string  `json:"country"`
		Sent           int64   `json:"sent"`
		Delivered      int64   `json:"delivered"`
		Final          int64   `json:"final"`
		Failed         int64   `json:"failed"`
		AvgDLRMs       int64   `json:"avg_dlr_ms"`
		AvgCost        float64 `json:"avg_cost"`
		DLRRate        float64 `json:"dlr_rate"`
		Score          float64 `json:"score"`
		Trusted        bool    `json:"trusted"` // enough DLRs for the score to count
	}
	out := []row{}
	for rows.Next() {
		var x row
		if err := rows.Scan(&x.ConnectionID, &x.ConnectionName, &x.CountryISO, &x.Country, &x.Sent, &x.Delivered, &x.Final,
			&x.Failed, &x.AvgDLRMs, &x.AvgCost); err != nil {
			s.dbError(w, err)
			return
		}
		if x.Final > 0 {
			x.DLRRate = float64(x.Delivered) / float64(x.Final)
		}
		x.Score = routing.QualityScore(x.Delivered, x.Final, x.AvgDLRMs)
		x.Trusted = x.Final >= routing.MinQualitySamples
		out = append(out, x)
	}
	if err := rows.Err(); err != nil {
		s.dbError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"rows": out, "min_samples": routing.MinQualitySamples,
		"balanced_tolerance": routing.BalancedTolerance})
}
