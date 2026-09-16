// Часть расширения на странице игры, но в своём, изолированном мире: клиент
// игры её не видит. Отсюда рисуется кнопка «Сила» с легендой, отсюда же
// идут сообщения обоим соседям — фону (сервер админки) и game-main.js
// (карта в клиенте игры).
//
// Цепочка при включении: состав партии из клиента → фон → /api/power →
// цвета по полосам силы → клиент перекрашивает карту.

(() => {
    "use strict";

    const CHANNEL = "admin-power";

    // Полосы силы — те же, что в режиме «Сила» в админке, но цвета ярче:
    // заливка на карте ложится поверх рельефа, и приглушённая палитра
    // админки на нём теряется. Бледность самого клиента (разбавление цвета
    // и полупрозрачность) снимает game-main.js, так что цвет на карте тот
    // же, что в легенде.
    // Цвет в виде rgba(…,255): так его отдаёт сам клиент игры, и в таком
    // виде его ждёт код, который этот цвет разбирает.
    const BANDS = [
        { key: "power-much-up", label: "Намного опаснее", color: [220, 20, 20] },
        { key: "power-up", label: "Опаснее", color: [255, 130, 0] },
        { key: "power-even", label: "Примерно поровну", color: [240, 210, 0] },
        { key: "power-down", label: "Слабее", color: [60, 200, 60] },
        { key: "power-much-down", label: "Намного слабее", color: [0, 150, 200] },
        { key: "unknown", label: "Счёта нет", color: [200, 200, 200] },
        { key: "ai", label: "Компьютер", color: [90, 90, 90] },
        { key: "me", label: "Вы", color: [230, 0, 230] },
    ];
    const rgba = ([r, g, b]) => `rgba(${r},${g},${b},255)`;
    const css = ([r, g, b]) => `rgb(${r},${g},${b})`;

    // Как часто перечитывать состав, пока раскраска включена: в партию
    // приходят новые игроки, а боты занимают места выбывших.
    const REFRESH_MS = 10 * 60 * 1000;

    let enabled = false;
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

    // --- кнопка и легенда ---

    function buildUI() {
        const host = document.createElement("div");
        host.id = "admin-power";
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
                         border-radius: 6px; background: rgba(20, 24, 29, .92); min-width: 190px; }
                ul { list-style: none; margin: 0; padding: 0; }
                li { display: flex; align-items: center; gap: 6px; }
                .sw { width: 12px; height: 12px; border-radius: 3px; flex: none; }
                .n { margin-left: auto; color: #8b96a1; }
                .status { margin: 0 0 6px; color: #8b96a1; max-width: 240px; }
                .status.error { color: #e0776f; }
                [hidden] { display: none !important; }
            </style>
            <div class="box">
                <button type="button" class="toggle">⚔ Сила</button>
                <div class="panel" hidden>
                    <p class="status"></p>
                    <ul class="legend"></ul>
                </div>
            </div>`;
        document.documentElement.appendChild(host);

        const toggle = root.querySelector(".toggle");
        toggle.addEventListener("click", () => setEnabled(!enabled));
        return {
            toggle,
            panel: root.querySelector(".panel"),
            status: root.querySelector(".status"),
            legend: root.querySelector(".legend"),
        };
    }

    function showStatus(text, isError) {
        ui.panel.hidden = false;
        ui.status.textContent = text || "";
        ui.status.hidden = !text;
        ui.status.classList.toggle("error", Boolean(isError));
    }

    function showLegend(counts) {
        ui.legend.textContent = "";
        if (!counts) return;
        for (const band of BANDS) {
            if (!counts[band.key]) continue;
            const li = document.createElement("li");
            const sw = document.createElement("span");
            sw.className = "sw";
            sw.style.background = css(band.color);
            const label = document.createElement("span");
            label.textContent = band.label;
            const n = document.createElement("span");
            n.className = "n";
            n.textContent = counts[band.key];
            li.append(sw, label, n);
            ui.legend.append(li);
        }
    }

    // --- раскраска ---

    async function paint() {
        showStatus("Считаем силу игроков…");
        showLegend(null);

        const answer = await ask("roster", "roster");
        if (!answer || !answer.roster) {
            showStatus("Партия ещё загружается — попробуйте через несколько секунд.", true);
            return;
        }
        const { me, players } = answer.roster;
        const humans = players.filter((p) => !p.ai && p.siteUserID);
        if (!me.siteUserID || humans.length === 0) {
            showStatus("Не удалось прочитать состав партии.", true);
            return;
        }

        const res = await chrome.runtime.sendMessage({
            type: "power",
            me: me.siteUserID,
            players: humans.map((p) => p.siteUserID),
        });
        if (!enabled) return; // выключили, пока ждали сервер
        if (!res || res.error) {
            showStatus(res ? res.error : "Расширение не ответило.", true);
            return;
        }

        const colors = {};
        const counts = {};
        for (const p of players) {
            let key;
            if (p.playerID === me.playerID) key = "me";
            else if (p.ai || !p.siteUserID) key = "ai";
            else key = (res.data.players[p.siteUserID] || {}).power || "unknown";
            const band = BANDS.find((b) => b.key === key) || BANDS.find((b) => b.key === "unknown");
            colors[p.playerID] = rgba(band.color);
            counts[band.key] = (counts[band.key] || 0) + 1;
        }

        const painted = await ask("paint", "painted", { colors });
        if (!painted || !painted.ok) {
            const detail = painted && painted.error ? ` (${painted.error})` : "";
            showStatus(`Клиент игры не дал перекрасить карту — возможно, его обновили.${detail}`, true);
            return;
        }
        const mine = res.data.me;
        showStatus(mine && mine.rated
            ? `Вы: ${mine.level} ур., кд ${mine.kd.toFixed(2)}, сила ${mine.danger.toFixed(2)}`
            : "Вашего счёта ещё нет — сравнивать не с чем, карта серая.", !(mine && mine.rated));
        showLegend(counts);
    }

    async function setEnabled(on) {
        enabled = on;
        ui.toggle.classList.toggle("on", on);
        await chrome.storage.local.set({ paintPower: on });
        clearInterval(refreshTimer);
        if (on) {
            await paint();
            refreshTimer = setInterval(() => enabled && paint(), REFRESH_MS);
        } else {
            ui.panel.hidden = true;
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
        const { paintPower } = await chrome.storage.local.get("paintPower");
        if (paintPower) setEnabled(true);
    }

    start();
})();
