package domain

import "testing"

// Кд и опасность — вся арифметика режима «Сила», и считать её надо так же,
// как считает сам клиент игры: сумма по всем видам войск, знаменатель
// не меньше единицы.
func TestUserStatsDanger(t *testing.T) {
	tests := []struct {
		name       string
		stats      UserStats
		wantKD     float64
		wantDanger float64
	}{
		{
			name:  "обычный игрок",
			stats: UserStats{Level: 17, Defeated: 23440, Casualties: 15020},
			// 23440 / 15020 = 1.5605…
			wantKD: 23440.0 / 15020.0, wantDanger: 17 * (23440.0 / 15020.0),
		},
		{
			// Двадцатый уровень с кд 1.0 спокойнее семнадцатого с кд 1.2 —
			// ради этого уровень и умножается на кд, а не складывается.
			name:   "уровень выше, а опасность ниже",
			stats:  UserStats{Level: 20, Defeated: 100, Casualties: 100},
			wantKD: 1, wantDanger: 20,
		},
		{
			// Ни одной потери: делить не на что, и знаменатель становится
			// единицей — иначе вышла бы бесконечность.
			name:   "не терял войск",
			stats:  UserStats{Level: 3, Defeated: 7},
			wantKD: 7, wantDanger: 21,
		},
		{
			name:   "не воевал вовсе",
			stats:  UserStats{Level: 5},
			wantKD: 0, wantDanger: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.stats.KD(); got != tt.wantKD {
				t.Errorf("кд = %v, ожидалось %v", got, tt.wantKD)
			}
			if got := tt.stats.Danger(); got != tt.wantDanger {
				t.Errorf("опасность = %v, ожидалось %v", got, tt.wantDanger)
			}
		})
	}

	// Семнадцатый с кд 1.2 опаснее двадцатого с кд 1.0 — тот самый случай,
	// ради которого формула и выбрана.
	strong := UserStats{Level: 17, Defeated: 12, Casualties: 10}
	calm := UserStats{Level: 20, Defeated: 10, Casualties: 10}
	if !(strong.Danger() > calm.Danger()) {
		t.Errorf("17 уровень с кд 1.2 (%v) должен быть опаснее 20-го с кд 1.0 (%v)",
			strong.Danger(), calm.Danger())
	}
}

// Спрошенный без уровня и неспрошенный — разные вещи: у первого сайт
// промолчал, второй просто стоит в очереди. Красить нельзя ни того, ни
// другого, но сказать о них надо по-разному.
func TestUserStatsKnownRated(t *testing.T) {
	var none UserStats
	if none.Known() || none.Rated() {
		t.Errorf("пустая запись считается известной: %+v", none)
	}
}
