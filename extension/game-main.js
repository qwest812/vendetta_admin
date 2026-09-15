// Часть расширения, которая работает внутри клиента игры — в том же мире
// JavaScript, что и сам клиент. Только отсюда видны его объекты: состояние
// партии (hup.gameState) и профили игроков.
//
// Как перекрашивается карта. Провинцию клиент заливает цветом владельца —
// profile.getPrimaryColor(). Мы подменяем этот метод у всех профилей разом
// (через их общий прототип) и отдаём свой цвет тем, кого раскрасили, а
// остальным — родной. Затем просим клиент обновить игроков
// (PlayerState.publishUpdate): на это событие карта сама пересчитывает
// цвета провинций. Выключение — пустая подмена и то же обновление.
//
// Сеть отсюда не трогаем и токен сюда не попадает: состав партии уходит
// соседнему скрипту расширения (game-content.js), цвета приходят от него.

(() => {
    "use strict";

    const CHANNEL = "vendetta-power";

    if (window.__vendettaPower) return;
    window.__vendettaPower = true;

    let colors = null;   // Map: номер игрока в партии → цвет
    let patched = false;

    function gameReady() {
        const hup = window.hup;
        return Boolean(hup && hup.gameState && typeof hup.isGameLoaded === "function"
            && hup.isGameLoaded() && hup.gameState.getPlayerState() && hup.config && hup.config.userData);
    }

    function players() {
        return Object.values(window.hup.gameState.getPlayerState().getPlayers() || {});
    }

    // patch подменяет getPrimaryColor на прототипе профилей. Один раз:
    // новые профили, приходящие с обновлениями партии, наследуют подмену.
    function patch() {
        if (patched) return true;
        const any = players().find((p) => p && typeof p.getPrimaryColor === "function");
        if (!any) return false;
        const proto = Object.getPrototypeOf(any);
        const original = proto.getPrimaryColor;
        proto.getPrimaryColor = function () {
            if (colors) {
                const c = colors.get(this.getPlayerID());
                if (c) return c;
            }
            return original.call(this);
        };
        patched = true;
        return true;
    }

    // roster — кто в партии: номер в партии и номер на сайте. У ботов
    // номера на сайте нет, их сила не считается.
    function roster() {
        const me = window.hup.config.userData;
        const list = [];
        for (const p of players()) {
            const id = p.getPlayerID();
            if (!(id > 0)) continue;
            const site = Number(p.getSiteUserID());
            list.push({
                playerID: id,
                siteUserID: site > 0 ? String(site) : "",
                ai: Boolean(p.getComputerPlayer()) || !(site > 0),
            });
        }
        return {
            gameID: String(me.gameID || ""),
            me: { playerID: Number(me.playerID), siteUserID: String(me.siteUserID || "") },
            players: list,
        };
    }

    // --- чистый цвет на карте ---
    //
    // Карта рисуется в два прохода. Сначала провинции ложатся в отдельную
    // текстуру: цвет страны там уже разбавлен (c·0.7 + 0.3,
    // provinceColor.correct), и шейдер провинций добавляет +0.3 белого и
    // затемнение рельефом и светом. Альфа этой текстуры — не прозрачность,
    // а данные: старший бит — «провинция выделена», остальное — маска суши
    // (у суши ~0.5, у моря и нейтральных клеток — единицы из 255). Трогать
    // её нельзя: с альфой 1 все провинции становятся «выделенными» и темнее.
    //
    // Потом плоскость карты собирает кадр из этой текстуры и текстуры
    // тумана войны: невидимое заливается белым на 80% (0.8·туман) и
    // притеняется (uFowIntensity). В той же текстуре тумана лежат границы
    // стран — их затемнение оставляем, иначе соседи одного цвета сольются.
    //
    // В оба шейдера вписывается переключатель uVendettaPaint. Пока он 1:
    // провинции суши берут наш цвет как есть, почти плоским, а туман не
    // белит и не притеняет. 0 — шейдеры ведут себя как родные.

    const PAINT_UNIFORM = "uVendettaPaint";

    function mapRenderer() {
        const ui = window.hup.ui;
        return ui && ui.mapWidget && ui.mapWidget.mapRenderer;
    }

    // patchShader вписывает переключатель в шейдер материала. Материалы
    // клиент может пересоздать (смена качества графики), поэтому
    // проверяется при каждой раскраске. transform возвращает новый шейдер
    // или null, если шейдер устроен не так, как мы ждём, — тогда он
    // остаётся родным.
    function patchShader(material, transform) {
        if (!material || typeof material.fragmentShader !== "string" || !material.uniforms) return false;
        if (material.fragmentShader.includes(PAINT_UNIFORM)) return true;
        const src = material.fragmentShader;
        const at = src.indexOf("void main(){");
        if (at < 0) return false;
        const patched = transform(src.slice(0, at) + `uniform float ${PAINT_UNIFORM};`, src.slice(at));
        if (!patched) return false;
        material.fragmentShader = patched;
        material.uniforms[PAINT_UNIFORM] = { value: 0 };
        material.needsUpdate = true;
        return true;
    }

    // Провинции: исходный цвет (разбавление снимается), без осветления и
    // затемнения, с лёгкой текстурой рельефа, если её нашли. Иначе
    // оранжевый на тёмном рельефе уходит в коричневый.
    function provinceShader(head, main) {
        const out = /gl_FragColor=vec4\((\w+),vColor\.w\);/;
        if (!out.test(main)) return null;
        const terrain = main.match(/float (\w+)=texture2D\(sTerrainColor,/);
        const relief = terrain ? `(0.85+0.15*${terrain[1]})` : "1.0";
        const body = main.slice("void main(){".length).replace(out,
            `gl_FragColor=vec4(mix($1,clamp((vColor.xyz-0.3)/0.7,0.0,1.0)*${relief},vdLand),vColor.w);`);
        return head + "void main(){" + `float vdLand=step(0.25,vColor.w)*${PAINT_UNIFORM};` + body;
    }

    // Плоскость карты: туман не белит и не притеняет.
    function planeShader(head, main) {
        const white = /mix\((\w+),vec3\(1\.0\),0\.8\*([\w.]+)\)/;
        const shade = /mix\(1\.0,([\w.]+),uFowIntensity\)/;
        if (!white.test(main) || !shade.test(main)) return null;
        return head + main
            .replace(white, `mix($1,vec3(1.0),0.8*$2*(1.0-${PAINT_UNIFORM}))`)
            .replace(shade, `mix(1.0,$1,uFowIntensity*(1.0-${PAINT_UNIFORM}))`);
    }

    // Режим карты. В режиме отношений клиент красит провинции цветом
    // отношения к игроку — нейтрал, союзник, враг, — а цвет страны влияет
    // лишь на яркость. Наши цвета там не видны, поэтому на время раскраски
    // карта переводится в политический режим, а выключение возвращает
    // прежний. Переключит человек режим сам — раскраска вернёт
    // политический при следующем перечитывании состава.
    //
    // Писать надо туда же, куда пишет кнопка режима в интерфейсе:
    // hup.renderCfg.relation (в «Мировой войне» — renderCfg.three.
    // relationModeWW2). mapRenderer.config — это renderCfg.three, и relation
    // в нём — геттер без сеттера: присваивание туда бросает ошибку.
    let savedMode = null;

    function setPolitical(on) {
        const cfg = window.hup.renderCfg;
        if (!cfg) return;
        const three = cfg.three || {};
        if (on) {
            if (!savedMode) savedMode = { relation: cfg.relation, relationModeWW2: three.relationModeWW2 };
            cfg.relation = false;
            if (three.relationModeWW2) three.relationModeWW2 = false;
        } else if (savedMode) {
            cfg.relation = savedMode.relation;
            if (savedMode.relationModeWW2) three.relationModeWW2 = savedMode.relationModeWW2;
            savedMode = null;
        }
    }

    function setClean(on) {
        const renderer = mapRenderer();
        if (!renderer) return false;
        setPolitical(on);
        const materials = [
            [renderer.provinces && renderer.provinces.material, provinceShader],
            [renderer.planeMaterial, planeShader],
        ];
        let ok = true;
        for (const [material, transform] of materials) {
            if (on && !patchShader(material, transform)) ok = false;
            if (material && material.uniforms && material.uniforms[PAINT_UNIFORM]) {
                material.uniforms[PAINT_UNIFORM].value = on ? 1 : 0;
            }
        }
        return ok;
    }

    function repaint() {
        window.hup.gameState.getPlayerState().publishUpdate();
    }

    function reply(msg) {
        window.postMessage({ channel: CHANNEL, from: "main", ...msg }, window.location.origin);
    }

    window.addEventListener("message", (event) => {
        const msg = event.data;
        if (event.source !== window || !msg || msg.channel !== CHANNEL || msg.from !== "content") return;

        try {
            switch (msg.type) {
            case "roster":
                reply(gameReady() ? { type: "roster", roster: roster() } : { type: "roster", notReady: true });
                break;
            case "paint":
                if (!gameReady() || !patch()) {
                    reply({ type: "painted", ok: false });
                    break;
                }
                colors = new Map(Object.entries(msg.colors).map(([id, c]) => [Number(id), c]));
                reply({ type: "painted", ok: true, clean: setClean(true) });
                repaint();
                break;
            case "clear":
                colors = null;
                if (gameReady()) {
                    setClean(false);
                    repaint();
                }
                reply({ type: "cleared" });
                break;
            }
        } catch (err) {
            // Клиент игры обновили, и что-то внутри называется иначе: скажем
            // об этом, а не упадём молча.
            reply({ type: "failed", error: String(err && err.message || err) });
        }
    });
})();
