package service

import (
	"context"
	"log/slog"

	"github.com/KenzoKasahara/go_kanban_webapp_hands_on/go-kanban/internal/model"
	"github.com/KenzoKasahara/go_kanban_webapp_hands_on/go-kanban/internal/repository"
)

// Notifier は外部通知の契約。Service は HTTP も Retry も知らない。
// 実装は Chapter 07 で作る。それまでは nil を渡す。
type Notifier interface {
	TaskStatusChanged(ctx context.Context, task model.Task, oldStatus string) error
}

type TaskService struct {
	tasks    repository.TaskRepository
	projects repository.ProjectRepository
	notifier Notifier
	logger   *slog.Logger
}

func NewTaskService(
	tasks repository.TaskRepository,
	projects repository.ProjectRepository,
	notifier Notifier,
	logger *slog.Logger,
) *TaskService {
	return &TaskService{tasks: tasks, projects: projects, notifier: notifier, logger: logger}
}

func (s *TaskService) Create(ctx context.Context, userID int64, in model.CreateTaskInput) (model.Task, error) {
	in.Normalize()

	if err := in.Validate(); err != nil {
		return model.Task{}, err
	}

	role, err := s.projects.RoleOf(ctx, in.ProjectID, userID)
	if err != nil {
		return model.Task{}, err
	}

	if !model.CanWriteTask(role) {
		return model.Task{}, model.ErrForbidden
	}

	return s.tasks.Create(ctx, in)
}

// Get はアクセス可能な Task だけを返す。
// 他人の Task は存在しない Task と同じ ErrNotFound になる。
func (s *TaskService) Get(ctx context.Context, userID, taskID int64) (model.Task, error) {
	task, _, err := s.tasks.FindForUser(ctx, taskID, userID)
	return task, err
}

// List は Project の Task 一覧を返す。keyword があればタイトルで絞り込む。
// メンバーかどうかだけを確認する。Viewer でも一覧は読める。
func (s *TaskService) List(ctx context.Context, userID, projectID int64, keyword string) ([]model.Task, error) {
	if _, err := s.projects.RoleOf(ctx, projectID, userID); err != nil {
		return nil, err
	}

	if keyword == "" {
		return s.tasks.ListByProject(ctx, projectID)
	}

	return s.tasks.Search(ctx, projectID, keyword)
}
