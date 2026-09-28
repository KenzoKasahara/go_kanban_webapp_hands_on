package middleware

import (
	"bytes"
	"errors"
	"log/slog"
	"net/http"

	"example.com/go-kanban/internal/httpx"
	"example.com/go-kanban/internal/model"
	"example.com/go-kanban/internal/repository"
)

const IdempotencyKeyHeader = "Idempotency-Key"

// captureWriter は Response を記録しつつ Client へも書き込む。
type captureWriter struct {
	http.ResponseWriter
	status int
	body   bytes.Buffer
}

func (w *captureWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *captureWriter) Write(b []byte) (int, error) {
	w.body.Write(b)
	return w.ResponseWriter.Write(b)
}

// Idempotency は同じ Idempotency-Key の再送に対して、
// 処理をやり直さず前回の結果を返す。
//
// RequireAuth の内側に置く。Key を利用者ごとに分離するため、User が必要になる。
func Idempotency(
	repo *repository.IdempotencyRepository,
	logger *slog.Logger,
	next http.Handler,
) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get(IdempotencyKeyHeader)
		if key == "" {
			next.ServeHTTP(w, r)
			return
		}

		user, ok := httpx.CurrentUser(r.Context())
		if !ok {
			httpx.RespondError(w, r, model.ErrUnauthorized)
			return
		}

		// Key は利用者ごとに分離する。
		// 共有すると、他人のKeyを指定して他人のResponseを読めてしまう。
		endpoint := r.Method + " " + r.URL.Path

		stored, err := repo.Find(r.Context(), key, user.ID, endpoint)

		switch {
		case err == nil:
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Idempotent-Replay", "true")
			w.WriteHeader(stored.StatusCode)

			if _, err := w.Write([]byte(stored.Body)); err != nil {
				logger.ErrorContext(r.Context(), "write replayed response",
					slog.String("error", err.Error()))
			}

			return
		case !errors.Is(err, repository.ErrNoStoredResponse):
			httpx.RespondError(w, r, err)
			return
		}

		capture := &captureWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(capture, r)

		// 成功した結果だけ保存する。
		// 失敗まで保存すると、一時障害による失敗が永続化される。
		if capture.status >= 200 && capture.status < 300 {
			if err := repo.Save(r.Context(), key, user.ID, endpoint, repository.StoredResponse{
				StatusCode: capture.status,
				Body:       capture.body.String(),
			}); err != nil {
				logger.ErrorContext(r.Context(), "save idempotency key",
					slog.String("error", err.Error()))
			}
		}
	})
}
