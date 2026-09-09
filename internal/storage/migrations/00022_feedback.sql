-- +goose Up
-- Обратная связь: обращение и переписка по нему. Пишет тот, кому выдан доступ
-- в админку, отвечает и закрывает админ. Отдельная переписка, а не заметка
-- на карточке: разговор идёт про саму админку, а не про игрока.
CREATE TABLE feedback_tickets (
    id             BIGSERIAL   PRIMARY KEY,
    -- Автора могут удалить, а обращение остаётся: имя сохраняется рядом,
    -- как у заметок на карточке игрока.
    author_id      BIGINT      REFERENCES users (id) ON DELETE SET NULL,
    author_name    TEXT        NOT NULL,
    subject        TEXT        NOT NULL,
    status         TEXT        NOT NULL DEFAULT 'open',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- updated_at двигает каждое сообщение: по нему список и сортируется,
    -- чтобы свежий разговор был сверху.
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- Когда автор в последний раз открывал переписку. По ней считается
    -- «есть ответ»: чужое сообщение новее этой отметки.
    author_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    closed_at      TIMESTAMPTZ,
    closed_by      BIGINT      REFERENCES users (id) ON DELETE SET NULL
);

CREATE INDEX feedback_tickets_author_idx ON feedback_tickets (author_id, updated_at DESC);
CREATE INDEX feedback_tickets_status_idx ON feedback_tickets (status, updated_at DESC);

CREATE TABLE feedback_messages (
    id          BIGSERIAL   PRIMARY KEY,
    ticket_id   BIGINT      NOT NULL REFERENCES feedback_tickets (id) ON DELETE CASCADE,
    author_id   BIGINT      REFERENCES users (id) ON DELETE SET NULL,
    author_name TEXT        NOT NULL,
    -- from_staff — ответ со стороны админки. Записывается на месте, а не
    -- вычисляется потом: роль автора со временем меняется, а кто отвечал
    -- на то сообщение — нет.
    from_staff  BOOLEAN     NOT NULL DEFAULT FALSE,
    body        TEXT        NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX feedback_messages_ticket_idx ON feedback_messages (ticket_id, created_at);

-- +goose Down
DROP TABLE feedback_messages;
DROP TABLE feedback_tickets;
