// Command gameinfo показывает, чем мы владеем в партии Supremacy 1914.
//
//	make game            — если активная партия одна, она и берётся
//	make game GAME=10886819
//
// Заход на игровой сервер игра засчитывает как вход в партию, поэтому
// команда разовая: никаких таймеров и повторов.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"time"

	"Vendetta_admin/internal/supremacy"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "ошибка:", err)
		os.Exit(1)
	}
}

func run() error {
	user, pass := os.Getenv("S1914_USER"), os.Getenv("S1914_PASSWORD")
	if user == "" || pass == "" {
		return fmt.Errorf("нужны S1914_USER и S1914_PASSWORD (они есть в .env)")
	}

	// В stderr пишем только предупреждения: обычный вывод — это отчёт,
	// его читает человек.
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	client := supremacy.NewClient(user, pass, os.Getenv("S1914_LANG"), log)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	gameID, title, err := pickGame(ctx, client, os.Args[1:])
	if err != nil {
		return err
	}

	state, err := client.GameState(ctx, gameID)
	if err != nil {
		return err
	}

	me := state.Players[state.Me]
	fmt.Printf("Партия %s%s, день %d\n", gameID, titleSuffix(title), state.Day)
	fmt.Printf("Играем за: %s (%s), номер в партии %d\n\n", nonEmpty(me.Nation, "неизвестно"), me.Name, state.Me)

	owned := state.Owned(state.Me)
	sort.Slice(owned, func(i, j int) bool {
		// Столица первой, дальше по алфавиту: так список читается как отчёт.
		if owned[i].Capital != owned[j].Capital {
			return owned[i].Capital
		}
		return owned[i].Name < owned[j].Name
	})

	fmt.Printf("Под контролем провинций: %d\n", len(owned))
	for _, p := range owned {
		mark := ""
		if p.Capital {
			mark = "  ← столица"
		}
		fmt.Printf("  %-28s мораль %3.0f%%%s\n", p.Name, p.Morale, mark)
	}

	var taken int
	for _, p := range state.Provinces {
		if p.Owner > 0 {
			taken++
		}
	}
	fmt.Printf("\nПровинций на карте: %d, из них занято: %d\n", len(state.Provinces), taken)
	return nil
}

// pickGame выбирает партию: либо её назвали аргументом, либо она одна.
func pickGame(ctx context.Context, c *supremacy.Client, args []string) (string, string, error) {
	if len(args) > 0 {
		return args[0], "", nil
	}

	games, err := c.MyGames(ctx)
	if err != nil {
		return "", "", err
	}
	switch len(games) {
	case 0:
		return "", "", fmt.Errorf("аккаунт сейчас не играет ни в одной партии")
	case 1:
		return games[0].GameID, games[0].Title, nil
	}

	var b []byte
	for _, g := range games {
		b = append(b, fmt.Sprintf("  %s — %s\n", g.GameID, g.Title)...)
	}
	return "", "", fmt.Errorf("активных партий несколько, укажите нужную:\n%s", b)
}

func titleSuffix(title string) string {
	if title == "" {
		return ""
	}
	return " — " + title
}

func nonEmpty(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
