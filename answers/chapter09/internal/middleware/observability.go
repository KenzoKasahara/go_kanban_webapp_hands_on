package middleware

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"time"

	"example.com/go-kanban/internal/httpx"
)

const RequestIDHeader = "X-Request-Id"

// RequestID は1Requestを追跡するためのIDを付与する。
// 障害調査で「このRequestに関係するログだけ」を絞り込むために使う。
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(RequestIDHeader)
		if id == "" {
			id = newRequestID()
		}

		w.Header().Set(RequestIDHeader, id)

		next.ServeHTTP(w, r.WithContext(httpx.WithRequestID(r.Context(), id)))
	})
}

func newRequestID() string {
	buf := make([]byte, 8)

	if _, err := rand.Read(buf); err != nil {
		return "unknown"
	}

	return hex.EncodeToString(buf)
}

// statusRecorder は書き込まれたStatusを記録する。
// http.ResponseWriter からは、書いたStatusを後から読めないため必要。
type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (w *statusRecorder) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusRecorder) Write(b []byte) (int, error) {
	n, err := w.ResponseWriter.Write(b)
	w.bytes += n

	return n, err
}

// AccessLog は1Requestにつき1行の構造化ログを出す。
//
// Cookie や Authorization Header は意図的に出力しない。
// ログは長期間保存され、閲覧範囲も広いため、機密情報を書くと漏洩範囲が広がる。
func AccessLog(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

		// 内側のmiddlewareが user_id を書き込めるよう、先に入れ物を用意する。
		ctx, fields := httpx.WithLogFields(r.Context())

		next.ServeHTTP(recorder, r.WithContext(ctx))

		attrs := []slog.Attr{
			slog.String("request_id", httpx.RequestIDFrom(ctx)),
			slog.String("method", r.Method),
			// r.URL.String() はクエリ文字列を含む。Path だけを出す。
			slog.String("path", r.URL.Path),
			slog.Int("status", recorder.status),
			slog.Int64("duration_ms", time.Since(start).Milliseconds()),
			slog.Int("bytes", recorder.bytes),
		}

		if fields.UserID != 0 {
			attrs = append(attrs, slog.Int64("user_id", fields.UserID))
		}

		level := slog.LevelInfo
		if recorder.status >= http.StatusInternalServerError {
			level = slog.LevelError
		}

		logger.LogAttrs(ctx, level, "http_request", attrs...)
	})
}

// Timeout は Request 全体の上限時間を設定する。
// Client が待っていない処理をServer側で続けないための仕組み。
func Timeout(d time.Duration, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), d)
		defer cancel()

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
