package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"Vendetta_admin/internal/supremacy"
)

// Файл, который программа кладёт при первом запуске, после заполнения
// должен читаться как есть: комментарии — инструкция, а не мусор.
func TestSettingsTemplateFilledIn(t *testing.T) {
	filled := strings.NewReplacer(
		"LOGIN=\n", "LOGIN=Dau7er\n",
		"PASSWORD=\n", "PASSWORD=secret\n",
		"GAMES=\n", "GAMES=10886819, 10900334;10886819\n",
	).Replace(settingsTemplate)

	// Блокнот сохраняет с меткой UTF-8 в начале — она не должна мешать.
	s, err := parseSettings(strings.NewReader("\ufeff" + filled))
	if err != nil {
		t.Fatalf("разбор: %v", err)
	}
	if s.Login != "Dau7er" || s.Password != "secret" || s.Lang != "ru" {
		t.Errorf("разобрано %+v", s)
	}
	if strings.Join(s.Games, ",") != "10886819,10900334" {
		t.Errorf("партии %v: повтор должен уйти, порядок остаться", s.Games)
	}
	if s.IntervalMin != 45*time.Minute || s.IntervalMax != 50*time.Minute {
		t.Errorf("пауза %v–%v", s.IntervalMin, s.IntervalMax)
	}
}

// Пустой шаблон и опечатки не должны молча превращаться в «ничего не делать»
// или в заходы раз в минуту.
func TestSettingsErrors(t *testing.T) {
	base := "LOGIN=a\nPASSWORD=b\nGAMES=10886819\n"
	cases := []struct {
		name string
		body string
		want string
	}{
		{"пустой шаблон", settingsTemplate, "LOGIN и PASSWORD"},
		{"без партий", "LOGIN=a\nPASSWORD=b\n", "GAMES"},
		{"ссылка вместо номера", "LOGIN=a\nPASSWORD=b\nGAMES=https://www.supremacy1914.com/play.php?gameID=1\n", "не номер партии"},
		{"опечатка в ключе", base + "INTERVL_MIN=45m\n", "незнакомая настройка"},
		{"слишком часто", base + "INTERVAL_MIN=1m\nINTERVAL_MAX=2m\n", "INTERVAL_MIN не меньше"},
		{"до меньше от", base + "INTERVAL_MIN=50m\nINTERVAL_MAX=45m\n", "INTERVAL_MAX"},
		{"время без единиц", base + "INTERVAL_MIN=45\n", "время вида"},
		{"строка без знака", base + "просто текст\n", "КЛЮЧ=значение"},
	}
	for _, c := range cases {
		_, err := parseSettings(strings.NewReader(c.body))
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: ошибка %v, ждали про %q", c.name, err, c.want)
		}
	}
}

// Подпись переживает перезапуск, но только для того же логина: сменили
// аккаунт в настройках — чужую подпись не берём.
func TestFileSessions(t *testing.T) {
	path := filepath.Join(t.TempDir(), sessionName)
	ctx := context.Background()
	store := fileSessions{path: path, login: "Dau7er"}

	if _, ok, err := store.LoadSession(ctx); ok || err != nil {
		t.Fatalf("без файла: ok=%v err=%v", ok, err)
	}
	saved := supremacy.SavedSession{UserID: "101408369", AuthHash: "abc", AuthTstamp: "1", SavedAt: time.Now().Truncate(time.Second)}
	if err := store.SaveSession(ctx, saved); err != nil {
		t.Fatalf("запись: %v", err)
	}
	got, ok, err := fileSessions{path: path, login: "dau7er"}.LoadSession(ctx)
	if err != nil || !ok || got.AuthHash != "abc" || !got.SavedAt.Equal(saved.SavedAt) {
		t.Errorf("тот же логин: %+v ok=%v err=%v", got, ok, err)
	}
	if _, ok, _ := (fileSessions{path: path, login: "другой"}).LoadSession(ctx); ok {
		t.Error("подпись досталась другому логину")
	}

	if err := os.WriteFile(path, []byte("{испорчен"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := store.LoadSession(ctx); ok || err != nil {
		t.Errorf("испорченный файл должен означать «войти заново»: ok=%v err=%v", ok, err)
	}
}
