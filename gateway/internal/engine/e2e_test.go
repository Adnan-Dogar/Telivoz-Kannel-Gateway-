package engine_test

import (
	"context"
	"io"
	"log/slog"
	"net"
	"os"
	"testing"
	"time"

	"github.com/Adnan-Dogar/telivoz-gateway/internal/engine"
	"github.com/Adnan-Dogar/telivoz-gateway/internal/sim"
	"github.com/Adnan-Dogar/telivoz-gateway/internal/smpp"
	tu "github.com/Adnan-Dogar/telivoz-gateway/internal/testutil"
	"github.com/jackc/pgx/v5/pgxpool"
)

type harness struct {
	t    *testing.T
	pool *pgxpool.Pool
	f    tu.Fixture
	eng  *engine.Engine
	addr string
	stop context.CancelFunc
}

func start(t *testing.T, pool *pgxpool.Pool, f tu.Fixture, addr string) *harness {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	var out io.Writer = io.Discard
	if testing.Verbose() {
		out = os.Stderr
	}
	eng := engine.New(pool, tu.Cipher(), slog.New(slog.NewTextHandler(out, &slog.HandlerOptions{Level: slog.LevelDebug})))
	eng.VendorResponseTimeout = 2 * time.Second
	if err := eng.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if addr == "" {
		addr = "127.0.0.1:0"
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	go eng.ServeSMPPListener(ctx, ln)
	h := &harness{t: t, pool: pool, f: f, eng: eng, addr: ln.Addr().String(), stop: func() { cancel(); eng.Stop(); ln.Close() }}
	t.Cleanup(h.stop)
	return h
}

func (h *harness) client() *sim.Client {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := sim.DialClient(ctx, h.addr, "client1", "pass1")
	if err != nil {
		h.t.Fatal(err)
	}
	h.t.Cleanup(c.Session.Close)
	return c
}

func (h *harness) num(sql string, args ...any) float64 {
	var v float64
	if err := h.pool.QueryRow(context.Background(), sql, args...).Scan(&v); err != nil {
		h.t.Fatalf("%v: %s", err, sql)
	}
	return v
}

func waitFor(t *testing.T, what string, timeout time.Duration, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func vendor(t *testing.T, v *sim.Vendor) string {
	addr, err := v.Start("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(v.Close)
	return addr
}

func waitBound(t *testing.T, eng *engine.Engine, connID int64) {
	waitFor(t, "vendor bind", 5*time.Second, func() bool {
		st := eng.ConnectionStatuses()[connID]
		return len(st.Binds) > 0 && st.Binds[0].State == "bound"
	})
}

func TestEndToEndDeliveryBillingAndDLR(t *testing.T) {
	pool := tu.DB(t)
	f := tu.Seed(t, pool)
	v := &sim.Vendor{Mode: sim.VendorOK, DLRDelay: 50 * time.Millisecond}
	conn := tu.Connection(t, pool, f.VendorID, "v1", vendor(t, v), "0.004")
	tu.Route(t, pool, "priority", conn)
	h := start(t, pool, f, "")
	waitBound(t, h.eng, conn)
	c := h.client()

	ctx := context.Background()
	const n = 50
	c.Blast(ctx, n, 200, "Acme", "92300")
	if c.Accepted.Load() != n {
		t.Fatalf("accepted %d of %d", c.Accepted.Load(), n)
	}
	waitFor(t, "all DLRs at client", 10*time.Second, func() bool { return c.Delivered.Load() == n })

	if got := v.Received.Load(); got != n {
		t.Fatalf("vendor received %d submits, want exactly %d", got, n)
	}
	if b := h.num(`SELECT balance FROM balances WHERE client_id = $1`, f.ClientID); b != 9.5 {
		t.Fatalf("balance %v, want 9.5", b)
	}
	if got := h.num(`SELECT count(*) FROM ledger WHERE kind = 'charge'`); got != n {
		t.Fatalf("%v charges, want %d", got, n)
	}
	if got := h.num(`SELECT count(*) FROM messages WHERE status = 'delivered' AND dlr_sent_at IS NOT NULL`); got != n {
		t.Fatalf("%v delivered+forwarded messages, want %d", got, n)
	}
	h.eng.Stop()
	if got := h.num(`SELECT COALESCE(sum(submitted),0) FROM stats_hourly`); got != n {
		t.Fatalf("stats submitted %v, want %d", got, n)
	}
	if got := h.num(`SELECT COALESCE(sum(delivered),0) FROM stats_hourly`); got != n {
		t.Fatalf("stats delivered %v, want %d", got, n)
	}
	if got := h.num(`SELECT COALESCE(sum(revenue - cost),0) FROM stats_hourly`); got < 0.29 || got > 0.31 {
		t.Fatalf("margin %v, want 0.30", got)
	}
}

func TestFailoverToNextVendor(t *testing.T) {
	pool := tu.DB(t)
	f := tu.Seed(t, pool)
	bad := &sim.Vendor{Mode: sim.VendorReject}
	good := &sim.Vendor{Mode: sim.VendorOK}
	c1 := tu.Connection(t, pool, f.VendorID, "bad", vendor(t, bad), "0.002")
	c2 := tu.Connection(t, pool, f.VendorID, "good", vendor(t, good), "0.005")
	tu.Route(t, pool, "lcr", c1, c2) // LCR puts the cheaper (rejecting) vendor first
	h := start(t, pool, f, "")
	waitBound(t, h.eng, c1)
	waitBound(t, h.eng, c2)
	c := h.client()
	id, err := c.Send(context.Background(), "Acme", "923001111111", "hello")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "DLR after failover", 5*time.Second, func() bool { return c.DLRFor(id) == "DELIVRD" })
	if bad.CountFor("923001111111") != 1 || good.CountFor("923001111111") != 1 {
		t.Fatalf("bad got %d, good got %d; want 1 and 1", bad.CountFor("923001111111"), good.CountFor("923001111111"))
	}
	if b := h.num(`SELECT balance FROM balances WHERE client_id = $1`, f.ClientID); b != 9.99 {
		t.Fatalf("balance %v, want 9.99 (charged once)", b)
	}
}

func TestAllVendorsRejectRefundsAndReportsFailure(t *testing.T) {
	pool := tu.DB(t)
	f := tu.Seed(t, pool)
	bad := &sim.Vendor{Mode: sim.VendorReject}
	c1 := tu.Connection(t, pool, f.VendorID, "bad", vendor(t, bad), "0.002")
	tu.Route(t, pool, "priority", c1)
	h := start(t, pool, f, "")
	waitBound(t, h.eng, c1)
	c := h.client()
	id, err := c.Send(context.Background(), "Acme", "923002222222", "hello")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "failure DLR", 5*time.Second, func() bool { return c.DLRFor(id) == "REJECTD" })
	if b := h.num(`SELECT balance FROM balances WHERE client_id = $1`, f.ClientID); b != 10 {
		t.Fatalf("balance %v, want 10 (refunded)", b)
	}
	if got := h.num(`SELECT count(*) FROM ledger WHERE kind = 'refund'`); got != 1 {
		t.Fatalf("%v refunds, want 1", got)
	}
}

func TestSilentVendorIsNeverResent(t *testing.T) {
	pool := tu.DB(t)
	f := tu.Seed(t, pool)
	silent := &sim.Vendor{Mode: sim.VendorNoAck}
	c1 := tu.Connection(t, pool, f.VendorID, "silent", vendor(t, silent), "0.002")
	tu.Route(t, pool, "priority", c1)
	h := start(t, pool, f, "")
	waitBound(t, h.eng, c1)
	c := h.client()
	if _, err := c.Send(context.Background(), "Acme", "923003333333", "hello"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "message marked unknown", 8*time.Second, func() bool {
		return h.num(`SELECT count(*) FROM messages WHERE status = 'unknown'`) == 1
	})
	time.Sleep(4 * time.Second) // well past the response timeout: nothing may be resent
	if got := silent.CountFor("923003333333"); got != 1 {
		t.Fatalf("silent vendor received %d copies, want exactly 1", got)
	}
}

func TestThrottledVendorRetriesSameMessage(t *testing.T) {
	pool := tu.DB(t)
	f := tu.Seed(t, pool)
	v := &sim.Vendor{Mode: sim.VendorThrottle, ThrottleN: 2}
	c1 := tu.Connection(t, pool, f.VendorID, "busy", vendor(t, v), "0.002")
	tu.Route(t, pool, "priority", c1)
	h := start(t, pool, f, "")
	waitBound(t, h.eng, c1)
	c := h.client()
	id, _ := c.Send(context.Background(), "Acme", "923004444444", "hello")
	waitFor(t, "delivery after throttling", 20*time.Second, func() bool { return c.DLRFor(id) == "DELIVRD" })
	if got := h.num(`SELECT count(*) FROM ledger WHERE kind = 'charge'`); got != 1 {
		t.Fatalf("%v charges, want 1", got)
	}
}

func TestHexVendorIDsStillMatchDLR(t *testing.T) {
	pool := tu.DB(t)
	f := tu.Seed(t, pool)
	v := &sim.Vendor{Mode: sim.VendorOK, HexIDs: true}
	c1 := tu.Connection(t, pool, f.VendorID, "hex", vendor(t, v), "0.002")
	tu.Route(t, pool, "priority", c1)
	h := start(t, pool, f, "")
	waitBound(t, h.eng, c1)
	c := h.client()
	id, _ := c.Send(context.Background(), "Acme", "923005555555", "hello")
	waitFor(t, "DLR matched across hex/decimal ids", 5*time.Second, func() bool { return c.DLRFor(id) == "DELIVRD" })
}

// A vendor that restarts reuses message IDs; the DLR must go to the newest message with that ID, not to the
// old one that used it before.
func TestReusedVendorIDsMatchTheNewestMessage(t *testing.T) {
	pool := tu.DB(t)
	f := tu.Seed(t, pool)
	v := &sim.Vendor{Mode: sim.VendorOK}
	c1 := tu.Connection(t, pool, f.VendorID, "v", vendor(t, v), "0.002")
	tu.Route(t, pool, "priority", c1)
	h := start(t, pool, f, "")
	waitBound(t, h.eng, c1)
	c := h.client()
	first, _ := c.Send(context.Background(), "Acme", "923005555551", "before the vendor restart")
	waitFor(t, "first DLR", 5*time.Second, func() bool { return c.DLRFor(first) == "DELIVRD" })
	v.ResetIDs()
	second, _ := c.Send(context.Background(), "Acme", "923005555552", "after the vendor restart")
	waitFor(t, "DLR for the message that reused the vendor ID", 5*time.Second, func() bool { return c.DLRFor(second) == "DELIVRD" })
}

func TestRejections(t *testing.T) {
	pool := tu.DB(t)
	f := tu.Seed(t, pool)
	v := &sim.Vendor{Mode: sim.VendorOK}
	c1 := tu.Connection(t, pool, f.VendorID, "v", vendor(t, v), "0.002")
	tu.Route(t, pool, "priority", c1)
	h := start(t, pool, f, "")
	c := h.client()
	ctx := context.Background()

	if _, err := c.Send(ctx, "Acme", "15551234567", "no US rate"); err == nil {
		t.Fatal("expected rejection without a rate")
	}
	if _, err := c.Send(ctx, "Acme", "12", "bad number"); err == nil {
		t.Fatal("expected invalid destination")
	}
	tu.Exec(t, pool, `UPDATE balances SET balance = 0.005 WHERE client_id = $1`, f.ClientID)
	if _, err := c.Send(ctx, "Acme", "923006666666", "poor"); err == nil {
		t.Fatal("expected insufficient balance")
	}
	if c.StatusCount(smpp.StatusRejectAppErr) != 2 || c.StatusCount(smpp.StatusInvDstAdr) != 1 {
		t.Fatalf("unexpected statuses: no-rate/balance %d, invalid %d", c.StatusCount(smpp.StatusRejectAppErr), c.StatusCount(smpp.StatusInvDstAdr))
	}
	if got := h.num(`SELECT count(*) FROM messages`); got != 0 {
		t.Fatalf("%v messages stored, want 0", got)
	}

	// Wrong password must not bind.
	bctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if _, err := sim.DialClient(bctx, h.addr, "client1", "wrong"); err == nil {
		t.Fatal("bind with wrong password succeeded")
	}
}

func TestDLRSurvivesGatewayRestart(t *testing.T) {
	pool := tu.DB(t)
	f := tu.Seed(t, pool)
	v := &sim.Vendor{Mode: sim.VendorOK, DLRDelay: 1500 * time.Millisecond}
	c1 := tu.Connection(t, pool, f.VendorID, "slowdlr", vendor(t, v), "0.002")
	tu.Route(t, pool, "priority", c1)
	h := start(t, pool, f, "")
	waitBound(t, h.eng, c1)
	c := h.client()
	id, err := c.Send(context.Background(), "Acme", "923007777777", "survive")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "message sent", 3*time.Second, func() bool { return h.num(`SELECT count(*) FROM messages WHERE status = 'sent'`) == 1 })

	// Restart the gateway before the DLR arrives (the old system lost these DLRs).
	addr := h.addr
	h.stop()
	c.Session.Close()
	h2 := start(t, pool, f, addr)
	waitBound(t, h2.eng, c1)
	c2 := h2.client()
	waitFor(t, "DLR delivered after restart", 15*time.Second, func() bool { return c2.DLRFor(id) == "DELIVRD" })
}

