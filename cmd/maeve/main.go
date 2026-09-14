// Command maeve — отдельная программа, которая только жмёт кнопку Мейв
// «призвать пехоту» в выбранных партиях Supremacy 1914. Без базы и без
// админки: настройки в maeve.txt рядом с exe, подпись входа — в
// maeve-session.json там же, лог — в окне и в maeve-log.txt.
//
// Собирается под Windows: make maeve-windows.
package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"Vendetta_admin/internal/supremacy"
)

// logName — лог рядом с exe: окно закроют, а что было ночью, узнать надо.
const logName = "maeve-log.txt"

func main() {
	dir, err := exeDir()
	if err == nil {
		err = run(dir)
	}
	var first firstRun
	if errors.As(err, &first) {
		fmt.Println(first.Error())
		pause()
		return
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "Ошибка:", err)
		pause()
		os.Exit(1)
	}
}

func run(dir string) error {
	settingsPath := filepath.Join(dir, settingsName)
	f, err := os.Open(settingsPath)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.WriteFile(settingsPath, []byte(settingsTemplate), 0o600); err != nil {
			return fmt.Errorf("не получилось создать %s: %w", settingsPath, err)
		}
		return firstRun{path: settingsPath}
	}
	if err != nil {
		return err
	}
	cfg, err := parseSettings(f)
	f.Close()
	if err != nil {
		return fmt.Errorf("%s: %w", settingsName, err)
	}

	logFile, err := os.OpenFile(filepath.Join(dir, logName), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("не получилось открыть лог: %w", err)
	}
	defer logFile.Close()
	log := slog.New(slog.NewTextHandler(io.MultiWriter(os.Stdout, logFile), nil))

	client := supremacy.NewClient(cfg.Login, cfg.Password, cfg.Lang, log)
	client.UseSessionStore(fileSessions{path: filepath.Join(dir, sessionName), login: cfg.Login})

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Список своих партий — первый вопрос к сайту: он же проверяет логин
	// и пароль. Ошибаться в них лучше сразу, при человеке, чем молча ночью.
	// В партию этот вопрос не заходит.
	checkCtx, cancelCheck := context.WithTimeout(ctx, 2*time.Minute)
	mine, err := checkAccount(checkCtx, client, cfg.Games, log)
	cancelCheck()
	if err != nil {
		return err
	}

	log.Info("программа запущена — окно не закрывайте, остановить: Ctrl+C",
		"аккаунт", cfg.Login, "партии", mine, "пауза от", cfg.IntervalMin, "до", cfg.IntervalMax)

	// Тот же автопилот, что в админке: первый обход сразу, дальше случайная
	// пауза. Отказы игры он пишет в лог и продолжает.
	supremacy.NewAutopilot(client, gameList{games: cfg.Games, log: log},
		cfg.IntervalMin, cfg.IntervalMax, log).Run(ctx)

	log.Info("программа остановлена")
	return nil
}

// checkAccount входит в игру и сверяет партии из настроек со своими.
// Чужая или закончившаяся партия — не причина не стартовать (список у сайта
// бывает запаздывает), но сказать о ней нужно: кнопку там нажать не выйдет.
// Ответ — партии с названиями, для лога.
func checkAccount(ctx context.Context, client *supremacy.Client, games []string,
	log *slog.Logger) ([]string, error) {

	list, err := client.MyGames(ctx)
	if err != nil {
		return nil, fmt.Errorf("не получилось войти в игру: %w", err)
	}
	titles := make(map[string]string, len(list))
	for _, g := range list {
		titles[g.GameID] = g.Title
	}
	out := make([]string, 0, len(games))
	for _, id := range games {
		title, ok := titles[id]
		if !ok {
			log.Warn("этой партии нет среди активных партий аккаунта — призвать в ней не выйдет", "партия", id)
			out = append(out, id)
			continue
		}
		out = append(out, id+" «"+title+"»")
	}
	return out, nil
}

// firstRun — первый запуск: файла настроек не было, и мы его создали. Это
// не ошибка, а шаг инструкции, и говорить о нём надо без слова «ошибка».
type firstRun struct{ path string }

func (f firstRun) Error() string {
	return "Создан файл настроек " + f.path + ".\nОткройте его блокнотом, впишите логин, пароль и номера партий " +
		"и запустите программу снова."
}

// exeDir — папка, где лежит exe. Не рабочая папка: по двойному клику она
// бывает какой угодно, а настройки человек положит рядом с программой.
func exeDir() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return filepath.Dir(exe), nil
}

// pause держит окно открытым после ошибки или первого запуска: запущенное двойным кликом, оно
// иначе закроется раньше, чем человек прочтёт, что не так.
func pause() {
	fmt.Println("Нажмите Enter, чтобы закрыть окно.")
	_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
}
