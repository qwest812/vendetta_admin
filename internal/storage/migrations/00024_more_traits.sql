-- +goose Up
-- Признаки, за которыми и приходят в поиск. Три новых и одно переименование:
-- «Мультиаккаунт» в разговоре зовут мультиводом, и в списке галочек должно
-- стоять то слово, которым люди пользуются.
--
-- Код при переименовании не трогаем: он опознаёт признак в адресе фильтра
-- и в журнале, и менять его значило бы обесценить старые ссылки и записи.
UPDATE traits SET name = 'Мультивод' WHERE code = 'multiaccount';

-- Знак «использует ускорялки» — нейтральный: это про способ игры, а не про
-- порядочность, и рядом с «донатером» ему место. Рут поменяет знак одним
-- выбором в «Настройках», если решит иначе.
INSERT INTO traits (code, name, kind, sort_order) VALUES
    ('lies',                 'Врёт',                 'bad',     22),
    ('kicks_from_coalition', 'Выгоняет из коалиции', 'bad',     24),
    ('uses_boosters',        'Использует ускорялки', 'neutral', 70)
ON CONFLICT (code) DO NOTHING;

-- +goose Down
-- Откат уносит и отметки у игроков: player_traits связан по ON DELETE CASCADE.
DELETE FROM traits WHERE code IN ('lies', 'kicks_from_coalition', 'uses_boosters');
UPDATE traits SET name = 'Мультиаккаунт' WHERE code = 'multiaccount';
