-- +goose Up
-- Знак признака переезжает из веса в собственное поле. Шкалы риска
-- и лояльности убраны: искать стали по конкретному поведению, а не по общей
-- оценке. Но цвет метки нужен по-прежнему — плохое, нейтральное и хорошее
-- должны отличаться взглядом, и раньше это решал знак веса.
--
-- Сам weight остаётся лежать нетронутым. По нему считался процент, и когда
-- процент вернётся в новом виде, начинать будет от чего.
ALTER TABLE traits ADD COLUMN kind TEXT NOT NULL DEFAULT 'neutral'
    CHECK (kind IN ('bad', 'neutral', 'good'));

UPDATE traits SET kind = CASE
    WHEN weight < 0 THEN 'bad'
    WHEN weight > 0 THEN 'good'
    ELSE 'neutral'
END;

-- +goose Down
ALTER TABLE traits DROP COLUMN kind;