func TestBlacklistRejects(t *testing.T) {
	pool := tu.DB(t)
	f := tu.Seed(t, pool)
	v := &sim.Vendor{Mode: sim.VendorOK}
	c1 := tu.Connection(t, pool, f.VendorID, "v", vendor(t, v), "0.002")
	tu.Route(t, pool, "priority", c1)
	tu.Exec(t, pool, `INSERT INTO blacklist (client_id, number) VALUES (NULL, '923001110000'), ($1, '923001110001')`, f.ClientID)
	h := start(t, pool, f, "")
	c := h.client()
	ctx := context.Background()
	for _, n := range []string{"923001110000", "+92 300 1110001"} {
		if _, err := c.Send(ctx, "Acme", n, "x"); err == nil {
			t.Fatalf("%s is blacklisted but was accepted", n)
		}
	}
	if _, err := c.Send(ctx, "Acme", "923001110002", "x"); err != nil {
		t.Fatalf("non-blacklisted number rejected: %v", err)
	}
	if got := h.num(`SELECT count(*) FROM messages`); got != 1 {
		t.Fatalf("%v messages stored, want 1", got)
	}
}

func TestFailoverAfterNegativeDLR(t *testing.T) {
	pool := tu.DB(t)
	f := tu.Seed(t, pool)
	bad := &sim.Vendor{Mode: sim.VendorOK, FailRatio: 1} // accepts, then reports UNDELIV
	good := &sim.Vendor{Mode: sim.VendorOK}
	c1 := tu.Connection(t, pool, f.VendorID, "undeliv", vendor(t, bad), "0.002")
	c2 := tu.Connection(t, pool, f.VendorID, "good", vendor(t, good), "0.004")
	tu.Exec(t, pool, `UPDATE connections SET failover_on_dlr = '{UNDELIV}' WHERE id = $1`, c1)
	tu.Route(t, pool, "priority", c1, c2)
	h := start(t, pool, f, "")
	waitBound(t, h.eng, c1)
	waitBound(t, h.eng, c2)
	c := h.client()
	id, err := c.Send(context.Background(), "Acme", "923002220000", "retry me")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "final DLR after DLR failover", 10*time.Second, func() bool { return c.DLRFor(id) == "DELIVRD" })
	if bad.CountFor("923002220000") != 1 || good.CountFor("923002220000") != 1 {
		t.Fatalf("vendor submits: bad %d, good %d; want 1 and 1", bad.CountFor("923002220000"), good.CountFor("923002220000"))
	}
	if got := h.num(`SELECT count(*) FROM ledger WHERE kind = 'charge'`); got != 1 {
		t.Fatalf("%v charges, want 1", got)
	}
	if got := h.num(`SELECT cost FROM messages`); got != 0.006 {
		t.Fatalf("cost %v, want both vendors' cost 0.006", got)
	}
}

