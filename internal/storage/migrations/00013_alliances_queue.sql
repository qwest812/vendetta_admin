-- +goose Up
-- Кланы игроков спрашивает фоновый воркер, а не страница. Чтобы он знал,
-- о ком спрашивать, страница записывает встреченных игроков сюда с пустым
-- checked_at: NULL означает «увидели, но ещё не спрашивали». Так открытие
-- карты не ходит в сеть вовсе.
ALTER TABLE supremacy_user_alliances ALTER COLUMN checked_at DROP NOT NULL;
ALTER TABLE supremacy_user_alliances ALTER COLUMN checked_at DROP DEFAULT;

-- Воркер каждый тик ищет неспрошенных и протухших: сначала первые, потом
-- самые давние. NULLS FIRST в индексе повторяет порядок этого запроса.
CREATE INDEX supremacy_user_alliances_checked_at
    ON supremacy_user_alliances (checked_at NULLS FIRST);

-- +goose Down
DROP INDEX supremacy_user_alliances_checked_at;
ALTER TABLE supremacy_user_alliances ALTER COLUMN checked_at SET DEFAULT now();
UPDATE supremacy_user_alliances SET checked_at = now() WHERE checked_at IS NULL;
ALTER TABLE supremacy_user_alliances ALTER COLUMN checked_at SET NOT NULL;
