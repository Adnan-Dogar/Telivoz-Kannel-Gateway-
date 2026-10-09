package api

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

// statsScope returns a condition on stats_hourly (alias s) for the user's visible clients/vendors.
func (s *Server) statsScope(r *http.Request) (string, []any) {
	sc := s.scopeFor(r.Context(), principal(r))
	switch {
	case sc.allClients:
		return "true", nil
	case principal(r).IsClient():
		return "s.client_id = ANY($1)", []any{sc.clientIDs}
	default:
		// Team members see their clients' traffic and their vendors' traffic.
		return `(s.client_id = ANY($1) OR s.connection_id IN (SELECT id FROM connections WHERE vendor_id = ANY($2)))`,
			[]any{sc.clientIDs, sc.vendorIDs}
	}
}

func period(r *http.Request) (time.Time, time.Time) {
	to := queryTime(r, "to", time.Now())
	from := queryTime(r, "from", to.Add(-24*time.Hour))
	return from, to
}

// statsLive: last two minutes per second, connection and bind status, queue depth.
func (s *Server) statsLive(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	out := map[string]any{"series": s.eng.Stats.Live(120)}
	if !p.IsClient() {
		var queued, outbox int64
		_ = s.db.QueryRow(r.Context(), `SELECT count(*) FROM send_queue`).Scan(&queued)
		_ = s.db.QueryRow(r.Context(), `SELECT count(*) FROM dlr_outbox`).Scan(&outbox)
		out["queue"] = queued
		out["dlr_outbox"] = outbox
		out["connections"] = s.eng.ConnectionStatuses()
		out["client_binds"] = s.eng.BoundSessions()
	}
	writeJSON(w, http.StatusOK, out)
}

// statsOverview: totals for the period, the previous period for comparison, and a time series.
func (s *Server) statsOverview(w http.ResponseWriter, r *http.Request) {
	from, to := period(r)
	bucket := "hour"
	switch span := to.Sub(from); {
	case span > 400*24*time.Hour:
		bucket = "month"
	case span > 72*time.Hour:
		bucket = "day"
	}
	cond, args := s.statsScope(r)
	n := len(args)
	args = append(args, from, to, from.Add(-to.Sub(from)))
	totals := `jsonb_build_object('submitted', COALESCE(sum(submitted),0), 'rejected', COALESCE(sum(rejected),0),
		'sent', COALESCE(sum(sent),0), 'failed', COALESCE(sum(failed),0), 'delivered', COALESCE(sum(delivered),0),
		'undelivered', COALESCE(sum(undelivered),0), 'parts', COALESCE(sum(parts),0),
		'revenue', COALESCE(sum(revenue),0), 'cost', COALESCE(sum(cost),0),
		'avg_dlr_ms', COALESCE(sum(dlr_latency_ms_sum) / NULLIF(sum(dlr_latency_count),0), 0))`
	sql := fmt.Sprintf(`SELECT jsonb_build_object(
		'from', $%[2]d::timestamptz, 'to', $%[3]d::timestamptz, 'bucket', '%[4]s',
		'totals', (SELECT %[5]s FROM stats_hourly s WHERE %[1]s AND hour >= date_trunc('hour', $%[2]d::timestamptz) AND hour < $%[3]d),
		'previous', (SELECT %[5]s FROM stats_hourly s WHERE %[1]s AND hour >= date_trunc('hour', $%[6]d::timestamptz) AND hour < date_trunc('hour', $%[2]d::timestamptz)),
		'series', COALESCE((SELECT jsonb_agg(jsonb_build_object('t', b.t, 'submitted', COALESCE(x.submitted, 0), 'sent', COALESCE(x.sent, 0),
				'delivered', COALESCE(x.delivered, 0), 'undelivered', COALESCE(x.undelivered, 0), 'failed', COALESCE(x.failed, 0),
				'revenue', COALESCE(x.revenue, 0), 'cost', COALESCE(x.cost, 0)) ORDER BY b.t)
			FROM generate_series(date_trunc('%[4]s', GREATEST($%[2]d::timestamptz,
					COALESCE((SELECT min(hour) FROM stats_hourly s WHERE %[1]s), $%[2]d::timestamptz))),
				date_trunc('%[4]s', $%[3]d::timestamptz - interval '1 second'), interval '1 %[4]s') AS b(t)
			LEFT JOIN (
				SELECT date_trunc('%[4]s', hour) AS t, sum(submitted) AS submitted, sum(sent) AS sent, sum(delivered) AS delivered,
					sum(undelivered) AS undelivered, sum(failed + rejected) AS failed, sum(revenue) AS revenue, sum(cost) AS cost
				FROM stats_hourly s WHERE %[1]s AND hour >= date_trunc('hour', $%[2]d::timestamptz) AND hour < $%[3]d
				GROUP BY 1) x ON x.t = b.t), '[]'))`, cond, n+1, n+2, bucket, totals, n+3)
	var raw []byte
	if err := s.db.QueryRow(r.Context(), sql, args...).Scan(&raw); err != nil {
		s.dbError(w, err)
		return
	}
	writeRaw(w, http.StatusOK, raw)
}

