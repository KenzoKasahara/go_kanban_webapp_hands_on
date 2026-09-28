package handler

import (
	"net/http"

	"github.com/KenzoKasahara/go_kanban_webapp_hands_on/go-kanban/internal/httpx"
	"github.com/KenzoKasahara/go_kanban_webapp_hands_on/go-kanban/internal/model"
)

func Health(w http.ResponseWriter, r *http.Request) {
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// currentUser は RequireAuth が載せた User を取り出す。
// RequireAuth を通っていない経路で呼ばれたら、認証されていないものとして扱う。
func currentUser(w http.ResponseWriter, r *http.Request) (model.User, bool) {
	user, ok := httpx.CurrentUser(r.Context())
	if !ok {
		httpx.RespondError(w, r, model.ErrUnauthorized)
	}

	return user, ok
}
