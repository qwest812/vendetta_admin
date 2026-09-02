-- +goose Up
-- Список друзей устроен ровно как список врагов: он личный, у каждого свой
-- набор и свой комментарий, поэтому ключ составной. Отдельная таблица, а не
-- колонка в user_enemies: одного и того же человека кто-то держит в друзьях,
-- а кто-то во врагах, и это разные записи разных людей.
CREATE TABLE user_friends (
    user_id    BIGINT      NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    player_id  BIGINT      NOT NULL REFERENCES players (id) ON DELETE CASCADE,
    comment    TEXT        NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, player_id)
);

-- Обратный индекс: по нему идёт каскад при удалении карточки игрока.
CREATE INDEX user_friends_player_idx ON user_friends (player_id);

-- +goose Down
DROP TABLE user_friends;
