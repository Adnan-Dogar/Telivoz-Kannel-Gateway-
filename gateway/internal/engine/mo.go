package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"net/http"
	"strings"
	"time"

	"github.com/Adnan-Dogar/telivoz-gateway/internal/gsm"
	"github.com/Adnan-Dogar/telivoz-gateway/internal/routing"
	"github.com/Adnan-Dogar/telivoz-gateway/internal/smpp"
	"github.com/google/uuid"
)

// handleMO stores an incoming message from a vendor and forwards it to the client its MO route names.
// A STOP-type reply also adds the sender to that client's blacklist.
func (e *Engine) handleMO(ctx context.Context, connectionID int64, from, to, text string) {
	snap := e.snap.Load()
	route := snap.MatchMO(to, text)
	id, _ := uuid.NewV7()
	now := time.Now().UTC()
	var clientID int64
	var accountID *int64
	status := "unrouted"
	if route != nil {
		clientID, status = route.ClientID, "received"
		if route.AccountID != 0 {
			a := route.AccountID
			accountID = &a
		}
	}
	_, err := e.db.Exec(ctx, `INSERT INTO messages (id, created_at, client_id, account_id, direction, source, destination, body,
			connection_id, status, wants_dlr)
		VALUES ($1, $2, $3, $4, 'mo', $5, $6, $7, $8, $9, $10)`,
		id, now, clientID, accountID, routing.NormalizeNumber(from), routing.NormalizeNumber(to), text, connectionID, status, route != nil)
	if err != nil {
		e.log.Error("store MO failed", "err", err)
		return
	}
	if route == nil {
		e.log.Warn("MO without route", "to", to)
		return
	}
	if route.AutoOptOut && routing.OptOutWords[routing.FirstWord(text)] {
		_, err := e.db.Exec(ctx, `INSERT INTO blacklist (client_id, number, reason) VALUES ($1, $2, $3)
			ON CONFLICT DO NOTHING`, route.ClientID, routing.NormalizeNumber(from), "opt-out reply to "+to)
		if err != nil {
			e.log.Error("opt-out failed", "err", err)
		} else {
			e.requestReload()
		}
	}
	e.forwardDLR(ctx, id, now)
}

// deliverMO forwards an incoming message to the client: deliver_sm on a bound SMPP receiver, or a JSON POST to
// the client's MO webhook.
func (e *Engine) deliverMO(ctx context.Context, m dlrMessage) error {
	var err error
	if m.kind == "smpp" {
		s := e.clients.receiver(m.accountID)
		if s == nil {
			return errNoReceiver
		}
		coding, parts := gsm.Split(m.body)
		ref := byte(rand.IntN(256))
		for i, p := range parts {
			sm := &smpp.Sm{SourceTon: 1, SourceNpi: 1, Source: m.source, DestTon: 1, DestNpi: 1, Destination: m.dest,
				DataCoding: coding, ShortMessage: p}
			if isAlnum(m.dest) {
				sm.DestTon, sm.DestNpi = 5, 0
			}
			if len(parts) > 1 {
				sm.EsmClass = smpp.EsmUDHI
				sm.ShortMessage = append(gsm.ConcatUDH(ref, len(parts), i+1), p...)
			}
			rctx, cancel := context.WithTimeout(ctx, 15*time.Second)
			_, err = s.Request(rctx, smpp.DeliverSm, sm)
			cancel()
			if err != nil {
				return err
			}
		}
	} else {
		if m.moWebhook == "" {
			return nil // nowhere to push: the message stays visible in the portal
		}
		body, _ := json.Marshal(map[string]any{"type": "mo", "id": m.id.String(), "from": m.source, "to": m.dest,
			"text": m.body, "received_at": m.createdAt.UTC().Format(time.RFC3339)})
		rctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		req, rerr := http.NewRequestWithContext(rctx, http.MethodPost, m.moWebhook, bytes.NewReader(body))
		if rerr != nil {
			return rerr
		}
		req.Header.Set("Content-Type", "application/json")
		resp, derr := http.DefaultClient.Do(req)
		if derr != nil {
			return derr
		}
		resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			return fmt.Errorf("MO webhook answered %d", resp.StatusCode)
		}
	}
	_, _ = e.db.Exec(ctx, `UPDATE messages SET status = 'delivered' WHERE id = $1 AND created_at = $2`, m.id, m.createdAt)
	return nil
}

// failoverWanted reports whether a negative DLR matches the connection's failover list: entries are a status
// ("UNDELIV") or a status with error code ("UNDELIV:011").
func failoverWanted(list []string, stat, errCode string) bool {
	for _, item := range list {
		item = strings.ToUpper(strings.TrimSpace(item))
		if item == stat || item == stat+":"+strings.ToUpper(strings.TrimSpace(errCode)) {
			return true
		}
	}
	return false
}

// rerouteAfterDLR resends a message through the next connection of its route plan after a negative DLR.
// The client is not charged again and receives a DLR only for the final attempt. It returns false when no
// connection is left to try, so the DLR is then handled normally.
func (e *Engine) rerouteAfterDLR(ctx context.Context, c connConfig, id uuid.UUID, createdAt time.Time, r *smpp.Receipt) (bool, error) {
	var plan []int64
	var current int64
	var status string
	if err := e.db.QueryRow(ctx, `SELECT route_plan, COALESCE(connection_id, 0), status FROM messages WHERE id = $1 AND created_at = $2`,
		id, createdAt).Scan(&plan, &current, &status); err != nil {
		return false, err
	}
	// Only the DLR of the connection currently responsible may reroute (late DLRs from earlier attempts are ignored).
	if current != c.ID || status != "sent" {
		return true, nil
	}
	var next int64
	for i, cid := range plan {
		if cid == c.ID && i+1 < len(plan) {
			next = plan[i+1]
		}
	}
	if next == 0 {
		return false, nil
	}
	tag, err := e.db.Exec(ctx, `WITH q AS (INSERT INTO send_queue (message_id, created_at, connection_id) VALUES ($1, $2, $3)
			ON CONFLICT (message_id) DO NOTHING)
		UPDATE messages SET status = 'queued', connection_id = $3, dlr_status = $4, dlr_error = $5,
			error = $6 WHERE id = $1 AND created_at = $2 AND status = 'sent'`,
		id, createdAt, next, r.Stat, r.Err, fmt.Sprintf("DLR %s (err %s) from %s, rerouted", r.Stat, r.Err, c.Name))
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() == 1 {
		e.log.Info("message rerouted after DLR", "message", id, "from", c.Name, "stat", r.Stat)
		e.wake(next)
	}
	return true, nil
}
