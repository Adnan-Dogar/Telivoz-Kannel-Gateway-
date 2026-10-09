// Package config reads the gateway settings from environment variables.
package config

import (
	"crypto/sha256"
	"fmt"
	"os"
	"strconv"
	"time"
)

type Config struct {
	DatabaseURL   string
	HTTPAddr      string // portal, admin API and client HTTP API
	SMPPAddr      string // SMPP server for clients
	SecretKey     []byte // encrypts vendor passwords; 32 bytes derived from GATEWAY_SECRET
	WebDir        string // built portal files served at /
	CookieSecure  bool
	SessionTTL    time.Duration
	DLRWebhookTTL time.Duration
}

func Load() (Config, error) {
	c := Config{
		DatabaseURL:   env("DATABASE_URL", "postgres://gateway:gateway@localhost:5432/gateway?sslmode=disable"),
		HTTPAddr:      env("HTTP_ADDR", ":8080"),
		SMPPAddr:      env("SMPP_ADDR", ":2775"),
		WebDir:        env("WEB_DIR", "web/dist"),
		CookieSecure:  env("COOKIE_SECURE", "false") == "true",
		SessionTTL:    durationEnv("SESSION_TTL", 12*time.Hour),
		DLRWebhookTTL: durationEnv("DLR_RETRY_TTL", 72*time.Hour),
	}
	secret := os.Getenv("GATEWAY_SECRET")
	if len(secret) < 16 {
		return c, fmt.Errorf("GATEWAY_SECRET must be set to at least 16 characters")
	}
	sum := sha256.Sum256([]byte(secret))
	c.SecretKey = sum[:]
	return c, nil
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func durationEnv(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
		if n, err := strconv.Atoi(v); err == nil {
			return time.Duration(n) * time.Second
		}
	}
	return def
}
