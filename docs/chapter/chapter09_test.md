# Chapter 09: Test と Refactoring

## この章の目的

ここまで実機で確認してきた挙動を、繰り返し自動で検証できる形に固定する。

4種類のテストを、それぞれ何に使うのかを区別して書く。

| 種類 | 何を検証するか | DB | 速度 |
|---|---|:---:|---|
| Unit Test | 業務ルール単体 | 不要 | ミリ秒 |
| Service Test（Fake 使用） | Service の判断 | 不要 | ミリ秒 |
| HTTP / Integration Test | HTTP から DB まで通した挙動 | 必要 | 秒 |
| Load Test | 負荷増加時の劣化 | 必要 | 分 |

最後に、ここまでの実装を振り返って責務を整理する。

## 現在地

本番対応(06-08) → **Test(09)**

## 完了条件

- [ ] `go test ./...` が通る
- [ ] `go test -race ./...` が通る
- [ ] Integration Test が実 DB に対して通る
- [ ] 状態遷移の全パターンがテストされている
- [ ] Retry の挙動がテストされている
- [ ] k6 で負荷をかけ、p95 / p99 を読める

## テストピラミッド

![下から Unit / Service / Integration / Load の4層。下ほど速く数を多く書け、上ほど遅いが実際の構成に近い](../images/chapter09_test/test_pyramid.svg)

下ほど速く、数を多く書ける。上ほど遅いが、実際の構成に近い。**同じことを複数の層でテストしない。**

---

## Part 1. Unit Test

### Step 1. 状態遷移をテストする

#### やること

`model.CanTransition` の全パターンをテストする。既知の3状態どうしの組み合わせ9通りと、未知の状態を渡した2通りを並べる。

#### 実行

`internal/model/task_test.go`。

```go
package model_test

import (
	"testing"

	"example.com/go-kanban/internal/model"
)

// Table Driven Test
// 条件を slice へまとめ、同じ検証処理を繰り返す。
// 条件の追加が1行で済み、どの条件が落ちたかも名前で分かる。
func TestCanTransition(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		from string
		to   string
		want bool
	}{
		{"todo to doing", model.StatusTodo, model.StatusDoing, true},
		{"doing to done", model.StatusDoing, model.StatusDone, true},
		{"doing back to todo", model.StatusDoing, model.StatusTodo, true},
		{"done back to doing", model.StatusDone, model.StatusDoing, true},
		{"todo to done is not allowed", model.StatusTodo, model.StatusDone, false},
		{"done to todo is not allowed", model.StatusDone, model.StatusTodo, false},
		{"todo to todo is not a transition", model.StatusTodo, model.StatusTodo, false},
		{"doing to doing is not a transition", model.StatusDoing, model.StatusDoing, false},
		{"done to done is not a transition", model.StatusDone, model.StatusDone, false},
		{"unknown source status", "archived", model.StatusTodo, false},
		{"unknown target status", model.StatusTodo, "archived", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := model.CanTransition(tt.from, tt.to); got != tt.want {
				t.Errorf("CanTransition(%q, %q) = %v, want %v", tt.from, tt.to, got, tt.want)
			}
		})
	}
}
```

<details>
<summary>GO NOTE: Table Driven Test の書き方</summary>

Go で最も一般的なテストの書き方。

```text
テストケースの一覧（slice）
 ├─ Case 1: {name, 入力, 期待値}
 ├─ Case 2: {name, 入力, 期待値}
 └─ Case 3: {name, 入力, 期待値}
        ↓
   同じ検証ロジックを繰り返す
```

| 要素 | 役割 |
|---|---|
| `name` フィールド | 失敗時にどのケースが落ちたか分かる |
| `t.Run(tt.name, ...)` | サブテストとして独立実行される |
| `t.Parallel()` | 並列実行して高速化する |
| 匿名 struct の slice | ケース追加が1行で済む |

`t.Errorf` は失敗を記録して、そのまま続ける。他のケースも確認したいときに使う。`t.Fatalf` は失敗を記録して、その場でテストを止める。以降の処理が無意味なときに使う。

</details>

---

### Step 2. 境界値をテストする

#### やること

Chapter 03 で触れた「rune 数で数える」挙動を、テストで固定する。

#### 実行

```go
// 日本語など、1文字が複数byteになる入力でも
// 「100文字」で判定できることを確認する。
func TestTitleLengthCountsRunesNotBytes(t *testing.T) {
	t.Parallel()

	input := model.CreateTaskInput{Title: "", Priority: "low"}
	for range model.MaxTitleLength {
		input.Title += "あ" // 3 bytes / 1 rune
	}

	input.Normalize()

	if err := input.Validate(); err != nil {
		t.Fatalf("100 runes should be valid, got %v", err)
	}

	input.Title += "あ"

	if err := input.Validate(); err == nil {
		t.Fatal("101 runes should be rejected")
	}
}
```

