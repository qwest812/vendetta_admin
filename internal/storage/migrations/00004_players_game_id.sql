-- +goose Up
-- Игровой ID постоянен, в отличие от ника: по нему карточка узнаётся после
-- любого переименования. У заведённых ранее карточек его нет — там NULL.
ALTER TABLE players ADD COLUMN game_id TEXT;

-- NULL'ы уникальный индекс считает различными, поэтому карточки без ID
-- уживаются друг с другом, а заполненные ID остаются уникальными.
CREATE UNIQUE INDEX players_game_id_key ON players (lower(game_id));
-- Поиск идёт по подстроке и ника, и ID — обоим нужен триграммный индекс.
CREATE INDEX players_game_id_trgm ON players USING gin (game_id gin_trgm_ops);

-- +goose Down
DROP INDEX players_game_id_trgm;
DROP INDEX players_game_id_key;
ALTER TABLE players DROP COLUMN game_id;
