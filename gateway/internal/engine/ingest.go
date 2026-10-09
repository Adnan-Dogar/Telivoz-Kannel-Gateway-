package engine

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Adnan-Dogar/telivoz-gateway/internal/gsm"
	"github.com/Adnan-Dogar/telivoz-gateway/internal/routing"
	"github.com/Adnan-Dogar/telivoz-gateway/internal/smpp"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Account is a client login (SMPP or HTTP) as needed by the engine.
type Account struct {
	ID       int64
	ClientID int64
	Kind     string
	Username string
	TPS      int
	MaxBinds int
}

// SubmitRequest is a message arriving from a client.
type SubmitRequest struct {
	Account     Account
	Source      string
	Destination string
	Text        string // decoded text (always set; used for display, rules and HTTP sends)
	DataCoding  byte
	Payload     []byte // SMPP: original short_message without UDH (forwarded as-is unless rules changed the text)
	UDH         []byte // SMPP: original user data header, if any
	ClientRef   string // HTTP Idempotency-Key / client message id: the same ref creates one message
	WantsDLR    bool
}

type SubmitResult struct {
	MessageID uuid.UUID
	Parts     int
	Price     routing.Micros
	Duplicate bool
}

// SubmitError is a rejection with a reason clients can act on.
type SubmitError struct {
	Code       string // stable machine code for the HTTP API
	SMPPStatus uint32
	Message    string
}

func (e *SubmitError) Error() string { return e.Message }

var (
	ErrThrottled    = &SubmitError{"throttled", smpp.StatusThrottled, "too many messages per second for this account"}
	ErrInvalidDest  = &SubmitError{"invalid_destination", smpp.StatusInvDstAdr, "invalid destination number"}
	ErrNoPrice      = &SubmitError{"no_rate", smpp.StatusRejectAppErr, "no rate configured for this destination"}
	ErrNoRouteFound = &SubmitError{"no_route", smpp.StatusRejectAppErr, "no route available for this destination"}
	ErrNoBalance    = &SubmitError{"insufficient_balance", smpp.StatusRejectAppErr, "insufficient balance"}
	ErrEmptyMessage = &SubmitError{"empty_message", smpp.StatusInvMsgLen, "message text is empty"}
	ErrInternal     = &SubmitError{"internal_error", smpp.StatusSysErr, "internal error, please retry"}
)

