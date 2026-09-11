package web

import (
	"context"
	"encoding/json"
	"net/http"
	"path"
	"strings"
	"time"

	"Vendetta_admin/internal/i18n"
	"Vendetta_admin/internal/supremacy/heroes"
)

// heroesView — карточка справочника героев в «Настройках». Показывает,
// что у нас есть и насколько оно свежее, а кнопка рядом это обновляет.
type heroesView struct {
	Count int
	// Named — скольким героям известны цифры бафов. Они приходят только
	// из партии, поэтому у новичка игры их может не быть, и честнее
	// сказать об этом, чем делать вид, что справочник полон.
	WithEffects int
	CollectedAt time.Time
	Client      string
	// Builtin — снимок вшитый: своего ещё не собирали.
	Builtin bool
	// UpdatedAt и UpdatedBy заполнены только у своего снимка.
	UpdatedAt time.Time
	UpdatedBy string
	// Error и Detail — что сказать про неудачное нажатие кнопки: первое
	// человеку, второе — причину словами игры. Done — что всё вышло.
	Error  string
	Detail string
	Done   bool
}

// heroesCard собирает состояние справочника: своё, если рут уже обновлял,
// иначе вшитое в образ. Одно место и на страницу, и на ответ кнопки —
// иначе они разошлись бы.
func (s *Server) heroesCard(ctx context.Context) (heroesView, error) {
	snap, builtin, err := s.heroSnapshot(ctx)
	if err != nil {
		return heroesView{}, err
	}

	view := heroesView{
		Count: len(snap.Heroes), CollectedAt: snap.CollectedAt,
		Client: snap.Client, Builtin: builtin,
	}
	for _, h := range snap.Heroes {
		for _, l := range h.Levels {
			if len(l.Effects) > 0 {
				view.WithEffects++
				break
			}
		}
	}
	if !builtin {
		saved, ok, err := s.heroes.Load(ctx)
		if err != nil {
			return heroesView{}, err
		}
		if ok {
			view.UpdatedAt, view.UpdatedBy = saved.UpdatedAt, saved.UpdatedBy
		}
	}
	return view, nil
}

// heroSnapshot отдаёт справочник, которым админка живёт сейчас. Второе
// значение true означает, что он вшит в образ: своего снимка ещё нет.
func (s *Server) heroSnapshot(ctx context.Context) (*heroes.Snapshot, bool, error) {
	if s.heroes != nil {
		saved, ok, err := s.heroes.Load(ctx)
		if err != nil {
			return nil, false, err
		}
		if ok {
			snap, err := heroes.Parse(saved.Raw)
			if err != nil {
				return nil, false, err
			}
			return snap, false, nil
		}
	}
	snap, err := heroes.Builtin()
	if err != nil {
		return nil, false, err
	}
	return snap, true, nil
}

// heroesRefresh пересобирает справочник из открытых источников игры:
// бандла клиента, словаря локализации и витрины магазина. Заходом в партию
// это не считается, но кнопка всё равно рутовая — ходит она от общего
// аккаунта проекта, как и всё остальное в игре.
//
// Цифры бафов переносятся из прошлого снимка: их игра отдаёт только
// в партии, и обновление их не трогает.
func (s *Server) heroesRefresh(w http.ResponseWriter, r *http.Request) {
	me := currentUser(r)

	view, err := s.refreshHeroes(r.Context(), langOf(r), me.ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if view.Error == "" {
		s.logAuditOn(r, "heroes.refresh", "settings", 0,
			map[string]any{"heroes": view.Count, "client": view.Client})
	}

	// Без htmx подменять карточку в странице некому — возвращаемся в раздел.
	if !hx(r) {
		http.Redirect(w, r, "/settings", http.StatusSeeOther)
		return
	}
	s.renderPartial(w, r, "settings", "heroes-card",
		map[string]any{"H": view, "CSRFToken": csrfToken(r)})
}

// refreshHeroes делает саму работу: спрашивает игру и кладёт снимок в базу.
// Неудача сбора — не ошибка сервера: игра могла перевыпустить клиент или
// просто не ответить, и человеку об этом надо сказать строкой в карточке,
// а не страницей с ошибкой.
func (s *Server) refreshHeroes(ctx context.Context, l i18n.Lang, by int64) (heroesView, error) {
	if s.games == nil || s.heroes == nil {
		view, err := s.heroesCard(ctx)
		if err != nil {
			return heroesView{}, err
		}
		view.Error = l.T("settings.heroes.nogame")
		return view, nil
	}

	prev, _, err := s.heroSnapshot(ctx)
	if err != nil {
		return heroesView{}, err
	}

	offers, err := s.games.HeroOffers(ctx)
	if err != nil {
		return s.heroesFailed(ctx, l, err)
	}
	snap, images, err := heroes.Fetch(ctx, nil, offers, prev)
	if err != nil {
		return s.heroesFailed(ctx, l, err)
	}

	raw, err := json.Marshal(snap)
	if err != nil {
		return heroesView{}, err
	}
	if err := s.heroes.Save(ctx, raw, snap.CollectedAt, by); err != nil {
		return heroesView{}, err
	}
	if err := s.heroes.SaveImages(ctx, images); err != nil {
		return heroesView{}, err
	}
	s.log.Info("справочник героев обновлён",
		"героев", len(snap.Heroes), "клиент", snap.Client, "user_id", by)

	view, err := s.heroesCard(ctx)
	if err != nil {
		return heroesView{}, err
	}
	view.Done = true
	return view, nil
}

// heroesFailed показывает прежнее состояние вместе с тем, почему обновление
// не вышло: старый справочник от неудачи никуда не делся.
func (s *Server) heroesFailed(ctx context.Context, l i18n.Lang, cause error) (heroesView, error) {
	s.log.Error("справочник героев не обновился", "err", cause)
	view, err := s.heroesCard(ctx)
	if err != nil {
		return heroesView{}, err
	}
	view.Error, view.Detail = l.T("settings.heroes.failed"), cause.Error()
	return view, nil
}

// heroImage отдаёт портрет героя: сначала свой, из базы, потом вшитый
// в образ. Своего нет ровно до первого обновления, вшитого — у героя,
// которого в игре ещё не было, когда собирали образ.
func (s *Server) heroImage(w http.ResponseWriter, r *http.Request) {
	// Имя приходит из адреса: от пути оставляем только файл, чтобы им
	// нельзя было попросить что-нибудь соседнее.
	name := path.Base(r.PathValue("name"))
	if name == "" || !strings.HasSuffix(name, ".png") {
		http.NotFound(w, r)
		return
	}

	if s.heroes != nil {
		data, ok, err := s.heroes.Image(r.Context(), name)
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		if ok {
			writePNG(w, data)
			return
		}
	}
	data, ok := heroes.BuiltinImage(name)
	if !ok {
		http.NotFound(w, r)
		return
	}
	writePNG(w, data)
}

func writePNG(w http.ResponseWriter, data []byte) {
	w.Header().Set("Content-Type", "image/png")
	// Портреты меняются разве что с выпуском игры, а страница с ними
	// открывается часто: пусть браузер держит их у себя.
	w.Header().Set("Cache-Control", "public, max-age=86400")
	_, _ = w.Write(data)
}
