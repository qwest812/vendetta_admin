package supremacy

// Потери в войнах — из газеты партии. Каждый день газета печатает сводку
// по каждой идущей войне: две страны и потери каждой с начала этой войны.
// Шаблон статьи такой (снят Supremacy Utilities, 18.2.1):
//
//	{{{countryLink 'Германия' '12'}}} - 12,400
//	{{{countryLink 'Франция' '7'}}} - 1.2 M
//
// Строк со ссылкой на страну и числом в сводке ровно две; остальной текст
// переводится на язык аккаунта, поэтому сводку узнаём по этим двум
// строкам, а не по словам.
//
// Счёт в сводке нарастающий, но только в пределах одной войны: заключили
// мир и снова воюют — счёт пошёл с нуля. Нам нужны потери за всю партию,
// поэтому падение счёта значит «началась новая война», и прежний итог
// откладывается в копилку.
//
// Игра отдаёт газету по одному дню за запрос. Прошедший день больше
// не меняется, поэтому разобранные дни держим в памяти: при следующем
// подсчёте спрашиваем только сегодняшний и новые.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// paperWorkers — сколько дней газеты спрашиваем разом. Игровой сервер
	// отвечает быстро, но сто запросов подряд со стороны выглядят как
	// обход, поэтому немного.
	paperWorkers = 3
	// paperTodayFresh — сколько держим сегодняшний день: он ещё дописывается.
	paperTodayFresh = 10 * time.Minute
	// paperIdle — партию, которую столько никто не считал, выбрасываем.
	paperIdle = 48 * time.Hour
)

// warLine — одна сводка о войне: две стороны и потери каждой с начала
// этой войны.
type warLine struct {
	A, B         int
	NameA, NameB string
	LostA, LostB int64
}

// paperDay — разобранный день газеты: сводки о войнах и рейтинги дня.
type paperDay struct {
	wars    []warLine
	economy []RankLine
	fear    []RankLine
	at      time.Time
}

type paperEntry struct {
	days map[int]paperDay
	used time.Time
}

// Duel — потери игрока в войне с одним противником за всю партию.
type Duel struct {
	OpponentID   int
	OpponentName string
	Lost         int64 // потерял сам
	Killed       int64 // потерял противник
}

// PlayerLosses — потери игрока за партию: всего и по противникам.
type PlayerLosses struct {
	ID       int
	Name     string // страна, как её назвала газета
	Lost     int64
	Killed   int64
	Opponent []Duel // по убыванию потерь
}

// Losses — итог по газете партии.
type Losses struct {
	Players []PlayerLosses // по убыванию потерь
	Days    int            // сколько дней газеты прочитано
	// Missing — дни, которые игра так и не отдала: итог без них неполный.
	// Why — чем она отказала в первом из них: обычно отказ у всех один.
	Missing []int
	Why     string
}

var countryLinkLine = regexp.MustCompile(`\{\{\{countryLink\s+'([^']*)'\s+'(\d+)'\}\}\}\s*[-–—]\s*(.+)$`)

// parseWar узнаёт сводку о войне в тексте статьи.
func parseWar(body string) (warLine, bool) {
	type side struct {
		id   int
		name string
		lost int64
	}
	var sides []side
	for _, line := range strings.Split(body, "\n") {
		m := countryLinkLine.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		id, err := strconv.Atoi(m[2])
		if err != nil || id <= 0 {
			return warLine{}, false
		}
		lost, ok := parseCount(m[3])
		if !ok {
			return warLine{}, false
		}
		sides = append(sides, side{id, m[1], lost})
	}
	if len(sides) != 2 || sides[0].id == sides[1].id {
		return warLine{}, false
	}
	a, b := sides[0], sides[1]
	if a.id > b.id {
		a, b = b, a
	}
	return warLine{A: a.id, B: b.id, NameA: a.name, NameB: b.name, LostA: a.lost, LostB: b.lost}, true
}

