// Package engine is the messaging core: it accepts messages (SMPP server and HTTP API), prices and routes
// them, charges the client, sends them to vendors, matches DLRs and forwards them to clients.
package engine

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Adnan-Dogar/telivoz-gateway/internal/auth"
	"github.com/Adnan-Dogar/telivoz-gateway/internal/routing"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Engine struct {
	db     *pgxpool.Pool
	cipher *auth.Cipher
	log    *slog.Logger
	Stats  *Stats

	snap atomic.Pointer[routing.Snapshot]

	limMu    sync.Mutex
	limiters map[int64]*tokenBucket // per client account

	clients *registry // bound client SMPP sessions

	vmu     sync.Mutex
	vendors map[int64]*vendorConn

	reloadMu      sync.Mutex
	reloadPending atomic.Bool
	ctx           context.Context

	// VendorResponseTimeout is how long to wait for a vendor's submit_sm_resp before giving up (without
	// resending). Defaults to 30s.
	VendorResponseTimeout time.Duration
}

func New(db *pgxpool.Pool, cipher *auth.Cipher, log *slog.Logger) *Engine {
	return &Engine{
		db: db, cipher: cipher, log: log, Stats: newStats(),
		limiters: map[int64]*tokenBucket{}, clients: newRegistry(), vendors: map[int64]*vendorConn{},
		VendorResponseTimeout: 30 * time.Second,
	}
}

// Start loads configuration, connects to vendors and starts the background loops.
func (e *Engine) Start(ctx context.Context) error {
	e.ctx = ctx
	if err := e.Reload(ctx); err != nil {
		return err
	}
	go e.every(ctx, 5*time.Second, func() { _ = e.Stats.Flush(ctx, e.db) })
	go e.every(ctx, 60*time.Second, func() {
		if err := e.Reload(ctx); err != nil {
			e.log.Error("periodic reload failed", "err", err)
		}
	})
	go e.every(ctx, 5*time.Second, func() { e.retryDLRs(ctx, 0) })
	go e.every(ctx, time.Hour, func() { e.maintenance(ctx) })
	e.maintenance(ctx)
	return nil
}

// Stop flushes statistics and unbinds from vendors.
func (e *Engine) Stop() {
	e.vmu.Lock()
	for _, v := range e.vendors {
		v.stop()
	}
	e.vmu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = e.Stats.Flush(ctx, e.db)
}

func (e *Engine) every(ctx context.Context, d time.Duration, fn func()) {
	t := time.NewTicker(d)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			fn()
		}
	}
}

// Snapshot returns the current routing snapshot.
func (e *Engine) Snapshot() *routing.Snapshot { return e.snap.Load() }

// Reload rebuilds the routing snapshot from the database and starts, stops or restarts vendor connections
// to match. Called after every configuration change and periodically; never needs a process restart.
func (e *Engine) Reload(ctx context.Context) error {
	e.reloadMu.Lock()
	defer e.reloadMu.Unlock()
	start := time.Now()
	snap, err := e.loadSnapshot(ctx)
	if err != nil {
		return fmt.Errorf("load routing snapshot: %w", err)
	}
	e.snap.Store(snap)
	if err := e.syncVendors(ctx); err != nil {
		return fmt.Errorf("sync vendor connections: %w", err)
	}
	e.log.Debug("configuration reloaded", "took", time.Since(start))
	return nil
}

