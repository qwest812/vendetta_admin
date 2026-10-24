-- +goose Up
-- Провинции по заходам сбора коалиций — сырьё для поиска мультиаккаунтов.
-- Обход и так получает полное состояние партии; здесь остаётся то, что
-- раньше выбрасывалось. Ни одного лишнего запроса к игре.
--
-- Только живые игроки с номером на сайте: передачи ботам и от ботов
-- о мультиаккаунтах ничего не говорят.

-- Последний известный владелец каждой провинции: с ним сравнивается
-- следующий заход. Кончилась партия — строки удаляются.
CREATE TABLE supremacy_province_owners (
    game_id      TEXT    NOT NULL,
    province_id  INTEGER NOT NULL,
    site_user_id TEXT    NOT NULL,
    PRIMARY KEY (game_id, province_id)
);

-- Сколько провинций у игрока на день партии: кривая роста. Два захода
-- в один день — остаётся последний.
CREATE TABLE supremacy_province_counts (
    game_id      TEXT        NOT NULL,
    day          INTEGER     NOT NULL,
    site_user_id TEXT        NOT NULL,
    provinces    INTEGER     NOT NULL,
    taken_at     TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (game_id, day, site_user_id)
);

-- Переходы провинций между игроками за партию. same_team — оба в одной
-- коалиции на момент захода: союзники друг друга не завоёвывают, и такой
-- переход — главный признак «кормёжки».
CREATE TABLE supremacy_province_transfers (
    game_id    TEXT    NOT NULL,
    from_site  TEXT    NOT NULL,
    to_site    TEXT    NOT NULL,
    same_team  BOOLEAN NOT NULL,
    provinces  INTEGER NOT NULL,
    first_day  INTEGER NOT NULL,
    last_day   INTEGER NOT NULL,
    PRIMARY KEY (game_id, from_site, to_site, same_team)
);
CREATE INDEX supremacy_province_transfers_from_idx ON supremacy_province_transfers (from_site);
CREATE INDEX supremacy_province_transfers_to_idx ON supremacy_province_transfers (to_site);

-- +goose Down
DROP TABLE supremacy_province_transfers;
DROP TABLE supremacy_province_counts;
DROP TABLE supremacy_province_owners;
