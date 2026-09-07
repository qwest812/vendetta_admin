-- +goose Up
-- Верхушка рейтинга альянсов игры. Таблица — снимок, а не история: каждый
-- обход переписывает её целиком, и о том, кто был в топе месяц назад,
-- здесь не остаётся ничего. Так решено намеренно: карте нужен только
-- сегодняшний топ.
--
-- Строк тут ровно столько, сколько мы просим у рейтинга, — десяток.
CREATE TABLE supremacy_top_alliances (
    alliance_id TEXT PRIMARY KEY,
    -- Место в общем рейтинге игры, а не среди этих десяти: если однажды
    -- захочется брать не с первого места, номер останется честным.
    rank        INT NOT NULL,
    elo         INT NOT NULL DEFAULT 0,
    name        TEXT NOT NULL DEFAULT '',
    tag         TEXT NOT NULL DEFAULT '',
    -- Одно на все строки: обход переписывает таблицу целиком, и по этому
    -- времени воркер понимает, пора ли идти за рейтингом снова.
    captured_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE supremacy_top_alliances;
