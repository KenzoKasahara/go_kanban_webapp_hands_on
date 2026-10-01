//go:build integration

package test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/KenzoKasahara/go_kanban_webapp_hands_on/go-kanban/internal/model"
)

// 認証・認可・IDOR を、実際のHTTPとDBを通して確認する。
func TestAuthorizationScenario(t *testing.T) {
	server, _ := newTestServer(t)

	alice := newClient(t, server.URL)
	bob := newClient(t, server.URL)

	alice.mustStatus(http.StatusCreated, http.MethodPost, "/users",
		map[string]string{"email": "alice@example.com", "password": "alice-password-1"})
	bobUser := decode[idResponse](t, bob.mustStatus(http.StatusCreated, http.MethodPost, "/users",
		map[string]string{"email": "bob@example.com", "password": "bob-password-123"}))

	// 未認証では作成できない。
	alice.mustStatus(http.StatusUnauthorized, http.MethodPost, "/projects",
		map[string]string{"name": "before login"})

	alice.mustStatus(http.StatusNoContent, http.MethodPost, "/login",
		map[string]string{"email": "alice@example.com", "password": "alice-password-1"})
	bob.mustStatus(http.StatusNoContent, http.MethodPost, "/login",
		map[string]string{"email": "bob@example.com", "password": "bob-password-123"})

	project := decode[idResponse](t, alice.mustStatus(
		http.StatusCreated, http.MethodPost, "/projects",
		map[string]string{"name": "Alice Board"}))

	task := decode[idResponse](t, alice.mustStatus(
		http.StatusCreated, http.MethodPost, projectPath(project.ID, "/tasks"),
		map[string]string{"title": "secret task", "priority": "high"}))

	// 本人は読める。
	alice.mustStatus(http.StatusOK, http.MethodGet, taskPath(task.ID, ""), nil)

	// 他人からは「存在しない」ように見える。403 を返すと、そのIDのTaskが
	// 存在することを攻撃者へ教えることになる。
	bob.mustStatus(http.StatusNotFound, http.MethodGet, taskPath(task.ID, ""), nil)

	// 存在しない Task も同じ 404。区別がつかないことが大事。
	bob.mustStatus(http.StatusNotFound, http.MethodGet, taskPath(task.ID+1000, ""), nil)

	// Project外のUserは一覧も作成もできない。
	bob.mustStatus(http.StatusForbidden, http.MethodGet, projectPath(project.ID, "/tasks"), nil)
	bob.mustStatus(http.StatusForbidden, http.MethodPost, projectPath(project.ID, "/tasks"),
		map[string]string{"title": "intruder", "priority": "low"})

	// Viewer として追加されると、読めるが書けない。
	alice.mustStatus(http.StatusNoContent, http.MethodPost, projectPath(project.ID, "/members"),
		map[string]any{"user_id": bobUser.ID, "role": model.RoleViewer})

	bob.mustStatus(http.StatusOK, http.MethodGet, taskPath(task.ID, ""), nil)
	bob.mustStatus(http.StatusForbidden, http.MethodPost, projectPath(project.ID, "/tasks"),
		map[string]string{"title": "intruder", "priority": "low"})
	bob.mustStatus(http.StatusForbidden, http.MethodPatch, taskPath(task.ID, "/status"),
		map[string]any{"status": "doing", "version": 1})

	// Owner 以外は Member を追加できない。
	bob.mustStatus(http.StatusForbidden, http.MethodPost, projectPath(project.ID, "/members"),
		map[string]any{"user_id": bobUser.ID, "role": model.RoleOwner})

	// Logout後はSessionが無効になる。
	alice.mustStatus(http.StatusNoContent, http.MethodPost, "/logout", nil)
	alice.mustStatus(http.StatusUnauthorized, http.MethodGet, taskPath(task.ID, ""), nil)
}

