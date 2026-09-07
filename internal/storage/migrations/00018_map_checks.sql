-- +goose Up
-- Сколько раз в сутки человек может посмотреть карту партии. Право личное
-- и живёт на пользователе, как и доступ к разделу «Игры»: меняет его рут,
-- каждому своё. Пятёрка по умолчанию — та же, с какой заводят новых.
-- Ноль означает запрет: карту такой человек не увидит вовсе.
ALTER TABLE users ADD COLUMN map_checks INT NOT NULL DEFAULT 5;

-- Израсходованное за день. Строка на пару «человек + день»: счёт обнуляется
-- сменой даты сам, без уборщика, а сама уборка нужна лишь затем, чтобы
-- таблица не росла вечно. В памяти это держать нельзя — перезапуск админки
-- раздал бы всем новые проверки.
CREATE TABLE user_map_checks (
    user_id BIGINT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    -- День считает приложение по своим часам, а не база: сутки должны
    -- кончаться там же, где кончаются они у людей, которые этим пользуются.
    day  DATE NOT NULL,
    used INT  NOT NULL DEFAULT 0,
    PRIMARY KEY (user_id, day)
);

CREATE INDEX user_map_checks_day_idx ON user_map_checks (day);

-- +goose Down
DROP TABLE user_map_checks;
ALTER TABLE users DROP COLUMN map_checks;
