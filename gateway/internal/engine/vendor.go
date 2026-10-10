package engine

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Adnan-Dogar/telivoz-gateway/internal/gsm"
	"github.com/Adnan-Dogar/telivoz-gateway/internal/routing"
	"github.com/Adnan-Dogar/telivoz-gateway/internal/smpp"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// connConfig is a vendor connection row.
type connConfig struct {
	ID          int64
	VendorID    int64
	Name        string
	Host        string
	Port        int
	SystemID    string
	Password    string
	SystemType  string
	BindMode    string
	Binds       int
	TPS         int
	Window      int
	SourceTon   byte
	SourceNpi   byte
	DestTon     byte
	DestNpi     byte
	DLRIDFormat string
	MaxAttempts int
	Enabled     bool
	// DLR statuses ("UNDELIV") or "STAT:ERR" pairs that trigger a resend through the next vendor.
	FailoverOnDLR []string
}

func (c connConfig) bindKey() string {
	// Changing any of these needs new binds; TPS changes apply live.
	return fmt.Sprintf("%s|%d|%s|%s|%s|%s|%d|%d", c.Host, c.Port, c.SystemID, c.Password, c.SystemType, c.BindMode, c.Binds, c.Window)
}

// BindStatus is the live state of one bind, shown in the portal.
type BindStatus struct {
	Index int       `json:"index"`
	State string    `json:"state"` // connecting, bound, down
	Since time.Time `json:"since"`
	Error string    `json:"error,omitempty"`
}

// ConnectionStatus is the live state of a vendor connection.
type ConnectionStatus struct {
	ID       int64        `json:"id"`
	Name     string       `json:"name"`
	Enabled  bool         `json:"enabled"`
	Binds    []BindStatus `json:"binds"`
	Sent     int64        `json:"sent"`
	Errors   int64        `json:"errors"`
	InFlight int64        `json:"in_flight"`
}

type bindSlot struct {
	mu      sync.Mutex
	session *smpp.Session
	status  BindStatus
}

type vendorConn struct {
	e       *Engine
	cfg     connConfig
	key     string
	ctx     context.Context
	cancel  context.CancelFunc
	slots   []*bindSlot
	limiter *tokenBucket
	wakeCh  chan struct{}
	rr      atomic.Uint64
	sent    atomic.Int64
	errs    atomic.Int64
	flight  atomic.Int64
	wg      sync.WaitGroup
}

// syncVendors starts, stops or restarts connection workers to match the database.
func (e *Engine) syncVendors(ctx context.Context) error {
	rows, err := e.db.Query(ctx, `SELECT c.id, c.vendor_id, c.name, c.host, c.port, c.system_id, c.password_enc, c.system_type,
			c.bind_mode, c.binds, c.tps, c.window_size, c.source_ton, c.source_npi, c.dest_ton, c.dest_npi, c.dlr_id_format,
			c.max_attempts, c.status = 'enabled' AND v.status = 'active', c.failover_on_dlr
		FROM connections c JOIN vendors v ON v.id = c.vendor_id`)
	if err != nil {
		return err
	}
	want := map[int64]connConfig{}
	for rows.Next() {
		var c connConfig
		var enc string
		var st, sn, dt, dn int16
		if err := rows.Scan(&c.ID, &c.VendorID, &c.Name, &c.Host, &c.Port, &c.SystemID, &enc, &c.SystemType, &c.BindMode,
			&c.Binds, &c.TPS, &c.Window, &st, &sn, &dt, &dn, &c.DLRIDFormat, &c.MaxAttempts, &c.Enabled, &c.FailoverOnDLR); err != nil {
			rows.Close()
			return err
		}
		c.SourceTon, c.SourceNpi, c.DestTon, c.DestNpi = byte(st), byte(sn), byte(dt), byte(dn)
		if c.Password, err = e.cipher.Decrypt(enc); err != nil {
			e.log.Error("cannot decrypt vendor password", "connection", c.Name, "err", err)
			continue
		}
		want[c.ID] = c
	}
	if err := rows.Err(); err != nil {
		return err
	}

	e.vmu.Lock()
	defer e.vmu.Unlock()
	for id, v := range e.vendors {
		c, ok := want[id]
		if !ok || !c.Enabled || c.bindKey() != v.key {
			v.stop()
			delete(e.vendors, id)
			continue
		}
		v.cfg = c
		v.limiter.setRate(c.TPS)
	}
	for id, c := range want {
		if !c.Enabled {
			continue
		}
		if _, running := e.vendors[id]; running {
			continue
		}
		v := e.newVendorConn(c)
		e.vendors[id] = v
		v.start()
	}
	return nil
}

