package repo

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Settings — общие переключатели админки. Ключ строкой затем, чтобы
// следующий такой переключатель не требовал ни миграции, ни нового типа.
type Settings struct{ pool *pgxpool.Pool }

func NewSettings(pool *pgxpool.Pool) *Settings { return &Settings{pool: pool} }

// Ключи настроек. Собраны здесь, чтобы опечатка в строке не превращалась
// молча в «значение по умолчанию».
const (
	// SettingCoalitionScan — включён ли обход партий за коалициями.
	// Выключается рутом: обход ходит в игру от общего аккаунта, и повод
	// остановить его может возникнуть быстрее, чем повод пересобрать образ.
	SettingCoalitionScan = "coalition_scan"
	// SettingS1914Session — подпись, которой админка ходит в Supremacy.
	// Лежит здесь, а не в отдельной таблице: это одна строка на всю
	// установку, ровно как переключатели рядом. Руками её никто не правит,
	// поэтому и updated_by у неё пустой.
	SettingS1914Session = "s1914_session"
)

// Bool читает переключатель. Незаданный ключ — не ошибка: значит, его
// не трогали, и действует умолчание.
func (r *Settings) Bool(ctx context.Context, key string, def bool) (bool, error) {
	var v string
	err := r.pool.QueryRow(ctx, `SELECT value FROM app_settings WHERE key = $1`, key).Scan(&v)
	if errors.Is(err, pgx.ErrNoRows) {
		return def, nil
	}
	if err != nil {
		return def, err
	}
	return v == "true", nil
}

// CoalitionScanEnabled — идёт ли сбор коалиций. По умолчанию да: настройка
// заводится в базе только когда её выключили, и отсутствие записи означает
// «никто не вмешивался».
func (r *Settings) CoalitionScanEnabled(ctx context.Context) (bool, error) {
	return r.Bool(ctx, SettingCoalitionScan, true)
}

// SetBool переключает и запоминает, кто это сделал.
func (r *Settings) SetBool(ctx context.Context, key string, on bool, by int64) error {
	value := "false"
	if on {
		value = "true"
	}
	_, err := r.pool.Exec(ctx,
		`INSERT INTO app_settings (key, value, updated_at, updated_by)
		 VALUES ($1, $2, now(), $3)
		 ON CONFLICT (key) DO UPDATE SET
		     value = EXCLUDED.value,
		     updated_at = EXCLUDED.updated_at,
		     updated_by = EXCLUDED.updated_by`, key, value, by)
	return err
}

// SavedSession — подпись Supremacy в том виде, в каком её отдаёт и принимает
// клиент игры. Своя структура здесь затем, чтобы repo не зависел от пакета
// supremacy, а supremacy — от базы.
type SavedSession struct {
	UserID     string    `json:"user_id"`
	AuthHash   string    `json:"auth_hash"`
	AuthTstamp string    `json:"auth_tstamp"`
	SavedAt    time.Time `json:"saved_at"`
}

// LoadSession достаёт подпись прошлого запуска. Второе значение false —
// её просто нет: ни разу не входили или базу почистили.
func (r *Settings) LoadSession(ctx context.Context) (SavedSession, bool, error) {
	var raw string
	err := r.pool.QueryRow(ctx,
		`SELECT value FROM app_settings WHERE key = $1`, SettingS1914Session).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return SavedSession{}, false, nil
	}
	if err != nil {
		return SavedSession{}, false, err
	}

	var s SavedSession
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		// Испорченную запись считаем отсутствующей: вход её перезапишет,
		// а падать из-за неё незачем.
		return SavedSession{}, false, nil
	}
	return s, true, nil
}

// SaveSession запоминает подпись до следующего запуска.
func (r *Settings) SaveSession(ctx context.Context, s SavedSession) error {
	raw, err := json.Marshal(s)
	if err != nil {
		return err
	}
	_, err = r.pool.Exec(ctx,
		`INSERT INTO app_settings (key, value, updated_at, updated_by)
		 VALUES ($1, $2, now(), NULL)
		 ON CONFLICT (key) DO UPDATE SET
		     value = EXCLUDED.value,
		     updated_at = EXCLUDED.updated_at,
		     updated_by = NULL`, SettingS1914Session, string(raw))
	return err
}
