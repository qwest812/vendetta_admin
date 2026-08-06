package supremacy

import (
	"context"
	"sync"
	"time"
)

// MemorySeenStore — кеш увиденных игр в памяти процесса. Перезапуск его
// обнуляет, так что в бою нужен вариант на базе; здесь он для тестов и как
// запасной вариант, когда хранилище не передали.
type MemorySeenStore struct {
	mu   sync.Mutex
	seen map[string]time.Time
}

func NewMemorySeenStore() *MemorySeenStore {
	return &MemorySeenStore{seen: make(map[string]time.Time)}
}

func (s *MemorySeenStore) Seen(_ context.Context, gameIDs []string) (map[string]bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make(map[string]bool, len(gameIDs))
	for _, id := range gameIDs {
		if _, ok := s.seen[id]; ok {
			out[id] = true
		}
	}
	return out, nil
}

func (s *MemorySeenStore) Mark(_ context.Context, gameID, _ string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Как и в базе: важно время первого сообщения, а не последнего.
	if _, ok := s.seen[gameID]; !ok {
		s.seen[gameID] = at
	}
	return nil
}

func (s *MemorySeenStore) Forget(_ context.Context, olderThan time.Time) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var n int64
	for id, at := range s.seen {
		if at.Before(olderThan) {
			delete(s.seen, id)
			n++
		}
	}
	return n, nil
}
