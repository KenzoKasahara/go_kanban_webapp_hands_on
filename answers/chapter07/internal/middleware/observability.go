package middleware

import (
	"context"
	"net/http"
	"time"
)

// このファイルには Chapter 08 で RequestID と AccessLog を追加する。

// Timeout は Request 全体の上限時間を設定する。
// Client が待っていない処理をServer側で続けないための仕組み。
func Timeout(d time.Duration, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), d)
		defer cancel()

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
