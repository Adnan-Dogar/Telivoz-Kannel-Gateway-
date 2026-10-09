package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Adnan-Dogar/telivoz-gateway/internal/gsm"
	"github.com/Adnan-Dogar/telivoz-gateway/internal/smpp"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// registry tracks bound client SMPP sessions per account.
type registry struct {
	mu       sync.Mutex
	sessions map[int64][]*smpp.Session
	rr       map[int64]int
}

func newRegistry() *registry {
	return &registry{sessions: map[int64][]*smpp.Session{}, rr: map[int64]int{}}
}

func (r *registry) add(accountID int64, s *smpp.Session) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sessions[accountID] = append(r.sessions[accountID], s)
}

func (r *registry) remove(accountID int64, s *smpp.Session) {
	r.mu.Lock()
	defer r.mu.Unlock()
	list := r.sessions[accountID]
	for i, x := range list {
		if x == s {
			r.sessions[accountID] = append(list[:i], list[i+1:]...)
			break
		}
	}
	if len(r.sessions[accountID]) == 0 {
		delete(r.sessions, accountID)
	}
}

func (r *registry) count(accountID int64) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.sessions[accountID])
}

// receiver returns a session of the account that accepts deliver_sm, round-robin.
func (r *registry) receiver(accountID int64) *smpp.Session {
	r.mu.Lock()
	defer r.mu.Unlock()
	list := r.sessions[accountID]
	for i := 0; i < len(list); i++ {
		r.rr[accountID]++
		s := list[r.rr[accountID]%len(list)]
		if s.CanReceive() {
			return s
		}
	}
	return nil
}

// BoundSession describes a client bind for the portal.
type BoundSession struct {
	AccountID int64  `json:"account_id"`
	SystemID  string `json:"system_id"`
	Mode      string `json:"mode"`
	Remote    string `json:"remote"`
}

func (e *Engine) BoundSessions() []BoundSession {
	e.clients.mu.Lock()
	defer e.clients.mu.Unlock()
	var out []BoundSession
	for id, list := range e.clients.sessions {
		for _, s := range list {
			mode := map[uint32]string{smpp.BindTransceiver: "trx", smpp.BindTransmitter: "tx", smpp.BindReceiver: "rx"}[s.BindCommand]
			out = append(out, BoundSession{AccountID: id, SystemID: s.SystemID, Mode: mode, Remote: s.RemoteAddr().String()})
		}
	}
	return out
}

// handleIncoming processes deliver_sm from a vendor: a DLR or an incoming (MO) message.
func (v *vendorConn) handleIncoming(s *smpp.Session, p *smpp.PDU) {
	if p.CommandID != smpp.DeliverSm {
		_ = s.Nack(p, smpp.StatusInvCmdID)
		return
	}
	sm := p.Body.(*smpp.Sm)
	// Always acknowledge; a vendor that gets errors back would just resend.
	_ = s.Respond(p, smpp.StatusOK, &smpp.MessageIDResp{})

	ctx := context.Background()
	if r, ok := smpp.ParseReceipt(sm); ok {
		if err := v.e.handleReceipt(ctx, v.cfg, r); err != nil {
			v.e.log.Warn("dlr not matched", "connection", v.cfg.Name, "vendor_id", r.ID, "err", err)
		}
		return
	}
	text, _ := gsm.Decode(sm.DataCoding, sm.Payload(), sm.EsmClass&smpp.EsmUDHI != 0)
	id, _ := uuid.NewV7()
	_, err := v.e.db.Exec(ctx, `INSERT INTO messages (id, created_at, client_id, direction, source, destination, body,
			data_coding, connection_id, status)
		VALUES ($1, now(), 0, 'mo', $2, $3, $4, $5, $6, 'delivered')`, id, sm.Source, sm.Destination, text, int16(sm.DataCoding), v.cfg.ID)
	if err != nil {
		v.e.log.Error("store MO failed", "err", err)
	}
}

var finalStatus = map[string]string{
	"DELIVRD": "delivered", "UNDELIV": "undelivered", "REJECTD": "rejected", "DELETED": "undelivered",
	"EXPIRED": "expired", "UNKNOWN": "unknown",
}

