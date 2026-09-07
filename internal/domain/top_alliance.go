package domain

import "time"

// TopAlliance — клан из верхушки рейтинга самой Supremacy. От обычного
// Alliance отличается тем, что принадлежит не игроку, а рейтингу: ключ
// здесь номер клана, а не номер человека на сайте игры.
//
// Хранится это снимком: обход переписывает всю десятку разом, прошлых
// снимков не остаётся. Rank — место в общем рейтинге игры, поэтому у первой
// десятки он и есть 1..10.
type TopAlliance struct {
	ID         string
	Rank       int
	Elo        int
	Name       string
	Tag        string
	CapturedAt time.Time
}
