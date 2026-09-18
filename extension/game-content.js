// Часть расширения на странице игры, но в своём, изолированном мире: клиент
// игры её не видит. Своих кнопок у неё нет — все они в боковой панели
// расширения. Здесь только мост: панель спрашивает, загрузилась ли партия,
// просит состав и отдаёт цвета, а этот скрипт передаёт всё это
// game-main.js, который живёт в мире клиента игры и один может до него
// дотянуться.
//
// Игра открывается во фрейме, а скрипт стоит во всех фреймах вкладки.
// Отвечает панели только тот, в чьём фрейме партия загрузилась: иначе
// первым мог бы ответить внешний фрейм — «партии нет».

(() => {
    "use strict";

    const CHANNEL = "admin-map";

    let gameFrame = false; // в этом ли фрейме партия
    let painted = false;   // перекрашена ли карта сейчас

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

    // --- связь с панелью ---

    // Решать, отвечать ли, надо сразу: фрейм без партии молчит, и тогда
    // панели отвечает тот, где партия есть.
    chrome.runtime.onMessage.addListener((msg, sender, sendResponse) => {
        if (!gameFrame || sender.id !== chrome.runtime.id || !msg || msg.to !== "game") {
            return false;
        }
        handle(msg).then(sendResponse);
        return true; // ответ придёт позже
    });

    async function handle(msg) {
        switch (msg.type) {
        case "ping":
            return { ready: true, painted };
        case "roster": {
            const answer = await ask("roster", "roster");
            if (answer && answer.type === "failed") {
                return { error: `Клиент игры не отдал состав партии — возможно, его обновили. (${answer.error})` };
            }
            if (!answer || !answer.roster) {
                return { error: "Партия ещё загружается — попробуйте через несколько секунд." };
            }
            return { roster: answer.roster };
        }
        case "paint": {
            const answer = await ask("paint", "painted", { colors: msg.colors });
            if (!answer || !answer.ok) {
                const detail = answer && answer.error ? ` (${answer.error})` : "";
                return { error: `Клиент игры не дал перекрасить карту — возможно, его обновили.${detail}` };
            }
            painted = true;
            return { ok: true };
        }
        case "clear":
            await ask("clear", "cleared");
            painted = false;
            return { ok: true };
        }
        return { error: "Неизвестный запрос." };
    }

    // --- запуск ---

    // Ждём, пока в этом фрейме загрузится партия. Спрашиваем лёгким
    // «ready»: состав читается только по нажатию в панели.
    async function start() {
        for (;;) {
            const answer = await ask("ready", "ready", {}, 2000);
            if (answer && answer.ready) break;
            await new Promise((r) => setTimeout(r, 3000));
        }
        gameFrame = true;
    }

    start();
})();
