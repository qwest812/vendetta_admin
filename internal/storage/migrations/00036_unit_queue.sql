-- +goose Up
-- Очередь войск живёт в той же таблице, что и очередь зданий: воркер
-- ведёт обе за один заход в партию, а заход игра засчитывает как вход.
-- Отдельная таблица со своим воркером означала бы вдвое больше входов.
--
-- kind различает списки: у зданий и войск в провинции разные слоты,
-- и порядок в каждом свой. upgrade_id у войска — номер типа юнита.
ALTER TABLE supremacy_build_queue
    ADD COLUMN kind  TEXT NOT NULL DEFAULT 'building'
        CHECK (kind IN ('building', 'unit')),
    -- Имя картинки у игры («railway», «car»): страница показывает её,
    -- не заходя в партию.
    ADD COLUMN image TEXT NOT NULL DEFAULT '';

DROP INDEX supremacy_build_queue_order;
CREATE INDEX supremacy_build_queue_order
    ON supremacy_build_queue (game_id, kind, province_id, position);

-- +goose Down
DROP INDEX supremacy_build_queue_order;
DELETE FROM supremacy_build_queue WHERE kind = 'unit';
ALTER TABLE supremacy_build_queue
    DROP COLUMN image,
    DROP COLUMN kind;
CREATE INDEX supremacy_build_queue_order
    ON supremacy_build_queue (game_id, province_id, position);
