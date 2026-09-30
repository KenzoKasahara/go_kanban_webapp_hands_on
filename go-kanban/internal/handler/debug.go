package handler

import (
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/KenzoKasahara/go_kanban_webapp_hands_on/go-kanban/internal/httpx"
	"github.com/KenzoKasahara/go_kanban_webapp_hands_on/go-kanban/internal/model"
)

// SlowQuery は Timeout を再現するための検証用Endpoint。
// pg_sleep により、指定秒数だけDB側で待つ。
func SlowQuery(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		seconds := r.URL.Query().Get("seconds")
		if seconds == "" {
			seconds = "3"
		}

		// 検証用でも入力は確認する。上限を設けないと、長時間 DB 接続を占有できてしまう。
		if s, err := strconv.ParseFloat(seconds, 64); err != nil || s < 0 || s > 10 {
			httpx.RespondError(w, r, model.Invalid("seconds must be a number between 0 and 10"))
			return
		}

		start := time.Now()

		var result int

		// Context は Handler から Repository、さらに DB Driver まで伝播する。
		// Timeout すると pgx が Query をキャンセルする。
		err := pool.QueryRow(r.Context(), "SELECT pg_sleep($1::float8), 1", seconds).
			Scan(nil, &result)
		if err != nil {
			httpx.RespondError(w, r, err)
			return
		}

		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"result":      result,
			"elapsed_ms":  time.Since(start).Milliseconds(),
			"slept_for_s": seconds,
		})
	}
}
