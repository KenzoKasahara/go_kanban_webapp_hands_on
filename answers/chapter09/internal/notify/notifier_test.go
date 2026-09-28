package notify_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"example.com/go-kanban/internal/model"
	"example.com/go-kanban/internal/notify"
)

// testConfig は Test が速く終わるよう、待機時間を短くした設定。
// Client Timeout は 200ms にする（TestTimeoutIsRetryable の前提）。
func testConfig(baseURL string) notify.Config {
	return notify.Config{
		BaseURL:     baseURL,
		Timeout:     200 * time.Millisecond,
		MaxAttempts: 3,
		BaseDelay:   10 * time.Millisecond,
	}
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// 外部APIの挙動は httptest.Server で再現する。
// 実際の外部サービスへ接続しないため、Testが速く、相手の状態にも依存しない。
func TestRetryBehavior(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		status       int
		wantAttempts int32
		wantErr      bool
	}{
		{"success on first attempt", http.StatusOK, 1, false},
		{"400 is not retried", http.StatusBadRequest, 1, true},
		{"404 is not retried", http.StatusNotFound, 1, true},
		{"429 is retried", http.StatusTooManyRequests, 3, true},
		{"500 is retried", http.StatusInternalServerError, 3, true},
		{"503 is retried", http.StatusServiceUnavailable, 3, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var attempts int32

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				atomic.AddInt32(&attempts, 1)
				w.WriteHeader(tt.status)
			}))
			defer server.Close()

			notifier := notify.NewHTTPNotifier(testConfig(server.URL), discardLogger())

			err := notifier.TaskStatusChanged(context.Background(), model.Task{ID: 1}, "todo")

			if tt.wantErr && err == nil {
				t.Fatal("expected error, got nil")
			}

			if !tt.wantErr && err != nil {
				t.Fatalf("expected success, got %v", err)
			}

			if got := atomic.LoadInt32(&attempts); got != tt.wantAttempts {
				t.Fatalf("attempts = %d, want %d", got, tt.wantAttempts)
			}
		})
	}
}

// 一時障害から復旧したら、Retry の途中でも成功として終わることを確認する。
func TestRetrySucceedsAfterTransientFailure(t *testing.T) {
	t.Parallel()

	var attempts int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 2回目までは 503、3回目で復旧する。
		if atomic.AddInt32(&attempts, 1) < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}

		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	notifier := notify.NewHTTPNotifier(testConfig(server.URL), discardLogger())

	if err := notifier.TaskStatusChanged(context.Background(), model.Task{ID: 1}, "todo"); err != nil {
		t.Fatalf("expected success after transient failure, got %v", err)
	}

	if got := atomic.LoadInt32(&attempts); got != 3 {
		t.Fatalf("attempts = %d, want 3", got)
	}
}

// 相手が応答しない場合、Client側のTimeoutで打ち切れることを確認する。
func TestTimeoutIsRetryable(t *testing.T) {
	t.Parallel()

	var attempts int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)
		time.Sleep(500 * time.Millisecond) // Client Timeout(200ms) より長い
	}))
	defer server.Close()

	notifier := notify.NewHTTPNotifier(testConfig(server.URL), discardLogger())

	start := time.Now()
	err := notifier.TaskStatusChanged(context.Background(), model.Task{ID: 1}, "todo")
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected timeout error")
	}

	if got := atomic.LoadInt32(&attempts); got != 3 {
		t.Fatalf("attempts = %d, want 3 (timeout should be retried)", got)
	}

	// 3回 x 200ms程度で終わる。相手のsleep(500ms x 3)を待っていない。
	if elapsed > 1200*time.Millisecond {
		t.Fatalf("took %v; client timeout does not seem to work", elapsed)
	}
}

// Context がキャンセルされたら、待機中でも即座に諦めることを確認する。
func TestRetryStopsOnContextCancel(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	cfg := testConfig(server.URL)
	cfg.BaseDelay = 2 * time.Second // 待機中にキャンセルされる状況を作る

	notifier := notify.NewHTTPNotifier(cfg, discardLogger())

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := notifier.TaskStatusChanged(ctx, model.Task{ID: 1}, "todo")

	if err == nil {
		t.Fatal("expected error")
	}

	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("took %v; retry ignored context cancellation", elapsed)
	}
}
