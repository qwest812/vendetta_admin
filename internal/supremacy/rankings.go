package supremacy

// Рейтинги дня из газеты. Газета печатает их по кругу, по несколько в
// день: самые богатые, самые устрашающие, крупнейшие экономики, наиболее
// эффективные экономики, самые могущественные армии, самые популярные.
// Формат строк у нескольких одинаковый — «популярные» неотличимы от
// «устрашающих», «эффективные экономики» от «крупнейших», — поэтому
// статью узнаём по заголовку. Берём три:
//
// Крупнейшие экономики — суточное производство ресурсов в тоннах:
//
//	1-й : {{{countryLink 'Франция' '2'}}} : 42 402 т.
//
// Самые могущественные армии — доля военной силы в процентах:
//
//	1-й : {{{countryLink 'Швеция' '6'}}} (6,45%)
//
// Самые устрашающие — место без числа, порядок строки:
//
//	1-й : {{{countryLink 'Украина' '43'}}}, Гетман MishaBlack

import (
	"context"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Виды рейтингов, которые берём из газеты.
const (
	RankEconomy = "economy"
	RankArmy    = "army"
	RankFear    = "fear"
)

// rankingKind узнаёт рейтинг по заголовку статьи — на русском и на
// английском, как их пишет игра. Пусто — статья не рейтинг или не наш.
func rankingKind(title string) string {
	t := strings.ToLower(title)
	switch {
	// Именно «крупнейшие»: у «наиболее эффективных» то же слово
	// «экономики», но число в них — цена производства в фунтах.
	case strings.Contains(t, "крупнейш") || strings.Contains(t, "largest") || strings.Contains(t, "biggest"):
		return RankEconomy
	// «Могущественные», а не просто «армия»: «Армия истреблена» — это
	// сводка о бое, а не рейтинг.
	case strings.Contains(t, "могуществ") || strings.Contains(t, "powerful") || strings.Contains(t, "strongest"):
		return RankArmy
	case strings.Contains(t, "устраша") || strings.Contains(t, "fear"):
		return RankFear
	}
	return ""
}

// RankLine — строка рейтинга: номер игрока в партии, как газета назвала
// страну, и число — тонны в сутки у экономик, доля в процентах у армий;
// у устрашения его нет.
type RankLine struct {
	PlayerID int
	Nation   string
	Value    float64
}

// После ссылки на страну — «: 42 402 т.» у экономик или «(6,45%)» у армий.
var reRankLine = regexp.MustCompile(`^\s*\d+\S*\s*:\s*\{\{\{countryLink '([^']*)' '(\d+)'\}\}\}\s*(?::\s*([\d\s\x{a0}\x{202f}.,]+)|\(\s*([\d.,]+)\s*%\s*\))?`)

// parsePaperRanking разбирает строки рейтинга по порядку мест.
func parsePaperRanking(body string) []RankLine {
	var out []RankLine
	for _, line := range strings.Split(body, "\n") {
		m := reRankLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		id, err := strconv.Atoi(m[2])
		if err != nil {
			continue
		}
		r := RankLine{PlayerID: id, Nation: m[1]}
		switch {
		case m[3] != "":
			if n, ok := parseCount(m[3]); ok {
				r.Value = float64(n)
			}
		case m[4] != "":
			if f, err := strconv.ParseFloat(strings.ReplaceAll(m[4], ",", "."), 64); err == nil {
				r.Value = f
			}
		}
		out = append(out, r)
	}
	return out
}

// RankDay — рейтинги одного дня; пусто — в этот день газета их не печатала.
type RankDay struct {
	Day     int
	Economy []RankLine
	Army    []RankLine
	Fear    []RankLine
}

// Rankings — рейтинги партии по дням, по порядку. Missing и Why — как
// у потерь: какие дни игра не отдала и чем отказала.
type Rankings struct {
	Days    []RankDay
	Missing []int
	Why     string
}

// GameRankings — рейтинги за все дни с первого по today. Читает ту же
// газету, что и потери, и из той же памяти: второй подсчёт — без похода.
func (c *Client) GameRankings(ctx context.Context, gameID string, today int) (*Rankings, error) {
	days, missing, why, err := c.readPaper(ctx, gameID, today)
	if err != nil {
		return nil, err
	}
	out := &Rankings{Missing: missing, Why: why}
	for d, day := range days {
		out.Days = append(out.Days, RankDay{Day: d, Economy: day.economy, Army: day.army, Fear: day.fear})
	}
	sort.Slice(out.Days, func(i, j int) bool { return out.Days[i].Day < out.Days[j].Day })
	return out, nil
}
