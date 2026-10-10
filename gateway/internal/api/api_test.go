package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Adnan-Dogar/telivoz-gateway/internal/api"
	"github.com/Adnan-Dogar/telivoz-gateway/internal/auth"
	"github.com/Adnan-Dogar/telivoz-gateway/internal/engine"
	"github.com/Adnan-Dogar/telivoz-gateway/internal/sheet"
	"github.com/Adnan-Dogar/telivoz-gateway/internal/sim"
	tu "github.com/Adnan-Dogar/telivoz-gateway/internal/testutil"
	"github.com/jackc/pgx/v5/pgxpool"
)

type env struct {
	t    *testing.T
	pool *pgxpool.Pool
	f    tu.Fixture
	srv  *httptest.Server
}

func setup(t *testing.T) *env {
	pool := tu.DB(t)
	f := tu.Seed(t, pool)
	v := &sim.Vendor{Mode: sim.VendorOK}
	addr, err := v.Start("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(v.Close)
	conn := tu.Connection(t, pool, f.VendorID, "v1", addr, "0.004")
	tu.Route(t, pool, "priority", conn)

	ctx, cancel := context.WithCancel(context.Background())
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	eng := engine.New(pool, tu.Cipher(), log)
	if err := eng.Start(ctx); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(api.New(pool, eng, tu.Cipher(), log, api.Options{WebDir: t.TempDir()}).Handler())
	t.Cleanup(func() { srv.Close(); cancel(); eng.Stop() })
	return &env{t: t, pool: pool, f: f, srv: srv}
}

// client is a browser-like session.
type client struct {
	e    *env
	http *http.Client
}

func (e *env) browser() *client {
	jar, _ := cookiejar.New(nil)
	return &client{e: e, http: &http.Client{Jar: jar}}
}

func (c *client) do(method, path string, body any, csrf bool) (int, map[string]any, string) {
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = strings.NewReader(string(b))
	}
	req, _ := http.NewRequest(method, c.e.srv.URL+path, r)
	req.Header.Set("Content-Type", "application/json")
	if csrf {
		req.Header.Set("X-Requested-With", "telivoz")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		c.e.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return resp.StatusCode, out, string(raw)
}

func (c *client) login(email, pw string) int {
	code, _, _ := c.do("POST", "/api/auth/login", map[string]string{"email": email, "password": pw}, true)
	return code
}

func (e *env) user(email, role string, managerID, clientID int64) int64 {
	h, _ := auth.HashPassword("password1")
	var mgr, cl any
	if managerID != 0 {
		mgr = managerID
	}
	if clientID != 0 {
		cl = clientID
	}
	return tu.ID(e.t, e.pool, `INSERT INTO users (email, name, password_hash, role, manager_id, client_id) VALUES ($1, $1, $2, $3, $4, $5) RETURNING id`,
		email, h, role, mgr, cl)
}

func TestLoginSessionAndCSRF(t *testing.T) {
	e := setup(t)
	b := e.browser()
	if code := b.login("admin@test", "wrong"); code != http.StatusUnauthorized {
		t.Fatalf("wrong password: %d", code)
	}
	if code, _, _ := b.do("GET", "/api/auth/me", nil, false); code != http.StatusUnauthorized {
		t.Fatalf("me without session: %d", code)
	}
	if code := b.login("ADMIN@test", "admin123"); code != http.StatusOK {
		t.Fatalf("login: %d", code)
	}
	if code, me, _ := b.do("GET", "/api/auth/me", nil, false); code != 200 || me["role"] != "admin" {
		t.Fatalf("me: %d %v", code, me)
	}
	if code, _, _ := b.do("POST", "/api/clients", map[string]any{"name": "NoCSRF"}, false); code != http.StatusForbidden {
		t.Fatalf("missing CSRF header must be rejected, got %d", code)
	}
	code, created, raw := b.do("POST", "/api/clients", map[string]any{"name": "Beta Ltd", "credit_limit": "5"}, true)
	if code != http.StatusCreated || created["balance"] != float64(0) {
		t.Fatalf("create client: %d %s", code, raw)
	}
	b.do("POST", "/api/auth/logout", nil, true)
	if code, _, _ := b.do("GET", "/api/auth/me", nil, false); code != http.StatusUnauthorized {
		t.Fatal("session must end at logout")
	}
}

func TestLoginLockout(t *testing.T) {
	e := setup(t)
	b := e.browser()
	for i := 0; i < 10; i++ {
		b.login("admin@test", "nope")
	}
	if code := b.login("admin@test", "admin123"); code != http.StatusTooManyRequests {
		t.Fatalf("expected lockout after 10 failures, got %d", code)
	}
}

func TestHierarchyScoping(t *testing.T) {
	e := setup(t)
	manager := e.user("mgr@test", "manager", 0, 0)
	sales := e.user("sales@test", "sales", manager, 0)
	other := e.user("other@test", "sales", 0, 0)
	salesClient := tu.ID(t, e.pool, `INSERT INTO clients (name, owner_id) VALUES ('Sales Client', $1) RETURNING id`, sales)
	_ = other

	m := e.browser()
	m.login("mgr@test", "password1")
	_, list, _ := m.do("GET", "/api/clients", nil, false)
	if list["total"] != float64(1) {
		t.Fatalf("manager should see exactly the team's client, got %v", list["total"])
	}
	o := e.browser()
	o.login("other@test", "password1")
	if _, list, _ := o.do("GET", "/api/clients", nil, false); list["total"] != float64(0) {
		t.Fatalf("unrelated sales user must see no clients, got %v", list["total"])
	}
	if code, _, _ := o.do("GET", "/api/clients/"+itoa(salesClient), nil, false); code != http.StatusNotFound {
		t.Fatalf("unrelated sales user opening another team's client: %d", code)
	}
	if code, _, _ := o.do("POST", "/api/clients", map[string]any{"name": "x"}, true); code != http.StatusForbidden {
		t.Fatalf("sales users cannot create clients: %d", code)
	}
}

func TestClientUserCannotSeeVendors(t *testing.T) {
	e := setup(t)
	e.user("cust@test", "client", 0, e.f.ClientID)
	c := e.browser()
	c.login("cust@test", "password1")
	if code, _, _ := c.do("GET", "/api/vendors", nil, false); code != http.StatusForbidden {
		t.Fatalf("client listing vendors: %d", code)
	}
	if code, _, _ := c.do("GET", "/api/stats/breakdown?dim=vendor", nil, false); code != http.StatusForbidden {
		t.Fatalf("client vendor breakdown: %d", code)
	}
	if _, list, _ := c.do("GET", "/api/clients", nil, false); list["total"] != float64(1) {
		t.Fatalf("client should see only itself: %v", list["total"])
	}
}

func itoa(n int64) string    { return strings.TrimSpace(strings.Repeat(" ", 0) + jsonNum(n)) }
func jsonNum(n int64) string { b, _ := json.Marshal(n); return string(b) }

func TestClientAPIv1AndLegacy(t *testing.T) {
	e := setup(t)
	admin := e.browser()
	admin.login("admin@test", "admin123")
	code, key, raw := admin.do("POST", "/api/accounts/"+itoa(e.f.HTTPAccID)+"/api-keys", map[string]string{"name": "test"}, true)
	if code != http.StatusCreated {
		t.Fatalf("create key: %d %s", code, raw)
	}
	apiKey := key["key"].(string)

	send := func(body, idem string) (int, map[string]any) {
		req, _ := http.NewRequest("POST", e.srv.URL+"/api/v1/messages", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+apiKey)
		if idem != "" {
			req.Header.Set("Idempotency-Key", idem)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return resp.StatusCode, out
	}
	code, first := send(`{"to":"+923001234567","from":"Acme","text":"hello"}`, "order-42")
	if code != http.StatusAccepted || first["id"] == nil {
		t.Fatalf("v1 send: %d %v", code, first)
	}
	_, again := send(`{"to":"+923001234567","from":"Acme","text":"hello"}`, "order-42")
	if again["id"] != first["id"] || again["duplicate"] != true {
		t.Fatalf("same Idempotency-Key must return the same message: %v vs %v", again, first)
	}
	var charges int
	_ = e.pool.QueryRow(context.Background(), `SELECT count(*) FROM ledger WHERE kind = 'charge'`).Scan(&charges)
	if charges != 1 {
		t.Fatalf("%d charges for one idempotent message", charges)
	}
	code, batch := send(`{"messages":[{"to":"923001111111","text":"a"},{"to":"1","text":"b"}]}`, "")
	results := batch["results"].([]any)
	if code != http.StatusAccepted || results[0].(map[string]any)["id"] == nil || results[1].(map[string]any)["error"] != "invalid_destination" {
		t.Fatalf("batch: %d %v", code, batch)
	}
	if code, _ := send(`{"to":"923001234567","text":"x"}`, ""); code != http.StatusAccepted {
		t.Fatalf("send: %d", code)
	}
	req, _ := http.NewRequest("POST", e.srv.URL+"/api/v1/messages", strings.NewReader(`{"to":"923001234567","text":"x"}`))
	req.Header.Set("Authorization", "Bearer tvz_wrong")
	resp, _ := http.DefaultClient.Do(req)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("bad key: %d", resp.StatusCode)
	}

	// Message status through the API, once the vendor DLR came back.
	deadline := time.Now().Add(5 * time.Second)
	for {
		req, _ := http.NewRequest("GET", e.srv.URL+"/api/v1/messages/"+first["id"].(string), nil)
		req.Header.Set("Authorization", "Bearer "+apiKey)
		resp, _ := http.DefaultClient.Do(req)
		var st map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&st)
		resp.Body.Close()
		if st["status"] == "delivered" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("message never reached delivered: %v", st)
		}
		time.Sleep(100 * time.Millisecond)
	}

	legacy := func(form url.Values) map[string]any {
		resp, err := http.PostForm(e.srv.URL+"/api/legacy", form)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return out
	}
	if out := legacy(url.Values{"username": {"api1"}, "password": {"bad"}, "to": {"923001234567"}, "message": {"x"}}); out["status"] != float64(403) {
		t.Fatalf("legacy bad login: %v", out)
	}
	out := legacy(url.Values{"username": {"api1"}, "password": {"apipass"}, "to": {"923001234567"}, "message": {"x"}, "messageid": {"m-1"}})
	if out["status"] != float64(1) || out["description"] != "Success [m-1]" {
		t.Fatalf("legacy send: %v", out)
	}
	if out := legacy(url.Values{"username": {"api1"}, "password": {"apipass"}, "action": {"balance"}}); out["status"] != float64(1) || out["balance"] == nil {
		t.Fatalf("legacy balance: %v", out)
	}
}

