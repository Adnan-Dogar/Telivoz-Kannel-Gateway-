package api

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Adnan-Dogar/telivoz-gateway/internal/engine"
	"github.com/Adnan-Dogar/telivoz-gateway/internal/routing"
	"github.com/jackc/pgx/v5"
)

func jsonEncode(w io.Writer, v any) error { return json.NewEncoder(w).Encode(v) }

const maxCampaignNumbers = 2_000_000

// createCampaign accepts a multipart upload: fields account_id, name, sender, text and a file with numbers
// (CSV/TXT; the first column holding digits is used). The file is streamed, never loaded whole into memory;
// numbers are normalized, de-duplicated and stored, then sent in the background at the account's TPS.
func (s *Server) createCampaign(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 200<<20)
	mr, err := r.MultipartReader()
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid", "expected a multipart upload")
		return
	}
	fields := map[string]string{}
	var numbers []string
	seen := map[string]struct{}{}
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid", "upload interrupted")
			return
		}
		if part.FileName() == "" {
			b, _ := io.ReadAll(io.LimitReader(part, 64<<10))
			fields[part.FormName()] = string(b)
			continue
		}
		sc := bufio.NewScanner(part)
		sc.Buffer(make([]byte, 64<<10), 1<<20)
		for sc.Scan() {
			for _, cell := range strings.FieldsFunc(sc.Text(), func(r rune) bool { return r == ',' || r == ';' || r == '\t' }) {
				n := routing.NormalizeNumber(strings.Trim(cell, `" `))
				if len(n) < 6 || len(n) > 15 {
					continue
				}
				if _, dup := seen[n]; !dup {
					seen[n] = struct{}{}
					numbers = append(numbers, n)
				}
				break
			}
			if len(numbers) > maxCampaignNumbers {
				writeError(w, http.StatusBadRequest, "too_many", fmt.Sprintf("at most %d numbers per campaign", maxCampaignNumbers))
				return
			}
		}
		if err := sc.Err(); err != nil {
			writeError(w, http.StatusBadRequest, "invalid", "could not read the file: "+err.Error())
			return
		}
	}
	accountID, _ := strconv.ParseInt(fields["account_id"], 10, 64)
	acc, err := s.accountFor(r, accountID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid", err.Error())
		return
	}
	text := fields["text"]
	if strings.TrimSpace(text) == "" || len(numbers) == 0 {
		writeError(w, http.StatusBadRequest, "invalid", "a message text and at least one valid number are required")
		return
	}
	var id int64
	err = pgx.BeginFunc(r.Context(), s.db, func(tx pgx.Tx) error {
		if err := tx.QueryRow(r.Context(), `INSERT INTO campaigns (client_id, account_id, name, sender, body, total, created_by)
			VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id`, acc.ClientID, acc.ID, fields["name"], fields["sender"], text,
			len(numbers), principal(r).UserID).Scan(&id); err != nil {
			return err
		}
		rows := make([][]any, len(numbers))
		for i, n := range numbers {
			rows[i] = []any{id, i, n}
		}
		_, err := tx.CopyFrom(r.Context(), pgx.Identifier{"campaign_numbers"}, []string{"campaign_id", "seq", "number"}, pgx.CopyFromRows(rows))
		return err
	})
	if err != nil {
		s.dbError(w, err)
		return
	}
	s.audit(r, "create", "campaigns", strconv.FormatInt(id, 10), map[string]any{"numbers": len(numbers)})
	s.campaigns.start(id)
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "total": len(numbers)})
}

func (s *Server) campaignScope(r *http.Request) (string, []any) {
	sc := s.scopeFor(r.Context(), principal(r))
	if sc.allClients {
		return "true", nil
	}
	return "c.client_id = ANY($1)", []any{sc.clientIDs}
}

func (s *Server) listCampaigns(w http.ResponseWriter, r *http.Request) {
	cond, args := s.campaignScope(r)
	var raw []byte
	err := s.db.QueryRow(r.Context(), fmt.Sprintf(`SELECT COALESCE(jsonb_agg(to_jsonb(x) ORDER BY x.id DESC), '[]') FROM (
		SELECT c.*, (SELECT name FROM clients cl WHERE cl.id = c.client_id) AS client_name FROM campaigns c
		WHERE %s ORDER BY c.id DESC LIMIT 200) x`, cond), args...).Scan(&raw)
	if err != nil {
		s.dbError(w, err)
		return
	}
	writeRaw(w, http.StatusOK, raw)
}

