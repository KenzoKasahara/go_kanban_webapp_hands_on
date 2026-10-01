package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Task struct {
	ID          int64  `json:"id"`
	ProjectID   int64  `json:"project_id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Priority    string `json:"priority"`
	Status      string `json:"status"`
	Version     int    `json:"version"`
}

type Project struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

var pool *pgxpool.Pool

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	if err := json.NewEncoder(w).Encode(body); err != nil {
		log.Printf("encode response: %v", err)
	}
}

// decodeJSON は未知のフィールドを拒否する。
// typo したフィールド名が黙って無視されると、利用者は「送ったのに反映されない」状態になる。
func decodeJSON(r *http.Request, dst any) error {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(dst); err != nil {
		return &ValidationError{Message: "request body is not valid JSON: " + err.Error()}
	}

	return nil
}

func pathID(r *http.Request, name string) (int64, error) {
	raw := r.PathValue(name)

	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		return 0, &ValidationError{Message: fmt.Sprintf("%s must be a positive integer", name)}
	}

	return id, nil
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func createProjectHandler(w http.ResponseWriter, r *http.Request) {
	user, ok := currentUser(r.Context())
	if !ok {
		respondError(w, ErrUnauthorized)
		return
	}

	var input struct {
		Name string `json:"name"`
	}

	if err := decodeJSON(r, &input); err != nil {
		respondError(w, err)
		return
	}

	// Project作成とOwner登録は「両方成功」か「両方失敗」でなければならない。
	// Projectだけ作られてMemberが居ないと、作成者本人すら操作できないProjectが残る。
	// Transactionの詳細は Chapter 06 で扱う。
	tx, err := pool.Begin(r.Context())
	if err != nil {
		respondError(w, fmt.Errorf("begin tx: %w", err))
		return
	}
	defer tx.Rollback(r.Context())

	var project Project

	err = tx.QueryRow(
		r.Context(),
		"INSERT INTO projects (name) VALUES ($1) RETURNING id, name",
		input.Name,
	).Scan(&project.ID, &project.Name)
	if err != nil {
		respondError(w, fmt.Errorf("insert project: %w", err))
		return
	}

	_, err = tx.Exec(
		r.Context(),
		"INSERT INTO project_members (project_id, user_id, role) VALUES ($1, $2, $3)",
		project.ID, user.ID, RoleOwner,
	)
	if err != nil {
		respondError(w, fmt.Errorf("insert project member: %w", err))
		return
	}

	if err := tx.Commit(r.Context()); err != nil {
		respondError(w, fmt.Errorf("commit: %w", err))
		return
	}

	writeJSON(w, http.StatusCreated, project)
}

func addMemberHandler(w http.ResponseWriter, r *http.Request) {
	user, ok := currentUser(r.Context())
	if !ok {
		respondError(w, ErrUnauthorized)
		return
	}

	projectID, err := pathID(r, "id")
	if err != nil {
		respondError(w, err)
		return
	}

	role, err := projectRole(r.Context(), projectID, user.ID)
	if err != nil {
		respondError(w, err)
		return
	}

	if !canManageMembers(role) {
		respondError(w, ErrForbidden)
		return
	}

	var input struct {
		UserID int64  `json:"user_id"`
		Role   string `json:"role"`
	}

	if err := decodeJSON(r, &input); err != nil {
		respondError(w, err)
		return
	}

	// DB の CHECK 制約でも弾けるが、その場合は 500 になる。先に 400 として返す。
	if !isValidRole(input.Role) {
		respondError(w, &ValidationError{Message: "role must be one of owner, member, viewer"})
		return
	}

	_, err = pool.Exec(
		r.Context(),
		"INSERT INTO project_members (project_id, user_id, role) VALUES ($1, $2, $3)",
		projectID, input.UserID, input.Role,
	)

	// 複合主キー (project_id, user_id) の違反 = すでにメンバー。
	if isUniqueViolation(err) {
		respondError(w, publicError(ErrConflict, "user is already a member"))
		return
	}

	// users への外部キー違反 = 存在しない User。
	if isForeignKeyViolation(err) {
		respondError(w, publicError(ErrNotFound, "user not found"))
		return
	}

	if err != nil {
		respondError(w, fmt.Errorf("insert project member: %w", err))
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func createTaskHandler(w http.ResponseWriter, r *http.Request) {
	user, ok := currentUser(r.Context())
	if !ok {
		respondError(w, ErrUnauthorized)
		return
	}

	projectID, err := pathID(r, "id")
	if err != nil {
		respondError(w, err)
		return
	}

	// 「誰か」は requireAuth が確定させた。ここで確認するのは「してよいか」。
	role, err := projectRole(r.Context(), projectID, user.ID)
	if err != nil {
		respondError(w, err)
		return
	}

	if !canWriteTask(role) {
		respondError(w, ErrForbidden)
		return
	}

	var input CreateTaskRequest

	if err := decodeJSON(r, &input); err != nil {
		respondError(w, err)
		return
	}

	input.Normalize()

	if err := input.Validate(); err != nil {
		respondError(w, err)
		return
	}

	var task Task

	err = pool.QueryRow(
		r.Context(),
		`INSERT INTO tasks (project_id, title, description, priority)
		 VALUES ($1, $2, $3, $4)
		 RETURNING id, project_id, title, description, priority, status, version`,
		projectID, input.Title, input.Description, input.Priority,
	).Scan(
		&task.ID, &task.ProjectID, &task.Title,
		&task.Description, &task.Priority, &task.Status, &task.Version,
	)

	// project が存在しない場合、DBは外部キー違反を返す。
	// これは Server の不具合ではなく「指定された project が無い」という利用者向けの情報。
	if isForeignKeyViolation(err) {
		respondError(w, ErrNotFound)
		return
	}

	if err != nil {
		respondError(w, fmt.Errorf("insert task: %w", err))
		return
	}

	writeJSON(w, http.StatusCreated, task)
}

func listTasksHandler(w http.ResponseWriter, r *http.Request) {
	user, ok := currentUser(r.Context())
	if !ok {
		respondError(w, ErrUnauthorized)
		return
	}

	projectID, err := pathID(r, "id")
	if err != nil {
		respondError(w, err)
		return
	}

	// メンバーかどうかだけを確認する。Viewer でも一覧は読める。
	if _, err := projectRole(r.Context(), projectID, user.ID); err != nil {
		respondError(w, err)
		return
	}

	rows, err := pool.Query(
		r.Context(),
		`SELECT id, project_id, title, description, priority, status, version
		 FROM tasks WHERE project_id = $1 ORDER BY id`,
		projectID,
	)
	if err != nil {
		respondError(w, fmt.Errorf("query tasks: %w", err))
		return
	}
	defer rows.Close()

	tasks := []Task{}

	for rows.Next() {
		var task Task

		if err := rows.Scan(
			&task.ID, &task.ProjectID, &task.Title,
			&task.Description, &task.Priority, &task.Status, &task.Version,
		); err != nil {
			respondError(w, fmt.Errorf("scan task: %w", err))
			return
		}

		tasks = append(tasks, task)
	}

	// ループを抜けた理由がエラーでないかを確認する。
	if err := rows.Err(); err != nil {
		respondError(w, fmt.Errorf("iterate tasks: %w", err))
		return
	}

	writeJSON(w, http.StatusOK, tasks)
}

func getTaskHandler(w http.ResponseWriter, r *http.Request) {
	user, ok := currentUser(r.Context())
	if !ok {
		respondError(w, ErrUnauthorized)
		return
	}

	taskID, err := pathID(r, "id")
	if err != nil {
		respondError(w, err)
		return
	}

	task, _, err := findTaskForUser(r.Context(), taskID, user.ID)
	if err != nil {
		respondError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, task)
}

func main() {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://kanban:local-dev-password@localhost:5432/kanban"
	}

	var err error

	pool, err = pgxpool.New(context.Background(), dsn)
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()

	if err := pool.Ping(context.Background()); err != nil {
		log.Fatal(err)
	}

	mux := http.NewServeMux()

	// 認証不要
	mux.HandleFunc("GET /health", healthHandler)
	mux.HandleFunc("POST /users", createUserHandler)
	mux.HandleFunc("POST /login", loginHandler)
	mux.HandleFunc("POST /logout", logoutHandler)

	// 認証必須
	mux.HandleFunc("POST /projects", requireAuth(createProjectHandler))
	mux.HandleFunc("POST /projects/{id}/members", requireAuth(addMemberHandler))
	mux.HandleFunc("POST /projects/{id}/tasks", requireAuth(createTaskHandler))
	mux.HandleFunc("GET /projects/{id}/tasks", requireAuth(listTasksHandler))
	mux.HandleFunc("GET /tasks/{id}", requireAuth(getTaskHandler))

	log.Println("server started on :8980")

	if err := http.ListenAndServe(":8980", mux); err != nil {
		log.Fatal(err)
	}
}
