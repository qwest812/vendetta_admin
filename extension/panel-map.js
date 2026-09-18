// Раскраска карты партии из боковой панели. Панель не закрывается, пока
// человек играет, поэтому все кнопки здесь: анализ, режимы, возврат цветов
// игры и легенда.
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

    let running = false;
    let mode = "clans";
    let tabId = null;
    let ready = false;
    let busy = false;
    let timer = null;
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

    // Перезагруженная вкладка — это новая партия или та же с родными
    // цветами: прежний анализ к ней уже не относится.
    function onUpdated(id, info) {
        if (info.status === "loading") analysed.delete(id);
        if (id === tabId) check();
    }

    $("map-analyse").addEventListener("click", analyse);
    $("map-reset").addEventListener("click", reset);

    return {
        async start() {
            if (running) return;
            running = true;
            const saved = await chrome.storage.local.get("paintMode");
            if (MODES.some(([key]) => key === saved.paintMode)) mode = saved.paintMode;
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
