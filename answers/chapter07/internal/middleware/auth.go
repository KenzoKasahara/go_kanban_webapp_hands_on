package middleware

import (
	"net/http"

	"example.com/go-kanban/internal/httpx"
	"example.com/go-kanban/internal/model"
	"example.com/go-kanban/internal/service"
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

		next.ServeHTTP(w, r.WithContext(httpx.WithUser(r.Context(), user)))
	})
}
