package handler

import (
	"net/http"

	"example.com/go-kanban/internal/httpx"
	"example.com/go-kanban/internal/model"
	"example.com/go-kanban/internal/service"
)

type AuthHandler struct {
	auth         *service.AuthService
	secureCookie bool
}

// NewAuthHandler の secureCookie は、本番(HTTPS)では true にする。
func NewAuthHandler(auth *service.AuthService, secureCookie bool) *AuthHandler {
	return &AuthHandler{auth: auth, secureCookie: secureCookie}
}

type credentialsRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (req credentialsRequest) toInput() model.Credentials {
	return model.Credentials{Email: req.Email, Password: req.Password}
}

func (h *AuthHandler) Register(w http.ResponseWriter, r *http.Request) {
	var req credentialsRequest

	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.RespondError(w, r, err)
		return
	}

	user, err := h.auth.Register(r.Context(), req.toInput())
	if err != nil {
		httpx.RespondError(w, r, err)
		return
	}

	httpx.WriteJSON(w, http.StatusCreated, user)
}

func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	var req credentialsRequest

	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.RespondError(w, r, err)
		return
	}

	session, err := h.auth.Login(r.Context(), req.toInput())
	if err != nil {
		httpx.RespondError(w, r, err)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     httpx.SessionCookieName,
		Value:    session.ID,
		Path:     "/",
		Expires:  session.ExpiresAt,
		HttpOnly: true,                 // JavaScriptから読めなくする
		Secure:   h.secureCookie,       // 本番(HTTPS)では true にする
		SameSite: http.SameSiteLaxMode, // 他サイトからの自動送信を抑制する
	})

	// ログインの成果物は Body ではなく Set-Cookie ヘッダにある。
	w.WriteHeader(http.StatusNoContent)
}

func (h *AuthHandler) Logout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(httpx.SessionCookieName); err == nil {
		if err := h.auth.Logout(r.Context(), cookie.Value); err != nil {
			httpx.RespondError(w, r, err)
			return
		}
	}

	http.SetCookie(w, &http.Cookie{
		Name:     httpx.SessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   h.secureCookie,
		SameSite: http.SameSiteLaxMode,
	})

	w.WriteHeader(http.StatusNoContent)
}
