package middleware

import (
	"net/http"

	"github.com/KenzoKasahara/go_kanban_webapp_hands_on/go-kanban/internal/httpx"
	"github.com/KenzoKasahara/go_kanban_webapp_hands_on/go-kanban/internal/model"
	"github.com/KenzoKasahara/go_kanban_webapp_hands_on/go-kanban/internal/service"
)

// RequireAuth はCookieのSessionからUserを特定し、Contextへ載せる。
//
// next を http.Handler として受け取るので、handler package を import しなくても
// handler を呼べる。どの handler を渡すかは app が決める。
func RequireAuth(auth *service.AuthService, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(httpx.SessionCookieName)
		if err != nil {
			httpx.RespondError(w, r, model.ErrUnauthorized)
			return
		}

		user, err := auth.Authenticate(r.Context(), cookie.Value)
		if err != nil {
			httpx.RespondError(w, r, err)
			return
		}

		// アクセスログへ user_id を載せる。
		if fields, ok := httpx.LogFieldsFrom(r.Context()); ok {
			fields.UserID = user.ID
		}

		next.ServeHTTP(w, r.WithContext(httpx.WithUser(r.Context(), user)))
	})
}
