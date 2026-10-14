-- +goose Up
-- Сбор войск в город: рут выбирает свою провинцию, и туда уходят армии —
-- разово кнопкой или автопилотом, каждая новая.
ALTER TABLE supremacy_game_tasks
    ADD COLUMN rally_province INTEGER,
    ADD COLUMN rally_city     TEXT NOT NULL DEFAULT '',
    ADD COLUMN rally_auto     BOOLEAN NOT NULL DEFAULT false,
    -- Армии, которые автопилот уже видел; новые — те, кого здесь нет.
    -- NULL — ещё не видел ни одной: первый заход только запоминает.
    ADD COLUMN rally_known    BIGINT[],
    ADD COLUMN rally_run_at   TIMESTAMPTZ,
    ADD COLUMN rally_result   TEXT NOT NULL DEFAULT '';

-- Автопилот ходит туда, где включено хоть что-то из его дел.
DROP INDEX supremacy_game_tasks_hero_deploy;
CREATE INDEX supremacy_game_tasks_autopilot
    ON supremacy_game_tasks (game_id) WHERE hero_deploy OR rally_auto;

-- +goose Down
DROP INDEX supremacy_game_tasks_autopilot;
CREATE INDEX supremacy_game_tasks_hero_deploy
    ON supremacy_game_tasks (game_id) WHERE hero_deploy;
ALTER TABLE supremacy_game_tasks
    DROP COLUMN rally_province,
    DROP COLUMN rally_city,
    DROP COLUMN rally_auto,
    DROP COLUMN rally_known,
    DROP COLUMN rally_run_at,
    DROP COLUMN rally_result;