Validation の網羅テスト。

```go
func TestCreateTaskInputValidate(t *testing.T) {
	t.Parallel()

	longTitle := ""
	for range model.MaxTitleLength + 1 {
		longTitle += "a"
	}

	tests := []struct {
		name    string
		input   model.CreateTaskInput
		wantErr bool
	}{
		{"valid", model.CreateTaskInput{Title: "write docs", Priority: "high"}, false},
		{"empty title", model.CreateTaskInput{Title: "", Priority: "high"}, true},
		{"whitespace title", model.CreateTaskInput{Title: "   ", Priority: "high"}, true},
		{"title too long", model.CreateTaskInput{Title: longTitle, Priority: "low"}, true},
		{"invalid priority", model.CreateTaskInput{Title: "ok", Priority: "SUPER_HIGH"}, true},
		{"empty priority defaults to medium", model.CreateTaskInput{Title: "ok"}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			input := tt.input
			input.Normalize()

			err := input.Validate()

			if tt.wantErr && err == nil {
				t.Fatalf("expected error, got nil")
			}

			if !tt.wantErr && err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
		})
	}
}
```

> **POINT**
> 「100 文字は OK、101 文字は NG」という境界のテストがあれば、`len([]rune(...))` を `len(...)` に書き換えた時点で落ちる。仕様が壊れたことに一番早く気づけるのは、こういう境界のテストだ。

---

## Part 2. Service Test（Fake Repository）

### Step 3. DB なしで Service を検証する

#### やること

Repository の interface を Fake に差し替え、Service の判断だけを検証する。

#### 実行

`internal/service/task_test.go`（抜粋）。

```go
// fakeTaskRepo は DB を使わずに Service の判断だけを検証するための実装。
// Repository が interface だから差し替えられる。
type fakeTaskRepo struct {
	task            model.Task
	role            string
	findErr         error
	updateErr       error
	updateCallCount int
}

func (f *fakeTaskRepo) FindForUser(context.Context, int64, int64) (model.Task, string, error) {
	if f.findErr != nil {
		return model.Task{}, "", f.findErr
	}

	return f.task, f.role, nil
}

func (f *fakeTaskRepo) UpdateStatusWithHistory(
	_ context.Context,
	_, _ int64,
	_, newStatus string,
	_ int,
) (model.Task, error) {
	f.updateCallCount++

	if f.updateErr != nil {
		return model.Task{}, f.updateErr
	}

	updated := f.task
	updated.Status = newStatus
	updated.Version++

	return updated, nil
}

// （Create / ListByProject / Search も interface を満たすために実装する）
```

テスト本体。`ErrForbidden` のような決まった値のエラーは `errors.Is` で比べる。`ValidationError` は入力ごとにメッセージが違う型なので、`errors.As` で型だけを確認する。この2つを `wantErr` と `wantValidationErr` で分けている。

```go
func TestChangeStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name              string
		currentStatus     string
		newStatus         string
		role              string
		findErr           error
		wantErr           error
		wantValidationErr bool
		wantUpdate        bool
	}{
		{
			name:          "member can move todo to doing",
			currentStatus: model.StatusTodo,
			newStatus:     model.StatusDoing,
			role:          model.RoleMember,
			wantUpdate:    true,
		},
		{
			name:          "viewer cannot change status",
			currentStatus: model.StatusTodo,
			newStatus:     model.StatusDoing,
			role:          model.RoleViewer,
			wantErr:       model.ErrForbidden,
		},
		{
			name:              "todo to done is rejected by business rule",
			currentStatus:     model.StatusTodo,
			newStatus:         model.StatusDone,
			role:              model.RoleOwner,
			wantValidationErr: true,
		},
		{
			name:          "inaccessible task looks like not found",
			currentStatus: model.StatusTodo,
			newStatus:     model.StatusDoing,
			role:          model.RoleOwner,
			findErr:       model.ErrNotFound,
			wantErr:       model.ErrNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tasks := &fakeTaskRepo{
				task:    model.Task{ID: 1, ProjectID: 1, Status: tt.currentStatus, Version: 1},
				role:    tt.role,
				findErr: tt.findErr,
			}

			svc := service.NewTaskService(tasks, &fakeProjectRepo{role: tt.role}, nil, discardLogger())

			_, err := svc.ChangeStatus(context.Background(), 1, 1, tt.newStatus, 1)

			switch {
			case tt.wantUpdate:
				if err != nil {
					t.Fatalf("expected success, got %v", err)
				}
			case tt.wantValidationErr:
				var validationErr *model.ValidationError
				if !errors.As(err, &validationErr) {
					t.Fatalf("expected ValidationError, got %v", err)
				}
			default:
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("expected %v, got %v", tt.wantErr, err)
				}
			}

			// 拒否されたリクエストで DB 更新が呼ばれていないことも確認する。
			if !tt.wantUpdate && tasks.updateCallCount != 0 {
				t.Fatalf("repository must not be called when rejected, got %d calls",
					tasks.updateCallCount)
			}
		})
	}
}
```

