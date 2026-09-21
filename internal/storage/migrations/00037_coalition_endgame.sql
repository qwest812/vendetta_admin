-- +goose Up
-- Коалиции игроки собирают под конец партии, а конец приходится на смену
-- дня: только в неё игра пересчитывает очки и объявляет победу. Поэтому
-- обход больше не ограничивается одним-четырьмя заходами посреди партии,
-- а следит за счётом и в эндшпиле приходит к каждой смене дня.
--
-- urgent — следующий заход срочный (перед сменой дня), такие обход берёт
-- вне общей очереди. fails — неудачи подряд: бросаем партию по ним,
-- а не по общему числу заходов, которых теперь бывает много.
ALTER TABLE supremacy_watched_games
    ADD COLUMN urgent BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN fails  INT     NOT NULL DEFAULT 0;

CREATE INDEX supremacy_watched_games_urgent
    ON supremacy_watched_games (next_check_at) WHERE urgent AND NOT done;

-- Скоростные партии прежнее правило отпускало после первого же захода —
-- посреди партии, до того как коалиции сложились. Недавние возвращаем
-- в обход: идущая дойдёт до эндшпиля, кончившаяся закроется первым же
-- заходом, игра сама скажет, что партия окончена.
UPDATE supremacy_watched_games
   SET done = false, next_check_at = now()
 WHERE done AND last_error = '' AND checked_at > now() - interval '5 days';

-- +goose Down
DROP INDEX supremacy_watched_games_urgent;
ALTER TABLE supremacy_watched_games
    DROP COLUMN fails,
    DROP COLUMN urgent;
