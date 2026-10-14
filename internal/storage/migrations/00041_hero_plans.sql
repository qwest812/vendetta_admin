-- +goose Up
-- Отложенное размещение героев: поставить героя в провинцию, когда
-- наступит нужный день партии. Воркер ходит в партию только к сроку.
CREATE TABLE supremacy_hero_plans (
    id          BIGSERIAL PRIMARY KEY,
    game_id     TEXT        NOT NULL,
    unit        INTEGER     NOT NULL,
    hero        TEXT        NOT NULL DEFAULT '',
    province    INTEGER     NOT NULL,
    city        TEXT        NOT NULL DEFAULT '',
    day         INTEGER     NOT NULL DEFAULT 0,
    -- Когда идти в партию; NULL — план закрыт: исполнен или исполнить
    -- его нельзя. Итог последнего захода — в run_at и result.
    next_at     TIMESTAMPTZ,
    run_at      TIMESTAMPTZ,
    result      TEXT        NOT NULL DEFAULT '',
    attempts    INTEGER     NOT NULL DEFAULT 0,
    added_by    BIGINT REFERENCES users (id) ON DELETE SET NULL,
    added_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX supremacy_hero_plans_game ON supremacy_hero_plans (game_id);
-- Воркер раз в минуту спрашивает открытые планы, у которых подошёл срок.
CREATE INDEX supremacy_hero_plans_due ON supremacy_hero_plans (next_at) WHERE next_at IS NOT NULL;

-- +goose Down
DROP TABLE supremacy_hero_plans;
