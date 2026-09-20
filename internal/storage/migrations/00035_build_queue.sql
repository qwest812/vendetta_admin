-- +goose Up
-- Очередь строительства: на каждую провинцию свой список зданий, и воркер
-- ставит следующее, как только в провинции освобождается слот.
--
-- Отдельного выключателя у очереди нет: есть записи — воркер ходит, пусто —
-- не ходит. Поэтому и хранится она таблицей, а не флагом на партии.
--
-- Названия провинции и здания лежат рядом с номерами намеренно: страница
-- показывает очередь, не заходя в партию, а заход стоит входа в игру.
CREATE TABLE supremacy_build_queue (
    id          BIGSERIAL PRIMARY KEY,
    game_id     TEXT        NOT NULL,
    province_id INT         NOT NULL,
    province    TEXT        NOT NULL DEFAULT '',
    upgrade_id  INT         NOT NULL,
    upgrade     TEXT        NOT NULL DEFAULT '',
    -- Порядок внутри провинции. Первая запись и есть то, что строим следующим.
    position    INT         NOT NULL,
    added_by    BIGINT      REFERENCES users(id) ON DELETE SET NULL,
    added_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX supremacy_build_queue_order
    ON supremacy_build_queue (game_id, province_id, position);

-- Когда воркеру прийти в партию снова. Считается по самой партии: к концу
-- идущей стройки или к тому мигу, когда накопятся ресурсы. Пусто означает
-- «зайти при первой возможности» — так выглядит только что созданная очередь.
ALTER TABLE supremacy_game_tasks
    ADD COLUMN build_next_at TIMESTAMPTZ,
    ADD COLUMN build_run_at  TIMESTAMPTZ,
    ADD COLUMN build_result  TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE supremacy_game_tasks
    DROP COLUMN build_result,
    DROP COLUMN build_run_at,
    DROP COLUMN build_next_at;
DROP TABLE supremacy_build_queue;
