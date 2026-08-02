-- +goose Up
CREATE EXTENSION IF NOT EXISTS pg_trgm;

CREATE TABLE users (
    id            BIGSERIAL   PRIMARY KEY,
    email         TEXT        NOT NULL,
    password_hash TEXT        NOT NULL,
    role          TEXT        NOT NULL CHECK (role IN ('root', 'admin', 'user')),
    is_active     BOOLEAN     NOT NULL DEFAULT TRUE,
    created_by    BIGINT      REFERENCES users (id) ON DELETE SET NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX users_email_key ON users (lower(email));

-- Рут в системе ровно один.
CREATE UNIQUE INDEX users_single_root ON users ((role)) WHERE role = 'root';

CREATE TABLE sessions (
    token_hash BYTEA       PRIMARY KEY,
    user_id    BIGINT      NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    csrf_token TEXT        NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX sessions_user_id_idx ON sessions (user_id);
CREATE INDEX sessions_expires_at_idx ON sessions (expires_at);

CREATE TABLE audit_log (
    id          BIGSERIAL   PRIMARY KEY,
    actor_id    BIGINT      REFERENCES users (id) ON DELETE SET NULL,
    actor_email TEXT        NOT NULL,
    action      TEXT        NOT NULL,
    target_type TEXT        NOT NULL,
    target_id   TEXT        NOT NULL,
    payload     JSONB       NOT NULL DEFAULT '{}'::jsonb,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX audit_log_created_at_idx ON audit_log (created_at DESC);
CREATE INDEX audit_log_target_idx ON audit_log (target_type, target_id);

-- +goose Down
DROP TABLE audit_log;
DROP TABLE sessions;
DROP TABLE users;
