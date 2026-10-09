package supremacy

import (
	"encoding/json"
	"testing"
)

// Строка neighborRelations — java-мапа: служебный «@c» рядом с номерами
// не должен ни ронять разбор, ни попадать в отношения.
func TestRelationsOf(t *testing.T) {
	got := relationsOf(json.RawMessage(`{"@c":"java.util.HashMap","2":1,"5":3,"7":"x","-1":1}`))
	if len(got) != 2 || got[2] != 1 || got[5] != 3 {
		t.Fatalf("relationsOf = %v", got)
	}
}

// Право прохода получают только боты с начала партии, у кого его ещё нет:
// люди, брошенные страны, выбывшие и те, у кого уже общая карта, — мимо.
func TestRightOfWayTargets(t *testing.T) {
	no := false
	g := &GameState{
		Me: 1,
		Players: map[int]Player{
			1: {ID: 1, NativeAI: true}, // это мы
			2: {ID: 2, NativeAI: nativeBot(true, nil, 0)},
			3: {ID: 3, NativeAI: nativeBot(true, nil, 0)},
			4: {ID: 4, NativeAI: nativeBot(true, nil, 0)},
			5: {ID: 5, NativeAI: nativeBot(true, nil, 101)},  // брошенная страна человека
			6: {ID: 6, NativeAI: nativeBot(true, &no, 0)},    // не бот с начала
			7: {ID: 7, NativeAI: nativeBot(false, nil, 101)}, // живой игрок
			8: {ID: 8, NativeAI: true, Defeated: true},
		},
		Relations: map[int]int{2: 1, 4: 4},
	}
	targets, total := g.RightOfWayTargets()
	if total != 3 {
		t.Fatalf("total = %d, want 3", total)
	}
	if len(targets) != 2 || targets[0].ID != 2 || targets[1].ID != 3 {
		t.Fatalf("targets = %+v", targets)
	}
}
