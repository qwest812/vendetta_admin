-- +goose Up
ALTER TABLE users ADD COLUMN nickname TEXT;

-- Ник обязателен, поэтому существующим строкам берём локальную часть почты,
-- а совпавшие ники разводим суффиксом с id.
UPDATE users SET nickname = split_part(email, '@', 1);
UPDATE users u SET nickname = u.nickname || '_' || u.id
 WHERE EXISTS (
     SELECT 1 FROM users o WHERE o.id <> u.id AND lower(o.nickname) = lower(u.nickname)
 );

ALTER TABLE users ALTER COLUMN nickname SET NOT NULL;
CREATE UNIQUE INDEX users_nickname_key ON users (lower(nickname));

-- Почта больше не обязательна. Пустую храним как NULL: уникальный индекс
-- по lower(email) считает NULL'ы различными, а пустые строки — дубликатами.
ALTER TABLE users ALTER COLUMN email DROP NOT NULL;

-- +goose Down
-- Возвращаем обязательную почту: безадресным пользователям выдаём
-- технический адрес, иначе NOT NULL не встанет.
UPDATE users SET email = 'user' || id || '@local' WHERE email IS NULL;
ALTER TABLE users ALTER COLUMN email SET NOT NULL;

DROP INDEX users_nickname_key;
ALTER TABLE users DROP COLUMN nickname;
