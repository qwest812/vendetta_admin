-- +goose Up
-- Признак теперь ставит человек, а не «кто-то»: у отметки появляется автор,
-- и в карточке напротив признака видно, сколько людей его отметили. Раньше
-- отметка была одна на всех, и второй заметивший то же самое ничего
-- к ней не добавлял.
ALTER TABLE player_traits ADD COLUMN user_id BIGINT REFERENCES users (id) ON DELETE CASCADE;

-- Прежним отметкам автора ищем в журнале: там записано, кто их ставил.
-- Не нашлось — оставляем на руте: отметка существует, и потерять её
-- было бы хуже, чем приписать не тому.
UPDATE player_traits pt
   SET user_id = COALESCE(
       (SELECT a.actor_id
          FROM audit_log a
          JOIN traits t ON t.id = pt.trait_id
         WHERE a.action = 'player.trait.add'
           AND a.target_id = pt.player_id::text
           AND a.payload ->> 'trait' = t.name
           AND a.actor_id IS NOT NULL
         ORDER BY a.created_at DESC
         LIMIT 1),
       (SELECT id FROM users WHERE role = 'root' LIMIT 1));

DELETE FROM player_traits WHERE user_id IS NULL;
ALTER TABLE player_traits ALTER COLUMN user_id SET NOT NULL;

-- Ключ теперь тройной: один и тот же признак ставят разные люди.
ALTER TABLE player_traits DROP CONSTRAINT player_traits_pkey;
ALTER TABLE player_traits ADD PRIMARY KEY (player_id, trait_id, user_id);

-- Заметки становятся комментариями: у человека на игрока он один, живёт
-- в ленте свежими сверху и подписи не показывает — авторов видят только
-- админы. Отдельный id, а не пара «игрок + автор» ключом, затем, что
-- комментарий переживает удаление того, кто его написал: сказанное о людях
-- не должно исчезать вместе с учётной записью.
CREATE TABLE player_comments (
    id         BIGSERIAL   PRIMARY KEY,
    player_id  BIGINT      NOT NULL REFERENCES players (id) ON DELETE CASCADE,
    author_id  BIGINT      REFERENCES users (id) ON DELETE SET NULL,
    body       TEXT        NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Один комментарий на человека — ровно то правило, о котором предупреждает
-- форма. На осиротевшие записи оно не распространяется: у них автора нет.
CREATE UNIQUE INDEX player_comments_author_key ON player_comments (player_id, author_id)
    WHERE author_id IS NOT NULL;
CREATE INDEX player_comments_player_idx ON player_comments (player_id, updated_at DESC);

-- Переезд: от каждого автора остаётся самая свежая заметка, обрезанная
-- до новой длины. Заметки без автора теряются — их некому приписать,
-- а анонимных комментариев с неизвестной датой правки в ленте не бывает.
INSERT INTO player_comments (player_id, author_id, body, created_at, updated_at)
SELECT DISTINCT ON (player_id, author_id)
       player_id, author_id, left(body, 500), created_at, created_at
  FROM player_notes
 WHERE author_id IS NOT NULL
 ORDER BY player_id, author_id, created_at DESC;

DROP TABLE player_notes;

-- +goose Down
CREATE TABLE player_notes (
    id           BIGSERIAL   PRIMARY KEY,
    player_id    BIGINT      NOT NULL REFERENCES players (id) ON DELETE CASCADE,
    author_id    BIGINT      REFERENCES users (id) ON DELETE SET NULL,
    author_email TEXT        NOT NULL,
    body         TEXT        NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX player_notes_player_idx ON player_notes (player_id, created_at DESC);

INSERT INTO player_notes (player_id, author_id, author_email, body, created_at)
SELECT c.player_id, c.author_id, COALESCE(u.email, ''), c.body, c.created_at
  FROM player_comments c
  LEFT JOIN users u ON u.id = c.author_id;

DROP TABLE player_comments;

-- Обратно к общей отметке: от нескольких авторов остаётся одна строка.
DELETE FROM player_traits pt
 WHERE pt.ctid <> (SELECT min(o.ctid) FROM player_traits o
                    WHERE o.player_id = pt.player_id AND o.trait_id = pt.trait_id);
ALTER TABLE player_traits DROP CONSTRAINT player_traits_pkey;
ALTER TABLE player_traits DROP COLUMN user_id;
ALTER TABLE player_traits ADD PRIMARY KEY (player_id, trait_id);
