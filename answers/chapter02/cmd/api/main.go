package main

import (
	"context"
	"encoding/json"
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

func healthHandler(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

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
	projectID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var input struct {
		Title       string `json:"title"`
		Description string `json:"description"`
		Priority    string `json:"priority"`
	}

	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if input.Priority == "" {
		input.Priority = "medium"
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
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusCreated, task)
}

func listTasksHandler(w http.ResponseWriter, r *http.Request) {
	projectID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	rows, err := pool.Query(
		r.Context(),
		`SELECT id, project_id, title, description, priority, status, version
		 FROM tasks WHERE project_id = $1 ORDER BY id`,
		projectID,
	)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
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
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		tasks = append(tasks, task)
	}

	writeJSON(w, http.StatusOK, tasks)
}

func getTaskHandler(w http.ResponseWriter, r *http.Request) {
	taskID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
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
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
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
