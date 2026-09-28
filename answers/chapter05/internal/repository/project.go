package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"example.com/go-kanban/internal/model"
)

type ProjectRepository interface {
	CreateWithOwner(ctx context.Context, name string, ownerID int64) (model.Project, error)
	RoleOf(ctx context.Context, projectID, userID int64) (string, error)
	AddMember(ctx context.Context, projectID, userID int64, role string) error
}

var _ ProjectRepository = (*PgProjectRepository)(nil)

type PgProjectRepository struct {
	pool *pgxpool.Pool
}

func NewProjectRepository(pool *pgxpool.Pool) *PgProjectRepository {
	return &PgProjectRepository{pool: pool}
}

// CreateWithOwner は Project 作成と Owner 登録を1つの Transaction で行う。
// Projectだけ作られてMemberが居ないと、作成者本人すら操作できないProjectが残る。
// Transactionの詳細は Chapter 06 で扱う。
func (r *PgProjectRepository) CreateWithOwner(ctx context.Context, name string, ownerID int64) (model.Project, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return model.Project{}, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	var project model.Project

	err = tx.QueryRow(
		ctx,
		"INSERT INTO projects (name) VALUES ($1) RETURNING id, name",
		name,
	).Scan(&project.ID, &project.Name)
	if err != nil {
		return model.Project{}, fmt.Errorf("insert project: %w", err)
	}

	_, err = tx.Exec(
		ctx,
		"INSERT INTO project_members (project_id, user_id, role) VALUES ($1, $2, $3)",
		project.ID, ownerID, model.RoleOwner,
	)
	if err != nil {
		return model.Project{}, fmt.Errorf("insert project member: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return model.Project{}, fmt.Errorf("commit: %w", err)
	}

	return project, nil
}

// RoleOf は user が project のメンバーかどうかと、その Role を返す。
// メンバーでなければ ErrForbidden。
func (r *PgProjectRepository) RoleOf(ctx context.Context, projectID, userID int64) (string, error) {
	var role string

	err := r.pool.QueryRow(
		ctx,
		"SELECT role FROM project_members WHERE project_id = $1 AND user_id = $2",
		projectID, userID,
	).Scan(&role)

	if errors.Is(err, pgx.ErrNoRows) {
		return "", model.ErrForbidden
	}

	if err != nil {
		return "", fmt.Errorf("query project role: %w", err)
	}

	return role, nil
}

func (r *PgProjectRepository) AddMember(ctx context.Context, projectID, userID int64, role string) error {
	_, err := r.pool.Exec(
		ctx,
		"INSERT INTO project_members (project_id, user_id, role) VALUES ($1, $2, $3)",
		projectID, userID, role,
	)

	// 複合主キー (project_id, user_id) の違反 = すでにメンバー。
	if IsUniqueViolation(err) {
		return model.Public(model.ErrConflict, "user is already a member")
	}

	// users への外部キー違反 = 存在しない User。
	if IsForeignKeyViolation(err) {
		return model.Public(model.ErrNotFound, "user not found")
	}

	if err != nil {
		return fmt.Errorf("insert project member: %w", err)
	}

	return nil
}
