package repo

import (
	"context"
	"errors"

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
