package web

import (
	"strings"
	"testing"
)

// Группа собирается через общего напарника, а пара с одной общей партией
// в неё не попадает: одна коалиция бывает случайной.
func TestTogetherGroups(t *testing.T) {
	side := func(id int, name string) coalitionSideView {
		return coalitionSideView{ID: id, Nation: name}
	}
	pairs := []coalitionPairView{
		{A: side(1, "A"), B: side(2, "B"), Total: 3},
		{A: side(2, "B"), B: side(3, "C"), Total: 2},
		{A: side(4, "D"), B: side(5, "E"), Total: 2},
		{A: side(1, "A"), B: side(6, "F"), Total: 1}, // ниже порога
	}
	marks, legend := togetherGroups(pairs)

	if len(legend) != 2 {
		t.Fatalf("групп %d, want 2: %+v", len(legend), legend)
	}
	if got := strings.Join(legend[0].Members, ","); got != "A,B,C" || legend[0].Games != 5 {
		t.Fatalf("первая группа: %+v", legend[0])
	}
	if marks[1].Color == "" || marks[1].Color != marks[3].Color || marks[1].Color == marks[4].Color {
		t.Fatalf("цвета: %+v", marks)
	}
	if _, ok := marks[6]; ok {
		t.Fatal("F играл с A один раз — не должен попасть в группу")
	}
	if got := strings.Join(marks[2].Partners, ","); got != "A,C" {
		t.Fatalf("напарники B: %q", got)
	}
}
