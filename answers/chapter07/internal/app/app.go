package app

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"example.com/go-kanban/internal/handler"
	"example.com/go-kanban/internal/middleware"
	"example.com/go-kanban/internal/notify"
	"example.com/go-kanban/internal/repository"
	"example.com/go-kanban/internal/service"
)

// Config は環境によって変わる設定。
type Config struct {
	// SecureCookie は Cookie に Secure 属性を付けるか。本番(HTTPS)では true にする。
	SecureCookie bool

	// RequestTimeout は Request 全体の上限時間。
	// ロードバランサなど、外側の Timeout より短くする。
	RequestTimeout time.Duration

	// NotifyURL は通知 API の Base URL。空なら通知しない。
	NotifyURL string

	// SlowQueryEnable は Timeout 検証用の GET /debug/slow を登録するか。
	// 任意の時間 DB 接続を占有できるので、本番では無効にする。
	SlowQueryEnable bool
}

func DefaultConfig() Config {
	return Config{
		SecureCookie:    false,
		RequestTimeout:  2 * time.Second,
		NotifyURL:       "",
		SlowQueryEnable: false,
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
	idempotencyRepo := repository.NewIdempotencyRepository(pool)

	// Notifier
	// interface 型の変数に nil の *HTTPNotifier を入れると「nil ではない interface」になり、
	// Service の `s.notifier != nil` をすり抜ける。未設定時は interface のまま nil にする。
	var notifier service.Notifier
	if cfg.NotifyURL != "" {
		notifier = notify.NewHTTPNotifier(notify.DefaultConfig(cfg.NotifyURL), logger)
	}

	// Service
	authService := service.NewAuthService(userRepo)
	taskService := service.NewTaskService(taskRepo, projectRepo, notifier, logger)
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

	// 再送されうる作成系のみ Idempotency を有効にする。
	idempotent := func(h http.HandlerFunc) http.Handler {
		return middleware.RequireAuth(authService,
			middleware.Idempotency(idempotencyRepo, logger, h))
	}

	mux.Handle("POST /projects", requireAuth(projectHandler.Create))
	mux.Handle("POST /projects/{id}/members", requireAuth(projectHandler.AddMember))
	mux.Handle("POST /projects/{id}/tasks", idempotent(taskHandler.Create))
	mux.Handle("GET /projects/{id}/tasks", requireAuth(taskHandler.ListByProject))
	mux.Handle("GET /tasks/{id}", requireAuth(taskHandler.Get))

	// PATCH は version による楽観ロックで再送から守られている（Chapter 06）。
	mux.Handle("PATCH /tasks/{id}/status", requireAuth(taskHandler.ChangeStatus))

	// Timeout の検証用（Part 1 Step 2）。
	if cfg.SlowQueryEnable {
		mux.Handle("GET /debug/slow", requireAuth(handler.SlowQuery(pool)))
	}

	// Middleware は外側から順に適用される。
	//
	//   Timeout -> mux
	//
	// Chapter 08 で RequestID と AccessLog を Timeout の外側へ追加する。
	var h http.Handler = mux
	h = middleware.Timeout(cfg.RequestTimeout, h)

	return h
}
