-- +goose Up
-- Снимки партий из расширения: по нажатию «Анализировать карту» оно
-- присылает то, что видит клиент игры у самого игрока, — номер партии,
-- день, карту, владельцев провинций, страны и ники. Ни одного запроса
-- к игре: клиент у игрока уже открыт. Хранится последний снимок партии.
CREATE TABLE supremacy_client_snapshots (
    game_id   TEXT        PRIMARY KEY,
    map_id    TEXT        NOT NULL,
    day       INTEGER     NOT NULL,
    -- [{"id":1,"site":"…","nation":"…","name":"…","ai":false,"team":0}, …]
    players   JSONB       NOT NULL,
    -- [[номер провинции, номер владельца в партии], …]
    owners    JSONB       NOT NULL,
    taken_at  TIMESTAMPTZ NOT NULL,
    taken_by  BIGINT      REFERENCES users (id) ON DELETE SET NULL
);

-- В каких партиях играет пользователь — по его же снимкам. По ним «Обзор»
-- показывает «Ваши партии» с мини-картой.
CREATE TABLE user_client_games (
    user_id   BIGINT      NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    game_id   TEXT        NOT NULL,
    player_id INTEGER     NOT NULL,
    seen_at   TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (user_id, game_id)
);

-- +goose Down
DROP TABLE user_client_games;
DROP TABLE supremacy_client_snapshots;
