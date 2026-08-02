-- +goose Up
CREATE TABLE clans (
    id         BIGSERIAL   PRIMARY KEY,
    name       TEXT        NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX clans_name_key ON clans (lower(name));

CREATE TABLE players (
    id         BIGSERIAL   PRIMARY KEY,
    nickname   TEXT        NOT NULL,
    clan_id    BIGINT      REFERENCES clans (id) ON DELETE SET NULL,
    created_by BIGINT      REFERENCES users (id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX players_nickname_key ON players (lower(nickname));
-- Триграммный индекс обслуживает поиск подстроки: ILIKE '%ник%'.
CREATE INDEX players_nickname_trgm ON players USING gin (nickname gin_trgm_ops);
CREATE INDEX players_clan_idx ON players (clan_id);

-- Справочник признаков. Вес со знаком: минус — риск, плюс — лояльность,
-- ноль — нейтральная пометка, которая не влияет на шкалы.
CREATE TABLE traits (
    id         BIGSERIAL   PRIMARY KEY,
    code       TEXT        NOT NULL UNIQUE,
    name       TEXT        NOT NULL,
    weight     INT         NOT NULL DEFAULT 0,
    is_active  BOOLEAN     NOT NULL DEFAULT TRUE,
    sort_order INT         NOT NULL DEFAULT 100,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE player_traits (
    player_id BIGINT NOT NULL REFERENCES players (id) ON DELETE CASCADE,
    trait_id  BIGINT NOT NULL REFERENCES traits (id) ON DELETE CASCADE,
    PRIMARY KEY (player_id, trait_id)
);

CREATE INDEX player_traits_trait_idx ON player_traits (trait_id);

CREATE TABLE player_notes (
    id           BIGSERIAL   PRIMARY KEY,
    player_id    BIGINT      NOT NULL REFERENCES players (id) ON DELETE CASCADE,
    author_id    BIGINT      REFERENCES users (id) ON DELETE SET NULL,
    author_email TEXT        NOT NULL,
    body         TEXT        NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX player_notes_player_idx ON player_notes (player_id, created_at DESC);

INSERT INTO traits (code, name, weight, sort_order) VALUES
    ('multiaccount',      'Мультиаккаунт',            -10, 10),
    ('breaks_agreements', 'Нарушает договорённости',   -8, 20),
    ('foul_language',     'Сквернословит',             -4, 30),
    ('plays_well',        'Хорошо играет',              6, 40),
    ('donator',           'Донатер',                    3, 50),
    ('night_player',      'Играет ночью',               0, 60);

-- +goose Down
DROP TABLE player_notes;
DROP TABLE player_traits;
DROP TABLE traits;
DROP TABLE players;
DROP TABLE clans;
