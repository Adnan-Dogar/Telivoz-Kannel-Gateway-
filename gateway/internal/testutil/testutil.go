// Package testutil sets up a clean PostgreSQL schema and seed data for integration tests.
// Tests using it are skipped unless TEST_DATABASE_URL is set.
package testutil

import (
	"context"
	"net"
	"os"
	"strconv"
	"testing"

	"github.com/Adnan-Dogar/telivoz-gateway/internal/auth"
	"github.com/Adnan-Dogar/telivoz-gateway/internal/db"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DB returns a pool on a freshly migrated, empty schema.
func DB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public;`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func Cipher() *auth.Cipher {
	c, _ := auth.NewCipher(make([]byte, 32))
	return c
}

// Fixture IDs created by Seed.
type Fixture struct {
	ClientID  int64
	AccountID int64
	HTTPAccID int64
	VendorID  int64
	AdminID   int64
}

func Exec(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("%v\n%s", err, sql)
	}
}

func ID(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) int64 {
	t.Helper()
	var id int64
	if err := pool.QueryRow(context.Background(), sql, args...).Scan(&id); err != nil {
		t.Fatalf("%v\n%s", err, sql)
	}
	return id
}

// Seed creates Pakistan (Jazz 92300), one client (balance 10.00, rate 0.01 for PK), one SMPP account
// "client1"/"pass1", one HTTP account "api1"/"apipass", one vendor and an admin user admin@test/admin123.
func Seed(t *testing.T, pool *pgxpool.Pool) Fixture {
	t.Helper()
	var f Fixture
	Exec(t, pool, `INSERT INTO countries (iso, name, dial_code) VALUES ('PK', 'Pakistan', '92'), ('US', 'United States', '1')`)
	net := ID(t, pool, `INSERT INTO networks (country_iso, mcc, mnc, name) VALUES ('PK', '410', '01', 'Jazz') RETURNING id`)
	Exec(t, pool, `INSERT INTO number_prefixes (prefix, network_id) VALUES ('92300', $1)`, net)
	adminHash, _ := auth.HashPassword("admin123")
	f.AdminID = ID(t, pool, `INSERT INTO users (email, name, password_hash, role) VALUES ('admin@test', 'Admin', $1, 'admin') RETURNING id`, adminHash)
	f.ClientID = ID(t, pool, `INSERT INTO clients (name, owner_id) VALUES ('Acme', $1) RETURNING id`, f.AdminID)
	Exec(t, pool, `INSERT INTO balances (client_id, balance) VALUES ($1, 10)`, f.ClientID)
	pw, _ := auth.HashPassword("pass1")
	f.AccountID = ID(t, pool, `INSERT INTO accounts (client_id, kind, username, password_hash, tps) VALUES ($1, 'smpp', 'client1', $2, 500) RETURNING id`, f.ClientID, pw)
	apw, _ := auth.HashPassword("apipass")
	f.HTTPAccID = ID(t, pool, `INSERT INTO accounts (client_id, kind, username, password_hash, tps) VALUES ($1, 'http', 'api1', $2, 500) RETURNING id`, f.ClientID, apw)
	Exec(t, pool, `INSERT INTO client_rates (client_id, country_iso, price, effective_from) VALUES ($1, 'PK', 0.01, now() - interval '1 day')`, f.ClientID)
	f.VendorID = ID(t, pool, `INSERT INTO vendors (name) VALUES ('VendorCo') RETURNING id`)
	return f
}

// Connection adds a vendor connection to addr with the given cost for PK and returns its id.
func Connection(t *testing.T, pool *pgxpool.Pool, vendorID int64, name, addr string, cost string) int64 {
	t.Helper()
	host, port, _ := net.SplitHostPort(addr)
	p, _ := strconv.Atoi(port)
	id := ID(t, pool, `INSERT INTO connections (vendor_id, name, host, port, system_id, password_enc, tps, window_size)
		VALUES ($1, $2, $3, $4, 'gw', $5, 1000, 20) RETURNING id`, vendorID, name, host, p, Cipher().Encrypt("pw"))
	Exec(t, pool, `INSERT INTO vendor_rates (connection_id, country_iso, price, effective_from) VALUES ($1, 'PK', $2::numeric, now() - interval '1 day')`, id, cost)
	return id
}

// Route adds a PK route over the connections (in order) with the policy.
func Route(t *testing.T, pool *pgxpool.Pool, policy string, conns ...int64) int64 {
	t.Helper()
	id := ID(t, pool, `INSERT INTO routes (name, country_iso, policy) VALUES ('pk', 'PK', $1) RETURNING id`, policy)
	for i, c := range conns {
		Exec(t, pool, `INSERT INTO route_targets (route_id, connection_id, position) VALUES ($1, $2, $3)`, id, c, i)
	}
	return id
}
