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

	// Слежение за лобби Supremacy 1914. Без S1914User воркер не запускается.
	S1914User      string
	S1914Password  string
	S1914Lang      string
	S1914Titles    []string
	S1914PollEvery time.Duration
}

func Load() (*Config, error) {
	cfg := &Config{
		DatabaseURL:  env("DATABASE_URL", "postgres://vendetta:vendetta@localhost:5432/vendetta?sslmode=disable"),
		ListenAddr:   env("LISTEN_ADDR", ":8080"),
		CookieSecure: env("COOKIE_SECURE", "false") == "true",
		RootEmail:    env("ROOT_EMAIL", ""),
		RootNickname: env("ROOT_NICKNAME", ""),
		RootPassword: env("ROOT_PASSWORD", ""),

		S1914User:     env("S1914_USER", ""),
		S1914Password: env("S1914_PASSWORD", ""),
		S1914Lang:     env("S1914_LANG", "ru"),
	}

	ttl, err := time.ParseDuration(env("SESSION_TTL", "168h"))
	if err != nil {
		return nil, fmt.Errorf("SESSION_TTL: %w", err)
	}
	cfg.SessionTTL = ttl

	poll, err := time.ParseDuration(env("S1914_POLL_INTERVAL", "1m"))
	if err != nil {
		return nil, fmt.Errorf("S1914_POLL_INTERVAL: %w", err)
	}
	if poll < 30*time.Second {
		return nil, fmt.Errorf("S1914_POLL_INTERVAL: не чаще раза в 30 секунд")
	}
	cfg.S1914PollEvery = poll

	for _, t := range strings.Split(env("S1914_WATCH_TITLES", ""), ",") {
		if t = strings.TrimSpace(t); t != "" {
			cfg.S1914Titles = append(cfg.S1914Titles, t)
		}
	}
	// Пустой S1914_WATCH_TITLES допустим и означает «сообщать про все игры»,
	// а вот пароль без ника (и наоборот) — наверняка недосмотр.
	if cfg.S1914User != "" && cfg.S1914Password == "" {
		return nil, fmt.Errorf("задан S1914_USER, но не задан S1914_PASSWORD")
	}
	if cfg.S1914Password != "" && cfg.S1914User == "" {
		return nil, fmt.Errorf("задан S1914_PASSWORD, но не задан S1914_USER")
	}

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
