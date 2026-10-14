package web

import (
	"context"
	"net/http"
	"time"
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