func (e *Engine) newVendorConn(c connConfig) *vendorConn {
	ctx, cancel := context.WithCancel(e.ctx)
	v := &vendorConn{e: e, cfg: c, key: c.bindKey(), ctx: ctx, cancel: cancel, limiter: newTokenBucket(c.TPS),
		wakeCh: make(chan struct{}, 1)}
	for i := 0; i < c.Binds; i++ {
		v.slots = append(v.slots, &bindSlot{status: BindStatus{Index: i, State: "connecting", Since: time.Now()}})
	}
	return v
}

func (e *Engine) wake(connectionID int64) {
	e.vmu.Lock()
	v := e.vendors[connectionID]
	e.vmu.Unlock()
	if v != nil {
		select {
		case v.wakeCh <- struct{}{}:
		default:
		}
	}
}

// ConnectionStatuses returns the live state of every running connection.
func (e *Engine) ConnectionStatuses() map[int64]ConnectionStatus {
	e.vmu.Lock()
	defer e.vmu.Unlock()
	out := map[int64]ConnectionStatus{}
	for id, v := range e.vendors {
		st := ConnectionStatus{ID: id, Name: v.cfg.Name, Enabled: true, Sent: v.sent.Load(), Errors: v.errs.Load(), InFlight: v.flight.Load()}
		for _, s := range v.slots {
			s.mu.Lock()
			st.Binds = append(st.Binds, s.status)
			s.mu.Unlock()
		}
		out[id] = st
	}
	return out
}

// RestartConnection drops and re-creates the binds of one connection.
func (e *Engine) RestartConnection(ctx context.Context, id int64) error {
	e.vmu.Lock()
	if v := e.vendors[id]; v != nil {
		v.stop()
		delete(e.vendors, id)
	}
	e.vmu.Unlock()
	return e.Reload(ctx)
}

func (v *vendorConn) start() {
	for _, s := range v.slots {
		v.wg.Add(1)
		go v.bindLoop(s)
	}
	v.wg.Add(1)
	go v.sendLoop()
}

func (v *vendorConn) stop() {
	v.cancel()
	for _, s := range v.slots {
		s.mu.Lock()
		if s.session != nil {
			sess := s.session
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()
				sess.Unbind(ctx)
			}()
		}
		s.mu.Unlock()
	}
}

func (v *vendorConn) setStatus(s *bindSlot, state string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.status.State != state {
		s.status.Since = time.Now()
	}
	s.status.State = state
	s.status.Error = ""
	if err != nil {
		s.status.Error = err.Error()
	}
}

// bindLoop keeps one bind connected, reconnecting with backoff.
func (v *vendorConn) bindLoop(s *bindSlot) {
	defer v.wg.Done()
	backoff := time.Second
	for v.ctx.Err() == nil {
		v.setStatus(s, "connecting", nil)
		cmd := smpp.BindTransceiver
		switch v.cfg.BindMode {
		case "tx":
			cmd = smpp.BindTransmitter
		case "rx":
			cmd = smpp.BindReceiver
		}
		dctx, cancel := context.WithTimeout(v.ctx, 20*time.Second)
		sess, err := smpp.Dial(dctx, net.JoinHostPort(v.cfg.Host, strconv.Itoa(v.cfg.Port)), cmd,
			&smpp.Bind{SystemID: v.cfg.SystemID, Password: v.cfg.Password, SystemType: v.cfg.SystemType},
			smpp.SessionOptions{Window: v.cfg.Window, EnquireInterval: 30 * time.Second, ResponseTimeout: v.e.VendorResponseTimeout,
				Handler: v.handleIncoming})
		cancel()
		if err != nil {
			v.setStatus(s, "down", err)
			v.e.log.Warn("vendor bind failed", "connection", v.cfg.Name, "err", err)
			select {
			case <-v.ctx.Done():
				return
			case <-time.After(backoff + time.Duration(rand.IntN(500))*time.Millisecond):
			}
			backoff = min(backoff*2, 30*time.Second)
			continue
		}
		backoff = time.Second
		s.mu.Lock()
		s.session = sess
		s.mu.Unlock()
		v.setStatus(s, "bound", nil)
		v.e.log.Info("vendor bound", "connection", v.cfg.Name, "bind", s.status.Index)
		select {
		case <-v.wakeCh:
		default:
		}
		v.wakeOnce()
		select {
		case <-sess.Done():
		case <-v.ctx.Done():
		}
		s.mu.Lock()
		s.session = nil
		s.mu.Unlock()
		v.setStatus(s, "down", errors.New("connection closed"))
	}
}