func TestStatsEndpoints(t *testing.T) {
	e := setup(t)
	// Latency 12345 ms over 7 DLRs gives a fractional average (1763.57), which must still load.
	tu.Exec(t, e.pool, `INSERT INTO stats_hourly (hour, client_id, connection_id, country_iso, submitted, sent, delivered, undelivered, revenue, cost,
			dlr_latency_ms_sum, dlr_latency_count)
		VALUES (date_trunc('hour', now()) - interval '1 hour', $1, 1, 'PK', 100, 100, 90, 10, 1.0, 0.4, 12345, 7)`, e.f.ClientID)
	b := e.browser()
	b.login("admin@test", "admin123")
	code, ov, raw := b.do("GET", "/api/stats/overview", nil, false)
	if code != 200 {
		t.Fatalf("overview: %d %s", code, raw)
	}
	totals := ov["totals"].(map[string]any)
	if totals["submitted"] != float64(100) || totals["delivered"] != float64(90) {
		t.Fatalf("totals: %v", totals)
	}
	code, bd, raw := b.do("GET", "/api/stats/breakdown?dim=client", nil, false)
	rows := bd["rows"].([]any)
	if code != 200 || len(rows) != 1 || rows[0].(map[string]any)["dlr_rate"] != 0.9 || rows[0].(map[string]any)["label"] != "Acme" ||
		rows[0].(map[string]any)["avg_dlr_ms"] != float64(1764) {
		t.Fatalf("breakdown: %d %s", code, raw)
	}
	resp, _ := b.http.Get(e.srv.URL + "/api/stats/breakdown?dim=country&format=csv")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "Pakistan,100,100,90,10,0,90.0%") {
		t.Fatalf("csv: %s", body)
	}
	if code, live, _ := b.do("GET", "/api/stats/live", nil, false); code != 200 || live["connections"] == nil {
		t.Fatalf("live: %d %v", code, live)
	}
}

