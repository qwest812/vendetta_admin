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
	"Vendetta_admin/internal/repo"
	"Vendetta_admin/internal/storage"
	"Vendetta_admin/internal/web"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	if err := run(log); err != nil {
		log.Error("остановка с ошибкой", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

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
	traits := repo.NewTraits(pool)

	if err := seedRoot(ctx, log, users, cfg); err != nil {
		return err
	}

	authSvc := auth.NewService(users, sessions, cfg.SessionTTL, cfg.CookieSecure)
	srv, err := web.NewServer(web.Deps{
		Log: log, Auth: authSvc, Users: users, Sessions: sessions,
		Audit: audit, Players: players, Traits: traits,
		Health: pool.Ping,
	})
	if err != nil {
		return err
	}

	go cleanupSessions(ctx, log, sessions)

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
