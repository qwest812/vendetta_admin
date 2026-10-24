-- +goose Up
-- Партия на последнем заходе сбора коалиций: карта, день и кто в какой
-- коалиции. Вместе с владельцами провинций (supremacy_province_owners)
-- этого хватает, чтобы нарисовать мини-карту «Ваших партий» без
-- расширения и без единого нового запроса к игре. Кончилась партия —
-- строка удаляется вместе с владельцами.
CREATE TABLE supremacy_province_games (
    game_id  TEXT        PRIMARY KEY,
    map_id   TEXT        NOT NULL,
    day      INTEGER     NOT NULL,
    -- {"номер на сайте": номер коалиции, …} — только живые игроки в коалиции.
    teams    JSONB       NOT NULL DEFAULT '{}',
    taken_at TIMESTAMPTZ NOT NULL
);

-- +goose Down
DROP TABLE supremacy_province_games;
