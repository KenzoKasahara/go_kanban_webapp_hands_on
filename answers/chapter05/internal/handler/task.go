package handler

import (
	"net/http"

	"example.com/go-kanban/internal/httpx"
	"example.com/go-kanban/internal/model"
	"example.com/go-kanban/internal/service"
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
