package api_test

import (
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

func itoa(n int64) string { return strings.TrimSpace(strings.Repeat(" ", 0) + jsonNum(n)) }
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
	tu.Exec(t, e.pool, `INSERT INTO stats_hourly (hour, client_id, connection_id, country_iso, submitted, sent, delivered, undelivered, revenue, cost)
		VALUES (date_trunc('hour', now()) - interval '1 hour', $1, 1, 'PK', 100, 100, 90, 10, 1.0, 0.4)`, e.f.ClientID)
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
	if code != 200 || len(rows) != 1 || rows[0].(map[string]any)["dlr_rate"] != 0.9 || rows[0].(map[string]any)["label"] != "Acme" {
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