#### Fake でしか検証できないこと

`updateCallCount` の確認に注目する。

```go
// 拒否されたリクエストで DB 更新が呼ばれていないことも確認する。
if !tt.wantUpdate && tasks.updateCallCount != 0 {
```

「403 が返った」だけなら実 DB でも確認できる。しかし「DB の更新処理が呼ばれていない」ことは、Fake を使わないと検証できない。

実装が「先に UPDATE してから権限チェックして、駄目ならロールバック」に変わっても、レスポンスは 403 のままで気づけない。Fake なら落ちる。

#### 副作用の失敗が主処理に影響しないことを検証する

```go
// 通知の失敗で Task 更新を失敗にしないことを確認する。
type failingNotifier struct{ called bool }

func (n *failingNotifier) TaskStatusChanged(context.Context, model.Task, string) error {
	n.called = true
	return errors.New("notification service is down")
}

func TestChangeStatusSucceedsWhenNotificationFails(t *testing.T) {
	t.Parallel()

	tasks := &fakeTaskRepo{
		task: model.Task{ID: 1, ProjectID: 1, Status: model.StatusTodo, Version: 1},
		role: model.RoleMember,
	}
	notifier := &failingNotifier{}

	svc := service.NewTaskService(tasks, &fakeProjectRepo{role: model.RoleMember},
		notifier, discardLogger())

	task, err := svc.ChangeStatus(context.Background(), 1, 1, model.StatusDoing, 1)
	if err != nil {
		t.Fatalf("notification failure must not fail the request: %v", err)
	}

	if !notifier.called {
		t.Fatal("notifier was not called")
	}

	if task.Status != model.StatusDoing {
		t.Fatalf("status = %q, want %q", task.Status, model.StatusDoing)
	}
}
```

Chapter 07 で決めた設計判断を、テストとして固定した。本物の外部 API を落とさなくても、「落ちたとき」の挙動を確かめられる。

---

## Part 3. Integration Test

### Step 4. 実 DB に対して HTTP から検証する

#### やること

`httptest.Server` で実際のアプリを起動し、HTTP クライアントとして叩く。

#### 実行

Build tag で通常のテストから分離する。`test/integration_test.go`。

```go
//go:build integration

// Integration Test は実際の PostgreSQL に対して実行する。
// 通常の `go test ./...` では実行されない。
//
//	go test -tags=integration ./test/...
package test

func newTestServer(t *testing.T) (*httptest.Server, *pgxpool.Pool) {
	t.Helper()

	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://kanban:local-dev-password@localhost:5432/kanban"
	}

	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect db: %v", err)
	}

	// テスト間で状態が残らないよう、毎回初期化する。
	_, err = pool.Exec(context.Background(),
		`TRUNCATE tasks, project_members, projects, sessions, users,
		 task_history, idempotency_keys RESTART IDENTITY CASCADE`)
	if err != nil {
		t.Fatalf("truncate: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	cfg := app.DefaultConfig()
	cfg.SlowQueryEnable = false

	server := httptest.NewServer(app.New(pool, logger, cfg))

	t.Cleanup(func() {
		server.Close()
		pool.Close()
	})

	return server, pool
}
```

Cookie を自動で扱うクライアント。

```go
func newClient(t *testing.T, baseURL string) *client {
	t.Helper()

	// Cookie Jar を持たせると、ログイン後の Session Cookie を http.Client が自動で送る。
	jar, err := newJar()
	if err != nil {
		t.Fatalf("cookie jar: %v", err)
	}

	return &client{t: t, baseURL: baseURL, http: &http.Client{Jar: jar}}
}
```

`app.New()` に依存を注入できる設計（Chapter 05）が、ここで効いてくる。グローバル変数の `pool` が残っていたら、テストごとに別の DB へ向けられない。

---

### Step 5. 認可シナリオをテストする

#### 実行

