package handler

import (
	"net/http"

	"example.com/go-kanban/internal/httpx"
	"example.com/go-kanban/internal/service"
)

// DebugHandler は Part 2・Part 3 の検証専用。
// 検証が終わったら、app.go のルーティングと一緒に削除する。
type DebugHandler struct {
	debug *service.DebugService
}

func NewDebugHandler(debug *service.DebugService) *DebugHandler {
	return &DebugHandler{debug: debug}
}

// UnsafeSearch は GET /debug/unsafe-search/{id}?q=...
// 文字列連結で SQL を組み立てる検索を呼ぶ。
func (h *DebugHandler) UnsafeSearch(w http.ResponseWriter, r *http.Request) {
	user, ok := currentUser(w, r)
	if !ok {
		return
	}

	projectID, err := httpx.PathID(r, "id")
	if err != nil {
		httpx.RespondError(w, r, err)
		return
	}

	tasks, err := h.debug.SearchUnsafe(r.Context(), user.ID, projectID, r.URL.Query().Get("q"))
	if err != nil {
		httpx.RespondError(w, r, err)
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"_warn": "this endpoint is intentionally vulnerable",
		"count": len(tasks),
		"tasks": tasks,
	})
}

// NPlusOne は GET /debug/nplus1/{id}
// N+1 版と JOIN 版の Query 数と所要時間を返す。
func (h *DebugHandler) NPlusOne(w http.ResponseWriter, r *http.Request) {
	user, ok := currentUser(w, r)
	if !ok {
		return
	}

	projectID, err := httpx.PathID(r, "id")
	if err != nil {
		httpx.RespondError(w, r, err)
		return
	}

	result, err := h.debug.CompareNPlusOne(r.Context(), user.ID, projectID)
	if err != nil {
		httpx.RespondError(w, r, err)
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"naive_queries": result.NaiveQueries,
		"naive_ms":      result.NaiveDuration.Milliseconds(),
		"join_queries":  result.JoinQueries,
		"join_ms":       result.JoinDuration.Milliseconds(),
		"rows":          result.Rows,
		"same_rows":     result.SameRows,
	})
}
