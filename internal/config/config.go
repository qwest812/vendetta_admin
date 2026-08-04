package config

import (
	"fmt"
	"os"
	"strings"
	"time"

	"Vendetta_admin/internal/domain"
)

type Config struct {
	DatabaseURL  string
	ListenAddr   string
	SessionTTL   time.Duration
	CookieSecure bool

	// Учётные данные рута. Применяются только при первом запуске:
	// если рут уже есть в базе, значения игнорируются.
	RootEmail    string
	RootNickname string
	RootPassword string
}

func Load() (*Config, error) {
	cfg := &Config{
		DatabaseURL:  env("DATABASE_URL", "postgres://vendetta:vendetta@localhost:5432/vendetta?sslmode=disable"),
		ListenAddr:   env("LISTEN_ADDR", ":8080"),
		CookieSecure: env("COOKIE_SECURE", "false") == "true",
		RootEmail:    env("ROOT_EMAIL", ""),
		RootNickname: env("ROOT_NICKNAME", ""),
		RootPassword: env("ROOT_PASSWORD", ""),
	}

	ttl, err := time.ParseDuration(env("SESSION_TTL", "168h"))
	if err != nil {
		return nil, fmt.Errorf("SESSION_TTL: %w", err)
	}
	cfg.SessionTTL = ttl

	// Входить можно по нику, поэтому почта рута необязательна — но хоть
	// один логин нужен. Ник по умолчанию берём из локальной части адреса.
	if cfg.RootNickname == "" {
		cfg.RootNickname, _, _ = strings.Cut(cfg.RootEmail, "@")
	}
	if cfg.RootNickname == "" || cfg.RootPassword == "" {
		return nil, fmt.Errorf("нужно задать ROOT_PASSWORD и хотя бы одно из ROOT_NICKNAME или ROOT_EMAIL")
	}
	if err := domain.ValidateNickname(cfg.RootNickname); err != nil {
		return nil, fmt.Errorf("ROOT_NICKNAME: %w", err)
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
