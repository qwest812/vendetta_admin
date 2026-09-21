package web

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"Vendetta_admin/internal/domain"
)

// Поиск игроков в лобби — только руту. Кого ищем, отмечают на карточке
// игрока или вписывают номер на сайте в списке раздела «Игры»; сам поиск
// ведёт воркер (supremacy.Hunter) и пишет о находках в телеграм.

// huntHitsShown — сколько последних находок показывать в списке.
const huntHitsShown = 20

// siteUserID — номер игрока на сайте: только цифры.
var siteUserID = regexp.MustCompile(`^[0-9]{1,12}$`)

// huntView — блок поиска на странице «Игры».
type huntView struct {
	Targets []domain.HuntTarget
	Hits    []domain.HuntHit
}

func (s *Server) huntCard(ctx context.Context) (*huntView, error) {
	targets, err := s.hunts.Targets(ctx)
	if err != nil {
		return nil, err
	}
	hits, err := s.hunts.Hits(ctx, huntHitsShown)
	if err != nil {
		return nil, err
	}
	return &huntView{Targets: targets, Hits: hits}, nil
}

// playerHunt включает и выключает поиск игрока с его карточки.
func (s *Server) playerHunt(w http.ResponseWriter, r *http.Request) {
	player, ok := s.loadPlayer(w, r)
	if !ok {
		return
	}
	back := "/players/" + strconv.FormatInt(player.ID, 10)
	if player.GameID == "" {
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	if r.PostFormValue("on") == "1" {
		if err := s.hunts.Add(r.Context(), player.GameID, player.Nickname, currentUser(r).ID); err != nil {
			s.serverError(w, r, err)
			return
		}
		s.logAuditOn(r, "hunt.add", "player", player.ID, map[string]any{"nickname": player.Nickname})
	} else {
		id, err := s.hunts.Find(r.Context(), player.GameID)
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		if id != 0 {
			if err := s.hunts.Remove(r.Context(), id); err != nil && !errors.Is(err, domain.ErrNotFound) {
				s.serverError(w, r, err)
				return
			}
		}
		s.logAuditOn(r, "hunt.remove", "player", player.ID, map[string]any{"nickname": player.Nickname})
	}
	http.Redirect(w, r, back, http.StatusSeeOther)
}

// huntAdd начинает искать игрока по номеру на сайте — того, кого в базе
// ещё нет. Негодный номер просто не принимается.
func (s *Server) huntAdd(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PostFormValue("site_user_id"))
	nick := strings.TrimSpace(r.PostFormValue("nickname"))
	if len([]rune(nick)) > 64 {
		nick = string([]rune(nick)[:64])
	}
	if siteUserID.MatchString(id) {
		if err := s.hunts.Add(r.Context(), id, nick, currentUser(r).ID); err != nil {
			s.serverError(w, r, err)
			return
		}
		s.logAudit(r, "hunt.add", 0, map[string]any{"site_user_id": id, "nickname": nick})
	}
	http.Redirect(w, r, "/games#hunts", http.StatusSeeOther)
}

// huntRemove перестаёт искать игрока из списка.
func (s *Server) huntRemove(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err == nil {
		if err := s.hunts.Remove(r.Context(), id); err != nil && !errors.Is(err, domain.ErrNotFound) {
			s.serverError(w, r, err)
			return
		}
		s.logAudit(r, "hunt.remove", id, map[string]any{"target": id})
	}
	http.Redirect(w, r, "/games#hunts", http.StatusSeeOther)
}
