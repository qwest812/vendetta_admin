// Карта партии: масштаб, перетаскивание и инспектор провинции.
//
// Масштаб меняет только viewBox у svg — очертания провинций тяжёлые,
// и перерисовывать их ради сдвига незачем. Блок карты htmx иногда
// заменяет целиком (состав приезжает позже), поэтому всё заводится
// заново на каждом новом .map-view, а уже заведённый помечается.
(function () {
    "use strict";

    var MAX_ZOOM = 12;   // во сколько раз можно приблизить
    var CLICK_SLOP = 4;  // сдвиг в пикселях, после которого это уже не щелчок

    function setup(root) {
        var view = root.querySelector(".map-view") || (root.matches && root.matches(".map-view") ? root : null);
        if (!view || view.dataset.ready) return;
        var svg = view.querySelector(".game-map");
        if (!svg) return;
        view.dataset.ready = "1";

        var base = svg.getAttribute("viewBox").split(/\s+/).map(Number);
        var W = base[2], H = base[3];
        var box = { x: 0, y: 0, w: W, h: H };

        function apply() {
            svg.setAttribute("viewBox", box.x + " " + box.y + " " + box.w + " " + box.h);
        }
        function clamp() {
            box.w = Math.min(W, Math.max(W / MAX_ZOOM, box.w));
            box.h = box.w * H / W;
            box.x = Math.min(W - box.w, Math.max(0, box.x));
            box.y = Math.min(H - box.h, Math.max(0, box.y));
        }
        // Точка экрана в координатах карты: масштаб держим вокруг неё,
        // чтобы под курсором оставалось то же место.
        function toMap(clientX, clientY) {
            var r = svg.getBoundingClientRect();
            return {
                x: box.x + (clientX - r.left) / r.width * box.w,
                y: box.y + (clientY - r.top) / r.height * box.h
            };
        }
        function zoomAt(factor, clientX, clientY) {
            var r = svg.getBoundingClientRect();
            if (clientX === undefined) {
                clientX = r.left + r.width / 2;
                clientY = r.top + r.height / 2;
            }
            var p = toMap(clientX, clientY);
            var fx = (p.x - box.x) / box.w, fy = (p.y - box.y) / box.h;
            box.w *= factor;
            box.h = box.w * H / W;
            box.x = p.x - fx * box.w;
            box.y = p.y - fy * box.h;
            clamp();
            apply();
        }

        svg.addEventListener("wheel", function (e) {
            e.preventDefault();
            zoomAt(Math.exp(e.deltaY * 0.0015), e.clientX, e.clientY);
        }, { passive: false });

        view.querySelectorAll(".map-zoom button").forEach(function (b) {
            b.addEventListener("click", function () {
                var z = b.dataset.zoom;
                if (z === "in") zoomAt(1 / 1.5);
                else if (z === "out") zoomAt(1.5);
                else { box = { x: 0, y: 0, w: W, h: H }; apply(); }
            });
        });

        // Перетаскивание и щипок на pointer-событиях: мышь, палец и перо
        // одним кодом. Щелчок — это нажатие без заметного сдвига.
        var pts = {}, moved = 0, pinch = 0;
        svg.addEventListener("pointerdown", function (e) {
            pts[e.pointerId] = { x: e.clientX, y: e.clientY };
            // Захват держит перетаскивание, даже когда курсор ушёл
            // за край карты; без него всё равно работает.
            try { svg.setPointerCapture(e.pointerId); } catch (err) { /* нет такого указателя */ }
            moved = 0;
            var ids = Object.keys(pts);
            if (ids.length === 2) {
                var a = pts[ids[0]], b = pts[ids[1]];
                pinch = Math.hypot(a.x - b.x, a.y - b.y);
            }
        });
        svg.addEventListener("pointermove", function (e) {
            var prev = pts[e.pointerId];
            if (!prev) return;
            var ids = Object.keys(pts);
            if (ids.length === 2) {
                pts[e.pointerId] = { x: e.clientX, y: e.clientY };
                var a = pts[ids[0]], b = pts[ids[1]];
                var d = Math.hypot(a.x - b.x, a.y - b.y);
                if (pinch > 0 && d > 0) zoomAt(pinch / d, (a.x + b.x) / 2, (a.y + b.y) / 2);
                pinch = d;
                moved = CLICK_SLOP + 1;
                return;
            }
            var dx = e.clientX - prev.x, dy = e.clientY - prev.y;
            moved += Math.abs(dx) + Math.abs(dy);
            if (moved <= CLICK_SLOP) return;
            svg.classList.add("dragging");
            var r = svg.getBoundingClientRect();
            box.x -= dx / r.width * box.w;
            box.y -= dy / r.height * box.h;
            clamp();
            apply();
            pts[e.pointerId] = { x: e.clientX, y: e.clientY };
        });
        function release(e) {
            var wasClick = Object.keys(pts).length === 1 && moved <= CLICK_SLOP;
            delete pts[e.pointerId];
            pinch = 0;
            svg.classList.remove("dragging");
            if (wasClick && e.type === "pointerup") pick(e.target);
        }
        svg.addEventListener("pointerup", release);
        svg.addEventListener("pointercancel", release);

        // Инспектор. Сведения о владельцах собрал сервер вместе с картой.
        var insp = view.querySelector(".map-inspector");
        var owners = {};
        try {
            owners = JSON.parse(view.querySelector(".map-owners").textContent) || {};
        } catch (err) { owners = {}; }
        var picked = null;
        var hintText = insp ? insp.querySelector(".insp-hint").textContent : "";

        function pick(target) {
            if (!insp) return;
            var poly = target && target.closest ? target.closest("polygon") : null;
            if (picked) picked.classList.remove("picked");
            picked = poly;
            var name = insp.querySelector(".insp-name");
            var hint = insp.querySelector(".insp-hint");
            var rows = insp.querySelector(".insp-rows");
            var card = insp.querySelector(".insp-card");
            insp.querySelectorAll("[data-f]").forEach(function (f) { f.hidden = true; });
            card.hidden = true;
            hint.textContent = hintText;
            if (!poly) {
                rows.hidden = true;
                hint.hidden = false;
                return;
            }
            poly.classList.add("picked");
            name.textContent = poly.dataset.name || "";
            var o = owners[poly.dataset.owner];
            if (!o) {
                rows.hidden = true;
                hint.hidden = false;
                hint.textContent = insp.dataset.neutral;
                return;
            }
            hint.hidden = true;
            rows.hidden = false;
            rows.querySelectorAll("[data-k]").forEach(function (row) {
                var v = o[row.dataset.k];
                var dd = row.querySelector("dd");
                dd.textContent = v === undefined || v === "" || v === 0 ? "" : String(v);
                // Полоса силы — тем же цветом, что красит карту.
                if (row.dataset.k === "powerText" && o.power && dd.textContent) {
                    var sw = document.createElement("span");
                    sw.className = "swatch " + o.power;
                    dd.prepend(sw);
                }
                row.hidden = !dd.textContent;
            });
            ["mine", "ai", "gone", "premium", "banned", "enemy", "friend"].forEach(function (f) {
                var el = insp.querySelector('[data-f="' + f + '"]');
                if (el) el.hidden = !o[f] || (f === "ai" && o.gone);
            });
            if (o.card) {
                card.href = "/players/" + o.card;
                card.hidden = false;
            }
        }
    }

    function setupAll() { document.querySelectorAll(".map-view").forEach(setup); }
    if (document.readyState === "loading") {
        document.addEventListener("DOMContentLoaded", setupAll);
    } else {
        setupAll();
    }
    // Блок карты, приехавший от htmx, — новый, и заводится заново.
    document.addEventListener("htmx:afterSettle", setupAll);
})();
