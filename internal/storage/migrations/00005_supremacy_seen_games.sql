-- +goose Up
-- Игры Supremacy, о которых воркер уже сообщил. Кеш нужен в базе, а не в
-- памяти: иначе каждый перезапуск приложения заново вываливает в лог всё
-- лобби целиком.
CREATE TABLE supremacy_seen_games (
    game_id     TEXT PRIMARY KEY,
    title       TEXT NOT NULL,
    reported_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Старые записи выметаются по времени — этому проходу нужен индекс.
CREATE INDEX supremacy_seen_games_reported_at ON supremacy_seen_games (reported_at);

-- +goose Down
DROP TABLE supremacy_seen_games;
