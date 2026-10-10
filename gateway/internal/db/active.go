package db

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
)

// activeLockKey is the PostgreSQL advisory lock that marks the active gateway.
const activeLockKey = 7_220_501

// ActiveLock makes sure only one gateway per database is active. A second server started against the same
// database waits in AcquireActive (standby) and takes over automatically when the active one stops or loses
// its database connection: the lock belongs to a database session and is released when that session ends.
type ActiveLock struct {
	conn *pgx.Conn
}

// AcquireActive blocks until this process holds the active lock (or ctx ends).
func AcquireActive(ctx context.Context, dsn string, log *slog.Logger) (*ActiveLock, error) {
	logged := false
	for {
		conn, err := pgx.Connect(ctx, dsn)
		if err == nil {
			var ok bool
			if err = conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, activeLockKey).Scan(&ok); err == nil && ok {
				if logged {
					log.Info("standby: the active gateway stopped, taking over")
				}
				return &ActiveLock{conn: conn}, nil
			}
			conn.Close(context.Background())
		}
		if err != nil {
			log.Warn("standby: cannot check the active lock", "err", err)
		} else if !logged {
			log.Info("standby: another gateway is active on this database; waiting to take over")
			logged = true
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
}

// Watch reports on lost when the lock's database session fails. The process must then stop serving: a
// standby may already have taken over.
func (l *ActiveLock) Watch(ctx context.Context, lost chan<- error) {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			pctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			err := l.conn.Ping(pctx)
			cancel()
			if err != nil && ctx.Err() == nil {
				lost <- fmt.Errorf("active lock session lost: %w", err)
				return
			}
		}
	}
}

// Release gives up the lock so a standby can take over at once.
func (l *ActiveLock) Release() {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = l.conn.Close(ctx)
}