// parseCount читает число так, как его напечатала газета на любом языке:
// «12,400», «12 400», «12.400», «1.2 M», «1,2 млн», «15k». Буквы после
// числа — множитель: «к/т» тысячи, «M/м» миллионы. Без множителя всё, кроме
// цифр, — разделители разрядов.
func parseCount(text string) (int64, bool) {
	runes := []rune(strings.TrimSpace(text))
	i := 0
	for i < len(runes) && (runes[i] >= '0' && runes[i] <= '9' || strings.ContainsRune(".,' \u00a0\u202f", runes[i])) {
		i++
	}
	num, rest := string(runes[:i]), strings.TrimSpace(string(runes[i:]))
	digits := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, num)
	if digits == "" {
		return 0, false
	}

	mult := 0.0
	if rest != "" {
		switch []rune(strings.ToLower(rest))[0] {
		case 'k', 'к', 'т':
			mult = 1e3
		case 'm', 'м':
			mult = 1e6
		}
	}
	if mult == 0 {
		n, err := strconv.ParseInt(digits, 10, 64)
		return n, err == nil
	}
	// С множителем число дробное: «1.2 M», «1,2 млн».
	clean := strings.Map(func(r rune) rune {
		switch {
		case r >= '0' && r <= '9':
			return r
		case r == '.' || r == ',':
			return '.'
		}
		return -1
	}, num)
	f, err := strconv.ParseFloat(clean, 64)
	if err != nil {
		return 0, false
	}
	return int64(math.Round(f * mult)), true
}

