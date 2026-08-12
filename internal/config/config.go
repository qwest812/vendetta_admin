package config

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"Vendetta_admin/internal/domain"
)

type Config struct {
	DatabaseURL  string
	ListenAddr   string
	SessionTTL   time.Duration
	CookieSecure bool
	LogLevel     slog.Level

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

	// Куда воркер пишет о найденных играх. Без токена и чата сообщения
	// остаются в логе приложения. Тема нужна только для групп-форумов.
	TelegramToken   string
	TelegramChatID  string
	TelegramTopicID int
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

		TelegramToken:  env("TELEGRAM_BOT_TOKEN", ""),
		TelegramChatID: env("TELEGRAM_CHAT_ID", ""),
	}

	// slog разбирает не только DEBUG/INFO/WARN/ERROR, но и сдвиги вида
	// «INFO-2», так что своей таблицы уровней не заводим.
	if err := cfg.LogLevel.UnmarshalText([]byte(env("LOG_LEVEL", "info"))); err != nil {
		return nil, fmt.Errorf("LOG_LEVEL: %w", err)
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

	// Половина настроек телеграма бесполезна: без чата боту некуда писать,
	// без токена — нечем. Тихо съесть такое хуже, чем не запуститься:
	// приложение работало бы, а сообщений никто бы не дождался.
	if cfg.TelegramToken != "" && cfg.TelegramChatID == "" {
		return nil, fmt.Errorf("задан TELEGRAM_BOT_TOKEN, но не задан TELEGRAM_CHAT_ID")
	}
	if cfg.TelegramChatID != "" && cfg.TelegramToken == "" {
		return nil, fmt.Errorf("задан TELEGRAM_CHAT_ID, но не задан TELEGRAM_BOT_TOKEN")
	}

	if topic := env("TELEGRAM_TOPIC_ID", ""); topic != "" {
		if cfg.TelegramChatID == "" {
			return nil, fmt.Errorf("задан TELEGRAM_TOPIC_ID, но не задан TELEGRAM_CHAT_ID")
		}
		// Тему задают числом. Строку телеграм не примет, а разбираться в его
		// отказе на каждой находке — то ещё удовольствие.
		id, err := strconv.Atoi(topic)
		if err != nil || id <= 0 {
			return nil, fmt.Errorf("TELEGRAM_TOPIC_ID: ожидалось положительное число, получили %q", topic)
		}
		cfg.TelegramTopicID = id
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
