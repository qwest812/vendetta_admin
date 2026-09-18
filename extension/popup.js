// Окно расширения — боковая панель Chrome: вход в админку по почте
// и паролю, а после входа — раскраска карты партии (panel-map.js).
//
// Сервер отдаёт за почту и пароль токен; расширение хранит его
// в chrome.storage.local и шлёт заголовком Authorization. Пароль нигде
// не хранится: после входа он больше не нужен.

"use strict";

const roles = { user: "пользователь", admin: "админ", root: "рут" };

const $ = (id) => document.getElementById(id);
const show = (el, on) => { el.hidden = !on; };

// Адрес сервера — только https: по голому http пароль и токен уходили бы
// открытым текстом. Исключение — свой компьютер, для проверки.
function normalizeServer(raw) {
    let text = raw.trim();
    if (!/^[a-z]+:\/\//i.test(text)) {
        text = "https://" + text;
    }
    let url;
    try {
        url = new URL(text);
    } catch {
        throw new Error("Не похоже на адрес сайта.");
    }
    const local = url.hostname === "localhost" || url.hostname === "127.0.0.1";
    if (url.protocol !== "https:" && !(url.protocol === "http:" && local)) {
        throw new Error("Нужен адрес с https://: по http пароль уходил бы открытым текстом.");
    }
    return url.origin;
}

// api — запрос к серверу. Ответ сервера всегда JSON; текст ошибки в нём
// готов к показу.
async function api(server, path, { method = "GET", token, body } = {}) {
    const headers = {};
    if (token) headers.Authorization = "Bearer " + token;
    if (body !== undefined) headers["Content-Type"] = "application/json";

    let res;
    try {
        res = await fetch(server + path, {
            method,
            headers,
            body: body === undefined ? undefined : JSON.stringify(body),
            // Куки сайта расширению не нужны: вход у него свой, по токену.
            credentials: "omit",
            cache: "no-store",
        });
    } catch {
        const err = new Error("Сервер не отвечает. Проверьте адрес и интернет.");
        err.offline = true;
        throw err;
    }

    let data = null;
    if (res.status !== 204) {
        data = await res.json().catch(() => null);
    }
    if (!res.ok) {
        const err = new Error((data && data.error) || `Ошибка сервера (${res.status}).`);
        err.status = res.status;
        throw err;
    }
    return data;
}

function showLogin(state, message) {
    const form = $("login");
    form.server.value = state.server || "";
    form.email.value = (state.user && state.user.email) || state.email || "";
    form.password.value = "";
    $("login-error").textContent = message || "";
    show($("login-error"), Boolean(message));
    show($("busy"), false);
    show($("signed-in"), false);
    show(form, true);
    mapPanel.stop();
    (form.server.value ? (form.email.value ? form.password : form.email) : form.server).focus();
}

function showSignedIn(state, warning) {
    $("nickname").textContent = state.user.nickname;
    $("role").textContent = roles[state.user.role] || state.user.role;
    $("server-line").textContent = state.server;
    $("me-warning").textContent = warning || "";
    show($("me-warning"), Boolean(warning));
    show($("busy"), false);
    show($("login"), false);
    show($("signed-in"), true);
    mapPanel.start();
}

async function load() {
    return chrome.storage.local.get(["server", "email", "token", "user"]);
}

// При открытии окна спрашиваем сервер, жив ли токен: его могли отозвать —
// сменой пароля, блокировкой или кнопкой «Отключить» в профиле.
async function start() {
    const state = await load();
    if (!state.token || !state.server || !state.user) {
        showLogin(state);
        return;
    }
    show($("busy"), true);
    try {
        const data = await api(state.server, "/api/auth/me", { token: state.token });
        state.user = data.user;
        await chrome.storage.local.set({ user: data.user });
        showSignedIn(state);
    } catch (err) {
        // 403 — расширение этому аккаунту закрыли: вход тоже забываем,
        // а причину показываем у формы.
        if (err.status === 401 || err.status === 403) {
            await chrome.storage.local.remove(["token", "user"]);
            showLogin({ server: state.server, email: state.user.email }, err.message);
            return;
        }
        // Сервер недоступен — это не выход: токен, скорее всего, жив.
        showSignedIn(state, err.message);
    }
}

$("login").addEventListener("submit", async (event) => {
    event.preventDefault();
    const form = event.currentTarget;
    const button = form.querySelector("button");

    let server;
    try {
        server = normalizeServer(form.server.value);
    } catch (err) {
        showLogin({ server: form.server.value, email: form.email.value }, err.message);
        return;
    }

    // Доступ к адресу сервера просим прямо в обработчике нажатия: Chrome
    // показывает этот вопрос только в ответ на действие человека.
    const granted = await chrome.permissions.request({ origins: [server + "/*"] });
    if (!granted) {
        showLogin({ server, email: form.email.value },
            "Без разрешения на этот адрес расширение не сможет связаться с админкой.");
        return;
    }

    button.disabled = true;
    try {
        const email = form.email.value.trim();
        const data = await api(server, "/api/auth/login", {
            method: "POST",
            body: { email, password: form.password.value },
        });
        const state = { server, email, token: data.token, user: data.user };
        await chrome.storage.local.set(state);
        showSignedIn(state);
    } catch (err) {
        showLogin({ server, email: form.email.value }, err.message);
    } finally {
        button.disabled = false;
    }
});

$("logout").addEventListener("click", async () => {
    const state = await load();
    // Токен гасим и на сервере, но не ждём его ради выхода: если сервер
    // недоступен, расширение всё равно должно забыть вход. Сам токен тогда
    // доживёт свой срок, а оборвать его можно в профиле.
    api(state.server, "/api/auth/logout", { method: "POST", token: state.token }).catch(() => {});
    loggingOut = true;
    await chrome.storage.local.remove(["token", "user"]);
    showLogin({ server: state.server, email: state.user && state.user.email });
    loggingOut = false;
});

// Вход могут забыть и не здесь: фон стирает токен, когда сервер ответил,
// что вход закончился или расширение аккаунту закрыто. Панель открыта
// долго, поэтому следит за этим сама.
// Причину фон оставляет в authError: «закрыто вашему аккаунту» и «вход
// закончился» человеку надо различать.
let loggingOut = false;
chrome.storage.onChanged.addListener(async (changes, area) => {
    if (area !== "local" || !changes.token || changes.token.newValue) return;
    if (loggingOut || $("signed-in").hidden) return;
    const state = await chrome.storage.local.get(["server", "email", "authError"]);
    await chrome.storage.local.remove("authError");
    showLogin({ server: state.server, email: state.email },
        state.authError || "Вход в расширение закончился — войдите снова.");
});

start();