func (s *Server) getCampaign(w http.ResponseWriter, r *http.Request) {
	id, ok := idParam(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid", "invalid id")
		return
	}
	cond, args := s.campaignScope(r)
	args = append(args, id)
	var raw []byte
	err := s.db.QueryRow(r.Context(), fmt.Sprintf(`SELECT to_jsonb(c) FROM campaigns c WHERE %s AND c.id = $%d`, cond, len(args)), args...).Scan(&raw)
	if err != nil {
		s.dbError(w, err)
		return
	}
	writeRaw(w, http.StatusOK, raw)
}

// campaignRunner sends campaign numbers in the background. Campaigns survive restarts: unfinished ones are
// resumed by ResumeCampaigns at startup.
type campaignRunner struct {
	s       *Server
	mu      sync.Mutex
	running map[int64]bool
}

func (c *campaignRunner) start(id int64) {
	c.mu.Lock()
	if c.running == nil {
		c.running = map[int64]bool{}
	}
	if c.running[id] {
		c.mu.Unlock()
		return
	}
	c.running[id] = true
	c.mu.Unlock()
	go func() {
		defer func() {
			c.mu.Lock()
			delete(c.running, id)
			c.mu.Unlock()
		}()
		if err := c.run(context.Background(), id); err != nil {
			c.s.log.Error("campaign failed", "campaign", id, "err", err)
			_, _ = c.s.db.Exec(context.Background(), `UPDATE campaigns SET status = 'failed', last_error = $2, finished_at = now() WHERE id = $1`, id, err.Error())
		}
	}()
}

// ResumeCampaigns restarts campaigns that were running when the gateway stopped.
func (s *Server) ResumeCampaigns(ctx context.Context) {
	rows, err := s.db.Query(ctx, `SELECT id FROM campaigns WHERE status IN ('queued', 'running')`)
	if err != nil {
		return
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	for _, id := range ids {
		s.campaigns.start(id)
	}
}

func (c *campaignRunner) run(ctx context.Context, id int64) error {
	db := c.s.db
	var acc engine.Account
	var sender, body string
	err := db.QueryRow(ctx, `UPDATE campaigns cp SET status = 'running' FROM accounts a
		WHERE cp.id = $1 AND a.id = cp.account_id
		RETURNING a.id, a.client_id, a.kind, a.username, a.tps, a.max_binds, cp.sender, cp.body`, id).
		Scan(&acc.ID, &acc.ClientID, &acc.Kind, &acc.Username, &acc.TPS, &acc.MaxBinds, &sender, &body)
	if err != nil {
		return err
	}
	for {
		rows, err := db.Query(ctx, `SELECT seq, number FROM campaign_numbers WHERE campaign_id = $1 ORDER BY seq LIMIT 500`, id)
		if err != nil {
			return err
		}
		type item struct {
			seq    int
			number string
		}
		var batch []item
		for rows.Next() {
			var it item
			if err := rows.Scan(&it.seq, &it.number); err != nil {
				rows.Close()
				return err
			}
			batch = append(batch, it)
		}
		rows.Close()
		if len(batch) == 0 {
			_, err := db.Exec(ctx, `UPDATE campaigns SET status = 'done', finished_at = now() WHERE id = $1`, id)
			return err
		}
		accepted, rejected := 0, 0
		var lastErr string
		for _, it := range batch {
			ref := fmt.Sprintf("campaign-%d-%d", id, it.seq) // makes a resumed campaign skip numbers already sent
			for {
				_, err := c.s.eng.Submit(ctx, engine.SubmitRequest{Account: acc, Source: sender, Destination: it.number,
					Text: body, ClientRef: ref, WantsDLR: true})
				if errors.Is(err, engine.ErrThrottled) {
					time.Sleep(time.Second / time.Duration(max(acc.TPS, 1)))
					continue
				}
				if err != nil {
					rejected++
					lastErr = err.Error()
					if errors.Is(err, engine.ErrNoBalance) {
						_, _ = db.Exec(ctx, `UPDATE campaigns SET status = 'failed', last_error = 'insufficient balance', finished_at = now(),
							processed = processed + $2, accepted = accepted + $3, rejected = rejected + $4 WHERE id = $1`,
							id, accepted+rejected, accepted, rejected)
						return nil
					}
				} else {
					accepted++
				}
				break
			}
		}
		if _, err := db.Exec(ctx, `WITH d AS (DELETE FROM campaign_numbers WHERE campaign_id = $1 AND seq <= $2)
			UPDATE campaigns SET processed = processed + $3, accepted = accepted + $4, rejected = rejected + $5,
				last_error = CASE WHEN $6 = '' THEN last_error ELSE $6 END WHERE id = $1`,
			id, batch[len(batch)-1].seq, len(batch), accepted, rejected, lastErr); err != nil {
			return err
		}
	}
}
