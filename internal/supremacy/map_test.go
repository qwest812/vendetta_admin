package supremacy

import (
	"encoding/base64"
	"testing"
)

// Контур провинции — это пары uint16 подряд, big-endian, уже в координатах
// карты. Вектор снят с файла живой карты 51_5: первая провинция начинается
// с точки (2163, 719) при центре (2157, 768).
func TestDecodeBorder(t *testing.T) {
	points, err := decodeBorder("CHMCzwh3AtA=")
	if err != nil {
		t.Fatalf("разбор: %v", err)
	}
	want := []Point{{X: 2163, Y: 719}, {X: 2167, Y: 720}}
	if len(points) != len(want) {
		t.Fatalf("точек = %d, ожидалось %d", len(points), len(want))
	}
	for i := range want {
		if points[i] != want[i] {
			t.Errorf("точка %d = %+v, ожидалась %+v", i, points[i], want[i])
		}
	}
}

// Обрезанный контур лучше отвергнуть, чем нарисовать кривой многоугольник.
func TestDecodeBorderRejectsOddLength(t *testing.T) {
	if _, err := decodeBorder(base64.StdEncoding.EncodeToString([]byte{1, 2, 3})); err == nil {
		t.Error("ожидалась ошибка на неполной паре координат")
	}
}

func TestParseMapGeometry(t *testing.T) {
	const doc = `{
	 "width": 2971, "height": 1985, "mapID": "51_5",
	 "locations": [
	   {"@c": "p",  "id": 1, "b": "AAEAAQACAAIAAwAB"},
	   {"@c": "sp", "id": 2, "b": "AAEAAQACAAIAAwAB"},
	   {"@c": "p",  "id": 3, "b": "AAEAAQ=="}
	 ]}`

	geo, err := parseMapGeometry("51_5", []byte(doc))
	if err != nil {
		t.Fatalf("разбор: %v", err)
	}
	if geo.Width != 2971 || geo.Height != 1985 {
		t.Errorf("размеры = %dx%d", geo.Width, geo.Height)
	}
	// Море не храним — оно рисуется фоном; вырожденный контур из одной
	// точки многоугольником не является.
	if len(geo.Land) != 1 {
		t.Fatalf("провинций = %d, ожидалась одна", len(geo.Land))
	}
	if got := geo.Land[1]; len(got) != 3 || got[0] != (Point{X: 1, Y: 1}) {
		t.Errorf("контур провинции 1 = %+v", got)
	}
}

// Пустая карта — это ошибка, а не пустой рисунок: иначе страница показала бы
// чёрный прямоугольник и молчала о причине.
func TestParseMapGeometryWithoutLand(t *testing.T) {
	const doc = `{"width": 10, "height": 10, "locations": [{"@c": "sp", "id": 1, "b": "AAEAAQACAAIAAwAB"}]}`
	if _, err := parseMapGeometry("51_5", []byte(doc)); err == nil {
		t.Error("ожидалась ошибка на карте без суши")
	}
}

// Цвет страны игра шлёт своим форматом, а браузеру нужен свой.
func TestCSSColor(t *testing.T) {
	tests := []struct{ in, want string }{
		{"rgba(230,190,140,255)", "rgb(230,190,140)"},
		{"rgba(0, 0, 0, 255)", "rgb(0,0,0)"},
		{"", ""},
		{"#ffffff", ""},
	}
	for _, tt := range tests {
		if got := cssColor(tt.in); got != tt.want {
			t.Errorf("cssColor(%q) = %q, ожидалось %q", tt.in, got, tt.want)
		}
	}
}