func (e *Engine) handleReceipt(ctx context.Context, c connConfig, r *smpp.Receipt) error {
	var id uuid.UUID
	var createdAt time.Time
	candidates := []string{r.ID}
	if c.DLRIDFormat != "same" {
		candidates = append(candidates, smpp.AlternateIDs(r.ID)...)
	}
	// A vendor can send the DLR before we have finished recording its submit_sm_resp (fast failures do this
	// routinely), so keep looking for a few seconds before giving up.
	found := false
	for try := 0; try < 25 && !found; try++ {
		if try > 0 {
			time.Sleep(200 * time.Millisecond)
		}
		for _, vid := range candidates {
			err := e.db.QueryRow(ctx, `SELECT message_id, created_at FROM vendor_message_ids WHERE connection_id = $1 AND vendor_id = $2`,
				c.ID, vid).Scan(&id, &createdAt)
			if err == nil {
				found = true
				break
			}
			if !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
		}
	}
	if !found {
		return fmt.Errorf("unknown vendor message id")
	}
	status, final := finalStatus[r.Stat]
	if !final {
		// ENROUTE / ACCEPTD: intermediate, just record it.
		_, err := e.db.Exec(ctx, `UPDATE messages SET dlr_status = $3 WHERE id = $1 AND created_at = $2`, id, createdAt, r.Stat)
		return err
	}
	var clientID, networkID int64
	var country string
	var wantsDLR, already bool
	err := e.db.QueryRow(ctx, `UPDATE messages SET status = $3, dlr_status = $4, dlr_error = $5, dlr_at = now()
		WHERE id = $1 AND created_at = $2
		RETURNING client_id, COALESCE(country_iso, ''), COALESCE(network_id, 0), wants_dlr, dlr_sent_at IS NOT NULL`,
		id, createdAt, status, r.Stat, r.Err).Scan(&clientID, &country, &networkID, &wantsDLR, &already)
	if err != nil {
		return err
	}
	e.Stats.DLR(clientID, c.ID, country, networkID, status == "delivered", time.Since(createdAt))
	if wantsDLR && !already {
		e.forwardDLR(ctx, id, createdAt)
	}
	return nil
}

type dlrMessage struct {
	id         uuid.UUID
	createdAt  time.Time
	accountID  int64
	kind       string
	webhook    string
	dlrFormat  string
	source     string
	dest       string
	body       string
	status     string
	dlrStatus  string
	dlrError   string
	clientRef  string
	parts      int
	dlrAt      time.Time
}

func (e *Engine) loadDLRMessage(ctx context.Context, id uuid.UUID, createdAt time.Time) (dlrMessage, error) {
	var m dlrMessage
	var parts int16
	var dlrAt *time.Time
	err := e.db.QueryRow(ctx, `SELECT m.id, m.created_at, COALESCE(m.account_id, 0), COALESCE(a.kind, ''), c.dlr_webhook_url,
			c.dlr_format, m.source, m.destination, m.body, m.status, m.dlr_status, m.dlr_error, m.client_ref, m.parts, m.dlr_at
		FROM messages m JOIN clients c ON c.id = m.client_id LEFT JOIN accounts a ON a.id = m.account_id
		WHERE m.id = $1 AND m.created_at = $2`, id, createdAt).
		Scan(&m.id, &m.createdAt, &m.accountID, &m.kind, &m.webhook, &m.dlrFormat, &m.source, &m.dest, &m.body, &m.status,
			&m.dlrStatus, &m.dlrError, &m.clientRef, &parts, &dlrAt)
	m.parts = int(parts)
	if dlrAt != nil {
		m.dlrAt = *dlrAt
	} else {
		m.dlrAt = time.Now()
	}
	return m, err
}

// forwardDLR delivers a DLR to the client now, or puts it in the outbox for retries.
func (e *Engine) forwardDLR(ctx context.Context, id uuid.UUID, createdAt time.Time) {
	m, err := e.loadDLRMessage(ctx, id, createdAt)
	if err != nil {
		e.log.Error("load message for dlr failed", "message", id, "err", err)
		return
	}
	if err := e.deliverDLR(ctx, m); err != nil {
		_, qerr := e.db.Exec(ctx, `INSERT INTO dlr_outbox (message_id, created_at, account_id, next_at)
			VALUES ($1, $2, $3, now() + INTERVAL '10 seconds') ON CONFLICT (message_id) DO NOTHING`, m.id, m.createdAt, m.accountID)
		if qerr != nil {
			e.log.Error("queue dlr failed", "message", id, "err", qerr)
		}
		return
	}
	_, _ = e.db.Exec(ctx, `UPDATE messages SET dlr_sent_at = now() WHERE id = $1 AND created_at = $2`, m.id, m.createdAt)
}

