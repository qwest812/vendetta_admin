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
	feedback := repo.NewFeedback(pool)

	if err := seedRoot(ctx, log, users, cfg); err != nil {
		return err
	}

	// Клиент игры нужен и воркеру лобби, и рутовому разделу «Игры»,
	// поэтому он один на приложение. Без S1914_USER остаётся пустым —
	// раздел это переживает и сам объясняет, чего не хватает.
	var s1914 *supremacy.Client
	if cfg.S1914User != "" {
		s1914 = supremacy.NewClient(cfg.S1914User, cfg.S1914Password, cfg.S1914Lang, log)
		// Подпись переживает перезапуск: сайт игры считает частые входы
		// подозрительными и отвечает отказом всем сразу, а живая подпись
		// делает вход при старте ненужным.
		s1914.UseSessionStore(s1914Sessions{settings})
	}

	authSvc := auth.NewService(users, sessions, cfg.SessionTTL, cfg.CookieSecure)
	deps := web.Deps{
		Log: log, Auth: authSvc, Users: users, Sessions: sessions,
		Audit: audit, Players: players, Clans: clans, Traits: traits,
		Enemies: enemies, Friends: friends, Feedback: feedback, Alliances: alliances, GamePlayers: gamePlayers,
		Tasks: tasks, HeroEvery: cfg.S1914HeroEvery, HeroEveryMax: cfg.S1914HeroEveryMax,
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

		// Воркеры расходятся по очереди, а не стартуют все разом: ходят они
		// в игру под одной подписью, и пятью запросами в одну секунду
		// начинать разговор с сайтом ни к чему. Смещение остаётся с ними
		// навсегда — тикер каждого отсчитывает от своего старта, поэтому
		// и дальше они не сходятся в одну секунду.
		//
		// Порядок по нужности: лобби ищет новые партии, и ждать ему обиднее
		// всех; топ кланов обновляется раз в шесть часов и подождёт минуту
		// без всякого ущерба.
		startAfter(ctx, 0, watcher.Run)
		startAfter(ctx, workerStagger, scanner.Run)

		// Автопилот ходит только в те партии, где кнопку включили руками,
		// поэтому запускается всегда: без включённых партий он молчит.
		autopilot := supremacy.NewAutopilot(s1914, tasks, cfg.S1914HeroEvery, cfg.S1914HeroEveryMax, log)
		startAfter(ctx, 2*workerStagger, autopilot.Run)

		// Кланы игроков спрашивает воркер, а не страница карты: сайт игры
		// отвечает про одного игрока за раз. Очередь ему наполняют сами
		// открытые партии, поэтому без них он молчит.
		alliancesWatcher := supremacy.NewAllianceWatcher(s1914, alliances, cfg.S1914AllianceEvery, log)
		startAfter(ctx, 3*workerStagger, alliancesWatcher.Run)

		// Топ кланов, в отличие от них, ни от каких партий не зависит:
		// он спрашивает рейтинг игры и сам решает, не пора ли обновиться.
		top := supremacy.NewTopWatcher(s1914, topAlliances, cfg.S1914TopEvery, log)
		startAfter(ctx, 4*workerStagger, top.Run)
	}

	httpSrv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		// Полторы минуты — не запас «на всякий случай», а следствие: страница
		// большой партии ждёт сайт игры до минуты (bigGameTimeout), и рвать
		// её раньше значит показать человеку пустоту вместо карты. Свой срок
		// у каждой страницы свой, этот — только верхняя граница.
		WriteTimeout: 90 * time.Second,
		IdleTimeout:  2 * time.Minute,
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

// cleanupMapChecks раз в час выбрасывает счёт проверок старше срока.
// Лимиту хватило бы и сегодняшнего дня, но по этим же строкам рут смотрит,
// сколько карт открывают в день, поэтому храним их, пока это интересно.
func cleanupMapChecks(ctx context.Context, log *slog.Logger, checks *repo.MapChecks) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			n, err := checks.Forget(ctx, repo.Day(time.Now().Add(-domain.MapChecksTTL)))
			if err != nil {
				log.Error("очистка счёта проверок", "err", err)
				continue
			}
			if n > 0 {
				log.Info("забыт счёт открытий карты", "count", n)
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

// s1914Sessions — переходник между настройками в базе и клиентом игры.
// У каждого своя структура подписи: repo не должен знать про Supremacy,
// а клиент игры — про базу.
type s1914Sessions struct{ settings *repo.Settings }

func (s s1914Sessions) LoadSession(ctx context.Context) (supremacy.SavedSession, bool, error) {
	saved, ok, err := s.settings.LoadSession(ctx)
	if err != nil || !ok {
		return supremacy.SavedSession{}, false, err
	}
	return supremacy.SavedSession{
		UserID: saved.UserID, AuthHash: saved.AuthHash,
		AuthTstamp: saved.AuthTstamp, SavedAt: saved.SavedAt,
	}, true, nil
}

func (s s1914Sessions) SaveSession(ctx context.Context, sess supremacy.SavedSession) error {
	return s.settings.SaveSession(ctx, repo.SavedSession{
		UserID: sess.UserID, AuthHash: sess.AuthHash,
		AuthTstamp: sess.AuthTstamp, SavedAt: sess.SavedAt,
	})
}

// workerStagger — шаг, с которым воркеры расходятся на старте. Пятнадцать
// секунд: меньше — и они всё ещё толкаются, больше — и запуск заметно
// растягивается, а лобби с топом кланов должны разъехаться в пределах минуты.
const workerStagger = 15 * time.Second

// startAfter запускает воркер, выждав своё. Остановка приложения отменяет
// и ожидание: воркеру, который ещё не начал, начинать уже незачем.
func startAfter(ctx context.Context, wait time.Duration, run func(context.Context)) {
	go func() {
		if wait > 0 {
			timer := time.NewTimer(wait)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
			}
		}
		run(ctx)
	}()
}
