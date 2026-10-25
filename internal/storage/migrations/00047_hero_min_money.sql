-- +goose Up
-- Порог денег для Мейв: призыв тратит деньги и рыбу, и за ночь автопилот
-- выедал всю казну. Не призываем, если денег меньше порога. По умолчанию
-- 20 000 — и для партий, где автопризыв уже включён.
ALTER TABLE supremacy_game_tasks ADD COLUMN hero_min_money INTEGER NOT NULL DEFAULT 20000;

-- +goose Down
ALTER TABLE supremacy_game_tasks DROP COLUMN hero_min_money;
