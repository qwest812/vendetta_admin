// Всё, что расширение делает в открытой партии, из боковой панели. Панель
// не закрывается, пока человек играет, поэтому все кнопки здесь: анализ
// карты, режимы, возврат цветов игры, легенда, отношения с ботами,
// здоровье выбранного стека, сравнение сил двух стеков и здания игроков.
//
// Работает с той вкладкой, что открыта в окне сейчас. Скрипт расширения
// на странице игры (game-content.js) отдаёт состав партии и красит карту;
// цвета всех шести режимов панель берёт у админки через фон — одним
// ответом, поэтому режим переключается без нового запроса.
//
// К админке панель идёт только по «Анализировать карту»: сама по себе
// она ничего не запрашивает и карту не трогает.

"use strict";

const mapPanel = (() => {
    // Подписи режимов до первого анализа: названия потом придут от админки
    // на языке аккаунта.
    const MODES = [
        ["clans", "Альянсы"],
        ["sides", "Мои списки"],
        ["teams", "Коалиции"],
        ["players", "Игроки"],
        ["power", "Сила"],
        ["top", "Топ альянсов"],
    ];

    // Как часто проверять, не открылась ли партия во вкладке, пока её нет.
    const WAIT_MS = 3000;

    const $ = (id) => document.getElementById(id);

    // Мирная часть шкалы отношений игры: значение, которое уходит
    // в действие, и как оно называется. Война и перемирие пачкой отсюда
    // не раздаются — такую кнопку легко нажать не подумав.
    const RELATIONS = [
        [1, "Мир"],
        [2, "Пакт о ненападении"],
        [3, "Право прохода"],
        [4, "Общая карта"],
        [5, "Взаимная защита"],
        [6, "Общая разведка"],
    ];

    let running = false;
    let mode = "clans";
    let tabId = null;
    let ready = false;
    let busy = false;
    let timer = null;
    let relation = 3;      // что выдаём ботам
    let relating = false;  // идёт выдача
    let buildingsList = null; // последний подсчёт зданий: игроки партии
    let buildingsOf = null;   // чей список показан
    // Анализ на вкладку: у каждой открытой партии свои цвета.
    const analysed = new Map(); // номер вкладки → режимы от админки

    function status(text, isError) {
        const el = $("map-status");
        el.textContent = text || "";
        el.hidden = !text;
        el.classList.toggle("error", Boolean(isError));
        el.classList.toggle("muted", !isError);
    }

    function renderModes() {
        const modes = analysed.get(tabId);
        const box = $("map-modes");
        box.textContent = "";
        for (const [key, title] of MODES) {
            const m = modes && modes.find((x) => x.key === key);
            const b = document.createElement("button");
            b.type = "button";
            b.className = "ghost" + (key === mode ? " active" : "");
            b.textContent = m ? m.title : title;
            // Пояснение к режиму — то же, что под картой в админке.
            b.title = m ? m.note || "" : "";
            b.disabled = !ready;
            b.addEventListener("click", () => choose(key));
            box.append(b);
        }
    }

    function renderLegend(rows) {
        const list = $("map-legend");
        list.textContent = "";
        for (const row of rows || []) {
            const li = document.createElement("li");
            const sw = document.createElement("span");
            sw.className = "sw";
            sw.style.background = row.color;
            const label = document.createElement("span");
            label.className = "label";
            label.textContent = row.label;
            label.title = row.label;
            const n = document.createElement("span");
            n.className = "n";
            n.textContent = row.count;
            li.append(sw, label, n);
            list.append(li);
        }
    }

    // send — запрос скрипту расширения на странице игры. Ответит только
    // фрейм с загруженной партией; нет такого — null.
    async function send(msg) {
        if (tabId == null) return null;
        try {
            return await chrome.tabs.sendMessage(tabId, { to: "game", ...msg });
        } catch {
            return null;
        }
    }

    // check смотрит, есть ли партия в открытой вкладке, и приводит панель
    // в соответствие.
    async function check() {
        if (!running) return;
        const [tab] = await chrome.tabs.query({ active: true, currentWindow: true });
        const changed = (tab ? tab.id : null) !== tabId;
        tabId = tab ? tab.id : null;
        const pong = await send({ type: "ping" });
        const wasReady = ready;
        ready = Boolean(pong && pong.ready);

        $("map-analyse").disabled = !ready || busy;
        $("map-analyse").textContent = analysed.has(tabId) ? "🔍 Обновить анализ" : "🔍 Анализировать карту";
        $("map-reset").hidden = !(ready && pong.painted);
        renderModes();
        // Отношения перечитываем только когда состояние партии
        // изменилось: пока панель просто висит открытой, дёргать клиент
        // игры незачем.
        if (changed || ready !== wasReady) showBots();
        $("army-read").disabled = !ready;
        $("buildings-read").disabled = !ready;
        $("fight-attack").disabled = !ready;
        $("fight-defense").disabled = !ready;
        // Стек и здания другой вкладки к этой не относятся.
        if (changed || !ready) {
            clearArmy();
            clearBuildings();
            clearFight();
        }
        // Выбранные для сравнения стеки помнит страница игры — вернулись
        // на её вкладку, показываем их снова.
        if (ready && (changed || !wasReady) && !$("fight").hidden) showFight();

        if (!ready) {
            renderLegend(null);
            status("Откройте партию на supremacy1914 (любой домен: .com, .pl, .de…) и дождитесь, пока загрузится карта.");
        } else if (changed || !wasReady) {
            const modes = analysed.get(tabId);
            const m = modes && modes.find((x) => x.key === mode);
            if (m && pong.painted) {
                status(m.status || "");
                renderLegend(m.legend);
            } else {
                renderLegend(null);
                status("Нажмите «Анализировать карту» — расширение спросит админку и перекрасит карту выбранным режимом.");
            }
        }
    }

    // paint красит карту выбранным режимом из уже полученных цветов.
    async function paint() {
        const modes = analysed.get(tabId);
        const m = modes && modes.find((x) => x.key === mode);
        renderModes();
        if (!m) return;
        const res = await send({ type: "paint", colors: m.colors });
        if (!res || res.error) {
            status(res ? res.error : "Вкладка с игрой не ответила — перезагрузите её.", true);
            return;
        }
        $("map-reset").hidden = false;
        status(m.status || "");
        renderLegend(m.legend);
    }

    async function analyse() {
        if (busy || !ready) return;
        busy = true;
        $("map-analyse").disabled = true;
        status("Анализируем карту…");
        renderLegend(null);
        try {
            const roster = await send({ type: "roster" });
            if (!roster || roster.error) {
                status(roster ? roster.error : "Вкладка с игрой не ответила — перезагрузите её.", true);
                return;
            }
            const { me, players, teams } = roster.roster;
            if (!me.site || players.length === 0) {
                status("Не удалось прочитать состав партии.", true);
                return;
            }
            const res = await chrome.runtime.sendMessage({ type: "map", me: me.site, players, teams });
            if (!res || res.error) {
                status(res ? res.error : "Расширение не ответило.", true);
                return;
            }
            analysed.set(tabId, res.data.modes);
            $("map-analyse").textContent = "🔍 Обновить анализ";
            await paint();
        } finally {
            busy = false;
            $("map-analyse").disabled = !ready;
        }
    }

    async function choose(key) {
        mode = key;
        await chrome.storage.local.set({ paintMode: key });
        if (analysed.has(tabId)) {
            await paint();
        } else {
            renderModes();
        }
    }

    async function reset() {
        await send({ type: "clear" });
        $("map-reset").hidden = true;
        renderLegend(null);
        status("Цвета игры возвращены. Выберите режим, чтобы перекрасить снова.");
    }

    // --- отношения с ботами ---

    function relationName(value) {
        const row = RELATIONS.find(([v]) => v === value);
        return row ? row[1] : `отношение ${value}`;
    }

    function fillRelations() {
        const select = $("diplomacy-relation");
        select.textContent = "";
        for (const [value, title] of RELATIONS) {
            const option = document.createElement("option");
            option.value = String(value);
            option.textContent = title;
            option.selected = value === relation;
            select.append(option);
        }
    }

    function diploStatus(text, isError) {
        const el = $("diplomacy-status");
        el.textContent = text || "";
        el.classList.toggle("error", Boolean(isError));
        el.classList.toggle("muted", !isError);
    }

    // botLine — сколько в партии ботов, как с ними сейчас и кто они.
    // Считается по нынешним отношениям: по этой же строке потом и видно,
    // что игра выдачу приняла. Названия — чтобы до нажатия было видно,
    // кого кнопка заденет.
    function botLine(bots) {
        const counts = new Map();
        for (const b of bots) counts.set(b.relation, (counts.get(b.relation) || 0) + 1);
        const parts = [...counts.entries()]
            .sort((a, b) => a[0] - b[0])
            .map(([value, n]) => `${relationName(value).toLowerCase()} — ${n}`);
        const names = bots.map((b) => b.name || b.id).join(", ");
        return `Ботов в партии: ${bots.length} (${parts.join(", ")}). Это: ${names}.`;
    }

    // showBots перечитывает отношения у клиента игры и приводит блок
    // в соответствие. Без партии кнопка гаснет: выдавать некому.
    async function showBots() {
        const select = $("diplomacy-relation");
        const apply = $("diplomacy-apply");
        select.disabled = !ready || relating;
        apply.disabled = !ready || relating;
        if (!ready) {
            diploStatus("Партия не открыта.");
            return;
        }
        if (relating) return;

        const res = await send({ type: "bots" });
        if (!res || res.error) {
            apply.disabled = true;
            diploStatus(res ? res.error : "Вкладка с игрой не ответила — перезагрузите её.", true);
            return;
        }
        if (res.bots.length === 0) {
            apply.disabled = true;
            diploStatus("Ботов в этой партии нет.");
            return;
        }
        diploStatus(botLine(res.bots));
    }

    async function applyRelation() {
        if (relating || !ready) return;
        relating = true;
        $("diplomacy-apply").disabled = true;
        $("diplomacy-relation").disabled = true;
        diploStatus("Отправляем в игру…");
        try {
            const res = await send({ type: "relate", relation });
            if (!res || res.error) {
                diploStatus(res ? res.error : "Вкладка с игрой не ответила — перезагрузите её.", true);
                return;
            }
            if (res.sent === 0) {
                diploStatus(`У всех ботов уже «${relationName(relation).toLowerCase()}» — отправлять нечего.`);
                return;
            }
            diploStatus(`Отправлено ботам: ${res.sent} из ${res.total}. Игра применит за пару секунд.`);
        } finally {
            // Кнопку оживляем в любом случае: после отказа нажать ещё раз
            // должно быть можно, а строку с причиной мы не трогаем.
            relating = false;
            $("diplomacy-apply").disabled = !ready;
            $("diplomacy-relation").disabled = !ready;
        }
        // Игра отвечает не сразу, поэтому перечитываем дважды: первый раз
        // на случай быстрого ответа, второй — когда он задержался.
        setTimeout(showBots, 1500);
        setTimeout(showBots, 5000);
    }

    // --- здоровье стека ---

    const pct = (hp, max) => (max > 0 ? Math.round(hp / max * 100) : 0);

    function clearArmy() {
        $("army-status").textContent = "";
        $("army-table").hidden = true;
    }

    function armyRow(cells, cls) {
        const tr = document.createElement("tr");
        cells.forEach((text, i) => {
            const td = document.createElement("td");
            td.textContent = text;
            if (i === 2 && cls) td.className = cls;
            tr.append(td);
        });
        return tr;
    }

    function trend(now, next) {
        if (next - now >= 0.5) return "up";
        if (now - next >= 0.5) return "down";
        return "";
    }

    // Здоровье — в процентах от наибольшего: так его показывает и игра.
    // Стек из разных войск складываем по очкам, а не средним процентов:
    // танк весит больше пехотинца.
    async function readArmy() {
        const table = $("army-table");
        const st = $("army-status");
        const res = await send({ type: "army" });
        if (!res || res.error) {
            table.hidden = true;
            st.textContent = res ? res.error : "Вкладка с игрой не ответила — перезагрузите её.";
            st.className = "error";
            return;
        }
        st.className = "muted";
        if (!res.army || res.army.units.length === 0) {
            table.hidden = true;
            st.textContent = "В игре не выбран стек. Выберите его на карте и нажмите снова.";
            return;
        }
        const a = res.army;
        st.textContent = [a.name, a.where, a.own ? "" : "чужой стек"].filter(Boolean).join(" · ");

        const body = table.tBodies[0];
        body.textContent = "";
        let hp = 0, next = 0, max = 0;
        for (const u of a.units) {
            const now = pct(u.hp, u.max), then = pct(u.next, u.max);
            body.append(armyRow([`${u.name} ×${u.size}`, `${now}%`, `${then}%`], trend(u.hp / u.max * 100, u.next / u.max * 100)));
            hp += u.hp; next += u.next; max += u.max;
        }
        const foot = table.tFoot;
        foot.textContent = "";
        if (a.units.length > 1) {
            foot.append(armyRow(["Весь стек", `${pct(hp, max)}%`, `${pct(next, max)}%`], trend(hp / max * 100, next / max * 100)));
        }
        table.hidden = false;
    }

    // --- сравнение сил ---

    const num = (x) => (x >= 100 ? Math.round(x) : Math.round(x * 10) / 10);

    function clearFight() {
        $("fight-status").textContent = "";
        $("fight-sides").textContent = "";
        $("fight-result").hidden = true;
    }

    function fightError(text) {
        const st = $("fight-status");
        st.textContent = text;
        st.className = "error";
    }

    function sideBox(title, s) {
        const box = document.createElement("div");
        box.className = "fight-side";
        const head = document.createElement("b");
        head.textContent = s
            ? `${title}: ${[s.name, s.nation].filter(Boolean).join(", ")} — ${pct(s.hp, s.max)}% здоровья`
            : `${title}: не выбрана`;
        box.append(head);
        if (s) {
            const units = document.createElement("span");
            units.className = "muted small";
            units.textContent = s.units.join(", ");
            box.append(units);
        }
        return box;
    }

    // rounds — за сколько раундов урон damage снимет hp, если бы обе
    // стороны не слабели. Не пробивает — прочерк.
    function rounds(hp, damage) {
        if (!(damage > 0)) return null;
        return Math.max(1, Math.ceil(hp / damage));
    }

    function verdict(r) {
        const kill = rounds(r.defense.hp, r.toDefense); // атаке нужно
        const die = rounds(r.attack.hp, r.toAttack);    // защите нужно
        const lines = [
            `Урон за раунд: атака → защита ${num(r.toDefense)}, защита → атака ${num(r.toAttack)}.`,
            `Раундов, чтобы стереть: защиту ${kill ?? "—"}, атаку ${die ?? "—"}.`,
        ];
        if (kill == null && die == null) lines.push("Стеки не могут навредить друг другу.");
        else if (die == null || (kill != null && kill < die)) lines.push("Перевес у атаки.");
        else if (kill == null || die < kill) lines.push("Перевес у защиты.");
        else lines.push("Силы примерно равны.");
        if (r.defense.fortress > 1) {
            lines.push(`У защиты крепость +${Math.round((r.defense.fortress - 1) * 100)}% — в расчёт не входит, на деле защите легче.`);
        }
        return lines;
    }

    function renderFight(r) {
        $("fight-status").textContent = "";
        const sides = $("fight-sides");
        sides.textContent = "";
        sides.append(sideBox("Атака", r.attack), sideBox("Защита", r.defense));
        if (!r.types) {
            $("fight-result").hidden = true;
            return;
        }
        const p = $("fight-verdict");
        p.textContent = "";
        for (const line of verdict(r)) {
            const div = document.createElement("div");
            div.textContent = line;
            p.append(div);
        }
        const body = $("fight-table").tBodies[0];
        body.textContent = "";
        for (const t of r.types) {
            const tr = document.createElement("tr");
            for (const text of [t.name, String(num(t.attack)), String(num(t.defense))]) {
                const td = document.createElement("td");
                td.textContent = text;
                tr.append(td);
            }
            tr.title = `Доля в составе: атака ${Math.round(t.attackShare * 100)}%, защита ${Math.round(t.defenseShare * 100)}%`;
            body.append(tr);
        }
        $("fight-result").hidden = false;
    }

    async function showFight() {
        const res = await send({ type: "compare" });
        if (!res || res.error) return; // молча: это лишь повтор показа
        if (!res.attack && !res.defense) {
            clearFight();
            return;
        }
        renderFight(res);
    }

    async function pickSide(side) {
        const res = await send({ type: "pick", side });
        if (!res || res.error) {
            fightError(res ? res.error : "Вкладка с игрой не ответила — перезагрузите её.");
            return;
        }
        if (!res.picked) {
            fightError("В игре не выбран стек. Выберите его на карте и нажмите снова.");
            return;
        }
        const cmp = await send({ type: "compare" });
        if (!cmp || cmp.error) {
            fightError(cmp ? cmp.error : "Вкладка с игрой не ответила — перезагрузите её.");
            return;
        }
        renderFight(cmp);
    }

    // --- здания ---

    function clearBuildings() {
        buildingsList = null;
        $("buildings-status").textContent = "";
        $("buildings-player").hidden = true;
        $("buildings-table").hidden = true;
    }

    function playerLabel(p) {
        const who = p.me ? "вы" : p.ai ? "бот" : p.player;
        return `${p.name}${who && who !== p.name ? ` (${who})` : ""} — ${p.provinces} пров.`;
    }

    // Считаем по нажатию, а не следим: пока панель открыта, клиент игры
    // зря не дёргаем. Выбранный игрок переживает пересчёт.
    async function readBuildings() {
        const st = $("buildings-status");
        const res = await send({ type: "buildings" });
        if (!res || res.error) {
            clearBuildings();
            st.textContent = res ? res.error : "Вкладка с игрой не ответила — перезагрузите её.";
            st.className = "error";
            return;
        }
        st.className = "muted";
        buildingsList = res.players;
        if (buildingsList.length === 0) {
            st.textContent = "На карте нет ни одной провинции с хозяином.";
            return;
        }
        st.textContent = "Посчитано сейчас. Нажмите снова, чтобы обновить.";
        if (!buildingsList.some((p) => p.id === buildingsOf)) buildingsOf = buildingsList[0].id;

        const select = $("buildings-player");
        select.textContent = "";
        for (const p of buildingsList) {
            const option = document.createElement("option");
            option.value = String(p.id);
            option.textContent = playerLabel(p);
            option.selected = p.id === buildingsOf;
            select.append(option);
        }
        select.hidden = false;
        renderBuildings();
    }

    // Недостроенные и разбитые здания — в «не готово». У чужих провинций
    // состояние бывает неизвестно: такие идут туда же отдельно, «?N».
    function renderBuildings() {
        const table = $("buildings-table");
        const p = buildingsList && buildingsList.find((x) => x.id === buildingsOf);
        if (!p) {
            table.hidden = true;
            return;
        }
        const body = table.tBodies[0];
        body.textContent = "";
        if (p.buildings.length === 0) {
            const tr = document.createElement("tr");
            const td = document.createElement("td");
            td.colSpan = 3;
            td.className = "muted";
            td.textContent = "Зданий нет.";
            tr.append(td);
            body.append(tr);
        }
        for (const b of p.buildings) {
            const tr = document.createElement("tr");
            const unfinished = String(b.unfinished || "") + (b.unknown ? ` ?${b.unknown}` : "");
            for (const text of [b.name, String(b.count), unfinished.trim()]) {
                const td = document.createElement("td");
                td.textContent = text;
                tr.append(td);
            }
            if (b.unknown) tr.title = `Не видно, в каком состоянии: ${b.unknown}`;
            body.append(tr);
        }
        table.hidden = false;
    }

    // Перезагруженная вкладка — это новая партия или та же с родными
    // цветами: прежний анализ к ней уже не относится.
    function onUpdated(id, info) {
        if (info.status === "loading") analysed.delete(id);
        if (id === tabId) check();
    }

    $("map-analyse").addEventListener("click", analyse);
    $("map-reset").addEventListener("click", reset);
    $("diplomacy-apply").addEventListener("click", applyRelation);
    $("army-read").addEventListener("click", readArmy);
    $("fight-attack").addEventListener("click", () => pickSide("attack"));
    $("fight-defense").addEventListener("click", () => pickSide("defense"));
    $("buildings-read").addEventListener("click", readBuildings);
    $("buildings-player").addEventListener("change", (event) => {
        buildingsOf = Number(event.currentTarget.value);
        renderBuildings();
    });
    $("diplomacy-relation").addEventListener("change", async (event) => {
        relation = Number(event.currentTarget.value);
        await chrome.storage.local.set({ botRelation: relation });
    });

    return {
        async start() {
            if (running) return;
            running = true;
            const saved = await chrome.storage.local.get(["paintMode", "botRelation"]);
            if (MODES.some(([key]) => key === saved.paintMode)) mode = saved.paintMode;
            if (RELATIONS.some(([value]) => value === saved.botRelation)) relation = saved.botRelation;
            fillRelations();
            chrome.tabs.onActivated.addListener(check);
            chrome.tabs.onUpdated.addListener(onUpdated);
            await check();
            // Пока партии нет, заглядываем снова: игра грузится долго,
            // и кнопка должна ожить сама, без щелчков по панели.
            timer = setInterval(() => { if (!ready) check(); }, WAIT_MS);
        },
        stop() {
            running = false;
            clearInterval(timer);
            chrome.tabs.onActivated.removeListener(check);
            chrome.tabs.onUpdated.removeListener(onUpdated);
        },
    };
})();