// Task更新と履歴追加が、同時に成功するか同時に失敗するかを確認する。
func TestStatusChangeWritesHistoryAtomically(t *testing.T) {
	server, pool := newTestServer(t)

	alice := newClient(t, server.URL)
	alice.registerAndLogin("alice@example.com", "alice-password-1")

	project := decode[idResponse](t, alice.mustStatus(
		http.StatusCreated, http.MethodPost, "/projects",
		map[string]string{"name": "Alice Board"}))

	task := decode[idResponse](t, alice.mustStatus(
		http.StatusCreated, http.MethodPost, projectPath(project.ID, "/tasks"),
		map[string]string{"title": "write docs", "priority": "high"}))

	// 許可されない遷移は 400 で、DBは変わらない。
	alice.mustStatus(http.StatusBadRequest, http.MethodPatch, taskPath(task.ID, "/status"),
		map[string]any{"status": "done", "version": 1})

	assertTaskStatus(t, pool, task.ID, model.StatusTodo, 1)
	assertHistoryCount(t, pool, task.ID, 0)

	// 許可された遷移は 200 で、Task と History が両方増える。
	alice.mustStatus(http.StatusOK, http.MethodPatch, taskPath(task.ID, "/status"),
		map[string]any{"status": "doing", "version": 1})

	assertTaskStatus(t, pool, task.ID, model.StatusDoing, 2)
	assertHistoryCount(t, pool, task.ID, 1)

	// 古い version では更新できない（Lost Update の防止）。
	alice.mustStatus(http.StatusConflict, http.MethodPatch, taskPath(task.ID, "/status"),
		map[string]any{"status": "done", "version": 1})

	assertTaskStatus(t, pool, task.ID, model.StatusDoing, 2)
	assertHistoryCount(t, pool, task.ID, 1)

	// 正しい version なら続けて更新できる。
	alice.mustStatus(http.StatusOK, http.MethodPatch, taskPath(task.ID, "/status"),
		map[string]any{"status": "done", "version": 2})

	assertTaskStatus(t, pool, task.ID, model.StatusDone, 3)
	assertHistoryCount(t, pool, task.ID, 2)
}

// 同じ Idempotency-Key の再送が二重作成にならないことを確認する。
func TestIdempotentTaskCreation(t *testing.T) {
	server, pool := newTestServer(t)

	alice := newClient(t, server.URL)
	alice.registerAndLogin("alice@example.com", "alice-password-1")

	project := decode[idResponse](t, alice.mustStatus(
		http.StatusCreated, http.MethodPost, "/projects",
		map[string]string{"name": "Alice Board"}))

	body := map[string]string{"title": "pay invoice", "priority": "high"}
	path := projectPath(project.ID, "/tasks")

	first := decode[idResponse](t, alice.mustStatusWithKey(
		http.StatusCreated, http.MethodPost, path, body, "key-123"))
	second := decode[idResponse](t, alice.mustStatusWithKey(
		http.StatusCreated, http.MethodPost, path, body, "key-123"))

	if first.ID != second.ID {
		t.Fatalf("retry created a new task: %d != %d", first.ID, second.ID)
	}

	assertTaskCount(t, pool, project.ID, 1)

	// Key が違えば別の処理として扱われる。
	alice.mustStatusWithKey(http.StatusCreated, http.MethodPost, path, body, "key-456")

	assertTaskCount(t, pool, project.ID, 2)

	// 他人が同じ Key を使っても、alice の Response は返らない。
	// bob は Project のメンバーではないので、通常どおり処理されて 403 になる。
	bob := newClient(t, server.URL)
	bob.registerAndLogin("bob@example.com", "bob-password-123")
	bob.mustStatusWithKey(http.StatusForbidden, http.MethodPost, path, body, "key-123")

	assertTaskCount(t, pool, project.ID, 2)
}

// Validation が HTTP の 400 として返り、DB に何も書かれないことを確認する。
// 境界値そのものは model の Unit Test で見ているので、ここでは経路だけを確認する。
func TestValidationThroughHTTP(t *testing.T) {
	server, pool := newTestServer(t)

	alice := newClient(t, server.URL)

	// 12文字未満のパスワードでは登録できない。
	alice.mustStatus(http.StatusBadRequest, http.MethodPost, "/users",
		map[string]string{"email": "alice@example.com", "password": "short"})

	alice.registerAndLogin("alice@example.com", "alice-password-1")

	// 同じメールアドレスでは登録できない。
	alice.mustStatus(http.StatusConflict, http.MethodPost, "/users",
		map[string]string{"email": "alice@example.com", "password": "alice-password-1"})

	project := decode[idResponse](t, alice.mustStatus(
		http.StatusCreated, http.MethodPost, "/projects",
		map[string]string{"name": "Alice Board"}))

	path := projectPath(project.ID, "/tasks")

	tests := []struct {
		name string
		body any
	}{
		{"whitespace title", map[string]string{"title": "   ", "priority": "high"}},
		{"title too long", map[string]string{"title": strings.Repeat("あ", model.MaxTitleLength+1)}},
		{"invalid priority", map[string]string{"title": "ok", "priority": "SUPER_HIGH"}},
		{"unknown field", map[string]string{"title": "ok", "priorty": "high"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := decode[errorResponse](t, alice.mustStatus(
				http.StatusBadRequest, http.MethodPost, path, tt.body))

			if resp.Error.Code != "invalid_request" {
				t.Fatalf("error code = %q, want invalid_request", resp.Error.Code)
			}
		})
	}

	// Path の id が数値でなければ 400。
	alice.mustStatus(http.StatusBadRequest, http.MethodGet, "/tasks/abc", nil)

	// 不正な Status は 400。
	alice.mustStatus(http.StatusBadRequest, http.MethodPatch, taskPath(1, "/status"),
		map[string]any{"status": "archived", "version": 1})

	assertTaskCount(t, pool, project.ID, 0)
}
