package web

import (
	"context"
	"net/http"
	"sort"
	"strconv"
	"time"

	"Vendetta_admin/internal/domain"
	"Vendetta_admin/internal/supremacy"
	"Vendetta_admin/internal/supremacy/heroes"
)

// gameHeroToggle включает и выключает автопризыв пехоты в партии.
//
// Включение сразу же идёт в партию: ждать до тика воркера незачем — он
// придёт через три четверти часа, а героиня всё это время простоит без
// дела. Сам заход разберётся, надо ли жать: если Мейв уже размещается,
// кнопка не нажимается, а страница скажет, сколько осталось.
func (s *Server) gameHeroToggle(w http.ResponseWriter, r *http.Request) {
	gameID := r.PathValue("id")
	on := r.PostFormValue("on") == "1"

	title := r.PostFormValue("title")
	if err := s.tasks.SetHeroDeploy(r.Context(), gameID, title, on, currentUser(r).ID); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.log.Info("автопризыв пехоты переключён",
		"gameID", gameID, "включён", on, "user_id", currentUser(r).ID)

	msg := ""
	if on {
		msg = s.heroDeploy(r, gameID, "при включении")
	}
	s.renderGame(w, r, gameID, false, msg)
}

// gameHeroRun — «призвать сейчас»: то же самое, что делает воркер, но по
// нажатию. Ждать до его тика, чтобы проверить одну кнопку, незачем, и итог
// пишется в те же поля — страница показывает последний заход независимо
// от того, кто его сделал.
func (s *Server) gameHeroRun(w http.ResponseWriter, r *http.Request) {
	gameID := r.PathValue("id")
	s.renderGame(w, r, gameID, false, s.heroDeploy(r, gameID, "вручную"))
}

// heroDeploy заходит в партию и жмёт кнопку Мейв, если её есть смысл жать:
// проверку «уже размещается» делает сам заход, и повторным нажатием таймер
// не сбивается. Возвращает то, что показать человеку: пусто, когда всё
// прошло гладко.
//
// Итог пишется в те же поля, что у воркера: страница показывает последний
// заход независимо от того, кто его сделал. Запись некритична — призыв уже
// случился (или не случился), и терять из-за базы ответ человеку было бы
// обидно.
func (s *Server) heroDeploy(r *http.Request, gameID, reason string) string {
	if s.games == nil {
		return langOf(r).T("games.noaccount.short")
	}

	ctx, cancel := context.WithTimeout(r.Context(), gamesTimeout)
	defer cancel()

	var msg string
	result, err := s.games.DeployInfantry(ctx, gameID)
	if err != nil {
		s.log.Error("призыв пехоты", "повод", reason, "gameID", gameID, "err", err)
		result, msg = err.Error(), err.Error()
	} else {
		s.log.Info("призыв пехоты", "повод", reason, "gameID", gameID,
			"user_id", currentUser(r).ID, "итог", result)
	}

	if err := s.tasks.MarkHeroRun(r.Context(), gameID, time.Now(), result); err != nil {
		s.log.Error("запись итога призыва", "gameID", gameID, "err", err)
	}
	return msg
}

// gameRightOfWay выдаёт право прохода всем ботам партии — разово, по
// нажатию. Итог показывается тут же: воркера у этой кнопки нет, и
// запоминать его некуда.
func (s *Server) gameRightOfWay(w http.ResponseWriter, r *http.Request) {
	gameID := r.PathValue("id")
	if s.games == nil {
		s.renderGame(w, r, gameID, false, langOf(r).T("games.noaccount.short"))
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), gamesTimeout)
	defer cancel()

	result, err := s.games.GrantRightOfWay(ctx, gameID)
	if err != nil {
		s.log.Error("право прохода ботам", "gameID", gameID, "err", err)
		s.renderGame(w, r, gameID, false, err.Error())
		return
	}
	s.log.Info("право прохода ботам", "gameID", gameID,
		"user_id", currentUser(r).ID, "итог", result)
	s.renderGameNotice(w, r, gameID, false, "", result)
}

// rallyProvince — своя провинция в выборе города для сбора войск.
type rallyProvince struct {
	ID      int
	Name    string
	Capital bool
}

