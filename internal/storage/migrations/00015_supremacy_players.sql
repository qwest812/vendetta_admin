-- +goose Up
-- Что игра рассказала про игрока, когда мы заходили в партию. Здесь только
-- свойства аккаунта, а не партии: бан живёт у человека, а поражение и выход
-- — у конкретной игры, и переносить их между партиями нельзя.
--
-- Таблица наполняется на каждом заходе в партию: раз уж состав всё равно
-- перед глазами, пусть остаётся и в базе — в следующий раз этот человек
-- может не встретиться, а бан знать полезно.
CREATE TABLE supremacy_players (
    site_user_id TEXT PRIMARY KEY,
    nickname     TEXT NOT NULL DEFAULT '',
    banned       BOOLEAN NOT NULL DEFAULT false,
    -- Когда бан увидели впервые. Снятый бан обнуляет и это поле: дата
    -- без бана означала бы «сидит до сих пор».
    banned_at    TIMESTAMPTZ,
    -- Когда и в какой партии видели последний раз.
    seen_at      TIMESTAMPTZ NOT NULL,
    seen_game_id TEXT NOT NULL DEFAULT ''
);

-- Забаненных мало, а списком их смотреть захочется.
CREATE INDEX supremacy_players_banned ON supremacy_players (site_user_id) WHERE banned;

-- +goose Down
DROP TABLE supremacy_players;
