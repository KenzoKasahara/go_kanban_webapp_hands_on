package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"

	"github.com/jackc/pgx/v5"
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

// createProjectHandler はこの章では書き換えない。Chapter 04 で丸ごと置き換える。
func createProjectHandler(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name string `json:"name"`
	}

	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var project Project

	err := pool.QueryRow(
		r.Context(),
		"INSERT INTO projects (name) VALUES ($1) RETURNING id, name",
		input.Name,
	).Scan(&project.ID, &project.Name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusCreated, project)
}

func createTaskHandler(w http.ResponseWriter, r *http.Request) {
	projectID, err := pathID(r, "id")
	if err != nil {
		respondError(w, err)
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
	projectID, err := pathID(r, "id")
	if err != nil {
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
	taskID, err := pathID(r, "id")
	if err != nil {
		respondError(w, err)
		return
	}

	var task Task

	err = pool.QueryRow(
		r.Context(),
		`SELECT id, project_id, title, description, priority, status, version
		 FROM tasks WHERE id = $1`,
		taskID,
	).Scan(
		&task.ID, &task.ProjectID, &task.Title,
		&task.Description, &task.Priority, &task.Status, &task.Version,
	)

	// pgx.ErrNoRows を「存在しない」という業務上の意味へ翻訳する。
	if errors.Is(err, pgx.ErrNoRows) {
		respondError(w, ErrNotFound)
		return
	}

	if err != nil {
		respondError(w, fmt.Errorf("query task: %w", err))
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
	mux.HandleFunc("GET /health", healthHandler)
	mux.HandleFunc("POST /projects", createProjectHandler)
	mux.HandleFunc("POST /projects/{id}/tasks", createTaskHandler)
	mux.HandleFunc("GET /projects/{id}/tasks", listTasksHandler)
	mux.HandleFunc("GET /tasks/{id}", getTaskHandler)

	log.Println("server started on :8080")

	if err := http.ListenAndServe(":8080", mux); err != nil {
		log.Fatal(err)
	}
}