func (v *vendorConn) wakeOnce() {
	select {
	case v.wakeCh <- struct{}{}:
	default:
	}
}

// transmitter returns a bound session that can send, round-robin.
func (v *vendorConn) transmitter() *smpp.Session {
	n := len(v.slots)
	start := int(v.rr.Add(1))
	for i := 0; i < n; i++ {
		s := v.slots[(start+i)%n]
		s.mu.Lock()
		sess := s.session
		s.mu.Unlock()
		if sess != nil && sess.CanTransmit() {
			return sess
		}
	}
	return nil
}

type queuedMessage struct {
	queueID   int64
	id        uuid.UUID
	createdAt time.Time
	clientID  int64
	accountID int64
	source    string
	dest      string
	body      string
	coding    byte
	payload   []byte
	udh       []byte
	wantsDLR  bool
	parts     int
	country   string
	networkID int64
	attempts  int
	plan      []int64
	price     routing.Micros
}

// sendLoop claims queued messages for this connection and submits them within the TPS limit.
func (v *vendorConn) sendLoop() {
	defer v.wg.Done()
	for v.ctx.Err() == nil {
		if v.transmitter() == nil {
			v.idle(2 * time.Second)
			continue
		}
		batch, err := v.claim(v.cfg.Window * len(v.slots) * 2)
		if err != nil {
			v.e.log.Error("claim queue failed", "connection", v.cfg.Name, "err", err)
			v.idle(2 * time.Second)
			continue
		}
		if len(batch) == 0 {
			v.idle(time.Second)
			continue
		}
		var wg sync.WaitGroup
		for _, m := range batch {
			if err := v.limiter.Wait(v.ctx); err != nil {
				return
			}
			sess := v.transmitter()
			if sess == nil {
				v.release(m, 2*time.Second)
				continue
			}
			wg.Add(1)
			v.flight.Add(1)
			go func(m queuedMessage) {
				defer wg.Done()
				defer v.flight.Add(-1)
				v.submit(sess, m)
			}(m)
		}
		wg.Wait()
	}
}

func (v *vendorConn) idle(d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-v.ctx.Done():
	case <-v.wakeCh:
	case <-t.C:
	}
}

