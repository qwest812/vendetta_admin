-- +goose Up
-- Кеш альянсов игроков в самой Supremacy. Альянс — это клан из игры,
-- а не из нашего справочника: сайт отдаёт его по номеру игрока, и карта
-- партии красится именно им. Спрашивать сайт на каждое открытие страницы
-- нельзя — в партии до сорока игроков, а это сорок запросов на чужой API.
CREATE TABLE supremacy_user_alliances (
    site_user_id TEXT PRIMARY KEY,
    -- Пустой alliance_id — это тоже ответ: «спросили, альянса нет».
    -- Без него игроки-одиночки опрашивались бы заново каждый раз.
    alliance_id  TEXT NOT NULL DEFAULT '',
    name         TEXT NOT NULL DEFAULT '',
    tag          TEXT NOT NULL DEFAULT '',
    checked_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE supremacy_user_alliances;
