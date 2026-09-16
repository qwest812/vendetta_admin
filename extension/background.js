// Фон расширения: единственное место, откуда оно ходит на сервер админки.
//
// Скрипт на странице игры сам этого сделать не может — его запросы
// считаются запросами страницы supremacy1914.com, и браузер не пустил бы их
// на чужой адрес. Фону же доступ к адресу админки человек выдал при входе.
// Токен тоже живёт только здесь и в окне расширения: странице игры он
// не показывается.

"use strict";

chrome.runtime.onMessage.addListener((msg, sender, sendResponse) => {
    // Слушаем только свои скрипты: чужие расширения сюда писать не должны.
    if (sender.id !== chrome.runtime.id || !msg || msg.type !== "power") {
        return false;
    }
    power(msg.me, msg.players).then(sendResponse);
    return true; // ответ придёт позже
});

// power спрашивает сервер, кто из игроков партии насколько сильнее.
// Ответ всегда объект: {data} при удаче или {error, status} при отказе —
// сообщения между частями расширения исключения не переносят.
async function power(me, players) {
    const { server, token } = await chrome.storage.local.get(["server", "token"]);
    if (!server || !token) {
        return { error: "Войдите в расширении «Админка» — значок справа от адресной строки.", status: 401 };
    }

    let res;
    try {
        res = await fetch(server + "/api/power", {
            method: "POST",
            headers: { "Content-Type": "application/json", Authorization: "Bearer " + token },
            body: JSON.stringify({ me, players }),
            credentials: "omit",
            cache: "no-store",
        });
    } catch {
        return { error: "Сервер админки не отвечает.", status: 0 };
    }

    const body = await res.json().catch(() => null);
    if (!res.ok) {
        if (res.status === 401) {
            // Токен погасили — забываем вход, окно расширения попросит войти.
            await chrome.storage.local.remove(["token", "user"]);
        }
        return { error: (body && body.error) || `Ошибка сервера (${res.status}).`, status: res.status };
    }
    return { data: body };
}
