package app

import (
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"example.com/go-kanban/internal/handler"
	"example.com/go-kanban/internal/middleware"
	"example.com/go-kanban/internal/repository"
	"example.com/go-kanban/internal/service"
)

// Config は環境によって変わる設定。
type Config struct {
	// SecureCookie は Cookie に Secure 属性を付けるか。本番(HTTPS)では true にする。
	SecureCookie bool
}

func DefaultConfig() Config {
	return Config{
		SecureCookie: false,
	}
}

// New は依存関係を組み立てて http.Handler を返す。
//
// 組み立てを1か所へ集めると、各層は「自分が必要とするもの」だけを
// 引数で受け取れる。Test では別の実装を差し込める。
func New(pool *pgxpool.Pool, logger *slog.Logger, cfg Config) http.Handler {
	// Repository
	taskRepo := repository.NewTaskRepository(pool)
	userRepo := repository.NewUserRepository(pool)
	projectRepo := repository.NewProjectRepository(pool)

	// Service
	// Notifier の実装は Chapter 07 で作る。それまでは nil を渡す。
	authService := service.NewAuthService(userRepo)
	taskService := service.NewTaskService(taskRepo, projectRepo, nil, logger)
	projectService := service.NewProjectService(projectRepo)

	// Handler
	authHandler := handler.NewAuthHandler(authService, cfg.SecureCookie)
	taskHandler := handler.NewTaskHandler(taskService)
	projectHandler := handler.NewProjectHandler(projectService)

	mux := http.NewServeMux()

	// 認証不要
	mux.HandleFunc("GET /health", handler.Health)
	mux.HandleFunc("POST /users", authHandler.Register)
	mux.HandleFunc("POST /login", authHandler.Login)
	mux.HandleFunc("POST /logout", authHandler.Logout)

	// 認証必須。requireAuth を1か所で包むことで、登録漏れによる認証抜けを防ぐ。
	requireAuth := func(h http.HandlerFunc) http.Handler {
		return middleware.RequireAuth(authService, h)
	}

	mux.Handle("POST /projects", requireAuth(projectHandler.Create))
	mux.Handle("POST /projects/{id}/members", requireAuth(projectHandler.AddMember))
	mux.Handle("POST /projects/{id}/tasks", requireAuth(taskHandler.Create))
	mux.Handle("GET /projects/{id}/tasks", requireAuth(taskHandler.ListByProject))
	mux.Handle("GET /tasks/{id}", requireAuth(taskHandler.Get))
	mux.Handle("PATCH /tasks/{id}/status", requireAuth(taskHandler.ChangeStatus))

	return mux
}
