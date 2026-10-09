-- +goose Up
-- Бывшие ники игроков. Карточка заводится один раз, а ник в игре меняют,
-- и раньше карточка так и оставалась со старым — или с подменным именем
-- из анонимной партии. Теперь ник карточки следует за игрой, а прежний
-- ложится сюда: «бывший такой-то» бывает важнее нынешнего, и по нему
-- же работает поиск.
CREATE TABLE player_nicknames (
    player_id   BIGINT      NOT NULL REFERENCES players(id) ON DELETE CASCADE,
    nickname    TEXT        NOT NULL,
    replaced_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Один и тот же ник в истории игрока — одна строка: туда-обратно его
-- меняют, а повтор ничего не добавляет.
CREATE UNIQUE INDEX player_nicknames_key ON player_nicknames (player_id, lower(nickname));
CREATE INDEX player_nicknames_trgm ON player_nicknames USING gin (nickname gin_trgm_ops);

-- Разовая починка: все карточки с игровым ID — в очередь воркера кланов,
-- он спросит у сайта нынешний ник. Тех, кого уже знаем, перепроверяем
-- заново: ник в их карточках мог устареть или прийти из анонимной партии.
--
-- Обнулять время проверки нельзя: пустое время значит «клан неизвестен»,
-- и карты посерели бы до конца обхода. Давняя дата держит клан известным,
-- а в очереди ставит его первым.
INSERT INTO supremacy_user_alliances (site_user_id)
SELECT DISTINCT p.game_id FROM players p WHERE coalesce(p.game_id, '') <> ''
ON CONFLICT (site_user_id) DO NOTHING;

UPDATE supremacy_user_alliances SET checked_at = '2000-01-01' WHERE checked_at IS NOT NULL;
UPDATE supremacy_alliances SET checked_at = '2000-01-01' WHERE checked_at IS NOT NULL;

-- +goose Down
DROP TABLE player_nicknames;
