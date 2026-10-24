-- +goose Up
-- Где замечены игроки из личных списков врагов. Пишут попутно поиск
-- в лобби и сбор коалиций — в партиях, куда и так заходят; ради врагов
-- никто никуда не ходит: лишние запросы с общего аккаунта — повод для
-- бана. В телеграм это не уходит: списки личные, и общий чат узнал бы,
-- кто кого считает врагом. Каждый видит в «Обзоре» только своих.
--
-- seen_at освежается при каждой новой встрече в той же партии.
CREATE TABLE supremacy_lobby_sightings (
    site_user_id TEXT        NOT NULL,
    game_id      TEXT        NOT NULL,
    title        TEXT        NOT NULL DEFAULT '',
    login        TEXT        NOT NULL DEFAULT '',
    first_seen   TIMESTAMPTZ NOT NULL,
    seen_at      TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (site_user_id, game_id)
);
CREATE INDEX supremacy_lobby_sightings_seen_idx ON supremacy_lobby_sightings (seen_at);

-- +goose Down
DROP TABLE supremacy_lobby_sightings;