func TestTwoFactorLogin(t *testing.T) {
	e := setup(t)
	b := e.browser()
	b.login("admin@test", "admin123")
	code, setupResp, raw := b.do("POST", "/api/auth/2fa/setup", nil, true)
	if code != 200 || setupResp["secret"] == nil {
		t.Fatalf("setup: %d %s", code, raw)
	}
	secret := setupResp["secret"].(string)
	if code, _, _ := b.do("POST", "/api/auth/2fa/enable", map[string]string{"code": "000000"}, true); code != 400 {
		t.Fatalf("wrong code accepted: %d", code)
	}
	valid, _ := auth.TOTPCode(secret, time.Now())
	if code, _, raw := b.do("POST", "/api/auth/2fa/enable", map[string]string{"code": valid}, true); code != 200 {
		t.Fatalf("enable: %d %s", code, raw)
	}

	n := e.browser()
	if code, out, _ := n.do("POST", "/api/auth/login", map[string]string{"email": "admin@test", "password": "admin123"}, true); code != 401 || out["error"] != "totp_required" {
		t.Fatalf("login without code: %d %v", code, out)
	}
	if code, out, _ := n.do("POST", "/api/auth/login", map[string]any{"email": "admin@test", "password": "admin123", "code": "123456"}, true); code != 401 || out["error"] != "invalid_code" {
		t.Fatalf("login with wrong code: %d %v", code, out)
	}
	valid, _ = auth.TOTPCode(secret, time.Now())
	if code, me, _ := n.do("POST", "/api/auth/login", map[string]any{"email": "admin@test", "password": "admin123", "code": valid}, true); code != 200 || me["totp_enabled"] != true {
		t.Fatalf("login with code: %d %v", code, me)
	}
	// An admin can reset it (lost phone); the user then signs in with the password only.
	if code, _, _ := n.do("POST", "/api/users/"+itoa(e.f.AdminID)+"/reset-2fa", nil, true); code != 200 {
		t.Fatalf("reset: %d", code)
	}
	if code := e.browser().login("admin@test", "admin123"); code != 200 {
		t.Fatalf("login after reset: %d", code)
	}
}