func (v *vendorConn) claim(limit int) ([]queuedMessage, error) {
	ctx := v.ctx
	rows, err := v.e.db.Query(ctx, `WITH c AS (
			UPDATE send_queue SET leased_until = now() + INTERVAL '2 minutes'
			WHERE id IN (SELECT id FROM send_queue WHERE connection_id = $1 AND available_at <= now()
				AND (leased_until IS NULL OR leased_until < now()) ORDER BY id LIMIT $2 FOR UPDATE SKIP LOCKED)
			RETURNING id, message_id, created_at)
		SELECT c.id, m.id, m.created_at, m.client_id, COALESCE(m.account_id, 0), m.source, m.destination, m.body,
			m.data_coding, m.payload, m.udh, m.wants_dlr, m.parts, COALESCE(m.country_iso, ''), COALESCE(m.network_id, 0),
			m.attempts, m.route_plan, m.price::text
		FROM c JOIN messages m ON m.id = c.message_id AND m.created_at = c.created_at
		ORDER BY c.id`, v.cfg.ID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []queuedMessage
	for rows.Next() {
		var m queuedMessage
		var coding, parts, attempts int16
		var price string
		if err := rows.Scan(&m.queueID, &m.id, &m.createdAt, &m.clientID, &m.accountID, &m.source, &m.dest, &m.body,
			&coding, &m.payload, &m.udh, &m.wantsDLR, &parts, &m.country, &m.networkID, &attempts, &m.plan, &price); err != nil {
			return nil, err
		}
		m.coding, m.parts, m.attempts = byte(coding), int(parts), int(attempts)
		m.price, _ = routing.ParseMicros(price)
		out = append(out, m)
	}
	return out, rows.Err()
}

// release puts a message back in the queue for a later attempt on the same connection (no attempt used).
func (v *vendorConn) release(m queuedMessage, after time.Duration) {
	_, err := v.e.db.Exec(context.Background(), `UPDATE send_queue SET leased_until = NULL,
		available_at = now() + make_interval(secs => $2) WHERE id = $1`, m.queueID, after.Seconds())
	if err != nil {
		v.e.log.Error("release queue entry failed", "message", m.id, "err", err)
	}
}

func isAlnum(s string) bool {
	for _, r := range s {
		if (r < '0' || r > '9') && r != '+' {
			return true
		}
	}
	return false
}

// buildParts returns the submit_sm bodies for a message.
func (v *vendorConn) buildParts(m queuedMessage) []*smpp.Sm {
	srcTon, srcNpi := v.cfg.SourceTon, v.cfg.SourceNpi
	if isAlnum(m.source) {
		srcTon, srcNpi = 5, 0
	} else if srcTon == 5 {
		srcTon, srcNpi = 1, 1
	}
	base := smpp.Sm{SourceTon: srcTon, SourceNpi: srcNpi, Source: m.source, DestTon: v.cfg.DestTon, DestNpi: v.cfg.DestNpi,
		Destination: m.dest, RegisteredDelivery: 1}

	if m.payload != nil {
		sm := base
		sm.DataCoding = m.coding
		sm.ShortMessage = append(append([]byte{}, m.udh...), m.payload...)
		if len(m.udh) > 0 {
			sm.EsmClass = smpp.EsmUDHI
		}
		return []*smpp.Sm{&sm}
	}
	coding, chunks := gsm.Split(m.body)
	if len(chunks) == 1 {
		sm := base
		sm.DataCoding = coding
		sm.ShortMessage = chunks[0]
		return []*smpp.Sm{&sm}
	}
	ref := byte(rand.IntN(256))
	var out []*smpp.Sm
	for i, c := range chunks {
		sm := base
		sm.DataCoding = coding
		sm.EsmClass = smpp.EsmUDHI
		sm.ShortMessage = append(gsm.ConcatUDH(ref, len(chunks), i+1), c...)
		out = append(out, &sm)
	}
	return out
}

// submit sends one message (all its parts) and records the outcome. Retry rules:
//   - vendor throttling / queue full: back to the queue on the same connection, no attempt used;
//   - send failed before reaching the vendor: same;
//   - vendor rejected: next connection in the route plan, up to the connection's max attempts;
//   - no answer from the vendor: never resent (it may have been delivered) - marked "unknown".
func (v *vendorConn) submit(sess *smpp.Session, m queuedMessage) {
	ctx, cancel := context.WithTimeout(v.ctx, 60*time.Second)
	defer cancel()
	var vendorIDs []string
	for i, sm := range v.buildParts(m) {
		resp, err := sess.Request(ctx, smpp.SubmitSm, sm)
		if err == nil {
			vendorIDs = append(vendorIDs, strings.TrimSpace(resp.Body.(*smpp.MessageIDResp).MessageID))
			continue
		}
		v.errs.Add(1)
		var se *smpp.StatusError
		switch {
		case i > 0:
			// Some parts already went out: do not resend anything.
			v.finishSent(m, vendorIDs, "partial: "+err.Error())
		case errors.As(err, &se) && (se.Status == smpp.StatusThrottled || se.Status == smpp.StatusMsgQFull):
			v.release(m, time.Duration(1+rand.IntN(4))*time.Second)
		case errors.As(err, &se):
			v.failover(m, fmt.Sprintf("vendor rejected: 0x%08x", se.Status))
		case errors.Is(err, smpp.ErrTimeout) || errors.Is(err, smpp.ErrAwaitingClosed):
			v.finishUnknown(m, err.Error())
		default:
			v.release(m, 2*time.Second)
		}
		return
	}
	v.finishSent(m, vendorIDs, "")
}

func (v *vendorConn) cost(m queuedMessage) routing.Micros {
	snap := v.e.snap.Load()
	c, _ := snap.VendorCost(v.cfg.ID, routing.Destination{CountryISO: m.country, NetworkID: m.networkID})
	return c * routing.Micros(m.parts)
}

func (v *vendorConn) finishSent(m queuedMessage, vendorIDs []string, note string) {
	cost := v.cost(m)
	err := pgx.BeginFunc(context.Background(), v.e.db, func(tx pgx.Tx) error {
		for _, vid := range vendorIDs {
			if _, err := tx.Exec(context.Background(), `INSERT INTO vendor_message_ids (connection_id, vendor_id, message_id, created_at)
				VALUES ($1, $2, $3, $4) ON CONFLICT DO NOTHING`, v.cfg.ID, vid, m.id, m.createdAt); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(context.Background(), `UPDATE messages SET status = 'sent', sent_at = now(), connection_id = $3,
				attempts = attempts + 1, cost = cost + $4::numeric, error = $5
			WHERE id = $1 AND created_at = $2`, m.id, m.createdAt, v.cfg.ID, cost.String(), note); err != nil {
			return err
		}
		_, err := tx.Exec(context.Background(), `DELETE FROM send_queue WHERE id = $1`, m.queueID)
		return err
	})
	if err != nil {
		v.e.log.Error("record sent message failed", "message", m.id, "err", err)
		return
	}
	v.sent.Add(1)
	v.e.Stats.Sent(m.clientID, v.cfg.ID, m.country, m.networkID, cost)
}

func (v *vendorConn) finishUnknown(m queuedMessage, reason string) {
	_, err := v.e.db.Exec(context.Background(), `WITH d AS (DELETE FROM send_queue WHERE id = $3)
		UPDATE messages SET status = 'unknown', sent_at = now(), connection_id = $4, attempts = attempts + 1, error = $5
		WHERE id = $1 AND created_at = $2`, m.id, m.createdAt, m.queueID, v.cfg.ID, "no vendor response: "+reason)
	if err != nil {
		v.e.log.Error("record unknown message failed", "message", m.id, "err", err)
	}
	v.e.Stats.Sent(m.clientID, v.cfg.ID, m.country, m.networkID, v.cost(m))
}

// failover moves the message to the next connection in its plan, or fails it (refund + failure DLR).
func (v *vendorConn) failover(m queuedMessage, reason string) {
	attempts := m.attempts + 1
	var next int64
	for i, id := range m.plan {
		if id == v.cfg.ID && i+1 < len(m.plan) {
			next = m.plan[i+1]
			break
		}
	}
	if next != 0 {
		_, err := v.e.db.Exec(context.Background(), `WITH q AS (UPDATE send_queue SET connection_id = $4, leased_until = NULL,
				available_at = now() WHERE id = $3)
			UPDATE messages SET connection_id = $4, attempts = $5, error = $6 WHERE id = $1 AND created_at = $2`,
			m.id, m.createdAt, m.queueID, next, attempts, reason)
		if err != nil {
			v.e.log.Error("failover failed", "message", m.id, "err", err)
			return
		}
		v.e.wake(next)
		return
	}
	v.e.failMessage(m, v.cfg.ID, attempts, reason)
}

// failMessage marks a message failed, refunds the client once and queues a failure DLR.
func (e *Engine) failMessage(m queuedMessage, connectionID int64, attempts int, reason string) {
	ctx := context.Background()
	err := pgx.BeginFunc(ctx, e.db, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM send_queue WHERE id = $1`, m.queueID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE messages SET status = 'failed', attempts = $3, error = $4, dlr_status = 'REJECTD',
				dlr_at = now() WHERE id = $1 AND created_at = $2`, m.id, m.createdAt, attempts, reason); err != nil {
			return err
		}
		// Refund once: the unique (message_id, kind) index makes a second refund impossible.
		var balance string
		err := tx.QueryRow(ctx, `UPDATE balances SET balance = balance + $2::numeric, updated_at = now()
			WHERE client_id = $1 AND NOT EXISTS (SELECT 1 FROM ledger WHERE message_id = $3 AND kind = 'refund')
			RETURNING balance::text`, m.clientID, m.price.String(), m.id).Scan(&balance)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO ledger (client_id, message_id, kind, amount, balance_after, note)
			VALUES ($1, $2, 'refund', $3::numeric, $4::numeric, $5)`, m.clientID, m.id, m.price.String(), balance, reason)
		return err
	})
	if err != nil {
		e.log.Error("fail message failed", "message", m.id, "err", err)
		return
	}
	e.Stats.Failed(m.clientID, connectionID, m.country, m.networkID, m.price)
	if m.wantsDLR {
		e.forwardDLR(ctx, m.id, m.createdAt)
	}
}
