package handler

import (
	"net/http"

	"github.com/KenzoKasahara/go_kanban_webapp_hands_on/go-kanban/internal/httpx"
	"github.com/KenzoKasahara/go_kanban_webapp_hands_on/go-kanban/internal/service"
)

type ProjectHandler struct {
	projects *service.ProjectService
}

func NewProjectHandler(projects *service.ProjectService) *ProjectHandler {
	return &ProjectHandler{projects: projects}
}

type createProjectRequest struct {
	Name string `json:"name"`
}

func (h *ProjectHandler) Create(w http.ResponseWriter, r *http.Request) {
	user, ok := currentUser(w, r)
	if !ok {
		return
	}

	var req createProjectRequest

	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.RespondError(w, r, err)
		return
	}

	project, err := h.projects.Create(r.Context(), user.ID, req.Name)
	if err != nil {
		httpx.RespondError(w, r, err)
		return
	}

	httpx.WriteJSON(w, http.StatusCreated, project)
}

type addMemberRequest struct {
	UserID int64  `json:"user_id"`
	Role   string `json:"role"`
}

func (h *ProjectHandler) AddMember(w http.ResponseWriter, r *http.Request) {
	user, ok := currentUser(w, r)
	if !ok {
		return
	}

	projectID, err := httpx.PathID(r, "id")
	if err != nil {
		httpx.RespondError(w, r, err)
		return
	}

	var req addMemberRequest

	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.RespondError(w, r, err)
		return
	}

	if err := h.projects.AddMember(r.Context(), user.ID, projectID, req.UserID, req.Role); err != nil {
		httpx.RespondError(w, r, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