func TestBlacklistPermissionsAndImport(t *testing.T) {
	e := setup(t)
	e.user("cust@test", "client", 0, e.f.ClientID)
	c := e.browser()
	c.login("cust@test", "password1")
	// A client adds to its own list (client_id is forced) and cannot touch the global list.
	code, row, raw := c.do("POST", "/api/blacklist", map[string]any{"number": "+92 300 999 0001", "client_id": 0}, true)
	if code != 201 || row["number"] != "923009990001" || row["client_id"] != float64(e.f.ClientID) {
		t.Fatalf("client add: %d %s", code, raw)
	}
	req, _ := http.NewRequest("POST", e.srv.URL+"/api/blacklist/import", strings.NewReader("923009990002\n923009990003, 923009990002\nbad"))
	req.Header.Set("X-Requested-With", "telivoz")
	resp, _ := c.http.Do(req)
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	resp.Body.Close()
	if out["added"] != float64(2) {
		t.Fatalf("import: %v", out)
	}
	sales := e.user("s@test", "sales", 0, 0)
	_ = sales
	s := e.browser()
	s.login("s@test", "password1")
	if code, _, _ := s.do("POST", "/api/blacklist", map[string]any{"number": "923009990009"}, true); code != http.StatusForbidden {
		t.Fatalf("sales adding to the global list: %d", code)
	}
	if code, _, _ := s.do("POST", "/api/blacklist", map[string]any{"number": "923009990009", "client_id": e.f.ClientID}, true); code != http.StatusForbidden {
		t.Fatalf("sales adding for a client outside their team: %d", code)
	}
	if _, list, _ := c.do("GET", "/api/blacklist", nil, false); list["total"] != float64(3) {
		t.Fatalf("client sees %v entries, want 3", list["total"])
	}
}

