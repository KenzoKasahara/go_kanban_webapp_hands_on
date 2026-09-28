package service_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"example.com/go-kanban/internal/model"
	"example.com/go-kanban/internal/repository"
	"example.com/go-kanban/internal/service"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// fakeTaskRepo は DB を使わずに Service の判断だけを検証するための実装。
// Repository が interface だから差し替えられる。
type fakeTaskRepo struct {
	task            model.Task
	role            string
	findErr         error
	updateErr       error
	updateCallCount int
}

var _ repository.TaskRepository = (*fakeTaskRepo)(nil)

func (f *fakeTaskRepo) FindForUser(context.Context, int64, int64) (model.Task, string, error) {
	if f.findErr != nil {
		return model.Task{}, "", f.findErr
	}

	return f.task, f.role, nil
}

func (f *fakeTaskRepo) UpdateStatusWithHistory(
	_ context.Context,
	_, _ int64,
	_, newStatus string,
	_ int,
) (model.Task, error) {
	f.updateCallCount++

	if f.updateErr != nil {
		return model.Task{}, f.updateErr
	}

	updated := f.task
	updated.Status = newStatus
	updated.Version++

	return updated, nil
}

// 以下は interface を満たすための実装。このテストでは呼ばれない。

func (f *fakeTaskRepo) Create(context.Context, model.CreateTaskInput) (model.Task, error) {
	return model.Task{}, errors.New("not implemented")
}

func (f *fakeTaskRepo) ListByProject(context.Context, int64) ([]model.Task, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeTaskRepo) Search(context.Context, int64, string) ([]model.Task, error) {
	return nil, errors.New("not implemented")
}

// fakeProjectRepo は RoleOf が決まった Role を返すだけの実装。
type fakeProjectRepo struct {
	role string
}

var _ repository.ProjectRepository = (*fakeProjectRepo)(nil)

func (f *fakeProjectRepo) RoleOf(context.Context, int64, int64) (string, error) {
	return f.role, nil
}

func (f *fakeProjectRepo) CreateWithOwner(context.Context, string, int64) (model.Project, error) {
	return model.Project{}, errors.New("not implemented")
}

func (f *fakeProjectRepo) AddMember(context.Context, int64, int64, string) error {
	return errors.New("not implemented")
}

func TestChangeStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name              string
		currentStatus     string
		newStatus         string
		role              string
		version           int
		findErr           error
		wantErr           error
		wantValidationErr bool
		wantUpdate        bool
	}{
		{
			name:          "member can move todo to doing",
			currentStatus: model.StatusTodo,
			newStatus:     model.StatusDoing,
			role:          model.RoleMember,
			wantUpdate:    true,
		},
		{
			name:          "viewer cannot change status",
			currentStatus: model.StatusTodo,
			newStatus:     model.StatusDoing,
			role:          model.RoleViewer,
			wantErr:       model.ErrForbidden,
		},
		{
			name:              "todo to done is rejected by business rule",
			currentStatus:     model.StatusTodo,
			newStatus:         model.StatusDone,
			role:              model.RoleOwner,
			wantValidationErr: true,
		},
		{
			name:              "unknown status is rejected before reading the task",
			currentStatus:     model.StatusTodo,
			newStatus:         "archived",
			role:              model.RoleOwner,
			wantValidationErr: true,
		},
		{
			name:          "inaccessible task looks like not found",
			currentStatus: model.StatusTodo,
			newStatus:     model.StatusDoing,
			role:          model.RoleOwner,
			findErr:       model.ErrNotFound,
			wantErr:       model.ErrNotFound,
		},
		{
			// Chapter 06 で見つけた順序の問題。
			// version が古ければ、遷移ルールより先に 409 になる。
			name:          "stale version is a conflict, not a validation error",
			currentStatus: model.StatusDoing,
			newStatus:     model.StatusDoing,
			role:          model.RoleMember,
			version:       2,
			wantErr:       model.ErrConflict,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tasks := &fakeTaskRepo{
				task:    model.Task{ID: 1, ProjectID: 1, Status: tt.currentStatus, Version: 1},
				role:    tt.role,
				findErr: tt.findErr,
			}

			version := tt.version
			if version == 0 {
				version = 1
			}

			svc := service.NewTaskService(tasks, &fakeProjectRepo{role: tt.role}, nil, discardLogger())

			_, err := svc.ChangeStatus(context.Background(), 1, 1, tt.newStatus, version)

			switch {
			case tt.wantUpdate:
				if err != nil {
					t.Fatalf("expected success, got %v", err)
				}
			case tt.wantValidationErr:
				var validationErr *model.ValidationError
				if !errors.As(err, &validationErr) {
					t.Fatalf("expected ValidationError, got %v", err)
				}
			default:
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("expected %v, got %v", tt.wantErr, err)
				}
			}

			// 拒否されたRequestで DB 更新が呼ばれていないことも確認する。
			if !tt.wantUpdate && tasks.updateCallCount != 0 {
				t.Fatalf("repository must not be called when rejected, got %d calls",
					tasks.updateCallCount)
			}
		})
	}
}

// 通知の失敗で Task 更新を失敗にしないことを確認する。
type failingNotifier struct{ called bool }

func (n *failingNotifier) TaskStatusChanged(context.Context, model.Task, string) error {
	n.called = true
	return errors.New("notification service is down")
}

func TestChangeStatusSucceedsWhenNotificationFails(t *testing.T) {
	t.Parallel()

	tasks := &fakeTaskRepo{
		task: model.Task{ID: 1, ProjectID: 1, Status: model.StatusTodo, Version: 1},
		role: model.RoleMember,
	}
	notifier := &failingNotifier{}

	svc := service.NewTaskService(tasks, &fakeProjectRepo{role: model.RoleMember},
		notifier, discardLogger())

	task, err := svc.ChangeStatus(context.Background(), 1, 1, model.StatusDoing, 1)
	if err != nil {
		t.Fatalf("notification failure must not fail the request: %v", err)
	}

	if !notifier.called {
		t.Fatal("notifier was not called")
	}

	if task.Status != model.StatusDoing {
		t.Fatalf("status = %q, want %q", task.Status, model.StatusDoing)
	}
}

// Repository の失敗（Commit 前の失敗）は、そのまま呼び出し元へ返す。
// 通知は Commit に成功したときだけ送る。
func TestChangeStatusDoesNotNotifyWhenUpdateFails(t *testing.T) {
	t.Parallel()

	updateErr := errors.New("insert task history: check constraint")

	tasks := &fakeTaskRepo{
		task:      model.Task{ID: 1, ProjectID: 1, Status: model.StatusTodo, Version: 1},
		role:      model.RoleMember,
		updateErr: updateErr,
	}
	notifier := &failingNotifier{}

	svc := service.NewTaskService(tasks, &fakeProjectRepo{role: model.RoleMember},
		notifier, discardLogger())

	_, err := svc.ChangeStatus(context.Background(), 1, 1, model.StatusDoing, 1)
	if !errors.Is(err, updateErr) {
		t.Fatalf("expected update error, got %v", err)
	}

	if notifier.called {
		t.Fatal("notifier must not be called when the update failed")
	}
}