// Submit validates, prices, routes and charges a message, then queues it for the vendor. The charge, the
// message and the queue entry are written in one transaction: a message is never charged twice and never
// charged without being queued. The client gets its acknowledgement only after the commit.
func (e *Engine) Submit(ctx context.Context, req SubmitRequest) (SubmitResult, error) {
	snap := e.snap.Load()
	dest := snap.Lookup(req.Destination)

	if !e.limiter(req.Account.ID, req.Account.TPS).Allow() {
		e.Stats.Rejected(req.Account.ClientID, dest.CountryISO, dest.NetworkID)
		return SubmitResult{}, ErrThrottled
	}
	if req.ClientRef != "" {
		if id, ok := e.findIdempotent(ctx, req.Account.ID, req.ClientRef); ok {
			return SubmitResult{MessageID: id, Duplicate: true}, nil
		}
	}
	reject := func(err *SubmitError) (SubmitResult, error) {
		e.Stats.Rejected(req.Account.ClientID, dest.CountryISO, dest.NetworkID)
		return SubmitResult{}, err
	}
	if len(dest.Number) < 6 || len(dest.Number) > 15 {
		return reject(ErrInvalidDest)
	}
	if req.Text == "" && len(req.Payload) == 0 {
		return reject(ErrEmptyMessage)
	}

	content, _, err := snap.ApplyContent(req.Account.ClientID, dest.CountryISO, routing.Content{Sender: req.Source, Text: req.Text})
	var blocked *routing.ErrBlocked
	if errors.As(err, &blocked) {
		return reject(&SubmitError{"blocked", smpp.StatusRejectAppErr, "message blocked by content policy"})
	}
	payload, udh, coding := req.Payload, req.UDH, req.DataCoding
	if content.Text != req.Text {
		// Text changed: re-encode instead of forwarding the original bytes.
		payload, udh = nil, nil
	}

	parts := 1
	if payload == nil {
		parts = gsm.Parts(content.Text)
		coding, _ = gsm.Split(content.Text)
	}

	price, ok := snap.ClientPrice(req.Account.ClientID, dest)
	if !ok {
		return reject(ErrNoPrice)
	}
	plan, err := snap.Select(routing.Request{
		ClientID: req.Account.ClientID, AccountID: req.Account.ID, Sender: content.Sender, Dest: dest, Price: price,
	})
	if err != nil {
		return reject(ErrNoRouteFound)
	}
	total := price * routing.Micros(parts)

	id, err := uuid.NewV7()
	if err != nil {
		return SubmitResult{}, ErrInternal
	}
	now := time.Now().UTC()
	var dup uuid.UUID
	err = pgx.BeginFunc(ctx, e.db, func(tx pgx.Tx) error {
		if req.ClientRef != "" {
			tag, err := tx.Exec(ctx, `INSERT INTO idempotency_keys (account_id, key, message_id) VALUES ($1, $2, $3)
				ON CONFLICT DO NOTHING`, req.Account.ID, req.ClientRef, id)
			if err != nil {
				return err
			}
			if tag.RowsAffected() == 0 {
				// Another request with the same key won the race.
				if err := tx.QueryRow(ctx, `SELECT message_id FROM idempotency_keys WHERE account_id = $1 AND key = $2`,
					req.Account.ID, req.ClientRef).Scan(&dup); err != nil {
					return err
				}
				return errDuplicate
			}
		}
		var balance string
		err := tx.QueryRow(ctx, `UPDATE balances b SET balance = b.balance - $1::numeric, updated_at = now()
			FROM clients c WHERE b.client_id = $2 AND c.id = b.client_id AND c.status = 'active'
			  AND b.balance - $1::numeric >= -c.credit_limit
			RETURNING b.balance::text`, total.String(), req.Account.ClientID).Scan(&balance)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNoBalance
		}
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO ledger (client_id, message_id, kind, amount, balance_after)
			VALUES ($1, $2, 'charge', $3::numeric, $4::numeric)`, req.Account.ClientID, id, (-total).String(), balance); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO messages (id, created_at, client_id, account_id, source, destination, body,
				data_coding, payload, udh, wants_dlr, parts, country_iso, network_id, route_id, connection_id, status,
				route_plan, price, client_ref)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,NULLIF($13,''),NULLIF($14,0),$15,$16,'queued',$17,$18::numeric,$19)`,
			id, now, req.Account.ClientID, req.Account.ID, content.Sender, dest.Number, content.Text, int16(coding),
			payload, udh, req.WantsDLR, parts, dest.CountryISO, dest.NetworkID, plan.RouteID, plan.Connections[0],
			plan.Connections, total.String(), req.ClientRef); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO send_queue (message_id, created_at, connection_id) VALUES ($1, $2, $3)`,
			id, now, plan.Connections[0])
		return err
	})
	switch {
	case errors.Is(err, errDuplicate):
		return SubmitResult{MessageID: dup, Duplicate: true}, nil
	case errors.Is(err, ErrNoBalance):
		return reject(ErrNoBalance)
	case err != nil:
		e.log.Error("submit failed", "account", req.Account.ID, "err", err)
		return SubmitResult{}, ErrInternal
	}

	e.Stats.Submitted(req.Account.ClientID, dest.CountryISO, dest.NetworkID, parts, total)
	e.wake(plan.Connections[0])
	return SubmitResult{MessageID: id, Parts: parts, Price: total}, nil
}

var errDuplicate = errors.New("duplicate client reference")

func (e *Engine) findIdempotent(ctx context.Context, accountID int64, key string) (uuid.UUID, bool) {
	var id uuid.UUID
	err := e.db.QueryRow(ctx, `SELECT message_id FROM idempotency_keys WHERE account_id = $1 AND key = $2`, accountID, key).Scan(&id)
	return id, err == nil
}

// LoadAccount reads an active account of an active client.
func (e *Engine) LoadAccount(ctx context.Context, kind, username string) (Account, string, []string, error) {
	var a Account
	var hash string
	var ips []string
	err := e.db.QueryRow(ctx, `SELECT a.id, a.client_id, a.kind, a.username, a.tps, a.max_binds, a.password_hash, a.allowed_ips
		FROM accounts a JOIN clients c ON c.id = a.client_id
		WHERE a.kind = $1 AND a.username = $2 AND a.status = 'active' AND c.status = 'active'`, kind, username).
		Scan(&a.ID, &a.ClientID, &a.Kind, &a.Username, &a.TPS, &a.MaxBinds, &hash, &ips)
	if err != nil {
		return a, "", nil, fmt.Errorf("account %q: %w", username, err)
	}
	return a, hash, ips, nil
}
