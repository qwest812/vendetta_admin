package repo

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Heroes — справочник героев Supremacy в базе: снимок целиком и портреты
// к нему. Пустая таблица — обычное дело: пока рут не нажимал «обновить»,
// админка живёт справочником, вшитым в образ.
type Heroes struct{ pool *pgxpool.Pool }

func NewHeroes(pool *pgxpool.Pool) *Heroes { return &Heroes{pool: pool} }

// HeroSnapshot — снимок так, как он лежит в базе: сами данные и следы
// обновления. Разбирать снимок здесь незачем — этим занят пакет героев.
type HeroSnapshot struct {
	Raw         []byte
	CollectedAt time.Time
	UpdatedAt   time.Time
	// UpdatedBy — ник того, кто нажал кнопку. Пусто, если этого человека
	// уже удалили: снимок от этого хуже не стал.
	UpdatedBy string
}

// Load достаёт снимок. Второе значение false означает, что своего снимка
// нет, — берите вшитый.
func (r *Heroes) Load(ctx context.Context) (HeroSnapshot, bool, error) {
	var s HeroSnapshot
	var nickname *string
	err := r.pool.QueryRow(ctx,
		`SELECT h.snapshot, h.collected_at, h.updated_at, u.nickname
		   FROM supremacy_heroes h
		   LEFT JOIN users u ON u.id = h.updated_by`).Scan(&s.Raw, &s.CollectedAt, &s.UpdatedAt, &nickname)
	if errors.Is(err, pgx.ErrNoRows) {
		return HeroSnapshot{}, false, nil
	}
	if err != nil {
		return HeroSnapshot{}, false, err
	}
	if nickname != nil {
		s.UpdatedBy = *nickname
	}
	return s, true, nil
}

// Save кладёт свежий снимок вместо прежнего. Истории снимков не храним:
// справочник отвечает на вопрос «как в игре сейчас», и прошлые ответы
// на него никому не нужны.
func (r *Heroes) Save(ctx context.Context, raw []byte, collectedAt time.Time, by int64) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO supremacy_heroes (id, snapshot, collected_at, updated_at, updated_by)
		 VALUES (TRUE, $1, $2, now(), $3)
		 ON CONFLICT (id) DO UPDATE SET
		     snapshot = EXCLUDED.snapshot,
		     collected_at = EXCLUDED.collected_at,
		     updated_at = EXCLUDED.updated_at,
		     updated_by = EXCLUDED.updated_by`, raw, collectedAt, by)
	return err
}

// SaveImages кладёт портреты. Сохраняются все, что пришли: файл героя
// игра иногда перерисовывает, и сверять байты дороже, чем переписать.
func (r *Heroes) SaveImages(ctx context.Context, images map[string][]byte) error {
	if len(images) == 0 {
		return nil
	}
	batch := &pgx.Batch{}
	for name, data := range images {
		batch.Queue(
			`INSERT INTO supremacy_hero_images (name, data, updated_at)
			 VALUES ($1, $2, now())
			 ON CONFLICT (name) DO UPDATE SET
			     data = EXCLUDED.data, updated_at = EXCLUDED.updated_at`, name, data)
	}
	return r.pool.SendBatch(ctx, batch).Close()
}

// Image отдаёт портрет по имени файла. Второе значение false означает,
// что своего портрета нет: значит, показывать надо вшитый.
func (r *Heroes) Image(ctx context.Context, name string) ([]byte, bool, error) {
	var data []byte
	err := r.pool.QueryRow(ctx,
		`SELECT data FROM supremacy_hero_images WHERE name = $1`, name).Scan(&data)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return data, true, nil
}
