package db_test

import (
	"context"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/Adnan-Dogar/telivoz-gateway/internal/db"
)

func TestOnlyOneActiveGateway(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := context.Background()
	first, err := db.AcquireActive(ctx, dsn, log)
	if err != nil {
		t.Fatal(err)
	}
	// A second gateway waits while the first is active...
	got := make(chan *db.ActiveLock, 1)
	go func() {
		l, err := db.AcquireActive(ctx, dsn, log)
		if err == nil {
			got <- l
		}
	}()
	select {
	case <-got:
		t.Fatal("two gateways active at once")
	case <-time.After(1500 * time.Millisecond):
	}
	// ...and takes over when the first stops.
	first.Release()
	select {
	case second := <-got:
		second.Release()
	case <-time.After(10 * time.Second):
		t.Fatal("standby did not take over")
	}
}
