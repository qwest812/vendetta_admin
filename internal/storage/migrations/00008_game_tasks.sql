-- +goose Up
-- Что админка делает в партии Supremacy сама. Настройка на партию, а не на
-- пользователя: игровой аккаунт в проекте один и общий, а раздел рутовый.
CREATE TABLE supremacy_game_tasks (
    game_id     TEXT PRIMARY KEY,
    title       TEXT NOT NULL DEFAULT '',
    -- Навык Мейв «призвать пехоту»: воркер жмёт его по таймеру.
    hero_deploy BOOLEAN NOT NULL DEFAULT false,
    updated_by  BIGINT REFERENCES users (id) ON DELETE SET NULL,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- Последний заход воркера: когда и чем кончился. Нужны на странице
    -- партии — иначе о работе воркера можно судить только по логу.
    last_run_at TIMESTAMPTZ,
    last_result TEXT NOT NULL DEFAULT ''
);

-- Воркер каждый раз спрашивает только включённые партии.
CREATE INDEX supremacy_game_tasks_hero_deploy
    ON supremacy_game_tasks (game_id) WHERE hero_deploy;

-- +goose Down
DROP TABLE supremacy_game_tasks;
