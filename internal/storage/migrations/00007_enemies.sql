-- +goose Up
-- Список врагов личный: у каждого пользователя свой набор и свой комментарий
-- «за что». Поэтому ключ составной — один игрок может числиться врагом сразу
-- у нескольких людей, и это разные записи.
CREATE TABLE user_enemies (
    user_id    BIGINT      NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    player_id  BIGINT      NOT NULL REFERENCES players (id) ON DELETE CASCADE,
    comment    TEXT        NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, player_id)
);

-- Обратный индекс: по нему карточка отвечает, у кого она во врагах, — сейчас
-- это никому не показывается, но каскад при удалении игрока идёт по нему же.
CREATE INDEX user_enemies_player_idx ON user_enemies (player_id);

-- +goose Down
DROP TABLE user_enemies;
