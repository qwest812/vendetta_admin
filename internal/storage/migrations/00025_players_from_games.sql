-- +goose Up
-- Карточка заводится теперь на каждого, кого встретили в партии, поэтому
-- опознавать человека должен игровой ID, а не ник. ID у аккаунта один
-- и навсегда, ник же меняют, и один и тот же ник со временем встречается
-- у разных людей: под уникальностью ника импорт спотыкался бы о чужую
-- старую карточку.
--
-- Уникальность ID остаётся (players_game_id_key), а ник просто индексируется
-- для поиска: точное совпадение обслуживает btree, подстроку — триграммы.
DROP INDEX players_nickname_key;
CREATE INDEX players_nickname_idx ON players (lower(nickname));

-- Задним числом: все, кого уже встречали в партиях, получают карточку.
-- Автор пуст — их завела не рука, а заход в партию. Безымянных пропускаем:
-- карточка без ника нечитаема, а ник сайт отдаёт не всегда.
INSERT INTO players (game_id, nickname)
SELECT s.site_user_id, s.nickname
  FROM supremacy_players s
 WHERE s.nickname <> ''
ON CONFLICT (lower(game_id)) DO NOTHING;

-- +goose Down
DELETE FROM players p
 WHERE p.created_by IS NULL
   AND p.game_id IS NOT NULL
   AND EXISTS (SELECT 1 FROM supremacy_players s WHERE lower(s.site_user_id) = lower(p.game_id))
   AND NOT EXISTS (SELECT 1 FROM player_notes n WHERE n.player_id = p.id)
   AND NOT EXISTS (SELECT 1 FROM player_traits t WHERE t.player_id = p.id);

CREATE UNIQUE INDEX players_nickname_key ON players (lower(nickname));
DROP INDEX players_nickname_idx;
