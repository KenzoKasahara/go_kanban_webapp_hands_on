package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"time"

	"github.com/KenzoKasahara/go_kanban_webapp_hands_on/go-kanban/internal/model"
)

// Config は Retry の挙動を決める。
type Config struct {
	BaseURL     string
	Timeout     time.Duration // 1回の試行の上限
	MaxAttempts int           // 初回を含む試行回数
	BaseDelay   time.Duration // 1回目の待機時間
}

func DefaultConfig(baseURL string) Config {
	return Config{
		BaseURL:     baseURL,
		Timeout:     500 * time.Millisecond,
		MaxAttempts: 3,
		BaseDelay:   100 * time.Millisecond,
	}
}

type HTTPNotifier struct {
	cfg    Config
	client *http.Client
	logger *slog.Logger
}

func NewHTTPNotifier(cfg Config, logger *slog.Logger) *HTTPNotifier {
	return &HTTPNotifier{
		cfg: cfg,
		// Timeout の無い http.Client を本番で使わない。
		// 相手が応答しない場合、Goroutine と Connection が滞留し続ける。
		client: &http.Client{Timeout: cfg.Timeout},
		logger: logger,
	}
}

// ErrRetryable は「もう一度試す価値がある」失敗。
var ErrRetryable = errors.New("retryable")

// statusChangedEvent は通知 API へ送る本文。
type statusChangedEvent struct {
	TaskID    int64  `json:"task_id"`
	ProjectID int64  `json:"project_id"`
	OldStatus string `json:"old_status"`
	NewStatus string `json:"new_status"`
}

// TaskStatusChanged は service.Notifier を満たす。
func (n *HTTPNotifier) TaskStatusChanged(ctx context.Context, task model.Task, oldStatus string) error {
	body, err := json.Marshal(statusChangedEvent{
		TaskID:    task.ID,
		ProjectID: task.ProjectID,
		OldStatus: oldStatus,
		NewStatus: task.Status,
	})
	if err != nil {
		return fmt.Errorf("encode notification: %w", err)
	}

	return n.postWithRetry(ctx, n.cfg.BaseURL+"/task-status-changed", body)
}

func (n *HTTPNotifier) postWithRetry(ctx context.Context, url string, body []byte) error {
	var lastErr error

	for attempt := 1; attempt <= n.cfg.MaxAttempts; attempt++ {
		err := n.post(ctx, url, body)
		if err == nil {
			return nil
		}

		lastErr = err

		// Retryしても結果が変わらない失敗は、その場で諦める。
		// 例: 400 は Request 自体が不正なので、何度送っても 400 のまま。
		if !errors.Is(err, ErrRetryable) {
			return err
		}

		if attempt == n.cfg.MaxAttempts {
			break
		}

		delay := n.backoff(attempt)

		n.logger.WarnContext(ctx, "notification retry",
			slog.Int("attempt", attempt),
			slog.Int64("delay_ms", delay.Milliseconds()),
			slog.String("error", err.Error()),
		)

		// 待機中も Context のキャンセルには従う。
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
	}

	return fmt.Errorf("notification failed after %d attempts: %w", n.cfg.MaxAttempts, lastErr)
}

func (n *HTTPNotifier) post(ctx context.Context, url string, body []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := n.client.Do(req)
	if err != nil {
		// 接続失敗・Timeout は一時障害の可能性がある。
		return fmt.Errorf("%w: %v", ErrRetryable, err)
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode < 300:
		return nil
	case resp.StatusCode == http.StatusTooManyRequests,
		resp.StatusCode >= 500:
		return fmt.Errorf("%w: notification returned %d", ErrRetryable, resp.StatusCode)
	default:
		return fmt.Errorf("notification returned %d", resp.StatusCode)
	}
}

// backoff は指数バックオフ + Jitter。
//
// Jitter が無いと、同時に失敗した全Clientが同じタイミングで再送し、
// 復旧しかけた相手を再び落とす（Thundering Herd / Retry Storm）。
func (n *HTTPNotifier) backoff(attempt int) time.Duration {
	base := n.cfg.BaseDelay * time.Duration(1<<(attempt-1))
	jitter := time.Duration(rand.Int64N(int64(base)))

	return base/2 + jitter
}
