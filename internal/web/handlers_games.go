package web

import (
	"context"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"Vendetta_admin/internal/supremacy"
)

// gamesTimeout ограничивает поход в игру: страницу открывает живой человек
// и ждать он готов недолго, а API Supremacy иногда отвечает минутами.
const gamesTimeout = 20 * time.Second

// gameSource — откуда берём игры аккаунта. Интерфейс, а не *supremacy.Client,
// чтобы страницу можно было проверить без похода в сеть. Пустое значение
// означает, что аккаунт игры не настроен (S1914_USER не задан).
type gameSource interface {
	MyGames(ctx context.Context) ([]supremacy.Game, error)
	GameState(ctx context.Context, gameID string) (*supremacy.GameState, error)
	DeployInfantry(ctx context.Context, gameID string) (string, error)
	UserID() string
}

// gameView — строка таблицы. Игра отдаёт всё строками, включая время,
// поэтому разбор дат делаем здесь, а не в шаблоне.
type gameView struct {
	ID       string
	Title    string
	State    string
	Day      string
	Players  string
	Language string
	PlayerID string
	Started  time.Time
	Joined   time.Time
}

// gamesList — активные игры того аккаунта, под которым админка ходит
// в Supremacy 1914. Раздел рутовый: аккаунт в проекте один и общий,
// это внутренняя кухня, а не чей-то личный список.
func (s *Server) gamesList(w http.ResponseWriter, r *http.Request) {
	data := map[string]any{"Games": nil, "Error": "", "PlayURL": ""}

	if s.games == nil {
		data["Error"] = "Аккаунт Supremacy 1914 не настроен: задайте S1914_USER и S1914_PASSWORD."
		s.render(w, r, http.StatusOK, "games", data)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), gamesTimeout)
	defer cancel()

	games, err := s.games.MyGames(ctx)
	if err != nil {
		// Отказ игры — это не поломка админки: показываем причину на самой
		// странице, чтобы было видно, когда протухла сессия или лежит API.
		s.log.Error("список игр Supremacy", "err", err)
		data["Error"] = "Не удалось получить список игр: " + err.Error()
		s.render(w, r, http.StatusOK, "games", data)
		return
	}

	data["Games"] = gameViews(games)
	data["PlayURL"] = supremacy.PlayURL(s.games.UserID())
	s.render(w, r, http.StatusOK, "games", data)
}