func TestStatement(t *testing.T) {
	e := setup(t)
	a := e.browser()
	a.login("admin@test", "admin123")
	month := time.Now().UTC().Format("2006-01")
	a.do("POST", "/api/clients/"+itoa(e.f.ClientID)+"/topup", map[string]any{"amount": "50", "note": "wire"}, true)
	tu.Exec(t, e.pool, `INSERT INTO ledger (client_id, kind, amount, balance_after) VALUES ($1, 'charge', -1.5, 58.5)`, e.f.ClientID)
	tu.Exec(t, e.pool, `INSERT INTO messages (id, created_at, client_id, destination, country_iso, price, status)
		SELECT gen_random_uuid(), now(), $1, '923001234567', 'PK', 0.01, CASE WHEN g <= 120 THEN 'delivered' ELSE 'sent' END
		FROM generate_series(1, 150) g`, e.f.ClientID)
	// A failed (refunded) message is not billed.
	tu.Exec(t, e.pool, `INSERT INTO messages (id, created_at, client_id, destination, country_iso, price, status)
		VALUES (gen_random_uuid(), now(), $1, '923001234567', 'PK', 0.01, 'failed')`, e.f.ClientID)
	code, st, raw := a.do("GET", "/api/clients/"+itoa(e.f.ClientID)+"/statement?month="+month, nil, false)
	if code != 200 || st["charged"] != 1.5 || st["closing_balance"] != 58.5 || len(st["payments"].([]any)) != 1 {
		t.Fatalf("statement: %d %s", code, raw)
	}
	usage := st["usage"].([]any)[0].(map[string]any)
	if usage["country"] != "Pakistan" || usage["messages"] != float64(150) || usage["delivered"] != float64(120) ||
		usage["unit_price"] != 0.01 {
		t.Fatalf("usage: %v", usage)
	}
}

func TestUsersScopedToTeam(t *testing.T) {
	e := setup(t)
	mgr := e.user("m@test", "manager", 0, 0)
	e.user("lead@test", "team_lead", mgr, 0)
	e.user("outsider@test", "sales", 0, 0)
	m := e.browser()
	m.login("m@test", "password1")
	if _, list, _ := m.do("GET", "/api/users", nil, false); list["total"] != float64(2) {
		t.Fatalf("manager sees %v users, want 2 (self and team)", list["total"])
	}
}

