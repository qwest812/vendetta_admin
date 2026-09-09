package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"Vendetta_admin/internal/auth"
	"Vendetta_admin/internal/config"
	"Vendetta_admin/internal/domain"
	"Vendetta_admin/internal/repo"
	"Vendetta_admin/internal/storage"
	"Vendetta_admin/internal/supremacy"
	"Vendetta_admin/internal/web"
)

func main() {
	// Уровень задаётся конфигом, а логгер нужен раньше, чем конфиг прочитан
	// (иначе ошибку чтения некуда написать). LevelVar разрешает это
	// противоречие: он подкручивается уже после старта.
	level := new(slog.LevelVar)
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
	if err := run(log, level); err != nil {
		log.Error("остановка с ошибкой", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger, level *slog.LevelVar) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	level.Set(cfg.LogLevel)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := storage.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	users := repo.NewUsers(pool)
	sessions := repo.NewSessions(pool)
	audit := repo.NewAudit(pool)
	players := repo.NewPlayers(pool)
	clans := repo.NewClans(pool)
	traits := repo.NewTraits(pool)
	enemies := repo.NewEnemies(pool)
	friends := repo.NewFriends(pool)
	tasks := repo.NewGameTasks(pool)
	alliances := repo.NewAlliances(pool)
	gamePlayers := repo.NewGamePlayers(pool)
	coalitions := repo.NewCoalitions(pool)
	settings := repo.NewSettings(pool)
	checked := repo.NewCheckedGames(pool)
	mapChecks := repo.NewMapChecks(pool)
	topAlliances := repo.NewTopAlliances(pool)
	userStats := repo.NewUserStats(pool)

	if err := seedRoot(ctx, log, users, cfg); err != nil {
		return err
	}

	// Клиент игры нужен и воркеру лобби, и рутовому разделу «Игры»,
	// поэтому он один на приложение. Без S1914_USER остаётся пустым —
	// раздел это переживает и сам объясняет, чего не хватает.
	var s1914 *supremacy.Client
	if cfg.S1914User != "" {
		s1914 = supremacy.NewClient(cfg.S1914User, cfg.S1914Password, cfg.S1914Lang, log)
	}

	authSvc := auth.NewService(users, sessions, cfg.SessionTTL, cfg.CookieSecure)
	deps := web.Deps{
		Log: log, Auth: authSvc, Users: users, Sessions: sessions,
		Audit: audit, Players: players, Clans: clans, Traits: traits,
		Enemies: enemies, Friends: friends, Alliances: alliances, GamePlayers: gamePlayers,
		Tasks: tasks, HeroEvery: cfg.S1914HeroEvery,
		Coalitions: coalitions, Settings: settings, Checked: checked, Checks: mapChecks,
		TopAlliances: topAlliances, UserStats: userStats,
		Health: pool.Ping, CookieSecure: cfg.CookieSecure,
	}
	// Присваиваем только настроенного клиента: типизированный nil в интерфейсе
	// на проверку `== nil` не отвечает, и раздел счёл бы аккаунт настроенным.
	if s1914 != nil {
		deps.Games = s1914
	}

	srv, err := web.NewServer(deps)
	if err != nil {
		return err
	}

	go cleanupSessions(ctx, log, sessions)
	go cleanupCheckedGames(ctx, log, checked)
	go cleanupMapChecks(ctx, log, mapChecks)

	if s1914 != nil {
		seen := repo.NewSupremacyGames(pool)

		// Нулевой notifier воркер понимает как «пиши в лог» — именно это и
		// нужно, когда телеграм не настроен.
		var notifier supremacy.Notifier
		if cfg.TelegramToken != "" {
			notifier = supremacy.NewTelegramNotifier(
				cfg.TelegramToken, cfg.TelegramChatID, cfg.TelegramTopicID, s1914.UserID)
			log.Info("о найденных играх пишем в телеграм",
				"chatID", cfg.TelegramChatID, "topicID", cfg.TelegramTopicID)
		} else {
			log.Warn("телеграм не настроен, о найденных играх будет только запись в логе")
		}

		watcher := supremacy.NewWatcher(s1914, cfg.S1914Titles, cfg.S1914PollEvery, log, notifier, seen)

		// Архив коалиций кормится тем же опросом лобби: заводить второй
		// незачем. Фильтр названий на него не распространяется — он про то,
		// о чём сообщать человеку, а собираем мы про все партии.
		scanner := supremacy.NewCoalitionScanner(s1914, coalitions, settings, cfg.S1914CoalitionEvery, log)
		watcher.Feed(scanner)

		go watcher.Run(ctx)
		go scanner.Run(ctx)

		// Автопилот ходит только в те партии, где кнопку включили руками,
		// поэтому запускается всегда: без включённых партий он молчит.
		go supremacy.NewAutopilot(s1914, tasks, cfg.S1914HeroEvery, log).Run(ctx)

		// Кланы игроков спрашивает воркер, а не страница карты: сайт игры
		// отвечает про одного игрока за раз. Очередь ему наполняют сами
		// открытые партии, поэтому без них он молчит.
		go supremacy.NewAllianceWatcher(s1914, alliances, cfg.S1914AllianceEvery, log).Run(ctx)

		// Топ кланов, в отличие от них, ни от каких партий не зависит:
		// он спрашивает рейтинг игры и сам решает, не пора ли обновиться.
		go supremacy.NewTopWatcher(s1914, topAlliances, cfg.S1914TopEvery, log).Run(ctx)
	}

	httpSrv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Info("сервер запущен", "addr", cfg.ListenAddr)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		log.Info("получен сигнал, останавливаемся")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return httpSrv.Shutdown(shutdownCtx)
}

func seedRoot(ctx context.Context, log *slog.Logger, users *repo.Users, cfg *config.Config) error {
	hash, err := auth.HashPassword(cfg.RootPassword)
	if err != nil {
		return err
	}
	created, err := users.EnsureRoot(ctx, cfg.RootEmail, cfg.RootNickname, hash)
	if err != nil {
		return err
	}
	if created {
		log.Info("создан рут-пользователь", "nickname", cfg.RootNickname, "email", cfg.RootEmail)
	}
	return nil
}

// cleanupMapChecks раз в час выбрасывает счёт проверок за прошедшие дни.
// На сам лимит это не влияет — он считается по сегодняшнему дню, — но
// таблице незачем помнить каждый день каждого.
func cleanupMapChecks(ctx context.Context, log *slog.Logger, checks *repo.MapChecks) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			n, err := checks.Forget(ctx, repo.Day(time.Now()))
			if err != nil {
				log.Error("очистка счёта проверок", "err", err)
				continue
			}
			if n > 0 {
				log.Info("забыт счёт проверок за прошлые дни", "count", n)
			}
		}
	}
}

// cleanupCheckedGames раз в час убирает из личных списков партии, к которым
// не возвращались дольше срока. Страница и без этого показывает только свежее
// — уборка нужна затем, чтобы таблица не росла вечно.
func cleanupCheckedGames(ctx context.Context, log *slog.Logger, checked *repo.CheckedGames) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			n, err := checked.Forget(ctx, time.Now().Add(-domain.CheckedGamesTTL))
			if err != nil {
				log.Error("очистка проверенных партий", "err", err)
				continue
			}
			if n > 0 {
				log.Info("забыты давние проверки партий", "count", n)
			}
		}
	}
}

// cleanupSessions раз в час подчищает протухшие сессии.
func cleanupSessions(ctx context.Context, log *slog.Logger, sessions *repo.Sessions) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			n, err := sessions.DeleteExpired(ctx)
			if err != nil {
				log.Error("очистка сессий", "err", err)
				continue
			}
			if n > 0 {
				log.Info("удалены протухшие сессии", "count", n)
			}
		}
	}
}
