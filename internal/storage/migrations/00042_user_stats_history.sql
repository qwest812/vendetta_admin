-- +goose Up
-- История боевого счёта: снимок на игрока за день. Сам счёт в
-- supremacy_user_stats при каждом обновлении перезаписывается, и без
-- снимков не видно, как игрок рос: «за месяц +2 уровня, кд с 1.4 до 1.7».
-- Строка дня обновляется до его конца — в истории остаётся последнее
-- за день.
CREATE TABLE supremacy_user_stats_history (
    site_user_id   TEXT    NOT NULL,
    day            DATE    NOT NULL,
    level          INTEGER NOT NULL,
    defeated       BIGINT  NOT NULL,
    casualties     BIGINT  NOT NULL,
    games          INTEGER NOT NULL,
    wins           INTEGER NOT NULL,
    overall_score  BIGINT  NOT NULL,
    PRIMARY KEY (site_user_id, day)
);

-- Отсчёт — с того, что уже известно: иначе первую разницу пришлось бы
-- ждать до следующего обновления каждого игрока.
INSERT INTO supremacy_user_stats_history
    (site_user_id, day, level, defeated, casualties, games, wins, overall_score)
SELECT site_user_id, checked_at::date, level, defeated, casualties, games,
       solo_wins + coalition_wins, overall_score
  FROM supremacy_user_stats
 WHERE level > 0;

-- +goose Down
DROP TABLE supremacy_user_stats_history;