// rallyProvinces — свои провинции для выбора города: столица первой,
// остальные по имени. Знает их только состояние партии.
func rallyProvinces(state *supremacy.GameState) []rallyProvince {
	if state == nil || state.Me <= 0 {
		return nil
	}
	var out []rallyProvince
	for _, p := range state.Provinces {
		if p.Owner == state.Me {
			out = append(out, rallyProvince{ID: p.ID, Name: p.Name, Capital: p.Capital})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Capital != out[j].Capital {
			return out[i].Capital
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// gameRallyCity выбирает город, куда собирать войска. В партию не ходит:
// номер и название приходят одним полем, как в очереди строительства.
func (s *Server) gameRallyCity(w http.ResponseWriter, r *http.Request) {
	gameID := r.PathValue("id")
	province, city, _, ok := idAndName(r.PostFormValue("province"))
	if !ok {
		s.renderGame(w, r, gameID, false, langOf(r).T("game.build.bad"))
		return
	}
	if err := s.tasks.SetRallyCity(r.Context(), gameID, r.PostFormValue("title"), province, city,
		currentUser(r).ID); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.log.Info("город сбора войск выбран", "gameID", gameID, "город", city, "user_id", currentUser(r).ID)
	s.renderGame(w, r, gameID, false, "")
}

// gameRallyNow шлёт в город все сухопутные армии — разово, по нажатию.
//
// Армии, которые сейчас в партии, запоминаются как виденные: включённый
// автопилот после этого считает новыми только те, что появятся потом.
func (s *Server) gameRallyNow(w http.ResponseWriter, r *http.Request) {
	gameID := r.PathValue("id")
	task, ok := s.rallyTask(w, r, gameID)
	if !ok {
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), gamesTimeout)
	defer cancel()

	var armies []int64
	result, msg := "", ""
	run, err := s.games.RallyArmies(ctx, gameID, task.RallyProvince, task.RallyCity)
	if err != nil {
		s.log.Error("сбор войск", "gameID", gameID, "err", err)
		result, msg = err.Error(), err.Error()
	} else {
		result, armies = run.Result, run.Armies
		s.log.Info("сбор войск", "gameID", gameID, "user_id", currentUser(r).ID, "итог", result)
	}
	if err := s.tasks.MarkRallyRun(r.Context(), gameID, time.Now(), result, armies); err != nil {
		s.log.Error("запись итога сбора", "gameID", gameID, "err", err)
	}
	s.renderGame(w, r, gameID, false, msg)
}

// gameRallyAuto включает и выключает сбор новых армий автопилотом.
//
// Включение сразу идёт в партию, но никого не трогает: заход только
// запоминает, какие армии уже есть. Новыми автопилот будет считать те,
// что появятся после включения, — а не те, что застанет через три четверти
// часа на своём первом обходе.
func (s *Server) gameRallyAuto(w http.ResponseWriter, r *http.Request) {
	gameID := r.PathValue("id")
	on := r.PostFormValue("on") == "1"
	task, ok := s.rallyTask(w, r, gameID)
	if !ok {
		return
	}
	if err := s.tasks.SetRallyAuto(r.Context(), gameID, on, currentUser(r).ID); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.log.Info("сбор новых армий переключён", "gameID", gameID, "включён", on, "user_id", currentUser(r).ID)
	if !on {
		s.renderGame(w, r, gameID, false, "")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), gamesTimeout)
	defer cancel()

	msg := ""
	run, err := s.games.RunAutopilot(ctx, gameID, supremacy.AutopilotJob{
		RallyTo: task.RallyProvince, RallyCity: task.RallyCity})
	if err != nil {
		s.log.Error("сбор войск при включении", "gameID", gameID, "err", err)
		run, msg = &supremacy.AutopilotRun{Rally: err.Error()}, err.Error()
	}
	if err := s.tasks.MarkRallyRun(r.Context(), gameID, time.Now(), run.Rally, run.Armies); err != nil {
		s.log.Error("запись итога сбора", "gameID", gameID, "err", err)
	}
	s.renderGame(w, r, gameID, false, msg)
}

// rallyTask — настройки партии для сбора войск. Без выбранного города
// собирать некуда, и об этом говорится на странице, а не ошибкой.
func (s *Server) rallyTask(w http.ResponseWriter, r *http.Request, gameID string) (*domain.GameTask, bool) {
	if s.games == nil {
		s.renderGame(w, r, gameID, false, langOf(r).T("games.noaccount.short"))
		return nil, false
	}
	task, err := s.tasks.Get(r.Context(), gameID)
	if err != nil {
		s.serverError(w, r, err)
		return nil, false
	}
	if task.RallyProvince <= 0 {
		s.renderGame(w, r, gameID, false, langOf(r).T("game.rally.nocity"))
		return nil, false
	}
	return task, true
}

// heroOption — герой в выборе для размещения: уровень, портрет, открыт
// ли уже его день и когда откроется. Opens — начало нужного дня
// настоящими часами; нулевое, если день уже наступил или его не посчитать.
type heroOption struct {
	supremacy.HeroChoice
	Level    int
	MaxLevel int
	// Image — имя файла портрета из справочника героев; пусто — портрета нет.
	Image string
	Day   int
	Opens time.Time
	Ready bool
}

// heroOptions — открытые герои аккаунта, которых нет на карте, с уровнем
// и днём доступности из справочника сайта. Уровень ноль — герой не открыт,
// и в выбор он не попадает: так прячет его и клиент игры. Без справочника
// уровень неизвестен, и показываются все, чей предмет есть в инвентаре.
func heroOptions(heroes []supremacy.HeroChoice, cards map[int]supremacy.HeroCard,
	state *supremacy.GameState, faces *heroes.Snapshot) []heroOption {

	out := make([]heroOption, 0, len(heroes))
	for _, h := range heroes {
		if h.OnMap {
			continue
		}
		card, carded := cards[h.Item]
		o := heroOption{HeroChoice: h, Day: card.Day, Opens: state.DayStart(card.Day), Ready: card.Day <= state.Day}
		if carded && len(card.Levels) > 0 {
			o.Level, o.MaxLevel = card.Level(state.Inventory), len(card.Levels)
			if o.Level == 0 {
				continue
			}
		}
		if faces != nil {
			if face, ok := faces.ByUnitType(h.Unit); ok {
				o.Image = face.Image
			}
		}
		out = append(out, o)
	}
	return out
}

// gameHeroOrder ставит приказ «героя — в провинцию» и сразу его
// разбирает: если можно — герой встаёт тут же, если нет — страница
// скажет, чего ждём, а дальше приказ доведёт воркер. Заход один и на все
// открытые приказы партии: место в лимите достаётся поставленному раньше.
func (s *Server) gameHeroOrder(w http.ResponseWriter, r *http.Request) {
	gameID := r.PathValue("id")
	unit, hero, _, okHero := idAndName(r.PostFormValue("hero"))
	province, city, _, okProvince := idAndName(r.PostFormValue("province"))
	if !okHero || !okProvince || s.heroPlans == nil {
		s.renderGame(w, r, gameID, false, langOf(r).T("game.build.bad"))
		return
	}
	if s.games == nil {
		s.renderGame(w, r, gameID, false, langOf(r).T("games.noaccount.short"))
		return
	}
	now := time.Now()
	id, err := s.heroPlans.Add(r.Context(), domain.HeroPlan{
		GameID: gameID, Unit: unit, Hero: hero, Province: province, City: city, NextAt: now,
	}, currentUser(r).ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.log.Info("приказ на размещение героя", "gameID", gameID, "герой", hero, "город", city,
		"user_id", currentUser(r).ID)

	orders, err := s.heroPlans.Open(r.Context(), gameID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), gamesTimeout)
	defer cancel()
	runs, err := s.games.RunHeroOrders(ctx, gameID, orders, now)
	if err != nil {
		// Не зашли — приказ стоит и дождётся воркера.
		s.log.Error("приказы героев", "gameID", gameID, "err", err)
		s.renderGame(w, r, gameID, false, err.Error())
		return
	}
	notice := ""
	for _, run := range runs {
		if err := s.heroPlans.Mark(r.Context(), run.ID, now, run.Result, run.Next); err != nil {
			s.log.Error("запись итога приказа героя", "gameID", gameID, "err", err)
		}
		if run.ID == id {
			notice = run.Result
		}
	}
	s.renderGameNotice(w, r, gameID, false, "", notice)
}

func (s *Server) gameHeroPlanRemove(w http.ResponseWriter, r *http.Request) {
	gameID := r.PathValue("id")
	id, err := strconv.ParseInt(r.PathValue("planID"), 10, 64)
	if err != nil || s.heroPlans == nil {
		s.renderGame(w, r, gameID, false, langOf(r).T("game.build.bad"))
		return
	}
	if err := s.heroPlans.Remove(r.Context(), gameID, id); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.renderGame(w, r, gameID, false, "")
}
