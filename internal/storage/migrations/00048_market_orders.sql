-- +goose Up
-- Заявки на покупку с биржи: брать ресурс не дороже своей цены, пока
-- не уйдёт сумма. Воркер ходит в партию раз в every минут, потраченная
-- сумма заявку закрывает (next_at = NULL). На ресурс одна заявка:
-- новая заменяет прежнюю.
CREATE TABLE supremacy_market_orders (
    id            BIGSERIAL PRIMARY KEY,
    game_id       TEXT             NOT NULL,
    resource_id   INTEGER          NOT NULL,
    resource      TEXT             NOT NULL DEFAULT '',
    budget        INTEGER          NOT NULL,
    max_price     DOUBLE PRECISION NOT NULL,
    every_min     INTEGER          NOT NULL DEFAULT 30,
    spent         INTEGER          NOT NULL DEFAULT 0,
    bought        INTEGER          NOT NULL DEFAULT 0,
    next_at       TIMESTAMPTZ,
    run_at        TIMESTAMPTZ,
    result        TEXT             NOT NULL DEFAULT '',
    added_by      BIGINT REFERENCES users (id) ON DELETE SET NULL,
    added_at      TIMESTAMPTZ      NOT NULL DEFAULT now(),
    UNIQUE (game_id, resource_id)
);

CREATE INDEX supremacy_market_orders_due ON supremacy_market_orders (next_at) WHERE next_at IS NOT NULL;

-- +goose Down
DROP TABLE supremacy_market_orders;