// gameViews готовит игры к показу: свежая партия сверху.
func gameViews(games []supremacy.Game) []gameView {
	out := make([]gameView, 0, len(games))
	for _, g := range games {
		out = append(out, gameView{
			ID:       g.GameID,
			Title:    g.Title,
			State:    gameState(g.State),
			Day:      g.DayOfGame,
			Players:  g.NrOfPlayers,
			Language: g.Language,
			PlayerID: g.PlayerID,
			Started:  unixTime(g.StartOfGame),
			Joined:   unixTime(g.JoinTime),
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Started.After(out[j].Started) })
	return out
}

// gameState переводит состояние партии. Незнакомое оставляем как есть:
// лучше показать английское слово, чем скрыть его пустотой.
func gameState(state string) string {
	switch state {
	case "running":
		return "идёт"
	case "readytojoin":
		return "набор"
	case "finished", "ended":
		return "завершена"
	}
	return state
}

// unixTime разбирает время игры: секунды строкой, ноль и мусор означают
// «неизвестно».
func unixTime(s string) time.Time {
	sec, err := strconv.ParseInt(s, 10, 64)
	if err != nil || sec <= 0 {
		return time.Time{}
	}
	return time.Unix(sec, 0)
}

// gameCard — страница одной партии: что это за партия и что админка делает
// в ней сама. Состав партии сюда не тянется по умолчанию: заход на игровой
// сервер игра засчитывает как вход в партию, поэтому его просят отдельно.
func (s *Server) gameCard(w http.ResponseWriter, r *http.Request) {
	s.renderGame(w, r, r.PathValue("id"), r.URL.Query().Get("state") == "1", "")
}

func (s *Server) renderGame(w http.ResponseWriter, r *http.Request, gameID string, withState bool, errMsg string) {
	if s.games == nil {
		s.render(w, r, http.StatusOK, "game", map[string]any{
			"GameID": gameID,
			"Error":  "Аккаунт Supremacy 1914 не настроен: задайте S1914_USER и S1914_PASSWORD.",
		})
		return
	}

	task, err := s.tasks.Get(r.Context(), gameID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}

	data := map[string]any{
		"GameID": gameID, "Task": task, "Error": errMsg,
		"Interval": s.heroEvery, "Game": nil, "State": nil, "Mine": nil,
	}

	ctx, cancel := context.WithTimeout(r.Context(), gamesTimeout)
	defer cancel()

	// Название и день берём из дешёвого списка партий: он же подтверждает,
	// что партия наша и ещё идёт.
	games, err := s.games.MyGames(ctx)
	if err != nil {
		data["Error"] = joinErrors(errMsg, "Не удалось получить список игр: "+err.Error())
		s.render(w, r, http.StatusOK, "game", data)
		return
	}
	for _, g := range gameViews(games) {
		if g.ID == gameID {
			view := g
			data["Game"] = &view
			break
		}
	}
	if data["Game"] == nil {
		data["Error"] = joinErrors(errMsg, "Партии "+gameID+" нет среди активных партий аккаунта.")
		s.render(w, r, http.StatusOK, "game", data)
		return
	}

	if withState {
		state, err := s.games.GameState(ctx, gameID)
		if err != nil {
			data["Error"] = joinErrors(errMsg, "Не удалось зайти в партию: "+err.Error())
			s.render(w, r, http.StatusOK, "game", data)
			return
		}
		mine := state.Owned(state.Me)
		sort.Slice(mine, func(i, j int) bool {
			if mine[i].Capital != mine[j].Capital {
				return mine[i].Capital
			}
			return mine[i].Name < mine[j].Name
		})
		data["State"] = state
		data["Me"] = state.Players[state.Me]
		data["Mine"] = mine
	}

	s.render(w, r, http.StatusOK, "game", data)
}

// gameHeroToggle включает и выключает автопризыв пехоты в партии.
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

	s.renderGame(w, r, gameID, false, "")
}

// gameHeroRun — «призвать сейчас»: то же самое, что делает воркер, но по
// нажатию. Ждать до его тика, чтобы проверить одну кнопку, незачем, и итог
// пишется в те же поля — страница показывает последний заход независимо
// от того, кто его сделал.
func (s *Server) gameHeroRun(w http.ResponseWriter, r *http.Request) {
	gameID := r.PathValue("id")
	if s.games == nil {
		s.renderGame(w, r, gameID, false, "Аккаунт Supremacy 1914 не настроен.")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), gamesTimeout)
	defer cancel()

	var msg string
	result, err := s.games.DeployInfantry(ctx, gameID)
	if err != nil {
		s.log.Error("призыв пехоты вручную", "gameID", gameID, "err", err)
		result, msg = err.Error(), err.Error()
	} else {
		s.log.Info("призыв пехоты вручную", "gameID", gameID, "user_id", currentUser(r).ID, "итог", result)
	}

	// Запись итога некритична: сам призыв уже случился (или не случился),
	// и терять из-за базы ответ человеку было бы обидно.
	if err := s.tasks.MarkHeroRun(r.Context(), gameID, time.Now(), result); err != nil {
		s.log.Error("запись итога призыва", "gameID", gameID, "err", err)
	}

	s.renderGame(w, r, gameID, false, msg)
}

// joinErrors склеивает предупреждение и ошибку: на странице место одно,
// а сказать иногда нужно и то и другое.
func joinErrors(parts ...string) string {
	kept := make([]string, 0, len(parts))
	for _, p := range parts {
		if p != "" {
			kept = append(kept, p)
		}
	}
	return strings.Join(kept, "; ")
}
