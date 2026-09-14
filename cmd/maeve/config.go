package main

import (
	"bufio"
	"fmt"
	"io"
	"strings"
	"time"
)

// settingsName — файл настроек рядом с exe. Текстовый, чтобы править его
// блокнотом: человеку на Windows не нужно знать про переменные окружения.
const settingsName = "maeve.txt"

// settingsTemplate — что кладём рядом с exe при первом запуске. Комментарии
// здесь и есть инструкция: другой у человека не будет.
const settingsTemplate = `# Настройки программы призыва пехоты Мейв в Supremacy 1914.
# Строки с # — комментарии. Поменяйте значения и запустите программу снова.

# Логин (ник или почта) и пароль от своего аккаунта в игре.
# Вход через Google не подходит: у аккаунта должен быть свой пароль.
LOGIN=
PASSWORD=

# Номера партий через запятую — в них программа будет жать кнопку Мейв.
# Номер виден в адресе партии. Пример: GAMES=10886819, 10900334
GAMES=

# Пауза между обходами — случайная, от и до. Заход в партию игра считает
# входом в неё, поэтому чаще раза в 45 минут ходить не стоит.
INTERVAL_MIN=45m
INTERVAL_MAX=50m

# Язык ответов игры.
LANG=ru
`

// settings — разобранный maeve.txt.
type settings struct {
	Login       string
	Password    string
	Games       []string
	IntervalMin time.Duration
	IntervalMax time.Duration
	Lang        string
}

// minInterval — чаще этого ходить не даём, даже если в файле опечатка:
// каждый заход — вход в партию, и робот, заходящий раз в минуту, виден сразу.
const minInterval = 5 * time.Minute

// parseSettings читает файл вида КЛЮЧ=значение. Незнакомые ключи — ошибка:
// опечатка в имени иначе молча оставила бы значение по умолчанию.
func parseSettings(r io.Reader) (*settings, error) {
	s := &settings{IntervalMin: 45 * time.Minute, IntervalMax: 50 * time.Minute, Lang: "ru"}

	sc := bufio.NewScanner(r)
	line := 0
	for sc.Scan() {
		line++
		text := strings.TrimSpace(sc.Text())
		// Блокнот сохраняет UTF-8 с меткой в начале файла.
		if line == 1 {
			text = strings.TrimPrefix(text, "\ufeff")
		}
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		key, value, ok := strings.Cut(text, "=")
		if !ok {
			return nil, fmt.Errorf("строка %d: нужно КЛЮЧ=значение, а там %q", line, text)
		}
		key, value = strings.ToUpper(strings.TrimSpace(key)), strings.TrimSpace(value)

		switch key {
		case "LOGIN":
			s.Login = value
		case "PASSWORD":
			s.Password = value
		case "GAMES":
			s.Games = parseGames(value)
		case "INTERVAL_MIN", "INTERVAL_MAX":
			d, err := time.ParseDuration(value)
			if err != nil {
				return nil, fmt.Errorf("строка %d: %s — это время вида 45m или 1h, а там %q", line, key, value)
			}
			if key == "INTERVAL_MIN" {
				s.IntervalMin = d
			} else {
				s.IntervalMax = d
			}
		case "LANG":
			if value != "" {
				s.Lang = value
			}
		default:
			return nil, fmt.Errorf("строка %d: незнакомая настройка %q", line, key)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}

	switch {
	case s.Login == "" || s.Password == "":
		return nil, fmt.Errorf("впишите LOGIN и PASSWORD от своего аккаунта в игре")
	case len(s.Games) == 0:
		return nil, fmt.Errorf("впишите в GAMES хотя бы один номер партии")
	case checkGames(s.Games) != nil:
		return nil, checkGames(s.Games)
	case s.IntervalMin < minInterval:
		return nil, fmt.Errorf("INTERVAL_MIN не меньше %v: каждый заход игра считает входом в партию", minInterval)
	case s.IntervalMax < s.IntervalMin:
		return nil, fmt.Errorf("INTERVAL_MAX должен быть не меньше INTERVAL_MIN")
	}
	return s, nil
}

// parseGames разбирает номера через запятую, пробел или точку с запятой
// и выкидывает повторы: дважды за обход в одну партию заходить незачем.
func parseGames(value string) []string {
	fields := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == ';' || r == ' ' || r == '\t'
	})
	seen := map[string]bool{}
	var out []string
	for _, f := range fields {
		if !seen[f] {
			seen[f] = true
			out = append(out, f)
		}
	}
	return out
}

// checkGames — все ли номера похожи на номера. Всё прочее скорее опечатка
// или вставленная целиком ссылка, и молча выкидывать её нельзя — тогда
// партию просто не обойдут.
func checkGames(games []string) error {
	for _, g := range games {
		for _, r := range g {
			if r < '0' || r > '9' {
				return fmt.Errorf("в GAMES %q — не номер партии: нужны только цифры", g)
			}
		}
	}
	return nil
}
