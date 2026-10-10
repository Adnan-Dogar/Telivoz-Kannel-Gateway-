// Command gateway runs the Telivoz SMS gateway.
//
//	gateway serve                       run the SMPP server, vendor connections, API and portal
//	gateway migrate                     apply database migrations and exit
//	gateway create-admin EMAIL PASSWORD create (or reset) an admin user
//	gateway import-legacy MYSQL_DSN     import data from the old MySQL database (safe to re-run)
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
	_ "time/tzdata" // embedded time zones: the binary needs no tzdata package on the host or in the image

	"github.com/Adnan-Dogar/telivoz-gateway/internal/api"
	"github.com/Adnan-Dogar/telivoz-gateway/internal/auth"
	"github.com/Adnan-Dogar/telivoz-gateway/internal/config"
	"github.com/Adnan-Dogar/telivoz-gateway/internal/db"
	"github.com/Adnan-Dogar/telivoz-gateway/internal/engine"
	"github.com/Adnan-Dogar/telivoz-gateway/internal/legacy"
)

func main() {
	level := slog.LevelInfo
	if os.Getenv("LOG_LEVEL") == "debug" {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(log)

	cmd := "serve"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	if err := run(cmd, os.Args[min(2, len(os.Args)):], log); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(cmd string, args []string, log *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	applied, err := db.Migrate(ctx, pool)
	if err != nil {
		return err
	}
	for _, v := range applied {
		log.Info("migration applied", "version", v)
	}
	cipher, err := auth.NewCipher(cfg.SecretKey)
	if err != nil {
		return err
	}

	switch cmd {
	case "migrate":
		return nil
	case "create-admin":
		if len(args) != 2 || len(args[1]) < 8 {
			return errors.New("usage: gateway create-admin EMAIL PASSWORD (password at least 8 characters)")
		}
		hash, err := auth.HashPassword(args[1])
		if err != nil {
			return err
		}
		_, err = pool.Exec(ctx, `INSERT INTO users (email, name, password_hash, role) VALUES (lower($1), 'Administrator', $2, 'admin')
			ON CONFLICT (lower(email)) DO UPDATE SET password_hash = EXCLUDED.password_hash, role = 'admin', status = 'active'`, args[0], hash)
		if err == nil {
			fmt.Println("admin ready:", args[0])
		}
		return err
	case "import-legacy":
		if len(args) != 1 {
			return errors.New("usage: gateway import-legacy 'user:pass@tcp(host:3306)/dbname'")
		}
		return legacy.Import(ctx, pool, cipher, args[0], log)
	case "serve":
	default:
		return fmt.Errorf("unknown command %q", cmd)
	}

	// One active gateway per database; a second server waits here as a hot standby.
	lock, err := db.AcquireActive(ctx, cfg.DatabaseURL, log)
	if err != nil {
		return err
	}
	defer lock.Release()
	lockLost := make(chan error, 1)
	go lock.Watch(ctx, lockLost)

	eng := engine.New(pool, cipher, log)
	if err := eng.Start(ctx); err != nil {
		return err
	}
	defer eng.Stop()

	smppErr := make(chan error, 1)
	go func() { smppErr <- eng.ServeSMPP(ctx, cfg.SMPPAddr) }()

	srv := api.New(pool, eng, cipher, log, api.Options{WebDir: cfg.WebDir, CookieSecure: cfg.CookieSecure, SessionTTL: cfg.SessionTTL})
	srv.ResumeCampaigns(ctx)
	httpSrv := &http.Server{Addr: cfg.HTTPAddr, Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second}
	httpErr := make(chan error, 1)
	go func() {
		log.Info("HTTP server listening", "addr", cfg.HTTPAddr)
		httpErr <- httpSrv.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		log.Info("shutting down")
	case err := <-smppErr:
		return fmt.Errorf("smpp server: %w", err)
	case err := <-httpErr:
		return fmt.Errorf("http server: %w", err)
	case err := <-lockLost:
		return err // exit so systemd restarts us as a standby; never run two active gateways
	}
	sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return httpSrv.Shutdown(sctx)
}