func TestMobileBearerToken(t *testing.T) {
	e := setup(t)
	code, out, raw := e.browser().do("POST", "/api/auth/login",
		map[string]any{"email": "admin@test", "password": "admin123", "token": true}, true)
	token, _ := out["token"].(string)
	if code != 200 || token == "" || out["user"].(map[string]any)["email"] != "admin@test" {
		t.Fatalf("token login: %d %s", code, raw)
	}
	get := func(auth string) int {
		req, _ := http.NewRequest("GET", e.srv.URL+"/api/auth/me", nil)
		req.Header.Set("Authorization", auth)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if c := get("Bearer " + token); c != 200 {
		t.Fatalf("bearer request: %d", c)
	}
	if c := get("Bearer wrong"); c != 401 {
		t.Fatalf("wrong bearer: %d", c)
	}
	// A plain browser login never exposes the token in the body.
	_, out, _ = e.browser().do("POST", "/api/auth/login", map[string]any{"email": "admin@test", "password": "admin123"}, true)
	if _, ok := out["token"]; ok {
		t.Fatal("cookie login returned the token")
	}
}

// raw sends a non-JSON body (or none) and returns the status and raw response.
func (c *client) raw(method, path string, body []byte) (int, []byte, http.Header) {
	req, _ := http.NewRequest(method, c.e.srv.URL+path, bytes.NewReader(body))
	req.Header.Set("X-Requested-With", "telivoz")
	resp, err := c.http.Do(req)
	if err != nil {
		c.e.t.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, out, resp.Header
}

func TestExcelRateImportAndExports(t *testing.T) {
	e := setup(t)
	a := e.browser()
	a.login("admin@test", "admin123")

	// Rates from an Excel file, as saved by Excel: numeric MNC without its leading zero, date as a serial number.
	tu.Exec(t, e.pool, `INSERT INTO networks (mcc, mnc, name, country_iso) VALUES ('410', '07', 'Test Net', 'PK') ON CONFLICT DO NOTHING`)
	var xlsx bytes.Buffer
	if err := sheet.WriteXLSX(&xlsx, "rates", [][]string{
		{"country_iso", "mcc", "mnc", "price", "effective_from"},
		{"PK", "", "", "0.0123", ""},
		{"PK", "410", "7", "0.0150", "46000"},
	}); err != nil {
		t.Fatal(err)
	}
	code, out, _ := a.raw("POST", "/api/rates/import?target=client&id="+itoa(e.f.ClientID), xlsx.Bytes())
	if code != 200 || !strings.Contains(string(out), `"imported":2`) {
		t.Fatalf("xlsx import: %d %s", code, out)
	}
	var eff time.Time
	if err := e.pool.QueryRow(context.Background(), `SELECT effective_from FROM client_rates r JOIN networks n ON n.id = r.network_id
		WHERE r.client_id = $1 AND n.mnc = '07'`, e.f.ClientID).Scan(&eff); err != nil || eff.Format("2006-01-02") != "2025-12-09" {
		t.Fatalf("network rate effective_from %v %v", eff, err)
	}

	// Message export: staff see vendor cost, clients do not (in the export or the JSON list).
	tu.Exec(t, e.pool, `INSERT INTO messages (id, created_at, client_id, destination, body, country_iso, price, cost, status, dlr_status)
		VALUES (gen_random_uuid(), now(), $1, '923001234567', 'hello, "world"', 'PK', 0.0123, 0.004, 'delivered', 'DELIVRD')`, e.f.ClientID)
	code, out, h := a.raw("GET", "/api/messages?format=csv", nil)
	if code != 200 || !strings.Contains(h.Get("Content-Disposition"), ".csv") ||
		!strings.Contains(string(out), "vendor_connection,cost") || !strings.Contains(string(out), `"hello, ""world"""`) {
		t.Fatalf("staff csv: %d %s", code, out)
	}
	code, out, _ = a.raw("GET", "/api/messages?format=xlsx", nil)
	rows, err := sheet.ReadXLSX(out)
	if code != 200 || err != nil || len(rows) != 2 || rows[1][5] != "923001234567" || rows[1][12] != "DELIVRD" {
		t.Fatalf("staff xlsx: %d %v %q", code, err, rows)
	}

	e.user("cust@test", "client", 0, e.f.ClientID)
	c := e.browser()
	c.login("cust@test", "password1")
	code, out, _ = c.raw("GET", "/api/messages?format=csv", nil)
	if code != 200 || strings.Contains(string(out), "cost") || strings.Contains(string(out), "vendor") {
		t.Fatalf("client csv shows vendor data: %d %s", code, out)
	}
	_, _, list := c.do("GET", "/api/messages", nil, false)
	if strings.Contains(list, `"cost"`) || strings.Contains(list, "connection_name") || !strings.Contains(list, "923001234567") {
		t.Fatalf("client list shows vendor data: %s", list)
	}

	// Report download as Excel.
	code, out, _ = a.raw("GET", "/api/stats/breakdown?dim=country&format=xlsx", nil)
	if rows, err := sheet.ReadXLSX(out); code != 200 || err != nil || rows[0][0] != "country" {
		t.Fatalf("report xlsx: %d %v %q", code, err, rows)
	}
}

func TestVendorQuality(t *testing.T) {
	e := setup(t)
	conn := tu.Connection(t, e.pool, e.f.VendorID, "q1", "127.0.0.1:1", "0.004")
	// 200 final DLRs, 180 delivered, 12 s average DLR time: score 90 - 2 = 88.
	tu.Exec(t, e.pool, `INSERT INTO stats_hourly (hour, client_id, connection_id, country_iso, sent, delivered, undelivered, cost,
			dlr_latency_ms_sum, dlr_latency_count)
		VALUES (date_trunc('hour', now()) - interval '2 hours', $1, $2, 'PK', 200, 180, 20, 0.8, 2400000, 200)`, e.f.ClientID, conn)
	a := e.browser()
	a.login("admin@test", "admin123")
	code, out, raw := a.do("GET", "/api/stats/vendor-quality", nil, false)
	rows, _ := out["rows"].([]any)
	if code != 200 || len(rows) != 1 {
		t.Fatalf("vendor quality: %d %s", code, raw)
	}
	r := rows[0].(map[string]any)
	if r["score"] != float64(88) || r["trusted"] != true || r["dlr_rate"] != 0.9 || r["avg_cost"] != 0.004 {
		t.Fatalf("row: %v", r)
	}
	e.user("cust@test", "client", 0, e.f.ClientID)
	c := e.browser()
	c.login("cust@test", "password1")
	if code, _, _ := c.do("GET", "/api/stats/vendor-quality", nil, false); code != http.StatusForbidden {
		t.Fatalf("client sees vendor quality: %d", code)
	}
	// The new route policies are accepted.
	if code, _, raw := a.do("POST", "/api/routes", map[string]any{"name": "best", "policy": "balanced", "country_iso": "PK"}, true); code != 201 {
		t.Fatalf("balanced route: %d %s", code, raw)
	}
}

func TestBrandingPerDomain(t *testing.T) {
	e := setup(t)
	get := func(host string) map[string]any {
		req, _ := http.NewRequest("GET", e.srv.URL+"/api/public/branding", nil)
		req.Host = host
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return out
	}
	if b := get("portal.example.com"); b["name"] != "Telivoz" {
		t.Fatalf("built-in default: %v", b)
	}
	a := e.browser()
	a.login("admin@test", "admin123")
	for _, in := range []map[string]any{
		{"domain": "", "name": "Telivoz Cloud", "primary_color": "#4F46E5"},
		{"domain": "sms.reseller.pk", "name": "Reseller SMS", "primary_color": "#0EA5E9", "logo": "data:image/png;base64,iVBORw0KGgo="},
	} {
		if code, _, raw := a.do("POST", "/api/branding", in, true); code != 201 {
			t.Fatalf("create branding: %d %s", code, raw)
		}
	}
	if b := get("sms.reseller.pk:443"); b["name"] != "Reseller SMS" || b["primary_color"] != "#0EA5E9" {
		t.Fatalf("reseller domain: %v", b)
	}
	if b := get("other.example.com"); b["name"] != "Telivoz Cloud" {
		t.Fatalf("default row: %v", b)
	}
	// Scripts or remote URLs are refused as logos, and colours must be hex.
	for _, bad := range []map[string]any{
		{"domain": "x.pk", "name": "X", "logo": "javascript:alert(1)"},
		{"domain": "y.pk", "name": "Y", "primary_color": "red; background:url(x)"},
	} {
		if code, _, _ := a.do("POST", "/api/branding", bad, true); code != http.StatusBadRequest {
			t.Fatalf("bad branding accepted: %v → %d", bad, code)
		}
	}
	// Only admins manage branding.
	e.user("m@test", "manager", 0, 0)
	m := e.browser()
	m.login("m@test", "password1")
	if code, _, _ := m.do("POST", "/api/branding", map[string]any{"domain": "z.pk", "name": "Z"}, true); code != http.StatusForbidden {
		t.Fatalf("manager wrote branding: %d", code)
	}
}
