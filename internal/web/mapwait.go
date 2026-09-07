package web

import (
	"fmt"
	"math/rand/v2"
	"time"
)

// waitSeconds — таймеру нужны целые секунды, и округлять их надо вверх:
// сказать «0», когда ждать ещё полсекунды, значит соврать.
func waitSeconds(d time.Duration) int {
	if d <= 0 {
		return 0
	}
	return int((d + time.Second - 1) / time.Second)
}

// waitClock — те же секунды для глаз. Час ожидания секундами не показать:
// «2951» ни о чём не говорит, а «49:11» читается сразу.
func waitClock(sec int) string {
	if sec < 0 {
		sec = 0
	}
	return fmt.Sprintf("%d:%02d", sec/60, sec%60)
}

// Сбор данных о партии занимает секунды, а из общего кэша они приходят
// мгновенно — и по этой мгновенности видно, что партию кто-то уже смотрел.
// Видеть этого человек не должен: карта для него всегда собирается примерно
// одинаково. loaderFloor — то самое «примерно», loaderJitter — разброс,
// без которого ровно пять секунд каждый раз подозрительнее самой разницы.
const (
	loaderFloor  = 5 * time.Second
	loaderJitter = 2 * time.Second
)

// loaderMs — сколько ещё держать «собираем данные», если страница собралась
// за spent. Считаем от начала запроса: ожидание ответа сервера человек уже
// отсидел, и добавлять к нему полные пять секунд незачем.
func loaderMs(spent time.Duration) int {
	left := loaderFloor + rand.N(loaderJitter) - spent
	if left <= 0 {
		return 0
	}
	return int(left / time.Millisecond)
}
