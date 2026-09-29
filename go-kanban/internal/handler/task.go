package handler

import (
	"net/http"

	"github.com/KenzoKasahara/go_kanban_webapp_hands_on/go-kanban/internal/httpx"
	"github.com/KenzoKasahara/go_kanban_webapp_hands_on/go-kanban/internal/model"
	"github.com/KenzoKasahara/go_kanban_webapp_hands_on/go-kanban/internal/service"
)

type TaskHandler struct {
	tasks *service.TaskService
}

func NewTaskHandler(tasks *service.TaskService) *TaskHandler {
	return &TaskHandler{tasks: tasks}
}

// createTaskRequest は HTTP の表現。model.CreateTaskInput とは分ける。
// JSON のキー名を変えても、Service や Repository へ波及しない。
type createTaskRequest struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	Priority    string `json:"priority"`
}

func (h *TaskHandler) Create(w http.ResponseWriter, r *http.Request) {
	user, ok := currentUser(w, r)
	if !ok {
		return
	}

	projectID, err := httpx.PathID(r, "id")
	if err != nil {
		httpx.RespondError(w, r, err)
		return
	}

	var req createTaskRequest

	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.RespondError(w, r, err)
		return
	}

	task, err := h.tasks.Create(r.Context(), user.ID, model.CreateTaskInput{
		ProjectID:   projectID,
		Title:       req.Title,
		Description: req.Description,
		Priority:    req.Priority,
	})
	if err != nil {
		httpx.RespondError(w, r, err)
		return
	}

	httpx.WriteJSON(w, http.StatusCreated, task)
}

// ListByProject は ?q= があればタイトルで絞り込む。
func (h *TaskHandler) ListByProject(w http.ResponseWriter, r *http.Request) {
	user, ok := currentUser(w, r)
	if !ok {
		return
	}

	projectID, err := httpx.PathID(r, "id")
	if err != nil {
		httpx.RespondError(w, r, err)
		return
	}

	tasks, err := h.tasks.List(r.Context(), user.ID, projectID, r.URL.Query().Get("q"))
	if err != nil {
		httpx.RespondError(w, r, err)
		return
	}

	httpx.WriteJSON(w, http.StatusOK, tasks)
}

func (h *TaskHandler) Get(w http.ResponseWriter, r *http.Request) {
	user, ok := currentUser(w, r)
	if !ok {
		return
	}

	taskID, err := httpx.PathID(r, "id")
	if err != nil {
		httpx.RespondError(w, r, err)
		return
	}

	task, err := h.tasks.Get(r.Context(), user.ID, taskID)
	if err != nil {
		httpx.RespondError(w, r, err)
		return
	}

	httpx.WriteJSON(w, http.StatusOK, task)
}

// changeStatusRequest の version は、Client が最後に読んだ Task の version。
// 楽観ロックの判定に使う。
type changeStatusRequest struct {
	Status  string `json:"status"`
	Version int    `json:"version"`
}

// ChangeStatus は PATCH /tasks/{id}/status
func (h *TaskHandler) ChangeStatus(w http.ResponseWriter, r *http.Request) {
	user, ok := currentUser(w, r)
	if !ok {
		return
	}

	taskID, err := httpx.PathID(r, "id")
	if err != nil {
		httpx.RespondError(w, r, err)
		return
	}

	var req changeStatusRequest

	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.RespondError(w, r, err)
		return
	}

	task, err := h.tasks.ChangeStatus(r.Context(), user.ID, taskID, req.Status, req.Version)
	if err != nil {
		httpx.RespondError(w, r, err)
		return
	}

	httpx.WriteJSON(w, http.StatusOK, task)
}
