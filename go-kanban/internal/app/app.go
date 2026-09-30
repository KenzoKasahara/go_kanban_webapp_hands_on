package app

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/KenzoKasahara/go_kanban_webapp_hands_on/go-kanban/internal/handler"
	"github.com/KenzoKasahara/go_kanban_webapp_hands_on/go-kanban/internal/middleware"
	"github.com/KenzoKasahara/go_kanban_webapp_hands_on/go-kanban/internal/notify"
	"github.com/KenzoKasahara/go_kanban_webapp_hands_on/go-kanban/internal/repository"
	"github.com/KenzoKasahara/go_kanban_webapp_hands_on/go-kanban/internal/service"
)

// Config は環境によって変わる設定。
type Config struct {
	// SecureCookie は Cookie に Secure 属性を付けるか。本番(HTTPS)では true にする。
	SecureCookie bool

	// DebugRoutes は検証用エンドポイント（GET /debug/slow）を登録するか。
	// 既定では登録しないので、消し忘れても公開されない。
	DebugRoutes bool

	// RequestTimeout は Request 全体の上限時間。
	RequestTimeout time.Duration

	// NotifyURL は通知 API の URL。空なら通知しない。
	NotifyURL string
}

func DefaultConfig() Config {
	return Config{
		SecureCookie:   false,
		DebugRoutes:    false,
		RequestTimeout: 2 * time.Second,
		NotifyURL:      "",
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
	// 変数は interface 型で宣言する。*notify.HTTPNotifier 型の nil を渡すと
	// interface としては nil にならず、Service の nil 判定をすり抜ける。
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
	mux.Handle("PATCH /tasks/{id}/status", requireAuth(taskHandler.ChangeStatus))

	// 検証用。DEBUG_ROUTES=1 で起動したときだけ登録する。
	if cfg.DebugRoutes {
		mux.Handle("GET /debug/slow", requireAuth(handler.SlowQuery(pool)))
	}

	// Middleware は外側から順に適用される。
	// Chapter 08 で、Timeout のさらに外側へ RequestID と AccessLog を追加する。
	var h http.Handler = mux
	h = middleware.Timeout(cfg.RequestTimeout, h)
	h = middleware.AccessLog(logger, h)
	h = middleware.RequestID(h)

	return h
}
