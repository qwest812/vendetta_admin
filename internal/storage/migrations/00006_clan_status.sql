-- +goose Up
-- Статус живёт у клана, а не у игрока: альянс помечается один раз, и метку
-- получают все его карточки сразу. Заведённые раньше кланы становятся
-- нейтральными — это же значение и у всех новых.
ALTER TABLE clans ADD COLUMN status TEXT NOT NULL DEFAULT 'neutral'
    CHECK (status IN ('ally', 'neutral', 'enemy'));

-- Поиск умеет фильтровать по статусу; нейтральных большинство, поэтому в
-- индексе от них толку нет.
CREATE INDEX clans_status_idx ON clans (status) WHERE status <> 'neutral';

-- +goose Down
DROP INDEX clans_status_idx;
ALTER TABLE clans DROP COLUMN status;
