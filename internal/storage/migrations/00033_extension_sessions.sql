-- +goose Up
-- Расширение Chrome входит по почте и паролю и получает свой токен. Токен —
-- та же сессия, только другого рода: так блокировка, смена пароля и роли
-- обрывают его ровно там же, где обрывают браузер (DeleteByUser), а уборщик
-- протухших и блок «кто сейчас в системе» о нём знают без отдельного кода.
--
-- Род проверяется при каждом чтении: токен расширения не годится как кука
-- сайта, а кука — как токен. Иначе утёкший из расширения токен открывал бы
-- формы сайта в обход CSRF, а кука — API в обход проверки происхождения.
ALTER TABLE sessions ADD COLUMN kind TEXT NOT NULL DEFAULT 'browser'
    CHECK (kind IN ('browser', 'extension'));

-- Откуда пришла попытка входа: с формы сайта или из расширения. В журнале
-- это разные дороги, и путать их при разборе нельзя.
ALTER TABLE login_events ADD COLUMN via TEXT NOT NULL DEFAULT 'browser'
    CHECK (via IN ('browser', 'extension'));

-- +goose Down
ALTER TABLE login_events DROP COLUMN via;
ALTER TABLE sessions DROP COLUMN kind;
