package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"example.com/go-kanban/internal/app"
)

func main() {
	// 構造化ログ。JSONで出すと、検索・集計・アラート設定がしやすい。
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	if err := run(logger); err != nil {
		logger.Error("server stopped with error", slog.String("error", err.Error()))
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://kanban:local-dev-password@localhost:5432/kanban"
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return err
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		return err
	}

	cfg := app.DefaultConfig()
	// 通知 API の宛先。未設定なら通知しない。
	cfg.NotifyURL = os.Getenv("NOTIFY_URL")
	// Timeout の検証中だけ DEBUG_ROUTES=1 で起動する。
	cfg.SlowQueryEnable = os.Getenv("DEBUG_ROUTES") == "1"

	server := &http.Server{
		Addr:              ":8080",
		Handler:           app.New(pool, logger, cfg),
		ReadHeaderTimeout: 5 * time.Second,
	}

	serverErr := make(chan error, 1)

	go func() {
		logger.Info("server started",
			slog.String("addr", server.Addr),
			slog.Duration("request_timeout", cfg.RequestTimeout),
			slog.String("notify_url", cfg.NotifyURL),
		)

		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
	}()

	select {
	case err := <-serverErr:
		return err
	case <-ctx.Done():
		// 処理中のRequestを終わらせてから止める。
		// いきなり落とすと、Commit直前の処理が中断される。
		logger.Info("shutdown signal received")

		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		return server.Shutdown(shutdownCtx)
	}
}
