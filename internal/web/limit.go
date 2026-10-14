package web

import (
	"strings"
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

// Сколько неудачных входов терпим, прежде чем перестать сверять пароль.
// Считаются только неудачи: свои входят с первого-второго раза, и удачный
// вход ничьего счёта не трогает.
//
// Подсеть — против того, кто перебирает пароли с одного места: десять
// промахов за четверть часа честный человек не набирает. Логин — против
// перебора одного аккаунта с многих адресов; предел у него выше и окно
// длиннее, потому что упереться в него может и сам владелец, если кто-то
// чужой долбит его почту. Тогда он подождёт час, а пароль останется цел.
const (
	loginSubnetFails  = 10
	loginSubnetWindow = 15 * time.Minute
	loginNameFails    = 20
	loginNameWindow   = time.Hour
)

// attemptLimiter — счёт попыток по ключу за скользящее окно. Живёт
// в памяти: перезапуск обнулит счёт, но админка перезапускается редко,
// а таблица ради одного тормоза того не стоит.
type attemptLimiter struct {
	mu    sync.Mutex
	seen  map[string][]time.Time
	limit int
	every time.Duration
}

func newAttemptLimiter(limit int, every time.Duration) *attemptLimiter {
	return &attemptLimiter{seen: map[string][]time.Time{}, limit: limit, every: every}
}

func newSignupLimiter() *attemptLimiter { return newAttemptLimiter(signupAttempts, signupWindow) }

// allow засчитывает попытку и отвечает, укладывается ли она в предел.
// Отказ не засчитывается повторно: иначе тот, кто упёрся в предел
// и продолжает жать кнопку, никогда бы из него не вышел.
func (l *attemptLimiter) allow(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.fullLocked(key, now) {
		return false
	}
	l.seen[key] = append(l.seen[key], now)
	return true
}

// full отвечает, исчерпан ли предел, ничего не засчитывая.
func (l *attemptLimiter) full(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.fullLocked(key, now)
}

// add засчитывает попытку без всяких проверок — для счёта неудач, который
// узнаёт об итоге уже после того, как попытку пропустил.
func (l *attemptLimiter) add(key string, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.forgetLocked(now)
	l.seen[key] = append(l.seen[key], now)
}

func (l *attemptLimiter) fullLocked(key string, now time.Time) bool {
	l.forgetLocked(now)
	return len(l.seen[key]) >= l.limit
}

// forgetLocked выбрасывает старое у всех сразу: ключей немного, а без
// уборки карта росла бы от каждого случайного адреса навсегда.
func (l *attemptLimiter) forgetLocked(now time.Time) {
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
}

// loginLimiter — счёт неудачных входов сразу по подсети и по логину.
// Форма сайта и вход расширения делят один счёт: пароль один и тот же,
// и перебирать его через API не должно быть проще, чем через форму.
type loginLimiter struct {
	subnets *attemptLimiter
	names   *attemptLimiter
}

func newLoginLimiter() *loginLimiter {
	return &loginLimiter{
		subnets: newAttemptLimiter(loginSubnetFails, loginSubnetWindow),
		names:   newAttemptLimiter(loginNameFails, loginNameWindow),
	}
}

// loginKey сводит логин к виду, в котором его ищет база: регистр почты
// не важен, и «Root@x» с «root@x» — один и тот же счёт.
func loginKey(login string) string { return strings.ToLower(strings.TrimSpace(login)) }

// blocked — пора ли отказать, не сверяя пароль.
func (l *loginLimiter) blocked(subnet, login string, now time.Time) bool {
	return l.subnets.full(subnet, now) || l.names.full(loginKey(login), now)
}

// failed засчитывает неудачный вход.
func (l *loginLimiter) failed(subnet, login string, now time.Time) {
	l.subnets.add(subnet, now)
	l.names.add(loginKey(login), now)
}
