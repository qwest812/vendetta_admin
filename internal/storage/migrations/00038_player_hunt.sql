-- +goose Up
-- Поиск игрока в лобби: рут отмечает игрока, а воркер раз в десять минут
-- смотрит составы открытых партий и, найдя его, пишет в телеграм.
--
-- Ищем по номеру на сайте: ник меняется, номер — нет. Ник храним рядом,
-- чтобы список и сообщение читались без похода в игру.
CREATE TABLE supremacy_hunt_targets (
    id           BIGSERIAL PRIMARY KEY,
    site_user_id TEXT        NOT NULL UNIQUE,
    nickname     TEXT        NOT NULL DEFAULT '',
    added_by     BIGINT      REFERENCES users(id) ON DELETE SET NULL,
    added_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Где уже нашли. О паре «игрок + партия» сообщаем один раз: воркер
-- смотрит лобби каждые десять минут, а партия стоит в нём часами.
CREATE TABLE supremacy_hunt_hits (
    target_id BIGINT      NOT NULL REFERENCES supremacy_hunt_targets(id) ON DELETE CASCADE,
    game_id   TEXT        NOT NULL,
    title     TEXT        NOT NULL DEFAULT '',
    nickname  TEXT        NOT NULL DEFAULT '',
    nation    TEXT        NOT NULL DEFAULT '',
    found_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (target_id, game_id)
);

CREATE INDEX supremacy_hunt_hits_recent ON supremacy_hunt_hits (found_at DESC);

-- +goose Down
DROP TABLE supremacy_hunt_hits;
DROP TABLE supremacy_hunt_targets;
