package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// Role は Project 内での権限を表す。
const (
	RoleOwner  = "owner"
	RoleMember = "member"
	RoleViewer = "viewer"
)

// canWriteTask は Task を作成・更新できる Role かどうかを判断する。
func canWriteTask(role string) bool {
	return role == RoleOwner || role == RoleMember
}

// canManageMembers は Member を追加できる Role かどうかを判断する。
func canManageMembers(role string) bool {
	return role == RoleOwner
}

// isValidRole は外部から受け取った role 文字列が定義済みの値かを判断する。
func isValidRole(role string) bool {
	switch role {
	case RoleOwner, RoleMember, RoleViewer:
		return true
	default:
		return false
	}
}

// projectRole は user が project のメンバーかどうかと、その Role を返す。
// メンバーでなければ ErrForbidden。
func projectRole(ctx context.Context, projectID, userID int64) (string, error) {
	var role string

	err := pool.QueryRow(
		ctx,
		"SELECT role FROM project_members WHERE project_id = $1 AND user_id = $2",
		projectID, userID,
	).Scan(&role)

	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrForbidden
	}

	if err != nil {
		return "", fmt.Errorf("query project role: %w", err)
	}

	return role, nil
}

// findTaskForUser は「Taskが存在するか」ではなく
// 「このUserから見てアクセス可能なTaskか」をひとつのQueryで判断する。
//
// WHERE t.id = $1 だけで取得してから権限を確認する実装にすると、
// 確認を忘れた経路がそのまま IDOR / BOLA になる。
func findTaskForUser(ctx context.Context, taskID, userID int64) (Task, string, error) {
	var (
		task Task
		role string
	)

	err := pool.QueryRow(
		ctx,
		`SELECT t.id, t.project_id, t.title, t.description,
		        t.priority, t.status, t.version, pm.role
		 FROM tasks t
		 JOIN project_members pm ON pm.project_id = t.project_id
		 WHERE t.id = $1 AND pm.user_id = $2`,
		taskID, userID,
	).Scan(
		&task.ID, &task.ProjectID, &task.Title, &task.Description,
		&task.Priority, &task.Status, &task.Version, &role,
	)

	// 存在しないTaskと、他人のTaskを同じ 404 で返す。
	// 403 で返すと「そのIDのTaskは存在する」ことを攻撃者へ教えることになる。
	if errors.Is(err, pgx.ErrNoRows) {
		return Task{}, "", ErrNotFound
	}

	if err != nil {
		return Task{}, "", fmt.Errorf("query task for user: %w", err)
	}

	return task, role, nil
}