func TestMOForwardingAndOptOut(t *testing.T) {
	pool := tu.DB(t)
	f := tu.Seed(t, pool)
	v := &sim.Vendor{Mode: sim.VendorOK}
	c1 := tu.Connection(t, pool, f.VendorID, "v", vendor(t, v), "0.002")
	tu.Route(t, pool, "priority", c1)
	tu.Exec(t, pool, `INSERT INTO mo_routes (name, number_prefix, client_id, account_id) VALUES ('short code', '8899', $1, $2)`, f.ClientID, f.AccountID)
	h := start(t, pool, f, "")
	waitBound(t, h.eng, c1)
	c := h.client()
	ctx := context.Background()
	if err := v.SendMO(ctx, "923003330000", "8899", "Hello there"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "MO at client", 5*time.Second, func() bool { return len(c.MOs()) == 1 })
	if got := c.MOs()[0]; got != "923003330000|8899|Hello there" {
		t.Fatalf("MO content: %q", got)
	}
	// STOP adds the sender to the client's blacklist; sending to them is then rejected.
	if err := v.SendMO(ctx, "923003330000", "8899", "STOP"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "opt-out applied", 8*time.Second, func() bool {
		_, err := c.Send(ctx, "Acme", "923003330000", "promo")
		return err != nil
	})
	if got := h.num(`SELECT count(*) FROM blacklist WHERE client_id = $1 AND number = '923003330000'`, f.ClientID); got != 1 {
		t.Fatalf("blacklist rows %v", got)
	}
	// An MO for a number without a route is stored but not forwarded.
	if err := v.SendMO(ctx, "923003330001", "7777", "lost"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "unrouted MO stored", 3*time.Second, func() bool {
		return h.num(`SELECT count(*) FROM messages WHERE direction = 'mo' AND status = 'unrouted'`) == 1
	})
}
