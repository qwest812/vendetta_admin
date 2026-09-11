-- +goose Up
-- Справочник героев Supremacy. Лежит одним снимком, а не разложен
-- по таблицам: читается он всегда целиком, пишется тоже целиком, и своя
-- таблица под каждое поле только мешала бы обновлению.
--
-- Строка здесь ровно одна — снимок общий на всю установку. Пустая таблица
-- не беда: тогда админка берёт справочник, вшитый в образ.
CREATE TABLE supremacy_heroes (
    id           BOOLEAN     PRIMARY KEY DEFAULT TRUE CHECK (id),
    snapshot     JSONB       NOT NULL,
    -- collected_at — когда снимок собран у игры, updated_at — когда он
    -- лёг к нам. Разные вещи: собранный снимок могли положить не сразу.
    collected_at TIMESTAMPTZ NOT NULL,
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_by   BIGINT      REFERENCES users (id) ON DELETE SET NULL
);

-- Портреты героев. Тоже в базе, а не файлами: обновить образ ради нового
-- героя нельзя, а показывать его без лица не хочется. Вшитые картинки
-- остаются запасным вариантом для тех, кого здесь ещё нет.
CREATE TABLE supremacy_hero_images (
    name       TEXT        PRIMARY KEY,
    data       BYTEA       NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE supremacy_hero_images;
DROP TABLE supremacy_heroes;
