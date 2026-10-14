package supremacy

// Путь армии по карте. Игра сама дорогу не ищет: клиент прокладывает её
// по графу дорог карты (connections_v2) и шлёт серверу готовую цепочку
// отрезков «gc» — от того места, где армия стоит, до цели. Сервер лишь
// проверяет её и сам считает скорость на каждом отрезке. Поэтому путь
// ищем мы, по тому же графу.
//
// Клиент ищет его A* по времени в пути. Скорость на отрезке зависит от
// местности и от того, чья земля, и считает её сервер; нам хватает длины,
// умноженной на цену провинции (см. FindRoute). Путь выходит тот же или
// почти тот же, а сервер принимает любой, лишь бы шёл по дорогам.

import (
	"container/heap"
	"math"
)

// Допуски клиента: короче routeMinStep отрезок не шлётся вовсе, а точка
// дальше routeTolerance от дороги на ней не стоит (maxToleranceSq = 1).
const (
	routeMinStep   = 0.1
	routeTolerance = 1.0
)

// nodeKey — вершина графа. Концы отрезков в файле карты заданы в сотых
// долях, поэтому и ключ в сотых: так совпадают концы соседних отрезков.
type nodeKey struct{ x, y int64 }

func keyOf(p MapPoint) nodeKey {
	return nodeKey{int64(math.Round(p.X * 100)), int64(math.Round(p.Y * 100))}
}

type routeEdge struct {
	to   nodeKey
	cost float64
}

// FindRoute ищет путь армии из from в to по дорогам суши. cost — цена
// прохода по провинции на единицу длины; бесконечная или неположительная
// закрывает провинцию. Возвращает точки пути по порядку, первая — from.
//
// from — где армия стоит. Обычно это вершина графа, но армия, остановленная
// на марше, стоит посреди отрезка: тогда путь начинается с него, в одну
// из двух сторон, — так же поступает и клиент.
func FindRoute(geo *MapGeometry, from, to MapPoint, cost func(province int) float64) ([]MapPoint, bool) {
	if geo == nil || len(geo.Roads) == 0 {
		return nil, false
	}

	graph := map[nodeKey][]routeEdge{}
	points := map[nodeKey]MapPoint{}
	link := func(a, b MapPoint, price float64) {
		ka, kb := keyOf(a), keyOf(b)
		points[ka], points[kb] = a, b
		graph[ka] = append(graph[ka], routeEdge{to: kb, cost: price})
		graph[kb] = append(graph[kb], routeEdge{to: ka, cost: price})
	}

	var (
		nearest     = math.Inf(1)
		onRoad      Road
		onRoadPrice float64
	)
	for _, r := range geo.Roads {
		price := cost(r.Province)
		if !(price > 0) || math.IsInf(price, 1) {
			continue
		}
		link(r.A, r.B, dist(r.A, r.B)*price)
		if d := segmentDist(from, r.A, r.B); d < nearest {
			nearest, onRoad, onRoadPrice = d, r, price
		}
	}

	start := keyOf(from)
	if _, ok := graph[start]; !ok {
		// Не в вершине: армия стоит на дороге между двумя вершинами. Дальше
		// допуска от любой дороги — значит, мы чего-то не понимаем в карте,
		// и гадать, куда её вести, не будем.
		if nearest > routeTolerance {
			return nil, false
		}
		points[start] = from
		graph[start] = []routeEdge{
			{to: keyOf(onRoad.A), cost: dist(from, onRoad.A) * onRoadPrice},
			{to: keyOf(onRoad.B), cost: dist(from, onRoad.B) * onRoadPrice},
		}
	}

	goal := keyOf(to)
	if _, ok := graph[goal]; !ok {
		return nil, false
	}

	prev, ok := shortest(graph, start, goal)
	if !ok {
		return nil, false
	}
	var path []MapPoint
	for k := goal; ; k = prev[k] {
		path = append(path, points[k])
		if k == start {
			break
		}
	}
	for i, j := 0, len(path)-1; i < j; i, j = i+1, j-1 {
		path[i], path[j] = path[j], path[i]
	}
	path[0] = from
	return path, true
}

// shortest — Дейкстра от start до goal. Возвращает, откуда пришли в каждую
// вершину, и дошли ли до цели.
func shortest(graph map[nodeKey][]routeEdge, start, goal nodeKey) (map[nodeKey]nodeKey, bool) {
	best := map[nodeKey]float64{start: 0}
	prev := map[nodeKey]nodeKey{}
	queue := &routeQueue{{key: start}}
	for queue.Len() > 0 {
		cur := heap.Pop(queue).(routeItem)
		if cur.key == goal {
			return prev, true
		}
		if cur.cost > best[cur.key] {
			continue
		}
		for _, e := range graph[cur.key] {
			next := cur.cost + e.cost
			if old, seen := best[e.to]; seen && old <= next {
				continue
			}
			best[e.to] = next
			prev[e.to] = cur.key
			heap.Push(queue, routeItem{key: e.to, cost: next})
		}
	}
	return nil, false
}

type routeItem struct {
	key  nodeKey
	cost float64
}

type routeQueue []routeItem

func (q routeQueue) Len() int           { return len(q) }
func (q routeQueue) Less(i, j int) bool { return q[i].cost < q[j].cost }
func (q routeQueue) Swap(i, j int)      { q[i], q[j] = q[j], q[i] }
func (q *routeQueue) Push(x any)        { *q = append(*q, x.(routeItem)) }
func (q *routeQueue) Pop() any {
	old := *q
	item := old[len(old)-1]
	*q = old[:len(old)-1]
	return item
}

// gotoCommands превращает путь в цепочку отрезков марша, как её шлёт
// клиент: каждый отрезок — от точки до следующей. Слишком короткие клиент
// выбрасывает, и мы тоже.
func gotoCommands(path []MapPoint) []any {
	var out []any
	for i := 1; i < len(path); i++ {
		a, b := path[i-1], path[i]
		if dist(a, b) < routeMinStep {
			continue
		}
		out = append(out, map[string]any{
			"@c": commandGoto,
			"sp": map[string]any{"x": a.X, "y": a.Y},
			"tp": map[string]any{"x": b.X, "y": b.Y},
		})
	}
	return out
}

func dist(a, b MapPoint) float64 { return math.Hypot(a.X-b.X, a.Y-b.Y) }

// segmentDist — расстояние от точки p до отрезка ab.
func segmentDist(p, a, b MapPoint) float64 {
	dx, dy := b.X-a.X, b.Y-a.Y
	length := dx*dx + dy*dy
	if length == 0 {
		return dist(p, a)
	}
	t := ((p.X-a.X)*dx + (p.Y-a.Y)*dy) / length
	t = math.Max(0, math.Min(1, t))
	return dist(p, MapPoint{X: a.X + t*dx, Y: a.Y + t*dy})
}
