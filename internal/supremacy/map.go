package supremacy

// Очертания провинций игра держит не в состоянии партии, а отдельным файлом
// на static-сервере — один на карту, общий для всех партий на ней. Состояние
// присылает только владельцев, поэтому карту рисуем из двух источников:
// геометрия отсюда, цвета — из состояния.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// mapJSONURL — где лежит геометрия карты. Файл статический и меняется
// вместе с картой, а не с партией, поэтому его не грех держать в памяти.
const mapJSONURL = "https://static.supremacy1914.com/fileadmin/mapjson/live/%s@high.json"

// Point — точка в координатах карты; их же использует поле width/height.
type Point struct{ X, Y int }

// MapGeometry — контуры провинций одной карты. Море не храним: его проще
// нарисовать фоном, чем полутысячей многоугольников.
type MapGeometry struct {
	ID     string
	Width  int
	Height int
	Land   map[int][]Point
}

// MapGeometry отдаёт очертания карты, скачивая файл один раз на процесс.
func (c *Client) MapGeometry(ctx context.Context, mapID string) (*MapGeometry, error) {
	if mapID == "" {
		return nil, fmt.Errorf("не задана карта")
	}

	c.mu.Lock()
	cached, ok := c.maps[mapID]
	c.mu.Unlock()
	if ok {
		return cached, nil
	}

	geo, err := c.fetchMap(ctx, mapID)
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	if c.maps == nil {
		c.maps = make(map[string]*MapGeometry)
	}
	c.maps[mapID] = geo
	c.mu.Unlock()
	return geo, nil
}

func (c *Client) fetchMap(ctx context.Context, mapID string) (*MapGeometry, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf(mapJSONURL, mapID), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("карта %s: %w", mapID, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("карта %s: http %d", mapID, resp.StatusCode)
	}
	// Файл крупный (около мегабайта), но не безразмерный.
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, fmt.Errorf("карта %s: чтение: %w", mapID, err)
	}
	return parseMapGeometry(mapID, raw)
}

// parseMapGeometry разбирает файл карты. Контур провинции лежит в поле «b»
// как base64: подряд идущие пары uint16 (big-endian) — это уже готовые
// координаты карты, никакого масштабирования не нужно.
func parseMapGeometry(mapID string, raw []byte) (*MapGeometry, error) {
	var doc struct {
		Width     int    `json:"width"`
		Height    int    `json:"height"`
		MapID     string `json:"mapID"`
		Locations []struct {
			Class  string `json:"@c"`
			ID     int    `json:"id"`
			Border string `json:"b"`
		} `json:"locations"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("карта %s: разбор: %w", mapID, err)
	}
	if doc.Width <= 0 || doc.Height <= 0 {
		return nil, fmt.Errorf("карта %s: не заданы размеры", mapID)
	}

	geo := &MapGeometry{ID: mapID, Width: doc.Width, Height: doc.Height, Land: map[int][]Point{}}
	for _, l := range doc.Locations {
		// «p» — суша, «sp» — море: рисуем только сушу.
		if l.Class != provinceLand || l.Border == "" {
			continue
		}
		points, err := decodeBorder(l.Border)
		if err != nil {
			return nil, fmt.Errorf("карта %s, провинция %d: %w", mapID, l.ID, err)
		}
		if len(points) >= 3 {
			geo.Land[l.ID] = points
		}
	}
	if len(geo.Land) == 0 {
		return nil, fmt.Errorf("карта %s: не нашлось ни одной провинции", mapID)
	}
	return geo, nil
}

func decodeBorder(border string) ([]Point, error) {
	raw, err := base64.StdEncoding.DecodeString(border)
	if err != nil {
		return nil, fmt.Errorf("контур не разобрался: %w", err)
	}
	if len(raw)%4 != 0 {
		return nil, fmt.Errorf("контур из %d байт не делится на пары координат", len(raw))
	}

	points := make([]Point, 0, len(raw)/4)
	for i := 0; i < len(raw); i += 4 {
		points = append(points, Point{
			X: int(raw[i])<<8 | int(raw[i+1]),
			Y: int(raw[i+2])<<8 | int(raw[i+3]),
		})
	}
	return points, nil
}