// sumWars складывает сводки по дням в итог за партию. Дни идут по порядку:
// падение счёта у пары — новая война, прежний итог уходит в копилку.
func sumWars(days map[int]paperDay) []PlayerLosses {
	type pair struct{ a, b int }
	type tally struct {
		nameA, nameB string
		bankA, bankB int64
		lastA, lastB int64
	}
	pairs := map[pair]*tally{}
	order := make([]int, 0, len(days))
	for d := range days {
		order = append(order, d)
	}
	sort.Ints(order)
	for _, d := range order {
		for _, w := range days[d].wars {
			k := pair{w.A, w.B}
			t := pairs[k]
			if t == nil {
				t = &tally{}
				pairs[k] = t
			}
			if w.LostA < t.lastA || w.LostB < t.lastB {
				t.bankA += t.lastA
				t.bankB += t.lastB
			}
			t.lastA, t.lastB = w.LostA, w.LostB
			t.nameA, t.nameB = w.NameA, w.NameB
		}
	}

	byID := map[int]*PlayerLosses{}
	add := func(id int, name string, opp int, oppName string, lost, killed int64) {
		p := byID[id]
		if p == nil {
			p = &PlayerLosses{ID: id}
			byID[id] = p
		}
		p.Name = name
		p.Lost += lost
		p.Killed += killed
		p.Opponent = append(p.Opponent, Duel{OpponentID: opp, OpponentName: oppName, Lost: lost, Killed: killed})
	}
	for k, t := range pairs {
		lostA, lostB := t.bankA+t.lastA, t.bankB+t.lastB
		add(k.a, t.nameA, k.b, t.nameB, lostA, lostB)
		add(k.b, t.nameB, k.a, t.nameA, lostB, lostA)
	}

	out := make([]PlayerLosses, 0, len(byID))
	for _, p := range byID {
		sort.Slice(p.Opponent, func(i, j int) bool {
			if p.Opponent[i].Lost != p.Opponent[j].Lost {
				return p.Opponent[i].Lost > p.Opponent[j].Lost
			}
			return p.Opponent[i].OpponentID < p.Opponent[j].OpponentID
		})
		out = append(out, *p)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Lost != out[j].Lost {
			return out[i].Lost > out[j].Lost
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// GameLosses считает потери в войнах партии по газете — за все дни
// с первого по today. Заходит наблюдателем: входом в партию это
// не считается. Прошедшие дни берутся из памяти.
func (c *Client) GameLosses(ctx context.Context, gameID string, today int) (*Losses, error) {
	days, missing, why, err := c.readPaper(ctx, gameID, today)
	if err != nil {
		return nil, err
	}
	return &Losses{Players: sumWars(days), Days: len(days), Missing: missing, Why: why}, nil
}

// readPaper отдаёт разобранные дни газеты с первого по today: что уже
// прочитано — из памяти, недостающее спрашивает у игры. Второе и третье
// значения — дни, которые игра не отдала, и чем она отказала.
func (c *Client) readPaper(ctx context.Context, gameID string, today int) (map[int]paperDay, []int, string, error) {
	if today < 1 {
		today = 1
	}
	now := time.Now()

	c.mu.Lock()
	if c.papers == nil {
		c.papers = make(map[string]*paperEntry)
	}
	for id, e := range c.papers {
		if now.Sub(e.used) > paperIdle {
			delete(c.papers, id)
		}
	}
	e := c.papers[gameID]
	if e == nil {
		e = &paperEntry{days: map[int]paperDay{}}
		c.papers[gameID] = e
	}
	e.used = now
	var need []int
	for d := 1; d <= today; d++ {
		got, ok := e.days[d]
		// Прошедший день не меняется; сегодняшний и вчерашний ещё могут
		// дописываться — их держим недолго.
		if !ok || (d >= today-1 && now.Sub(got.at) > paperTodayFresh) {
			need = append(need, d)
		}
	}
	c.mu.Unlock()

	var (
		missing []int
		reason  string
	)
	if len(need) > 0 {
		acc, err := c.gameAccess(ctx, gameID)
		if err != nil {
			return nil, nil, "", err
		}
		fetched, failed, why := c.fetchPaperDays(ctx, acc, gameID, need)
		if len(fetched) == 0 && len(failed) > 0 && ctx.Err() != nil {
			return nil, nil, "", ctx.Err()
		}
		missing, reason = failed, why
		c.mu.Lock()
		for d, day := range fetched {
			e.days[d] = day
		}
		c.mu.Unlock()
	}

	c.mu.Lock()
	days := make(map[int]paperDay, len(e.days))
	for d, day := range e.days {
		if d <= today {
			days[d] = day
		}
	}
	c.mu.Unlock()

	sort.Ints(missing)
	return days, missing, reason, nil
}

// fetchPaperDays спрашивает дни газеты в несколько потоков. Не отданные
// игрой дни возвращаются вторым списком, а первая причина отказа — третьим:
// без неё на странице видно только «не отдала», и искать почему пришлось
// бы в логе сервера.
func (c *Client) fetchPaperDays(ctx context.Context, acc *gameAccess, gameID string,
	days []int) (map[int]paperDay, []int, string) {

	var (
		mu     sync.Mutex
		wg     sync.WaitGroup
		out    = make(map[int]paperDay, len(days))
		failed []int
		why    string
	)
	queue := make(chan int)
	for range min(paperWorkers, len(days)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for d := range queue {
				day, err := c.paperDay(ctx, acc, gameID, d)
				mu.Lock()
				if err != nil {
					c.log.Warn("день газеты", "gameID", gameID, "день", d, "err", err)
					failed = append(failed, d)
					if why == "" {
						why = err.Error()
					}
				} else {
					day.at = time.Now()
					out[d] = day
				}
				mu.Unlock()
			}
		}()
	}
	for _, d := range days {
		if ctx.Err() != nil {
			mu.Lock()
			failed = append(failed, d)
			mu.Unlock()
			continue
		}
		queue <- d
	}
	close(queue)
	wg.Wait()
	if why == "" && ctx.Err() != nil {
		why = ctx.Err().Error()
	}
	return out, failed, why
}

// paperRetry — паузы перед повторами дня газеты после сбоя прокси игры.
// Заминки у неё секундные; дольше ждать — держать человека у кнопки.
var paperRetry = []time.Duration{time.Second, 3 * time.Second}

// gatewayError — прокси игры не достучался до сервера партии: ответ
// 502, 503 или 504. Это заминка, а не отказ, и её стоит переждать.
func gatewayError(err error) bool {
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	switch apiErr.Status {
	case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	}
	return false
}

// paperDay спрашивает один день газеты — тем же запросом, каким клиент
// игры листает газету: UltUpdateGameStateAction с stateType 2 и номером
// дня в option.
//
// Сбой прокси игры (502, 503, 504) повторяется: газета — только чтение,
// и повтор ничего не задвоит. Без этого одна заминка сервера проваливала
// разом все дни (так и было 15.10.2026: девять дней — девять 502 за одну
// миллисекунду).
func (c *Client) paperDay(ctx context.Context, acc *gameAccess, gameID string, day int) (paperDay, error) {
	var raw json.RawMessage
	for attempt := 0; ; attempt++ {
		err := c.gsCall(ctx, acc, gameID, 2, "ultshared.action.UltUpdateGameStateAction", 0,
			map[string]any{
				"stateType": 2,
				// Ноль, как шлёт клиент, а не пустая строка: на пустую
				// игровой сервер отвечает 502 (замечено 15.10.2026).
				"stateID":           "0",
				"addStateIDsOnSent": false,
				"option":            day,
				"actions":           nil,
				"lastCallDuration":  50,
			}, &raw)
		if err == nil {
			break
		}
		if attempt >= len(paperRetry) || !gatewayError(err) {
			return paperDay{}, err
		}
		select {
		case <-time.After(paperRetry[attempt]):
		case <-ctx.Done():
			return paperDay{}, err
		}
	}
	articles, err := paperArticles(raw)
	if err != nil {
		return paperDay{}, fmt.Errorf("день %d: %w", day, err)
	}
	var out paperDay
	for _, a := range articles {
		if w, ok := parseWar(a.Body); ok {
			out.wars = append(out.wars, w)
		}
		switch rankingKind(a.Title) {
		case RankEconomy:
			out.economy = parsePaperRanking(a.Body)
		case RankFear:
			out.fear = parsePaperRanking(a.Body)
		}
	}
	return out, nil
}

// paperArticles достаёт тексты статей из ответа. Ответ бывает самим
// состоянием газеты или полным ответом со states["2"]; список статей —
// массивом или java-списком ["java.util.ArrayList", [...]].
// paperArticle — статья газеты: заголовок и текст.
type paperArticle struct {
	Title string `json:"title"`
	Body  string `json:"messageBody"`
}

func paperArticles(raw json.RawMessage) ([]paperArticle, error) {
	var wrapped struct {
		States map[string]json.RawMessage `json:"states"`
	}
	if json.Unmarshal(raw, &wrapped) == nil {
		if paper, ok := wrapped.States["2"]; ok {
			raw = paper
		}
	}
	var paper struct {
		Articles json.RawMessage `json:"articles"`
	}
	if err := json.Unmarshal(raw, &paper); err != nil {
		return nil, fmt.Errorf("разбор газеты: %w", err)
	}
	list := javaList(paper.Articles)
	out := make([]paperArticle, 0, len(list))
	for _, a := range list {
		var art paperArticle
		if json.Unmarshal(a, &art) == nil && art.Body != "" {
			out = append(out, art)
		}
	}
	return out, nil
}

// javaList разворачивает список: обычный массив или java-обёртку
// ["java.util.ArrayList", [...]].
func javaList(raw json.RawMessage) []json.RawMessage {
	var list []json.RawMessage
	if json.Unmarshal(raw, &list) != nil {
		return nil
	}
	if len(list) == 2 {
		var tag string
		var inner []json.RawMessage
		if json.Unmarshal(list[0], &tag) == nil && json.Unmarshal(list[1], &inner) == nil {
			return inner
		}
	}
	return list
}
