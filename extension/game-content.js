// Часть расширения на странице игры, но в своём, изолированном мире: клиент
// игры её не видит. Отсюда рисуется кнопка «Карта» с режимами и легендой,
// отсюда же идут сообщения обоим соседям — фону (сервер админки)
// и game-main.js (карта в клиенте игры).
//
// Цепочка при включении: состав партии из клиента → фон → /api/map →
// цвета всех режимов разом → клиент перекрашивает карту выбранным. Смена
// режима на сервер уже не ходит: цвета всех шести приехали одним ответом.

(() => {
    "use strict";

    const CHANNEL = "admin-map";

    // Как часто перечитывать состав, пока раскраска включена: в партию
    // приходят новые игроки, боты занимают места выбывших, а альянсы
    // незнакомых игроков сервер узнаёт не сразу.
    const REFRESH_MS = 10 * 60 * 1000;

    let enabled = false;
    let mode = "clans";   // выбранный режим, помнится между партиями
    let modes = null;     // ответ сервера: режимы с цветами и легендами
    let refreshTimer = null;
    let ui = null;

    // --- связь с game-main.js ---

    const pending = new Map();
    window.addEventListener("message", (event) => {
        const msg = event.data;
        if (event.source !== window || !msg || msg.channel !== CHANNEL || msg.from !== "main") return;
        if (msg.type === "failed") {
            // game-main.js упал посреди запроса — ждущим отдаём ошибку сразу,
            // а не через таймаут, и с её текстом.
            for (const wait of pending.values()) wait(msg);
            pending.clear();
            return;
        }
        const wait = pending.get(msg.type);
        if (wait) {
            pending.delete(msg.type);
            wait(msg);
        }
    });

    function ask(type, answerType, extra = {}, timeout = 5000) {
        return new Promise((resolve) => {
            const timer = setTimeout(() => {
                pending.delete(answerType);
                resolve(null);
            }, timeout);
            pending.set(answerType, (msg) => {
                clearTimeout(timer);
                resolve(msg);
            });
            window.postMessage({ channel: CHANNEL, from: "content", type, ...extra }, window.location.origin);
        });
    }

    // --- кнопка, режимы и легенда ---

    // Подписи режимов до ответа сервера: кнопки нужны сразу, а названия
    // потом всё равно приедут с сервера на языке аккаунта.
    const MODES = [
        ["clans", "Альянсы"],
        ["sides", "Мои списки"],
        ["teams", "Коалиции"],
        ["players", "Игроки"],
        ["power", "Сила"],
        ["top", "Топ альянсов"],
    ];

    function buildUI() {
        const host = document.createElement("div");
        host.id = "admin-map";
        // Своя тень: стили игры не трогают кнопку, а наши — игру.
        const root = host.attachShadow({ mode: "closed" });
        root.innerHTML = `
            <style>
                :host { all: initial; }
                .box { position: fixed; left: 12px; bottom: 12px; z-index: 2147483647;
                       font: 13px/1.4 system-ui, -apple-system, "Segoe UI", Roboto, sans-serif;
                       color: #e6e9ec; }
                button { border: 1px solid #2c343d; border-radius: 6px; background: #1c2229;
                         color: #e6e9ec; padding: 6px 10px; font: inherit; cursor: pointer; }
                button.on { background: #c4453c; border-color: #c4453c; color: #fff; }
                .panel { margin-top: 6px; padding: 8px 10px; border: 1px solid #2c343d;
                         border-radius: 6px; background: rgba(20, 24, 29, .92); width: 260px; }
                .modes { display: grid; grid-template-columns: 1fr 1fr; gap: 4px; margin-bottom: 8px; }
                .modes button { padding: 4px 6px; font-size: 12px; color: #8b96a1; }
                .modes button.active { border-color: #c4453c; color: #fff; }
                ul { list-style: none; margin: 0; padding: 0; max-height: 40vh; overflow-y: auto; }
                li { display: flex; align-items: center; gap: 6px; }
                .sw { width: 12px; height: 12px; border-radius: 3px; flex: none; }
                .label { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
                .n { margin-left: auto; color: #8b96a1; padding-left: 6px; }
                .status { margin: 0 0 6px; color: #8b96a1; }
                .status.error { color: #e0776f; }
                [hidden] { display: none !important; }
            </style>
            <div class="box">
                <button type="button" class="toggle">🎨 Карта</button>
                <div class="panel" hidden>
                    <div class="modes"></div>
                    <p class="status"></p>
                    <ul class="legend"></ul>
                </div>
            </div>`;
        document.documentElement.appendChild(host);

        const toggle = root.querySelector(".toggle");
        toggle.addEventListener("click", () => setEnabled(!enabled));

        const buttons = new Map();
        const box = root.querySelector(".modes");
        for (const [key, title] of MODES) {
            const b = document.createElement("button");
            b.type = "button";
            b.textContent = title;
            b.addEventListener("click", () => chooseMode(key));
            box.append(b);
            buttons.set(key, b);
        }
        return {
            toggle,
            buttons,
            panel: root.querySelector(".panel"),
            status: root.querySelector(".status"),
            legend: root.querySelector(".legend"),
        };
    }

    function showStatus(text, isError) {
        ui.status.textContent = text || "";
        ui.status.hidden = !text;
        ui.status.classList.toggle("error", Boolean(isError));
    }

    function showModes() {
        for (const [key, b] of ui.buttons) {
            b.classList.toggle("active", key === mode);
            const m = modes && modes.find((x) => x.key === key);
            if (m) {
                b.textContent = m.title;
                // Пояснение к режиму — то же, что под картой в админке.
                // Длинное, поэтому подсказкой, а не текстом в панели.
                b.title = m.note || "";
            }
        }
    }

    function showLegend(rows) {
        ui.legend.textContent = "";
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
            ui.legend.append(li);
        }
    }

    // --- раскраска ---

    // load спрашивает сервер о цветах всех режимов для этой партии.
    async function load() {
        showStatus("Загружаем карту…");
        showLegend(null);

        const answer = await ask("roster", "roster");
        if (!answer || !answer.roster) {
            showStatus("Партия ещё загружается — попробуйте через несколько секунд.", true);
            return false;
        }
        const { me, players, teams } = answer.roster;
        if (!me.site || players.length === 0) {
            showStatus("Не удалось прочитать состав партии.", true);
            return false;
        }

        const res = await chrome.runtime.sendMessage({ type: "map", me: me.site, players, teams });
        if (!enabled) return false; // выключили, пока ждали сервер
        if (!res || res.error) {
            showStatus(res ? res.error : "Расширение не ответило.", true);
            return false;
        }
        modes = res.data.modes;
        return true;
    }

    // paint красит карту выбранным режимом из уже загруженных цветов.
    async function paint() {
        showModes();
        const m = modes && modes.find((x) => x.key === mode);
        if (!m) return;
        const painted = await ask("paint", "painted", { colors: m.colors });
        if (!painted || !painted.ok) {
            const detail = painted && painted.error ? ` (${painted.error})` : "";
            showStatus(`Клиент игры не дал перекрасить карту — возможно, его обновили.${detail}`, true);
            return;
        }
        showStatus(m.status || "");
        showLegend(m.legend);
    }

    async function refresh() {
        if (await load()) await paint();
    }

    async function chooseMode(key) {
        mode = key;
        await chrome.storage.local.set({ paintMode: key });
        if (!enabled) {
            await setEnabled(true);
            return;
        }
        await paint();
    }

    async function setEnabled(on) {
        enabled = on;
        ui.toggle.classList.toggle("on", on);
        ui.panel.hidden = !on;
        await chrome.storage.local.set({ paintOn: on });
        clearInterval(refreshTimer);
        if (on) {
            showModes();
            await refresh();
            refreshTimer = setInterval(() => enabled && refresh(), REFRESH_MS);
        } else {
            modes = null;
            await ask("clear", "cleared");
        }
    }

    // --- запуск ---

    // Кнопку показываем, только когда партия загрузилась: на экране выбора
    // партий ей делать нечего.
    async function start() {
        for (;;) {
            const answer = await ask("roster", "roster", {}, 2000);
            if (answer && answer.roster) break;
            await new Promise((r) => setTimeout(r, 3000));
        }
        ui = buildUI();
        const saved = await chrome.storage.local.get(["paintOn", "paintMode", "paintPower"]);
        if (MODES.some(([key]) => key === saved.paintMode)) mode = saved.paintMode;
        // paintPower — как это помнила версия 0.2, где режим был один.
        if (saved.paintOn || (saved.paintOn === undefined && saved.paintPower)) {
            if (saved.paintOn === undefined) mode = "power";
            setEnabled(true);
        }
    }

    start();
})();
