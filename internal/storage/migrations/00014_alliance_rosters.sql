-- +goose Up
-- Кланы, состав которых стоит забрать целиком. Сайт отдаёт весь список
-- участников одним запросом, поэтому дешевле спросить клан, чем каждого
-- его игрока по отдельности. Таблица работает как очередь: клан попадает
-- сюда, как только встретился в ответе про любого игрока.
CREATE TABLE supremacy_alliances (
    alliance_id TEXT PRIMARY KEY,
    name        TEXT NOT NULL DEFAULT '',
    tag         TEXT NOT NULL DEFAULT '',
    -- NULL — «клан знаем, состав ещё не забирали».
    checked_at  TIMESTAMPTZ
);

-- Воркер ищет неспрошенные и протухшие составы в том же порядке, что
-- и по игрокам: сначала новые, потом самые давние.
CREATE INDEX supremacy_alliances_checked_at
    ON supremacy_alliances (checked_at NULLS FIRST);

-- Кланы, которые уже встречались у игроков, ставим в очередь сразу:
-- иначе их состав ждал бы новой встречи.
INSERT INTO supremacy_alliances (alliance_id)
SELECT DISTINCT alliance_id FROM supremacy_user_alliances WHERE alliance_id <> ''
ON CONFLICT (alliance_id) DO NOTHING;

-- +goose Down
DROP TABLE supremacy_alliances;
