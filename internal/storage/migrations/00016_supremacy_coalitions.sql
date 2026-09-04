-- +goose Up
-- Архив коалиций: кто с кем состоял в союзе в прошлых партиях. Собирается
-- фоновым обходом, а нужен ради одного вопроса — «эти двое уже играли
-- вместе?». Ответ на него виден на карте партии до того, как союз сложится
-- заново.
--
-- Данные добываются заходом наблюдателем, и момент съёма выбран нарочно
-- посреди партии, а не в конце: к финалу коалиции распускают, и вместе
-- с ними исчезает состав — остаются одни названия. Проверено на живой
-- завершённой партии: три коалиции, все распущены, ни одного участника.

-- Партии, за которыми следим. Заводятся из лобби, закрываются по расписанию.
CREATE TABLE supremacy_watched_games (
    game_id     TEXT PRIMARY KEY,
    title       TEXT NOT NULL DEFAULT '',
    language    TEXT NOT NULL DEFAULT '',
    -- speed — во сколько раз партия быстрее обычной: 1, 4, 10. Игра называет
    -- обратную величину (timeScale), но группировать удобнее по скорости:
    -- союз в скоростной партии и союз в двухмесячной стоят разного.
    speed       REAL NOT NULL DEFAULT 1,
    started_at  TIMESTAMPTZ,
    state       TEXT NOT NULL DEFAULT '',
    day_of_game INT NOT NULL DEFAULT 0,
    -- Расписание живёт в строке, а не только в коде: у быстрых и медленных
    -- партий оно разное, и сдвинуть срок одной партии иногда нужно руками.
    next_check_at TIMESTAMPTZ NOT NULL,
    checked_at    TIMESTAMPTZ,
    checks        INT NOT NULL DEFAULT 0,
    -- last_error — чем кончился последний заход, если не удался. Пусто —
    -- значит всё прошло хорошо.
    last_error  TEXT NOT NULL DEFAULT '',
    -- done — партию больше не трогаем: своё она уже отдала.
    done        BOOLEAN NOT NULL DEFAULT false,
    first_seen  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Обход спрашивает ровно одно: кому пора. Частичный индекс под этот вопрос
-- и сделан — завершённых партий со временем станет несравнимо больше живых.
CREATE INDEX supremacy_watched_games_due
    ON supremacy_watched_games (next_check_at) WHERE NOT done;

-- Коалиции, которые мы застали живыми. Распущенные не пишем: состава
-- у них уже нет, а название без людей ничего не говорит.
CREATE TABLE supremacy_coalitions (
    game_id    TEXT NOT NULL,
    team_id    INT  NOT NULL,
    name       TEXT NOT NULL DEFAULT '',
    first_seen TIMESTAMPTZ NOT NULL,
    last_seen  TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (game_id, team_id)
);

-- Кто в коалиции состоял. Хранится интервалом, а не снимком: увидели того
-- же человека снова — сдвинули last_seen. Снимки росли бы линейно по времени
-- и отвечали бы хуже.
--
-- Ников и стран здесь по одной причине: показать пару людьми, а не номерами,
-- когда карточки в базе для них ещё нет.
CREATE TABLE supremacy_coalition_members (
    game_id      TEXT NOT NULL,
    team_id      INT  NOT NULL,
    site_user_id TEXT NOT NULL,
    login        TEXT NOT NULL DEFAULT '',
    nation       TEXT NOT NULL DEFAULT '',
    first_seen   TIMESTAMPTZ NOT NULL,
    last_seen    TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (game_id, team_id, site_user_id)
);

-- Пары «кто с кем» нигде не хранятся: это self-join по (game_id, team_id),
-- и вход в него — список участников открытой партии.
CREATE INDEX supremacy_coalition_members_user
    ON supremacy_coalition_members (site_user_id);

-- Общие переключатели админки. Заводится ради одного — рут должен уметь
-- остановить обход, — но ключ строкой затем, чтобы следующий такой
-- переключатель не требовал ещё одной миграции.
CREATE TABLE app_settings (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_by BIGINT REFERENCES users (id) ON DELETE SET NULL
);

-- +goose Down
DROP TABLE app_settings;
DROP TABLE supremacy_coalition_members;
DROP TABLE supremacy_coalitions;
DROP TABLE supremacy_watched_games;
