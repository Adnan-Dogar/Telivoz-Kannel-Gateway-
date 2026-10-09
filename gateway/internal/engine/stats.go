package engine

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/Adnan-Dogar/telivoz-gateway/internal/routing"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// statKey is one row of stats_hourly.
type statKey struct {
	hour         time.Time
	clientID     int64
	connectionID int64
	country      string
	networkID    int64
}

type statRow struct {
	submitted, rejected, sent, failed, delivered, undelivered, parts int64
	revenue, cost                                                    routing.Micros
	latencySum, latencyCount                                         int64
}

const liveWindow = 120 // seconds of per-second history kept for live charts

// Stats aggregates counters in memory: per-second series for the live dashboard, and hourly rows that are
// flushed to stats_hourly every few seconds.
type Stats struct {
	mu      sync.Mutex
	pending map[statKey]*statRow
	// per-second ring buffers, indexed by unix second % liveWindow
	secIn, secOut, secDLR [liveWindow]int64
	secStamp             [liveWindow]int64
}

func newStats() *Stats { return &Stats{pending: map[statKey]*statRow{}} }

func (s *Stats) row(clientID, connectionID int64, country string, networkID int64) *statRow {
	k := statKey{time.Now().UTC().Truncate(time.Hour), clientID, connectionID, country, networkID}
	r := s.pending[k]
	if r == nil {
		r = &statRow{}
		s.pending[k] = r
	}
	return r
}

func (s *Stats) tick(series *[liveWindow]int64, n int64) {
	now := time.Now().Unix()
	i := now % liveWindow
	if s.secStamp[i] != now {
		s.secStamp[i] = now
		s.secIn[i], s.secOut[i], s.secDLR[i] = 0, 0, 0
	}
	series[i] += n
}

func (s *Stats) Submitted(clientID int64, country string, networkID int64, parts int, revenue routing.Micros) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.row(clientID, 0, country, networkID)
	r.submitted++
	r.parts += int64(parts)
	r.revenue += revenue
	s.tick(&s.secIn, 1)
}

func (s *Stats) Rejected(clientID int64, country string, networkID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.row(clientID, 0, country, networkID).rejected++
	s.tick(&s.secIn, 1)
}

func (s *Stats) Sent(clientID, connectionID int64, country string, networkID int64, cost routing.Micros) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.row(clientID, connectionID, country, networkID)
	r.sent++
	r.cost += cost
	s.tick(&s.secOut, 1)
}

// Failed records a message that no vendor accepted; its charge was refunded.
func (s *Stats) Failed(clientID, connectionID int64, country string, networkID int64, refund routing.Micros) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.row(clientID, connectionID, country, networkID)
	r.failed++
	r.revenue -= refund
}

func (s *Stats) DLR(clientID, connectionID int64, country string, networkID int64, delivered bool, latency time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.row(clientID, connectionID, country, networkID)
	if delivered {
		r.delivered++
	} else {
		r.undelivered++
	}
	if latency > 0 {
		r.latencySum += latency.Milliseconds()
		r.latencyCount++
	}
	s.tick(&s.secDLR, 1)
}

// LivePoint is one second of live traffic.
type LivePoint struct {
	T   int64 `json:"t"`
	In  int64 `json:"in"`
	Out int64 `json:"out"`
	DLR int64 `json:"dlr"`
}

// Live returns the last `seconds` seconds of traffic, oldest first (the current second is excluded).
func (s *Stats) Live(seconds int) []LivePoint {
	if seconds > liveWindow-1 {
		seconds = liveWindow - 1
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().Unix()
	out := make([]LivePoint, 0, seconds)
	for t := now - int64(seconds); t < now; t++ {
		i := t % liveWindow
		p := LivePoint{T: t}
		if s.secStamp[i] == t {
			p.In, p.Out, p.DLR = s.secIn[i], s.secOut[i], s.secDLR[i]
		}
		out = append(out, p)
	}
	return out
}

func (s *Stats) take() map[statKey]*statRow {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.pending
	s.pending = map[statKey]*statRow{}
	return p
}

func (s *Stats) restore(p map[statKey]*statRow) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, v := range p {
		r := s.pending[k]
		if r == nil {
			s.pending[k] = v
			continue
		}
		r.submitted += v.submitted
		r.rejected += v.rejected
		r.sent += v.sent
		r.failed += v.failed
		r.delivered += v.delivered
		r.undelivered += v.undelivered
		r.parts += v.parts
		r.revenue += v.revenue
		r.cost += v.cost
		r.latencySum += v.latencySum
		r.latencyCount += v.latencyCount
	}
}

// Flush adds the pending counters to stats_hourly. On failure they are kept for the next attempt.
func (s *Stats) Flush(ctx context.Context, db *pgxpool.Pool) error {
	p := s.take()
	if len(p) == 0 {
		return nil
	}
	batch := &pgx.Batch{}
	for k, r := range p {
		batch.Queue(`INSERT INTO stats_hourly AS s (hour, client_id, connection_id, country_iso, network_id, submitted, rejected,
				sent, failed, delivered, undelivered, parts, revenue, cost, dlr_latency_ms_sum, dlr_latency_count)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13::numeric,$14::numeric,$15,$16)
			ON CONFLICT (hour, client_id, connection_id, country_iso, network_id) DO UPDATE SET
				submitted = s.submitted + EXCLUDED.submitted, rejected = s.rejected + EXCLUDED.rejected,
				sent = s.sent + EXCLUDED.sent, failed = s.failed + EXCLUDED.failed,
				delivered = s.delivered + EXCLUDED.delivered, undelivered = s.undelivered + EXCLUDED.undelivered,
				parts = s.parts + EXCLUDED.parts, revenue = s.revenue + EXCLUDED.revenue, cost = s.cost + EXCLUDED.cost,
				dlr_latency_ms_sum = s.dlr_latency_ms_sum + EXCLUDED.dlr_latency_ms_sum,
				dlr_latency_count = s.dlr_latency_count + EXCLUDED.dlr_latency_count`,
			k.hour, k.clientID, k.connectionID, k.country, k.networkID, r.submitted, r.rejected, r.sent, r.failed,
			r.delivered, r.undelivered, r.parts, r.revenue.String(), r.cost.String(), r.latencySum, r.latencyCount)
	}
	err := pgx.BeginFunc(ctx, db, func(tx pgx.Tx) error { return tx.SendBatch(ctx, batch).Close() })
	if err != nil {
		s.restore(p)
		slog.Warn("stats flush failed, will retry", "err", err)
	}
	return err
}