var breakdownDims = map[string]struct{ key, label string }{
	"client":     {"s.client_id", "(SELECT name FROM clients c WHERE c.id = s.client_id)"},
	"connection": {"s.connection_id", "(SELECT name FROM connections c WHERE c.id = s.connection_id)"},
	"vendor":     {"(SELECT vendor_id FROM connections c WHERE c.id = s.connection_id)", "(SELECT v.name FROM connections c JOIN vendors v ON v.id = c.vendor_id WHERE c.id = s.connection_id)"},
	"country":    {"s.country_iso", "(SELECT name FROM countries c WHERE c.iso = s.country_iso)"},
	"network":    {"s.network_id", "(SELECT n.name || ' (' || n.mcc || '-' || n.mnc || ')' FROM networks n WHERE n.id = s.network_id)"},
	"owner":      {"(SELECT owner_id FROM clients c WHERE c.id = s.client_id)", "(SELECT u.name FROM clients c JOIN users u ON u.id = c.owner_id WHERE c.id = s.client_id)"},
}

// statsBreakdown groups the period by a dimension: client, connection, vendor, country, network or owner
// (account manager). ?format=csv downloads it.
func (s *Server) statsBreakdown(w http.ResponseWriter, r *http.Request) {
	dimName := r.URL.Query().Get("dim")
	dim, ok := breakdownDims[dimName]
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid", "dim must be client, connection, vendor, country, network or owner")
		return
	}
	if principal(r).IsClient() && dimName != "country" && dimName != "network" {
		writeError(w, http.StatusForbidden, "forbidden", "not allowed")
		return
	}
	from, to := period(r)
	cond, args := s.statsScope(r)
	n := len(args)
	args = append(args, from, to)
	sql := fmt.Sprintf(`SELECT k, label, submitted, sent, delivered, undelivered, failed, revenue, cost, avg_dlr_ms FROM (
		SELECT (%[1]s)::text AS k, min(%[2]s) AS label, sum(submitted) AS submitted, sum(sent) AS sent, sum(delivered) AS delivered,
			sum(undelivered) AS undelivered, sum(failed + rejected) AS failed, sum(revenue) AS revenue, sum(cost) AS cost,
			COALESCE(round(sum(dlr_latency_ms_sum)::numeric / NULLIF(sum(dlr_latency_count), 0))::bigint, 0) AS avg_dlr_ms
		FROM stats_hourly s WHERE %[3]s AND %[6]s AND hour >= date_trunc('hour', $%[4]d::timestamptz) AND hour < $%[5]d
		GROUP BY 1) x WHERE submitted + sent + delivered + undelivered + failed > 0 ORDER BY submitted DESC, sent DESC LIMIT 500`,
		dim.key, dim.label, cond, n+1, n+2, map[bool]string{true: "s.connection_id <> 0", false: "true"}[dimName == "vendor" || dimName == "connection"])
	rows, err := s.db.Query(r.Context(), sql, args...)
	if err != nil {
		s.dbError(w, err)
		return
	}
	defer rows.Close()
	type row struct {
		Key         string  `json:"key"`
		Label       string  `json:"label"`
		Submitted   int64   `json:"submitted"`
		Sent        int64   `json:"sent"`
		Delivered   int64   `json:"delivered"`
		Undelivered int64   `json:"undelivered"`
		Failed      int64   `json:"failed"`
		Revenue     float64 `json:"revenue"`
		Cost        float64 `json:"cost"`
		Margin      float64 `json:"margin"`
		DLRRate     float64 `json:"dlr_rate"`
		AvgDLRMs    int64   `json:"avg_dlr_ms"`
	}
	var out []row
	for rows.Next() {
		var x row
		var key, label *string
		if err := rows.Scan(&key, &label, &x.Submitted, &x.Sent, &x.Delivered, &x.Undelivered, &x.Failed, &x.Revenue, &x.Cost, &x.AvgDLRMs); err != nil {
			s.dbError(w, err)
			return
		}
		if key != nil {
			x.Key = *key
		}
		if label != nil {
			x.Label = *label
		}
		if x.Label == "" {
			x.Label = map[bool]string{true: "Unknown", false: x.Key}[x.Key == "" || x.Key == "0"]
		}
		x.Margin = x.Revenue - x.Cost
		if final := x.Delivered + x.Undelivered; final > 0 {
			x.DLRRate = float64(x.Delivered) / float64(final)
		}
		out = append(out, x)
	}
	if err := rows.Err(); err != nil {
		s.dbError(w, err)
		return
	}
	if r.URL.Query().Get("format") == "csv" {
		w.Header().Set("Content-Type", "text/csv")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="report-%s-%s.csv"`, dimName, from.Format("20060102")))
		cw := csv.NewWriter(w)
		_ = cw.Write([]string{dimName, "submitted", "sent", "delivered", "undelivered", "failed", "delivery_rate", "revenue", "cost", "margin", "avg_dlr_ms"})
		for _, x := range out {
			_ = cw.Write([]string{x.Label, strconv.FormatInt(x.Submitted, 10), strconv.FormatInt(x.Sent, 10),
				strconv.FormatInt(x.Delivered, 10), strconv.FormatInt(x.Undelivered, 10), strconv.FormatInt(x.Failed, 10),
				strconv.FormatFloat(x.DLRRate*100, 'f', 1, 64) + "%", strconv.FormatFloat(x.Revenue, 'f', 4, 64),
				strconv.FormatFloat(x.Cost, 'f', 4, 64), strconv.FormatFloat(x.Margin, 'f', 4, 64), strconv.FormatInt(x.AvgDLRMs, 10)})
		}
		cw.Flush()
		return
	}
	if out == nil {
		out = []row{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"dim": dimName, "rows": out})
}
