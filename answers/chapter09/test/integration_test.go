//go:build integration

// Integration Test は実際の PostgreSQL に対して実行する。
// 通常の `go test ./...` では実行されない。
//
//	go test -tags=integration ./test/...
//
// 各 Test の最初に全テーブルを TRUNCATE する。
// 開発用の DB を消さないよう、既定では kanban_test DB に接続する。
package test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"example.com/go-kanban/internal/app"
	"example.com/go-kanban/internal/middleware"
)

const defaultTestDSN = "postgres://kanban:local-dev-password@localhost:5432/kanban_test"

func newTestServer(t *testing.T) (*httptest.Server, *pgxpool.Pool) {
	t.Helper()

	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		dsn = defaultTestDSN
	}

	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect db: %v", err)
	}

	// Test間で状態が残らないよう、毎回初期化する。
	_, err = pool.Exec(context.Background(),
		`TRUNCATE tasks, project_members, projects, sessions, users,
		 task_history, idempotency_keys RESTART IDENTITY CASCADE`)
	if err != nil {
		pool.Close()
		t.Fatalf("truncate: %v (Test 用 DB の kanban_test を作成したか確認する)", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	cfg := app.DefaultConfig()
	cfg.DebugRoutes = false

	server := httptest.NewServer(app.New(pool, logger, cfg))

	t.Cleanup(func() {
		server.Close()
		pool.Close()
	})

	return server, pool
}

// --- HTTP Client -------------------------------------------------------

type client struct {
	t       *testing.T
	baseURL string
	http    *http.Client
}

func newJar() (http.CookieJar, error) {
	return cookiejar.New(nil)
}

func newClient(t *testing.T, baseURL string) *client {
	t.Helper()

	// Cookie Jar を持たせると、Login後のSession Cookieが自動で送られる。
	jar, err := newJar()
	if err != nil {
		t.Fatalf("cookie jar: %v", err)
	}

	return &client{t: t, baseURL: baseURL, http: &http.Client{Jar: jar}}
}

// do は Request を送り、Status と Body を返す。body が nil なら Body を送らない。
func (c *client) do(method, path string, body any, headers map[string]string) (int, []byte) {
	c.t.Helper()

	var reader io.Reader

	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			c.t.Fatalf("encode request body: %v", err)
		}

		reader = bytes.NewReader(raw)
	}

	req, err := http.NewRequest(method, c.baseURL+path, reader)
	if err != nil {
		c.t.Fatalf("build request: %v", err)
	}

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		c.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		c.t.Fatalf("read response body: %v", err)
	}

	return resp.StatusCode, respBody
}

// mustStatus は Status が期待どおりでなければ Test を止める。
// 以降の手順が前の結果に依存するため、Errorf ではなく Fatalf にする。
func (c *client) mustStatus(want int, method, path string, body any) []byte {
	c.t.Helper()

	got, respBody := c.do(method, path, body, nil)
	if got != want {
		c.t.Fatalf("%s %s: status = %d, want %d, body = %s", method, path, got, want, respBody)
	}

	return respBody
}

func (c *client) mustStatusWithKey(want int, method, path string, body any, key string) []byte {
	c.t.Helper()

	got, respBody := c.do(method, path, body, map[string]string{
		middleware.IdempotencyKeyHeader: key,
	})
	if got != want {
		c.t.Fatalf("%s %s (key=%s): status = %d, want %d, body = %s",
			method, path, key, got, want, respBody)
	}

	return respBody
}

// registerAndLogin は User を登録してログインし、登録された User の id を返す。
func (c *client) registerAndLogin(email, password string) int64 {
	c.t.Helper()

	creds := map[string]string{"email": email, "password": password}

	user := decode[idResponse](c.t, c.mustStatus(http.StatusCreated, http.MethodPost, "/users", creds))
	c.mustStatus(http.StatusNoContent, http.MethodPost, "/login", creds)

	return user.ID
}

// --- Response ----------------------------------------------------------

type idResponse struct {
	ID int64 `json:"id"`
}

type errorResponse struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func decode[T any](t *testing.T, body []byte) T {
	t.Helper()

	var v T

	if err := json.Unmarshal(body, &v); err != nil {
		t.Fatalf("decode %T: %v, body = %s", v, err, body)
	}

	return v
}

func projectPath(id int64, suffix string) string {
	return fmt.Sprintf("/projects/%d%s", id, suffix)
}

func taskPath(id int64, suffix string) string {
	return fmt.Sprintf("/tasks/%d%s", id, suffix)
}

// --- DB Assertion ------------------------------------------------------
//
// HTTP の Response だけでなく、DB に何が書かれたかを直接確認する。

func assertTaskStatus(t *testing.T, pool *pgxpool.Pool, taskID int64, wantStatus string, wantVersion int) {
	t.Helper()

	var (
		status  string
		version int
	)

	err := pool.QueryRow(context.Background(),
		"SELECT status, version FROM tasks WHERE id = $1", taskID,
	).Scan(&status, &version)
	if err != nil {
		t.Fatalf("query task %d: %v", taskID, err)
	}

	if status != wantStatus || version != wantVersion {
		t.Fatalf("task %d: status=%q version=%d, want status=%q version=%d",
			taskID, status, version, wantStatus, wantVersion)
	}
}

func assertHistoryCount(t *testing.T, pool *pgxpool.Pool, taskID int64, want int) {
	t.Helper()

	var got int

	err := pool.QueryRow(context.Background(),
		"SELECT count(*) FROM task_history WHERE task_id = $1", taskID,
	).Scan(&got)
	if err != nil {
		t.Fatalf("count history: %v", err)
	}

	if got != want {
		t.Fatalf("task %d: history rows = %d, want %d", taskID, got, want)
	}
}

func assertTaskCount(t *testing.T, pool *pgxpool.Pool, projectID int64, want int) {
	t.Helper()

	var got int

	err := pool.QueryRow(context.Background(),
		"SELECT count(*) FROM tasks WHERE project_id = $1", projectID,
	).Scan(&got)
	if err != nil {
		t.Fatalf("count tasks: %v", err)
	}

	if got != want {
		t.Fatalf("project %d: task rows = %d, want %d", projectID, got, want)
	}
}