var errNoReceiver = errors.New("client has no receiver bind")

func (e *Engine) deliverDLR(ctx context.Context, m dlrMessage) error {
	stat := m.dlrStatus
	if stat == "" {
		stat = "UNKNOWN"
	}
	switch m.kind {
	case "smpp":
		s := e.clients.receiver(m.accountID)
		if s == nil {
			return errNoReceiver
		}
		r := &smpp.Receipt{ID: m.id.String(), SubmitAt: m.createdAt, DoneAt: m.dlrAt, Stat: stat, Err: m.dlrError, Text: m.body}
		srcTon, srcNpi := byte(1), byte(1)
		dstTon, dstNpi := byte(1), byte(1)
		if isAlnum(m.source) {
			dstTon, dstNpi = 5, 0
		}
		rctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		_, err := s.Request(rctx, smpp.DeliverSm, r.Sm(m.dest, m.source, srcTon, srcNpi, dstTon, dstNpi))
		return err
	default:
		if m.webhook == "" {
			return nil // HTTP client without a webhook: status is available through the API
		}
		payload := map[string]any{
			"id": m.id.String(), "client_ref": m.clientRef, "to": m.dest, "from": m.source, "status": m.status,
			"dlr_status": stat, "error": m.dlrError, "parts": m.parts, "done_at": m.dlrAt.UTC().Format(time.RFC3339),
		}
		if m.dlrFormat == "legacy" {
			// The old DLR pusher's payload: status is the Kannel DLR mask (1 delivered, 2 failed, 16 rejected).
			mask := 2
			switch stat {
			case "DELIVRD":
				mask = 1
			case "REJECTD":
				mask = 16
			}
			payload = map[string]any{"from": m.source, "id": m.id.String(), "to": m.dest, "status": fmt.Sprint(mask),
				"merchant_reference": m.clientRef, "date": m.createdAt.Format("2006-01-02 15:04:05")}
		}
		body, _ := json.Marshal(payload)
		rctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(rctx, http.MethodPost, m.webhook, bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return err
		}
		resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			return fmt.Errorf("webhook answered %d", resp.StatusCode)
		}
		return nil
	}
}

// retryDLRs sends DLRs waiting in the outbox (accountID 0 = all accounts). Called every few seconds and
// right after a client binds.
func (e *Engine) retryDLRs(ctx context.Context, accountID int64) {
	for {
		rows, err := e.db.Query(ctx, `SELECT id, message_id, created_at, attempts FROM dlr_outbox
			WHERE next_at <= now() AND expires_at > now() AND ($1 = 0 OR account_id = $1)
			ORDER BY id LIMIT 200`, accountID)
		if err != nil {
			e.log.Error("read dlr outbox failed", "err", err)
			return
		}
		type item struct {
			id        int64
			msg       uuid.UUID
			createdAt time.Time
			attempts  int
		}
		var items []item
		for rows.Next() {
			var it item
			if err := rows.Scan(&it.id, &it.msg, &it.createdAt, &it.attempts); err == nil {
				items = append(items, it)
			}
		}
		rows.Close()
		if len(items) == 0 {
			return
		}
		delivered := 0
		for _, it := range items {
			m, err := e.loadDLRMessage(ctx, it.msg, it.createdAt)
			if err == nil {
				err = e.deliverDLR(ctx, m)
			}
			if err == nil {
				delivered++
				_, _ = e.db.Exec(ctx, `WITH d AS (DELETE FROM dlr_outbox WHERE id = $1)
					UPDATE messages SET dlr_sent_at = now() WHERE id = $2 AND created_at = $3`, it.id, it.msg, it.createdAt)
				continue
			}
			// Not bound yet: wait for the bind (which triggers a flush) but check again in a minute.
			delay := time.Minute
			if !errors.Is(err, errNoReceiver) {
				delay = min(time.Duration(1<<min(it.attempts, 8))*10*time.Second, 30*time.Minute)
			}
			_, _ = e.db.Exec(ctx, `UPDATE dlr_outbox SET attempts = attempts + 1, next_at = now() + make_interval(secs => $2)
				WHERE id = $1`, it.id, delay.Seconds())
		}
		if delivered == 0 || len(items) < 200 {
			return
		}
	}
}

// NormalizeStatus maps a DLR stat for display.
func NormalizeStatus(s string) string { return strings.ToLower(s) }
