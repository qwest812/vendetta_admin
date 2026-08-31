-- +goose Up
-- Профиль пользователя админки: кто он и под каким ником играет.
-- Все поля необязательные — доступ выдают до того, как человек их заполнит.
ALTER TABLE users
    ADD COLUMN full_name TEXT NOT NULL DEFAULT '',
    ADD COLUMN city      TEXT NOT NULL DEFAULT '',
    -- Игровой ID хранится строкой, как и у карточек игроков: игра отдаёт
    -- его строкой, и арифметики с ним нет.
    ADD COLUMN game_id   TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE users
    DROP COLUMN full_name,
    DROP COLUMN city,
    DROP COLUMN game_id;
