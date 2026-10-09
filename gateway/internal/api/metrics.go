package api

import (
	"context"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// metricsHandler exposes Prometheus metrics: traffic rates, queue sizes and vendor bind states.
func (s *Server) metricsHandler() http.Handler {
	reg := prometheus.NewRegistry()
	reg.MustRegister(prometheus.NewGoCollector(), prometheus.NewProcessCollector(prometheus.ProcessCollectorOpts{}))
	rate := func(name, help string, pick func(in, out, dlr int64) int64) prometheus.GaugeFunc {
		return prometheus.NewGaugeFunc(prometheus.GaugeOpts{Name: name, Help: help}, func() float64 {
			var sum int64
			pts := s.eng.Stats.Live(10)
			for _, p := range pts {
				sum += pick(p.In, p.Out, p.DLR)
			}
			return float64(sum) / float64(max(len(pts), 1))
		})
	}
	count := func(name, help, sql string) prometheus.GaugeFunc {
		return prometheus.NewGaugeFunc(prometheus.GaugeOpts{Name: name, Help: help}, func() float64 {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			var n int64
			_ = s.db.QueryRow(ctx, sql).Scan(&n)
			return float64(n)
		})
	}
	reg.MustRegister(
		rate("gateway_tps_in", "Messages received per second (10s average)", func(in, _, _ int64) int64 { return in }),
		rate("gateway_tps_out", "Messages sent to vendors per second (10s average)", func(_, out, _ int64) int64 { return out }),
		rate("gateway_dlr_per_second", "DLRs received per second (10s average)", func(_, _, d int64) int64 { return d }),
		count("gateway_send_queue", "Messages waiting for a vendor", `SELECT count(*) FROM send_queue`),
		count("gateway_dlr_outbox", "DLRs waiting for the client", `SELECT count(*) FROM dlr_outbox`),
		prometheus.NewGaugeFunc(prometheus.GaugeOpts{Name: "gateway_vendor_binds_down", Help: "Vendor binds not bound"}, func() float64 {
			down := 0
			for _, c := range s.eng.ConnectionStatuses() {
				for _, b := range c.Binds {
					if b.State != "bound" {
						down++
					}
				}
			}
			return float64(down)
		}),
		prometheus.NewGaugeFunc(prometheus.GaugeOpts{Name: "gateway_client_binds", Help: "Bound client SMPP sessions"}, func() float64 {
			return float64(len(s.eng.BoundSessions()))
		}),
	)
	return promhttp.HandlerFor(reg, promhttp.HandlerOpts{})
}
