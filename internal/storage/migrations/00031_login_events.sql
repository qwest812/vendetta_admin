-- +goose Up
-- Журнал входов: кто, откуда и получилось ли. Отдельно от audit_log —
-- тот про правки данных, и неудачные пароли утопили бы его в шуме.
CREATE TABLE login_events (
    id         BIGSERIAL   PRIMARY KEY,
    -- Пусто, когда такого логина в базе нет. Попытку всё равно
    -- записываем: по ней и видно, что чей-то ник перебирают, а сам
    -- набранный логин остаётся в login.
    user_id    BIGINT      REFERENCES users (id) ON DELETE SET NULL,
    login      TEXT        NOT NULL,
    ip         INET        NOT NULL,
    -- Подсеть, до которой свёрнут адрес. Считает её приложение, а не
    -- база: маска — это правило о том, что мы считаем «тем же местом»,
    -- и жить оно должно там, где проверяется тестом. См. domain.LoginPlace.
    subnet     INET        NOT NULL,
    user_agent TEXT        NOT NULL,
    ok         BOOLEAN     NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX login_events_created_at_idx ON login_events (created_at DESC);
CREATE INDEX login_events_user_idx ON login_events (user_id, created_at DESC);
CREATE INDEX login_events_ip_idx ON login_events (ip);

-- Адрес живой сессии. По нему видно не «когда входили», а «откуда сидят
-- сейчас»: две сессии одного человека с разных подсетей в один момент —
-- самое внятное, что вообще можно сказать про раздачу доступа.
-- Пусто у сессий, заведённых до этой миграции.
ALTER TABLE sessions ADD COLUMN ip INET;

-- +goose Down
ALTER TABLE sessions DROP COLUMN ip;
DROP TABLE login_events;
