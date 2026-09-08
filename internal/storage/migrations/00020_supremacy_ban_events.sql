-- +goose Up
-- Журнал банов: строка на каждую замеченную смену статуса аккаунта.
-- Сама supremacy_players держит только сегодняшнее состояние — снятый бан
-- стирает там и флаг, и дату, и о том, что человек однажды сидел, не
-- остаётся ничего. Здесь остаётся.
--
-- Время в строке — когда мы бан заметили, а не когда его выдали: игра
-- сообщает только текущее состояние, и узнаём мы о нём тогда, когда
-- заходим в партию с этим человеком.
CREATE TABLE supremacy_ban_events (
    id           BIGSERIAL PRIMARY KEY,
    site_user_id TEXT NOT NULL,
    -- Каким статус стал: true — забанили, false — бан сняли.
    banned       BOOLEAN NOT NULL,
    noticed_at   TIMESTAMPTZ NOT NULL,
    -- В какой партии заметили. Пусто — заход был без номера партии.
    game_id      TEXT NOT NULL DEFAULT ''
);

-- Журнал читается всегда про одного человека и всегда свежим вперёд.
CREATE INDEX supremacy_ban_events_player
    ON supremacy_ban_events (site_user_id, noticed_at DESC);

-- То, что уже знаем, переносим: у забаненных есть дата первой встречи
-- с баном, и терять её при переезде обидно. Про снятые баны прошлого
-- сказать нечего — их и не было где хранить.
INSERT INTO supremacy_ban_events (site_user_id, banned, noticed_at, game_id)
SELECT site_user_id, true, COALESCE(banned_at, seen_at), seen_game_id
  FROM supremacy_players WHERE banned;

-- +goose Down
DROP TABLE supremacy_ban_events;