`test/scenario_test.go`（抜粋）。

```go
// 認証・認可・IDOR を、実際の HTTP と DB を通して確認する。
func TestAuthorizationScenario(t *testing.T) {
	server, _ := newTestServer(t)

	alice := newClient(t, server.URL)
	bob := newClient(t, server.URL)

	alice.mustStatus(http.StatusCreated, http.MethodPost, "/users",
		map[string]string{"email": "alice@example.com", "password": "alice-password-1"})
	bob.mustStatus(http.StatusCreated, http.MethodPost, "/users",
		map[string]string{"email": "bob@example.com", "password": "bob-password-123"})

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

	// 他人からは「存在しない」ように見える。403 を返すと、その ID の Task が
	// 存在することを攻撃者へ教えてしまう。
	bob.mustStatus(http.StatusNotFound, http.MethodGet, taskPath(task.ID, ""), nil)

	// Project 外のユーザーは一覧も作成もできない。
	bob.mustStatus(http.StatusForbidden, http.MethodGet, projectPath(project.ID, "/tasks"), nil)
	bob.mustStatus(http.StatusForbidden, http.MethodPost, projectPath(project.ID, "/tasks"),
		map[string]string{"title": "intruder", "priority": "low"})

	// Viewer として追加されると、読めるが書けない。
	alice.mustStatus(http.StatusNoContent, http.MethodPost, projectPath(project.ID, "/members"),
		map[string]any{"user_id": 2, "role": model.RoleViewer})

	bob.mustStatus(http.StatusOK, http.MethodGet, taskPath(task.ID, ""), nil)
	bob.mustStatus(http.StatusForbidden, http.MethodPost, projectPath(project.ID, "/tasks"),
		map[string]string{"title": "intruder", "priority": "low"})

	// ログアウト後は Session が無効になる。
	alice.mustStatus(http.StatusNoContent, http.MethodPost, "/logout", nil)
	alice.mustStatus(http.StatusUnauthorized, http.MethodGet, taskPath(task.ID, ""), nil)
}
```

Chapter 04 で curl を使って手で確認したことを、いつでも自動で再実行できるようになった。

---

### Step 6. DB の状態まで検証する

#### やること

HTTP のレスポンスだけでなく、DB に何が書かれたかを確認する。

#### 実行

```go
// Task 更新と履歴追加が、同時に成功するか同時に失敗するかを確認する。
func TestStatusChangeWritesHistoryAtomically(t *testing.T) {
	server, pool := newTestServer(t)

	// （alice の登録・ログイン・Project と Task の作成は省略）

	// 許可されない遷移は 400 で、DB は変わらない。
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
}
```

> **POINT**
> 拒否されたリクエストで DB が変わっていないことまで確認している。
> 「400 が返った」だけでは、DB に書いてからロールバックしたのか、そもそも書いていないのか区別できない。

冪等性の検証。

```go
// 同じ Idempotency-Key の再送が二重作成にならないことを確認する。
func TestIdempotentTaskCreation(t *testing.T) {
	server, pool := newTestServer(t)

	// （省略）

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
}
```

---

### Step 7. テストを実行する

#### 実行

```bash
# Unit / Service / Notify（DB 不要）
go test ./...

# 競合状態の検出付き
go test -race ./...

# Integration（実 DB が必要）
go test -tags=integration ./test/...
```

#### 期待結果

検証環境での実際の出力。

```text
=== go test ./... ===
?   	example.com/go-kanban/cmd/api	[no test files]
?   	example.com/go-kanban/internal/app	[no test files]
?   	example.com/go-kanban/internal/handler	[no test files]
?   	example.com/go-kanban/internal/httpx	[no test files]
?   	example.com/go-kanban/internal/middleware	[no test files]
ok  	example.com/go-kanban/internal/model	(cached)
ok  	example.com/go-kanban/internal/notify	1.508s
?   	example.com/go-kanban/internal/repository	[no test files]
ok  	example.com/go-kanban/internal/service	(cached)

=== go test -race ./... ===
ok  	example.com/go-kanban/internal/model	1.193s
ok  	example.com/go-kanban/internal/notify	2.630s
ok  	example.com/go-kanban/internal/service	1.477s

=== integration ===
ok  	example.com/go-kanban/test	0.810s
```

Integration Test の内訳。

```text
--- PASS: TestAuthorizationScenario (0.29s)
--- PASS: TestStatusChangeWritesHistoryAtomically (0.15s)
--- PASS: TestIdempotentTaskCreation (0.14s)
--- PASS: TestValidationThroughHTTP (0.14s)
PASS
```

