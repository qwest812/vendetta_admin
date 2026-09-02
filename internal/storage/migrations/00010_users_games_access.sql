-- +goose Up
-- Доступ к разделу «Игры» выдаётся поштучно и только рутом: раздел ходит
-- в Supremacy под общим аккаунтом проекта, а заход в партию игра засчитывает
-- как вход в неё. Рут проходит по роли, флаг ему не нужен.
ALTER TABLE users ADD COLUMN games_access BOOLEAN NOT NULL DEFAULT FALSE;

-- +goose Down
ALTER TABLE users DROP COLUMN games_access;
