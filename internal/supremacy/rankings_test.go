package supremacy

import "testing"

// Строки сняты с газеты партии 10926066: экономики — день 2,
// устрашающие — день 9.
func TestParseRankings(t *testing.T) {
	const economy = "Крупнейшие экономики и суточное производство ресурсов в тоннах:\n" +
		"1-й : {{{countryLink 'Франция' '2'}}} : 42 402 т.\n" +
		"2-й : {{{countryLink 'Украина' '43'}}} : 41 847 т.\n"
	got := parsePaperRanking(economy)
	if len(got) != 2 || got[0] != (RankLine{2, "Франция", 42402}) || got[1] != (RankLine{43, "Украина", 41847}) {
		t.Errorf("экономики = %+v", got)
	}

	const army = "Оценивая военный потенциал стран Европы, ведущие международные военные эксперты называют сильнейшими:\n" +
		"1-й : {{{countryLink 'Швеция' '6'}}} (6,45%)\n" +
		"2-й : {{{countryLink 'Украина' '43'}}} (5,68%)\n"
	a := parsePaperRanking(army)
	if len(a) != 2 || a[0] != (RankLine{6, "Швеция", 6.45}) || a[1] != (RankLine{43, "Украина", 5.68}) {
		t.Errorf("армии = %+v", a)
	}

	const fear = "Опрос общественного мнения показал, что следующие страны являются самыми устрашающими:\n" +
		"1-й : {{{countryLink 'Украина' '43'}}}, Гетман MishaBlack\n" +
		"2-й : {{{countryLink 'Египет' '25'}}}, Султан Boec1990\n"
	f := parsePaperRanking(fear)
	if len(f) != 2 || f[0] != (RankLine{43, "Украина", 0}) || f[1] != (RankLine{25, "Египет", 0}) {
		t.Errorf("устрашающие = %+v", f)
	}
}

// Статью узнаём по заголовку: «популярные» и «армии» пишутся теми же
// строками, но это другие рейтинги.
func TestRankingKind(t *testing.T) {
	for title, want := range map[string]string{
		"Крупнейшие экономики":            RankEconomy,
		"Largest economies":               RankEconomy,
		"Самые устрашающие страны мира":   RankFear,
		"Most feared countries":           RankFear,
		"Самые популярные страны мира":    "",
		"Самые могущественные армии мира": RankArmy,
		"Самые богатые страны":            "",
	} {
		if got := rankingKind(title); got != want {
			t.Errorf("%q → %q, ожидалось %q", title, got, want)
		}
	}
}
