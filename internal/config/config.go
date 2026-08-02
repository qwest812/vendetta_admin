package config

import (
	"fmt"
	"os"
	"time"
)

type Config struct {
	DatabaseURL  string
	ListenAddr   string
	SessionTTL   time.Duration
	CookieSecure bool

	// Учётные данные рута. Применяются только при первом запуске:
	// если рут уже есть в базе, значения игнорируются.
	RootEmail    string
	RootPassword string
}

func Load() (*Config, error) {
	cfg := &Config{
		DatabaseURL:  env("DATABASE_URL", "postgres://vendetta:vendetta@localhost:5432/vendetta?sslmode=disable"),
		ListenAddr:   env("LISTEN_ADDR", ":8080"),
		CookieSecure: env("COOKIE_SECURE", "false") == "true",
		RootEmail:    env("ROOT_EMAIL", ""),
		RootPassword: env("ROOT_PASSWORD", ""),
	}

	ttl, err := time.ParseDuration(env("SESSION_TTL", "168h"))
	if err != nil {
		return nil, fmt.Errorf("SESSION_TTL: %w", err)
	}
	cfg.SessionTTL = ttl

	if cfg.RootEmail == "" || cfg.RootPassword == "" {
		return nil, fmt.Errorf("нужно задать ROOT_EMAIL и ROOT_PASSWORD")
	}
	if len(cfg.RootPassword) < 12 {
		return nil, fmt.Errorf("ROOT_PASSWORD короче 12 символов")
	}
	return cfg, nil
}

func env(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}
