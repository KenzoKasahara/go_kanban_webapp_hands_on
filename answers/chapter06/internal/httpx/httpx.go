// Package httpx は handler と middleware が共有する HTTP の共通処理を置く。
package httpx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"

	"example.com/go-kanban/internal/model"
)

// --- Request Context ---------------------------------------------------
//
// handler も middleware も、この package を経由して Context を読み書きする。
// middleware が handler を import すると、handler -> middleware との間で
// import cycle になるため、共有部分をここへ集約する。

const SessionCookieName = "kanban_session"

// contextKey は他packageのkeyと衝突しないよう独自型にする。
type contextKey string

const userContextKey contextKey = "user"

func WithUser(ctx context.Context, user model.User) context.Context {
	return context.WithValue(ctx, userContextKey, user)
}

// CurrentUser は認証middlewareが載せた User を取り出す。
func CurrentUser(ctx context.Context) (model.User, bool) {
	user, ok := ctx.Value(userContextKey).(model.User)
	return user, ok
}

// --- Request -----------------------------------------------------------

// DecodeJSON は未知のfieldを拒否する。
// typoした field 名が黙って無視されると、利用者は「送ったのに反映されない」状態になる。
func DecodeJSON(r *http.Request, dst any) error {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(dst); err != nil {
		return model.Invalid("request body is not valid JSON: " + err.Error())
	}

	return nil
}

func PathID(r *http.Request, name string) (int64, error) {
	raw := r.PathValue(name)

	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		return 0, model.Invalid(fmt.Sprintf("%s must be a positive integer", name))
	}

	return id, nil
}

// --- Response ----------------------------------------------------------

func WriteJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	if err := json.NewEncoder(w).Encode(body); err != nil {
		slog.Error("encode response", slog.String("error", err.Error()))
	}
}

type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type errorEnvelope struct {
	Error errorBody `json:"error"`
}

func writeErrorBody(w http.ResponseWriter, status int, code, message string) {
	WriteJSON(w, status, errorEnvelope{
		Error: errorBody{Code: code, Message: message},
	})
}

// publicMessage は err に利用者向け文言が付いていればそれを、
// 無ければ fallback を返す。
func publicMessage(err error, fallback string) string {
	var publicErr *model.PublicError

	if errors.As(err, &publicErr) {
		return publicErr.PublicMessage()
	}

	return fallback
}

// RespondError は内部 error を HTTP Status へ変換する唯一の場所。
func RespondError(w http.ResponseWriter, r *http.Request, err error) {
	var validationErr *model.ValidationError

	switch {
	case errors.As(err, &validationErr):
		writeErrorBody(w, http.StatusBadRequest, "invalid_request", validationErr.Message)
	case errors.Is(err, model.ErrUnauthorized):
		writeErrorBody(w, http.StatusUnauthorized, "unauthorized", "authentication required")
	case errors.Is(err, model.ErrForbidden):
		writeErrorBody(w, http.StatusForbidden, "forbidden", "operation not allowed")
	case errors.Is(err, model.ErrNotFound):
		writeErrorBody(w, http.StatusNotFound, "not_found",
			publicMessage(err, "resource not found"))
	case errors.Is(err, model.ErrConflict):
		writeErrorBody(w, http.StatusConflict, "conflict",
			publicMessage(err, "resource was updated by another request"))
	default:
		slog.ErrorContext(r.Context(), "unexpected error", slog.String("error", err.Error()))
		writeErrorBody(w, http.StatusInternalServerError, "internal_error", "internal server error")
	}
}
