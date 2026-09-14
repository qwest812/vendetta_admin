package web

import (
	"sync"
	"time"
)

// Сколько попыток регистрации пускаем с одной подсети за окно. Считаются
// все дошедшие до проверок, а не только удачные: каждая такая попытка —
// вопрос к сайту игры под общим аккаунтом проекта или сверка пароля
// заведённого аккаунта, и перебирать ни то, ни другое снаружи не должны.
//
// Десяти хватает с запасом: честный человек ошибается в пароле или ID
// раз-другой, а за одним вайфаем регистрируются от силы несколько своих.
const (
	signupAttempts = 10
	signupWindow   = 24 * time.Hour
)

// signupLimiter — счёт попыток регистрации по подсетям. Живёт в памяти:
// перезапуск обнулит счёт, но админка перезапускается редко, а таблица
// ради одного тормоза того не стоит.
type signupLimiter struct {
	mu    sync.Mutex
	seen  map[string][]time.Time
	limit int
	every time.Duration
}

func newSignupLimiter() *signupLimiter {
	return &signupLimiter{seen: map[string][]time.Time{}, limit: signupAttempts, every: signupWindow}
}

// allow засчитывает попытку с подсети и отвечает, укладывается ли она
// в предел. Отказ тоже не засчитывается повторно: иначе тот, кто упёрся
// в предел и продолжает жать кнопку, никогда бы из него не вышел.
func (l *signupLimiter) allow(subnet string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	// Старое выбрасываем у всех сразу: подсетей немного, а без уборки
	// карта росла бы от каждого случайного адреса навсегда.
	since := now.Add(-l.every)
	for key, times := range l.seen {
		kept := times[:0]
		for _, t := range times {
			if t.After(since) {
				kept = append(kept, t)
			}
		}
		if len(kept) == 0 {
			delete(l.seen, key)
		} else {
			l.seen[key] = kept
		}
	}

	if len(l.seen[subnet]) >= l.limit {
		return false
	}
	l.seen[subnet] = append(l.seen[subnet], now)
	return true
}
