package service

import (
	"context"

	"github.com/KenzoKasahara/go_kanban_webapp_hands_on/go-kanban/internal/model"
	"github.com/KenzoKasahara/go_kanban_webapp_hands_on/go-kanban/internal/repository"
)

type ProjectService struct {
	projects repository.ProjectRepository
}

func NewProjectService(projects repository.ProjectRepository) *ProjectService {
	return &ProjectService{projects: projects}
}

// Create は Project を作り、作成者を Owner として登録する。
func (s *ProjectService) Create(ctx context.Context, userID int64, name string) (model.Project, error) {
	return s.projects.CreateWithOwner(ctx, name, userID)
}

// AddMember は Owner だけが実行できる。
func (s *ProjectService) AddMember(ctx context.Context, userID, projectID, targetUserID int64, role string) error {
	current, err := s.projects.RoleOf(ctx, projectID, userID)
	if err != nil {
		return err
	}

	if !model.CanManageMembers(current) {
		return model.ErrForbidden
	}

	// DB の CHECK 制約でも弾けるが、その場合は 500 になる。先に 400 として返す。
	if !model.IsValidRole(role) {
		return model.Invalid("role must be one of owner, member, viewer")
	}

	return s.projects.AddMember(ctx, projectID, targetUserID, role)
}
