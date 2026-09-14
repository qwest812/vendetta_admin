package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"strings"
	"time"

	"Vendetta_admin/internal/domain"
	"Vendetta_admin/internal/supremacy"
)

// sessionName — файл с подписью входа рядом с exe. Без него каждый запуск
// входил бы в игру заново, а частые входы сайт считает подозрительными.
const sessionName = "maeve-session.json"

// fileSessions хранит подпись в файле. Логин пишется вместе с ней: сменили
// аккаунт в настройках — чужая подпись не подходит, входим заново.
type fileSessions struct {
	path  string
	login string
}

type savedFile struct {
	Login string `json:"login"`
	supremacy.SavedSession
}

func (f fileSessions) LoadSession(context.Context) (supremacy.SavedSession, bool, error) {
	b, err := os.ReadFile(f.path)
	if errors.Is(err, os.ErrNotExist) {
		return supremacy.SavedSession{}, false, nil
	}
	if err != nil {
		return supremacy.SavedSession{}, false, err
	}
	var saved savedFile
	// Испорченный файл — не повод не стартовать: просто войдём заново,
	// и он перезапишется.
	if err := json.Unmarshal(b, &saved); err != nil || !strings.EqualFold(saved.Login, f.login) {
		return supremacy.SavedSession{}, false, nil
	}
	return saved.SavedSession, true, nil
}

func (f fileSessions) SaveSession(_ context.Context, s supremacy.SavedSession) error {
	b, err := json.MarshalIndent(savedFile{Login: f.login, SavedSession: s}, "", "  ")
	if err != nil {
		return err
	}
	// Подпись — почти пароль: читать её остальным незачем.
	return os.WriteFile(f.path, b, 0o600)
}

// gameList — партии из настроек вместо таблицы в базе. Итог захода пишется
// в лог: страницы, где его показать, у программы нет.
type gameList struct {
	games []string
	log   *slog.Logger
}

func (g gameList) HeroDeployEnabled(context.Context) ([]domain.GameTask, error) {
	tasks := make([]domain.GameTask, 0, len(g.games))
	for _, id := range g.games {
		tasks = append(tasks, domain.GameTask{GameID: id, HeroDeploy: true})
	}
	return tasks, nil
}

func (g gameList) MarkHeroRun(_ context.Context, gameID string, _ time.Time, result string) error {
	g.log.Info("партия обойдена", "партия", gameID, "итог", result)
	return nil
}