func (e *Engine) loadSnapshot(ctx context.Context) (*routing.Snapshot, error) {
	b := routing.NewBuilder()

	rows, err := e.db.Query(ctx, `SELECT iso, dial_code FROM countries`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var iso, code string
		if err := rows.Scan(&iso, &code); err != nil {
			return nil, err
		}
		b.DialCode(code, iso)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	rows, err = e.db.Query(ctx, `SELECT id, COALESCE(country_iso, ''), mcc, mnc, name FROM networks`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var n routing.Network
		if err := rows.Scan(&n.ID, &n.CountryISO, &n.MCC, &n.MNC, &n.Name); err != nil {
			return nil, err
		}
		b.Network(n)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	rows, err = e.db.Query(ctx, `SELECT prefix, network_id FROM number_prefixes`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var p string
		var id int64
		if err := rows.Scan(&p, &id); err != nil {
			return nil, err
		}
		b.Prefix(p, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Current rate per key: the newest one already in effect.
	rows, err = e.db.Query(ctx, `SELECT DISTINCT ON (client_id, country_iso, COALESCE(network_id, 0))
			client_id, country_iso, COALESCE(network_id, 0), price::text
		FROM client_rates WHERE effective_from <= now()
		ORDER BY client_id, country_iso, COALESCE(network_id, 0), effective_from DESC`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var owner, net int64
		var iso, price string
		if err := rows.Scan(&owner, &iso, &net, &price); err != nil {
			return nil, err
		}
		p, _ := routing.ParseMicros(price)
		b.ClientRate(owner, iso, net, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	rows, err = e.db.Query(ctx, `SELECT DISTINCT ON (connection_id, country_iso, COALESCE(network_id, 0))
			connection_id, country_iso, COALESCE(network_id, 0), price::text
		FROM vendor_rates WHERE effective_from <= now()
		ORDER BY connection_id, country_iso, COALESCE(network_id, 0), effective_from DESC`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var owner, net int64
		var iso, price string
		if err := rows.Scan(&owner, &iso, &net, &price); err != nil {
			return nil, err
		}
		p, _ := routing.ParseMicros(price)
		b.VendorRate(owner, iso, net, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	rows, err = e.db.Query(ctx, `SELECT c.id, c.vendor_id, c.name, c.status = 'enabled' AND v.status = 'active', c.max_attempts
		FROM connections c JOIN vendors v ON v.id = c.vendor_id`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var c routing.Connection
		if err := rows.Scan(&c.ID, &c.VendorID, &c.Name, &c.Enabled, &c.MaxAttempts); err != nil {
			return nil, err
		}
		b.Connection(c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	rows, err = e.db.Query(ctx, `SELECT r.id, r.name, r.priority, COALESCE(r.client_id, 0), COALESCE(r.account_id, 0),
			COALESCE(r.country_iso, ''), COALESCE(r.network_id, 0), r.sender_match, r.sender_pattern, r.policy, r.allow_loss,
			COALESCE(array_agg(t.connection_id ORDER BY t.position) FILTER (WHERE t.connection_id IS NOT NULL), '{}'),
			COALESCE(array_agg(t.position ORDER BY t.position) FILTER (WHERE t.connection_id IS NOT NULL), '{}'),
			COALESCE(array_agg(t.weight ORDER BY t.position) FILTER (WHERE t.connection_id IS NOT NULL), '{}')
		FROM routes r LEFT JOIN route_targets t ON t.route_id = r.id
		WHERE r.status = 'active' GROUP BY r.id`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var r routing.Route
		var conns []int64
		var pos, weights []int32
		if err := rows.Scan(&r.ID, &r.Name, &r.Priority, &r.ClientID, &r.AccountID, &r.CountryISO, &r.NetworkID,
			&r.SenderMatch, &r.SenderPattern, &r.Policy, &r.AllowLoss, &conns, &pos, &weights); err != nil {
			return nil, err
		}
		for i := range conns {
			r.Targets = append(r.Targets, routing.Target{ConnectionID: conns[i], Position: int(pos[i]), Weight: int(weights[i])})
		}
		if err := b.Route(r); err != nil {
			e.log.Error("route skipped", "err", err)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	rows, err = e.db.Query(ctx, `SELECT id, name, priority, COALESCE(client_id, 0), COALESCE(country_iso, ''),
			sender_pattern, text_pattern, action, find, replace_with
		FROM content_rules WHERE status = 'active'`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var c routing.ContentRule
		if err := rows.Scan(&c.ID, &c.Name, &c.Priority, &c.ClientID, &c.CountryISO, &c.SenderPattern, &c.TextPattern,
			&c.Action, &c.Find, &c.ReplaceWith); err != nil {
			return nil, err
		}
		if err := b.ContentRule(c); err != nil {
			e.log.Error("content rule skipped", "err", err)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows, err = e.db.Query(ctx, `SELECT COALESCE(client_id, 0), number FROM blacklist`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var cid int64
		var n string
		if err := rows.Scan(&cid, &n); err != nil {
			return nil, err
		}
		b.Blacklist(cid, n)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Delivery quality per connection and country over the last 24 hours (refreshed with every reload).
	rows, err = e.db.Query(ctx, `SELECT connection_id, country_iso, sum(delivered), sum(delivered + undelivered),
			COALESCE(sum(dlr_latency_ms_sum) / NULLIF(sum(dlr_latency_count), 0), 0)::bigint
		FROM stats_hourly WHERE hour >= now() - interval '24 hours' AND connection_id <> 0 AND country_iso <> ''
		GROUP BY 1, 2`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var q routing.Quality
		if err := rows.Scan(&q.ConnectionID, &q.CountryISO, &q.Delivered, &q.Final, &q.AvgDLRMs); err != nil {
			return nil, err
		}
		b.Quality(q)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	rows, err = e.db.Query(ctx, `SELECT id, name, priority, number_prefix, keyword, client_id, COALESCE(account_id, 0), auto_opt_out
		FROM mo_routes WHERE status = 'active'`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var r routing.MORoute
		if err := rows.Scan(&r.ID, &r.Name, &r.Priority, &r.NumberPrefix, &r.Keyword, &r.ClientID, &r.AccountID, &r.AutoOptOut); err != nil {
			return nil, err
		}
		b.MORoute(r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return b.Build(), nil
}

// requestReload reloads configuration soon, coalescing bursts of changes (e.g. many opt-outs) into one reload.
func (e *Engine) requestReload() {
	if !e.reloadPending.CompareAndSwap(false, true) {
		return
	}
	time.AfterFunc(2*time.Second, func() {
		e.reloadPending.Store(false)
		if e.ctx == nil {
			return
		}
		if err := e.Reload(e.ctx); err != nil {
			e.log.Error("reload failed", "err", err)
		}
	})
}

func (e *Engine) limiter(accountID int64, tps int) *tokenBucket {
	e.limMu.Lock()
	defer e.limMu.Unlock()
	l := e.limiters[accountID]
	if l == nil {
		l = newTokenBucket(tps)
		e.limiters[accountID] = l
	} else {
		l.setRate(tps)
	}
	return l
}

// maintenance creates upcoming message partitions and removes expired bookkeeping rows.
func (e *Engine) maintenance(ctx context.Context) {
	stmts := []string{
		`SELECT ensure_messages_partition(now())`,
		`SELECT ensure_messages_partition(now() + INTERVAL '1 month')`,
		`DELETE FROM vendor_message_ids WHERE created_at < now() - INTERVAL '7 days'`,
		`DELETE FROM idempotency_keys WHERE created_at < now() - INTERVAL '7 days'`,
		`DELETE FROM sessions WHERE expires_at < now()`,
		`DELETE FROM dlr_outbox WHERE expires_at < now()`,
	}
	for _, s := range stmts {
		if _, err := e.db.Exec(ctx, s); err != nil {
			e.log.Error("maintenance step failed", "sql", s, "err", err)
		}
	}
}
