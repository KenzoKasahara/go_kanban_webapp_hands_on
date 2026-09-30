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

// ChangeStatus は Status 変更の業務ルールをまとめて適用する。
//
// Handler ではなく Service に置く理由:
// CLI・Batch・別APIから同じ操作を行っても、同じルールが適用されるようにするため。
func (s *TaskService) ChangeStatus(
	ctx context.Context,
	userID, taskID int64,
	newStatus string,
	version int,
) (model.Task, error) {
	if !model.IsValidStatus(newStatus) {
		return model.Task{}, model.Invalid("status must be one of: todo, doing, done")
	}

	// 認可: アクセスできないTaskは 404 として扱われる。
	current, role, err := s.tasks.FindForUser(ctx, taskID, userID)
	if err != nil {
		return model.Task{}, err
	}

	if !model.CanWriteTask(role) {
		return model.Task{}, model.ErrForbidden
	}

	// 競合検出は業務ルール判定より先に行う。
	//
	// 逆順にすると、他Requestが先に doing へ変えた直後の Request は
	// 「doing から doing へは遷移できない」という 400 になる。
	// 利用者にとっての事実は「手元の情報が古い」なので 409 を返し、
	// 再読み込みを促す。
	if current.Version != version {
		return model.Task{}, model.Public(model.ErrConflict,
			"task was updated by another request; reload and retry")
	}

	// 業務ルール: 許可された遷移かどうか。
	if !model.CanTransition(current.Status, newStatus) {
		return model.Task{}, model.Invalid(
			"cannot change status from " + current.Status + " to " + newStatus)
	}

	// 同時更新検出: 読んだ version のまま更新できるか。
	updated, err := s.tasks.UpdateStatusWithHistory(
		ctx, taskID, userID, current.Status, newStatus, version)
	if err != nil {
		return model.Task{}, err
	}

	// 通知の失敗で Task 更新を失敗にしない。
	// DBは既に Commit 済みで、ここで error を返すと利用者は
	// 「失敗した」と判断して再送し、二重処理の原因になる。
	if s.notifier != nil {
		if err := s.notifier.TaskStatusChanged(ctx, updated, current.Status); err != nil {
			s.logger.WarnContext(ctx, "notification failed",
				slog.Int64("task_id", updated.ID),
				slog.String("error", err.Error()),
			)
		}
	}

	return updated, nil
}
