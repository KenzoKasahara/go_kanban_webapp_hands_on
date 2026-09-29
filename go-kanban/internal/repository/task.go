package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/KenzoKasahara/go_kanban_webapp_hands_on/go-kanban/internal/model"
)

// TaskRepository は Service が必要とする振る舞いだけを宣言する。
// Service は PostgreSQL も pgx も知らない。
type TaskRepository interface {
	Create(ctx context.Context, in model.CreateTaskInput) (model.Task, error)
	FindForUser(ctx context.Context, taskID, userID int64) (model.Task, string, error)
	ListByProject(ctx context.Context, projectID int64) ([]model.Task, error)
	Search(ctx context.Context, projectID int64, keyword string) ([]model.Task, error)
}

// コンパイル時に「PgTaskRepository は TaskRepository を満たすか」を確認する。
// 満たさなくなったら、利用箇所ではなくここで失敗する。
var _ TaskRepository = (*PgTaskRepository)(nil)

// taskColumns は Scan 順と SELECT 順のズレを防ぐために1か所へまとめる。
const taskColumns = `id, project_id, title, description, priority, status, version, assignee_id`

type PgTaskRepository struct {
	pool *pgxpool.Pool
}

func NewTaskRepository(pool *pgxpool.Pool) *PgTaskRepository {
	return &PgTaskRepository{pool: pool}
}

func scanTask(row pgx.Row) (model.Task, error) {
	var task model.Task

	err := row.Scan(
		&task.ID, &task.ProjectID, &task.Title, &task.Description,
		&task.Priority, &task.Status, &task.Version, &task.AssigneeID,
	)

	return task, err
}

// collectTasks は複数行の結果を Task のスライスにする。
// 0件のときも nil ではなく空スライスを返し、JSON で null にならないようにする。
func collectTasks(rows pgx.Rows) ([]model.Task, error) {
	defer rows.Close()

	tasks := []model.Task{}

	for rows.Next() {
		task, err := scanTask(rows)
		if err != nil {
			return nil, fmt.Errorf("scan task: %w", err)
		}

		tasks = append(tasks, task)
	}

	// ループを抜けた理由がエラーでないかを確認する。
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate tasks: %w", err)
	}

	return tasks, nil
}

func (r *PgTaskRepository) Create(ctx context.Context, in model.CreateTaskInput) (model.Task, error) {
	row := r.pool.QueryRow(
		ctx,
		`INSERT INTO tasks (project_id, title, description, priority)
		 VALUES ($1, $2, $3, $4)
		 RETURNING `+taskColumns,
		in.ProjectID, in.Title, in.Description, in.Priority,
	)

	task, err := scanTask(row)

	if IsForeignKeyViolation(err) {
		return model.Task{}, model.Public(model.ErrNotFound, "project not found")
	}

	if err != nil {
		return model.Task{}, fmt.Errorf("insert task: %w", err)
	}

	return task, nil
}

// FindForUser は「存在するか」ではなく
// 「このUserから見てアクセス可能か」を1つのQueryで判断する。
func (r *PgTaskRepository) FindForUser(ctx context.Context, taskID, userID int64) (model.Task, string, error) {
	var (
		task model.Task
		role string
	)

	err := r.pool.QueryRow(
		ctx,
		`SELECT t.id, t.project_id, t.title, t.description,
		        t.priority, t.status, t.version, t.assignee_id, pm.role
		 FROM tasks t
		 JOIN project_members pm ON pm.project_id = t.project_id
		 WHERE t.id = $1 AND pm.user_id = $2`,
		taskID, userID,
	).Scan(
		&task.ID, &task.ProjectID, &task.Title, &task.Description,
		&task.Priority, &task.Status, &task.Version, &task.AssigneeID, &role,
	)

	// 存在しないTaskと他人のTaskを区別せず ErrNotFound にする。
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Task{}, "", model.ErrNotFound
	}

	if err != nil {
		return model.Task{}, "", fmt.Errorf("query task for user: %w", err)
	}

	return task, role, nil
}

func (r *PgTaskRepository) ListByProject(ctx context.Context, projectID int64) ([]model.Task, error) {
	rows, err := r.pool.Query(
		ctx,
		`SELECT `+taskColumns+` FROM tasks WHERE project_id = $1 ORDER BY id`,
		projectID,
	)
	if err != nil {
		return nil, fmt.Errorf("query tasks: %w", err)
	}

	return collectTasks(rows)
}

// Search はキーワードでTaskを絞り込む。
// 値は必ずプレースホルダ($2) で渡し、SQL文と連結しない。
func (r *PgTaskRepository) Search(
	ctx context.Context,
	projectID int64,
	keyword string,
) ([]model.Task, error) {
	rows, err := r.pool.Query(
		ctx,
		`SELECT `+taskColumns+`
		 FROM tasks
		 WHERE project_id = $1 AND title ILIKE '%' || $2 || '%'
		 ORDER BY id`,
		projectID, keyword,
	)
	if err != nil {
		return nil, fmt.Errorf("search tasks: %w", err)
	}

	return collectTasks(rows)
}
