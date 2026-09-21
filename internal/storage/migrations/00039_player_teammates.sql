-- +goose Up
-- «Играют вместе» — связь двух карточек, отмеченная руками: админ знает
-- из чата, по наблюдениям, со слов. Архив коалиций находит такие пары
-- сам, но только среди тех, кого застал в одной коалиции; эта связь —
-- то, что знают люди.
--
-- Пара хранится одной строкой в порядке player_a < player_b: связь
-- взаимная, и «A с B» — то же самое, что «B с A».
CREATE TABLE player_teammates (
    player_a BIGINT      NOT NULL REFERENCES players(id) ON DELETE CASCADE,
    player_b BIGINT      NOT NULL REFERENCES players(id) ON DELETE CASCADE,
    -- note — откуда известно: коротко, для тех, кто будет смотреть потом.
    note     TEXT        NOT NULL DEFAULT '',
    added_by BIGINT      REFERENCES users(id) ON DELETE SET NULL,
    added_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (player_a, player_b),
    CHECK (player_a < player_b)
);

-- Карточка ищет связи с обеих сторон: по player_a хватает первичного ключа.
CREATE INDEX player_teammates_b ON player_teammates (player_b);

-- +goose Down
DROP TABLE player_teammates;