<details>
<summary>GO NOTE: <code>-race</code> と Build Tag</summary>

`-race`（Race Detector）

複数の goroutine が同じメモリを、少なくとも一方が書き込みで、同期なしにアクセスしている箇所を検出する。[Chapter 06 Part 1](./chapter06_transaction.md#part-1-goroutine-と-data-race) で、実際に `DATA RACE` を検出した。

```bash
go test -race ./...
```

実行速度は数倍遅くなり、メモリも増える。CI では有効にして、ローカルで素早く回すときは外す運用がよく見られる。

Chapter 08 の `LogFields` はポインタ経由で書き換えているので、競合しないか `-race` で確かめておきたい。1 リクエストを 1 goroutine が処理するため、実際には検出されない。

Build Tag

```go
//go:build integration
```

ファイルの先頭（package 宣言の前、空行を挟む）に書くと、`-tags=integration` を付けたときだけコンパイルされる。

| 目的 | 効果 |
|---|---|
| `go test ./...` を速く保つ | DB が要るテストが混ざらない |
| DB なしでも開発できる | CI の一部ジョブで DB を立てなくてよい |
| 意図的に実行を分ける | 遅いテストを別ジョブへ |

</details>

---

## Part 4. Load Test

### Step 8. k6 で負荷をかける

#### やること

同時アクセス数を段階的に上げ、何が劣化するかを観察する。

#### 目的の確認

> **POINT**
> 知りたいのは「何ユーザーまで耐えたか」より、「負荷が増えたとき、どこが最初に悪化するか」のほう。

#### 実行

`scripts/load-test.js`。

```javascript
import http from 'k6/http';
import { check, group } from 'k6';

const BASE_URL = __ENV.BASE_URL || 'http://localhost:8080';
const EMAIL = __ENV.EMAIL || 'loadtest@example.com';
const PASSWORD = __ENV.PASSWORD || 'loadtest-password-1';

export const options = {
  // 一気に高負荷をかけず、段階的に増やして劣化の始まる地点を探す。
  stages: [
    { duration: '30s', target: 10 },
    { duration: '30s', target: 50 },
    { duration: '30s', target: 100 },
    { duration: '30s', target: 0 },
  ],
  thresholds: {
    // 平均ではなく p95 / p99 を見る。
    // 平均が速くても、一部の利用者だけ極端に遅いことがある。
    http_req_duration: ['p(95)<500', 'p(99)<1000'],
    http_req_failed: ['rate<0.01'],
  },
};

// k6 は setup を全 VU の開始前に1回だけ実行する。
export function setup() {
  http.post(`${BASE_URL}/users`, JSON.stringify({ email: EMAIL, password: PASSWORD }), {
    headers: { 'Content-Type': 'application/json' },
  });

  const login = http.post(`${BASE_URL}/login`, JSON.stringify({ email: EMAIL, password: PASSWORD }), {
    headers: { 'Content-Type': 'application/json' },
  });

  if (login.status !== 204) {
    throw new Error(`login failed: ${login.status} ${login.body}`);
  }

  const cookie = login.cookies['kanban_session'][0].value;

  const project = http.post(`${BASE_URL}/projects`, JSON.stringify({ name: 'load test' }), {
    headers: { 'Content-Type': 'application/json', Cookie: `kanban_session=${cookie}` },
  });

  return { cookie, projectId: JSON.parse(project.body).id };
}

export default function (data) {
  const headers = {
    'Content-Type': 'application/json',
    Cookie: `kanban_session=${data.cookie}`,
  };

  group('list tasks', () => {
    const res = http.get(`${BASE_URL}/projects/${data.projectId}/tasks`, { headers });
    check(res, { 'list is 200': (r) => r.status === 200 });
  });

  group('create task', () => {
    const res = http.post(
      `${BASE_URL}/projects/${data.projectId}/tasks`,
      JSON.stringify({ title: `task ${__VU}-${__ITER}`, priority: 'low' }),
      { headers },
    );
    check(res, { 'create is 201': (r) => r.status === 201 });
  });
}
```

API サーバ（`go run ./cmd/api`）と DB を起動した状態で、k6 を Docker で実行する。サーバログはあとで集計に使うので、ファイルへ保存しておく。

```bash
# 別ターミナルで API を起動（ログを server.log に残す）
go run ./cmd/api > server.log 2>&1

# k6 を実行（スクリプトは標準入力から渡す）
docker run --rm -i \
  -e BASE_URL=http://host.docker.internal:8080 \
  grafana/k6:2.3.0 run - < scripts/load-test.js
```

| 指定 | 理由 |
|---|---|
| `-e BASE_URL=http://host.docker.internal:8080` | コンテナの中の `localhost` はコンテナ自身を指す。ホストで動く API へは `host.docker.internal` で届く |
| `run -` と `< scripts/load-test.js` | スクリプトを標準入力で渡す。Volume のマウントが不要になり、Windows のパス表記の違いに悩まされない |
| `grafana/k6:2.3.0` | タグでバージョンを固定する。`latest` だと実行時期によって結果の表示形式が変わる |

> **NOTE**
> `host.docker.internal` は Docker Desktop（Windows / macOS）で使える。Linux の Docker Engine では `--add-host=host.docker.internal:host-gateway` を追加する。

#### 期待結果

検証環境での実際の出力（抜粋）。約 2 分で終わる。

```text
  █ THRESHOLDS

    http_req_duration
    ✗ 'p(95)<500' p(95)=1.34s
    ✗ 'p(99)<1000' p(99)=2.39s

    http_req_failed
    ✓ 'rate<0.01' rate=0.00%

  █ TOTAL RESULTS

    checks_succeeded...: 100.00% 19132 out of 19132
    ✓ list is 200
    ✓ create is 201

    HTTP
    http_req_duration..............: avg=253.31ms min=1.83ms med=34.23ms  max=4.56s p(90)=848.79ms p(95)=1.34s
    http_req_failed................: 0.00%  0 out of 19135
    http_reqs......................: 19135  159.173414/s

    EXECUTION
    iterations.....................: 9566   79.574229/s

    NETWORK
    data_received..................: 5.9 GB 49 MB/s
    data_sent......................: 4.3 MB 36 kB/s

time="2026-09-25T01:49:36Z" level=error msg="thresholds on metrics 'http_req_duration' have been crossed"
```

エラーは 0 件だが、p95 / p99 のしきい値を超えた。中央値は 34ms なのに p95 は 1.34s になっている。一部のリクエストだけが極端に遅い。

#### 何が起きたのか

サーバログの `duration_ms` と `bytes` を、負荷の段階ごとに集計した。

| 経過時間 | VU | 一覧 GET の件数 | 一覧 GET の p95（サーバ側） | 一覧のレスポンスサイズ（中央値） | 作成 POST の p95（サーバ側） |
|---|---|---|---|---|---|
| 0〜30s | 0 → 10 | 4123 | 14ms | 264KB | 4ms |
| 30〜60s | 10 → 50 | 2397 | 26ms | 682KB | 6ms |
| 60〜90s | 50 → 100 | 1691 | 37ms | 941KB | 9ms |
| 90〜120s | 100 → 0 | 1354 | 42ms | 1.1MB | 9ms |

> **観測された問題**
> VU を 10 → 50 → 100 と増やしたのに、処理できた件数は減った。
> スクリプトは1回ごとに Task を1件作る。一覧 API はページングなしで全件を返すので、Task が増えるほどレスポンスが大きくなる。最後は 9566 件、1 回 1.2MB になった。
> 2 分間で受信したデータは 5.9GB。遅くしていたのは同時アクセス数そのものより、膨らみ続けるレスポンスのほうだった。

サーバ側の p95 は最大 42ms で、k6 が測った p95（1.34s）よりはるかに小さい。差の大部分は、大きなレスポンスをクライアントへ運ぶのにかかっている。1.2MB の一覧を1件だけ取得して比べると、この経路の差が分かる。

| 取得元 | 応答時間（3回） |
|---|---|
| ホストから `curl` | 15ms / 15ms / 13ms |
| コンテナから `host.docker.internal` 経由 | 105ms / 54ms / 44ms |

k6 をコンテナで動かすと、Docker Desktop の仮想ネットワークを通る分だけ転送が遅くなる。100 VU が 1MB 級のレスポンスを同時に受け取ると、この経路の帯域（今回は約 49MB/s）が詰まり、待ち時間が p95 / p99 に現れる。

> **POINT**
> k6 の数値は「クライアントから見た時間」で、ネットワークや負荷をかける側の環境も含む。サーバログの `duration_ms` は「サーバが処理した時間」だけを表す。両方を並べれば、遅さがサーバの中にあるのか外にあるのかを切り分けられる。
> 今回はサーバ側も 14ms → 42ms と悪化しており、レスポンスが大きくなるほど JSON の生成と書き込みが重くなっていることも分かる。

直すなら、一覧 API にページング（`LIMIT` と、次のページを示すカーソルや `offset`）を入れて、1 回に返す件数に上限を設ける。本ハンズオンでは実装せず、[残っている改善候補](#残っている改善候補)に挙げている。

> **NOTE**
> `setup()` は実行のたびに新しい Project を作るので、再実行しても一覧は空の状態から始まる。2 回目以降は `setup()` のユーザー登録が 409 になり、`http_req_failed` に 1 件計上される（しきい値 1% には影響しない）。
> 負荷テストで作った Task や Project は DB に残り続ける。不要になったら消しておく（`project_members`・`tasks`・`task_history` は外部キーの `ON DELETE CASCADE` で一緒に消える）。
>
> ```bash
> docker compose exec -T db psql -U kanban -d kanban -c \
>   "DELETE FROM projects WHERE name = 'load test';"
> ```

#### 何を観察するか

| 指標 | 見方 |
|---|---|
| p50（中央値） | 半数の利用者が体験する速度 |
| p95 | 20人に1人が体験する遅さ |
| p99 | 100人に1人が体験する遅さ。ここが実際のクレームになる |
| avg（平均） | 単独で見ない。外れ値に引きずられる、あるいは埋もれる |
| Throughput（req/s） | 処理量。頭打ちになる点が限界 |
| Error Rate | 負荷で失敗し始める点 |

> **POINT**
> 平均応答時間だけを見ると、「一部の利用者だけが 5 秒待たされている」状況を見逃す。
> 1000 件のうち 980 件が 10ms、20 件が 5000ms なら、平均は約 110ms で、それほど悪く見えない。一方、p99 は 5000ms になる。

#### アプリ以外も見る

```text
負荷をかけながら、同時に観察する

  アプリ側    CPU、メモリ、Goroutine 数
  DB 側       接続数、スロークエリ、ロック待ち
  ログ        duration_ms の分布、エラー率
```

```bash
# DB の接続数を見る
docker compose exec db psql -U kanban -d kanban -c \
  "SELECT count(*), state FROM pg_stat_activity WHERE datname='kanban' GROUP BY state;"

# アクセスログから遅いリクエストを抽出する
grep http_request server.log | grep -E '"duration_ms":[0-9]{3,}'
```

Chapter 08 で入れた `duration_ms` が、ここで使える。

#### 負荷テストで見つかりやすい問題

| 問題 | 現れ方 |
|---|---|
| N+1（Chapter 05） | データ件数の増加に伴い、応答時間が線形に悪化する |
| ページングのない一覧 | データ件数に比例してレスポンスが大きくなり、転送量と応答時間が増える（今回実際に発生） |
| 接続プール不足 | 同時数を上げると急に待ち時間が増える |
| ロック競合 | 更新系のスループットが頭打ちになる |
| Index 不足 | データ量を増やすと急激に遅くなる |
| メモリリーク | 時間経過とともにメモリが増え続ける |

> **WARNING**
> 高い VU 数（500 以上など）は、負荷をかける側のマシンや、共有環境の他システムに影響する。
> 実行前に環境を確認する。共有環境や本番に近い環境では、事前の合意なく実行しない。

---

## Part 5. Refactoring

### Step 9. 責務を振り返る

#### やること

機能追加ではなく、ここまで増えた責務を整理する。

#### 判断基準

```text
このコードは、何が変わったときに変更が必要になるか？

  HTTP の仕様（URL、Status、JSON 形式）  → Handler
  業務ルール（誰が何をしてよいか）        → Service
  DB スキーマ、SQL                       → Repository
  ドメインの概念そのもの                  → Model
```

1つのファイルが複数の理由で変更されるなら、分割を検討する。

#### 現在の構成を確認する

```text
internal/
├── model/         102 行 + 27 行 + 46 行    依存なし。テストが速い
├── repository/    333 + 121 + 109 + 77 行   SQL のみ
├── service/       133 + 115 + 45 行         業務判断のみ
├── handler/       156 + 94 + 78 + 105 行    HTTP 変換のみ
├── httpx/         151 行                    handler / middleware の共通語彙
├── middleware/    36 + 106 + 95 行          全リクエスト共通の前処理
├── notify/        148 行                    外部 API と Retry
└── app/           依存の組み立てとルーティング
```

Chapter 04 時点では `cmd/api/` に5ファイル865行、`main.go` だけで378行だった。

#### やらないこと

> **WARNING**
> 「綺麗に見えるから」という理由だけで interface や package を増やさない。

このハンズオンで抽象化を導入した箇所と、その具体的な理由。

| 導入したもの | 具体的な理由 |
|---|---|
| `TaskRepository` interface | Fake に差し替えて Service を DB なしでテストするため |
| `Notifier` interface | 外部 API を落とさずに失敗時の挙動をテストするため |
| `httpx` package | import cycle のコンパイルエラーを解消するため |
| `app.New()` の引数注入 | テストごとに別の設定・DB を渡すため |

いずれも「そうしないと困る」という具体的な問題があった。逆に、次のものは導入していない。

| 導入しなかったもの | 理由 |
|---|---|
| `ProjectService` の interface | 差し替える必要が生じていない |
| DI コンテナ | `app.New()` の 20 行で足りている |
| Repository ごとの独立した interface ファイル | 実装と並べたほうが対応を追いやすい |
| ドメインイベント / CQRS | 今の規模では複雑さだけが増える |

#### 残っている改善候補

<details>
<summary>このハンズオンで意図的に残した課題</summary>

| 課題 | 現状 | 本番で必要になること |
|---|---|---|
| `/debug/*` エンドポイント | 検証用に有効 | 本番ではビルドから除外するか、無効化する |
| Idempotency キーの有効期限 | 無期限に保存 | 定期削除バッチ |
| レートリミット | なし | ログイン試行の回数制限は必須 |
| CSRF トークン | `SameSite=Lax` のみ | Cookie 認証なら要検討 |
| Migration の管理 | 手動で psql に流す | `golang-migrate` などのツール |
| Handler のテスト | Integration でカバー | `httptest.NewRecorder()` による単体テストも可能 |
| Repository のテスト | Integration でカバー | 実 DB に対する Repository 単体テスト |
| 一覧のページング | 全件を返す（負荷テストで 1 回 1.2MB まで増えた） | `LIMIT` とカーソルで 1 回の件数に上限を設ける |
| メトリクス | ログのみ | Prometheus などへのエクスポート |
| 分散トレーシング | `request_id` のみ | OpenTelemetry |

</details>

---

## この章のまとめ

| テスト結果 | 値 |
|---|---|
| `go test ./...` | 全パス |
| `go test -race ./...` | 全パス |
| Integration（4 シナリオ） | 全パス、0.81 秒 |
| k6 負荷テスト（Docker） | エラー 0%。p95 1.34s / p99 2.39s でしきい値超過。原因はページングのない一覧 |

---

## ハンズオン全体の振り返り

完了したら、次の問いに自分の言葉で答えられるか確認する。答えられない項目があれば、該当する章へ戻る。

1. Handler / Service / Repository を分ける理由は何か（Chapter 05）
2. Validation と DB Constraint の両方が必要な理由は何か（Chapter 03）
3. Authentication と Authorization の違いは何か（Chapter 04）
4. 他人の資源に 403 ではなく 404 を返す理由は何か（Chapter 04）
5. Transaction が必要になる境界はどこか（Chapter 06）
6. Optimistic Lock は Lost Update をどう防ぐのか（Chapter 06）
7. Timeout・Retry・冪等性をセットで考える理由は何か（Chapter 07）
8. ログに出してよい情報と出してはいけない情報の境界はどこか（Chapter 08）
9. Unit / Integration / Load Test はそれぞれ何を保証するのか（Chapter 09）

より詳細なチェックリストは [Appendix](./appendix.md) にまとめている。

### 「動く CRUD」と「本番で運用できる CRUD」の違い

Chapter 02 で作ったものと、Chapter 09 時点のものを比べる。

| 観点 | Chapter 02 | Chapter 09 |
|---|---|---|
| 不正入力 | 201 で保存される | 400 と理由を返す |
| 存在しない資源 | 500 + 内部情報 | 404 + 一般的な文言 |
| 認証 | なし | Session Cookie |
| 認可 | なし | Role + オブジェクト単位 |
| 他人の資源 | 誰でも読める | 404（存在も漏らさない） |
| SQL Injection | 対策なし | プレースホルダ |
| 複数更新 | バラバラに実行 | Transaction |
| 同時更新 | 静かに消える | 409 で検出 |
| 遅い処理 | 永遠に待つ | 2 秒で打ち切り |
| 外部 API 障害 | 主処理も失敗 | Retry + 主処理は継続 |
| 再送 | 二重作成 | 冪等キーで抑止 |
| 障害調査 | ログなし | 構造化ログ + request_id |
| 変更履歴 | なし | Transaction 内の監査ログ |
| 検証 | 手動 | 自動テスト |

手元に残るのはカンバンアプリだけではない。コードを読んで「本番ではここが危ない」と自分で気づき、再現して、直して、テストで固定する。その一連の手順を、ここまでの章で一度ずつ通ってきた。

お疲れさまでした。片付けは [README の「片付け」](./README.md#片付け)を参照する。
