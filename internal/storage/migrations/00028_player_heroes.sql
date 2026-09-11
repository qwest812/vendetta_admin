-- +goose Up
-- Кто из героев есть у игрока и какого они уровня. Отметка личная, как
-- у признаков: видят героя в партии разные люди и в разное время, а уровень
-- со временем растёт — общая запись затирала бы одно наблюдение другим.
--
-- Нулевого уровня здесь не бывает: «героя нет» — это отсутствие строки,
-- и радиокнопка «0» в карточке её удаляет. Хранить нули значило бы держать
-- по два десятка пустых строк на каждого, кто хоть раз открыл форму.
--
-- Герой опознаётся номером типа юнита — тем же, которым он приходит
-- в составе армии и лежит в справочнике (internal/supremacy/heroes).
-- Внешнего ключа на справочник нет: он живёт файлом и обновляется кнопкой,
-- а отметка о герое, которого в игре переименовали, остаётся верной.
CREATE TABLE player_heroes (
    player_id    BIGINT      NOT NULL REFERENCES players (id) ON DELETE CASCADE,
    unit_type_id INTEGER     NOT NULL,
    user_id      BIGINT      NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    level        INTEGER     NOT NULL CHECK (level > 0),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (player_id, unit_type_id, user_id)
);

CREATE INDEX player_heroes_player_idx ON player_heroes (player_id);

-- +goose Down
DROP TABLE player_heroes;
