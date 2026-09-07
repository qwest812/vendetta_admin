-- +goose Up
-- Партии, которые пользователь смотрел. Список личный: партии проверяет
-- каждый свои, и чужие ему ни к чему — поэтому ключ составной, как у списков
-- друзей и врагов. Строка на партию, а не на заход: список отвечает на вопрос
-- «чем я сейчас занят», а не «когда и куда я ходил», и журналом ему быть
-- незачем — от повторной проверки двигается дата.
CREATE TABLE user_checked_games (
    user_id BIGINT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    -- Номер партии — строка и тут, и в остальной админке: игра отдаёт его
    -- строкой, и своего смысла числом он не приобретает.
    game_id TEXT NOT NULL,
    -- Название на момент проверки: партия может уже кончиться, а список
    -- должен остаться читаемым без похода в игру.
    title      TEXT        NOT NULL DEFAULT '',
    checked_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, game_id)
);

-- По дате идёт уборка отстоявшихся: она про всех сразу, и ключ ей не помощник.
CREATE INDEX user_checked_games_checked_idx ON user_checked_games (checked_at);

-- +goose Down
DROP TABLE user_checked_games;
