# Chapter 09: Test と Refactoring

## この章の目的

ここまで実機で確認してきた挙動を、繰り返し自動で検証できる形に固定する。

4種類のテストを、それぞれ何に使うのかを区別して書く。

| 種類 | 何を検証するか | DB | 速度 |
| --- | --- | :---: | --- |
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

## 完成時点のコード

この章を終えた時点のコード全体、つまりハンズオン全体の完成形は [answers/chapter09/](../../answers/chapter09/) にある。本文に抜粋しか載っていないテストの全体を確認できる。

---

## Part 1. Unit Test

### Step 1. 状態遷移をテストする

#### やること

`model.CanTransition` の全パターンをテストする。既知の3状態どうしの組み合わせ9通りと、未知の状態を渡した2通りを並べる。

あわせて、Status の文字列が正しい値かを判定する `model.IsValidStatus` もテストする。

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

// 大文字の "TODO" や空文字も不正な値として扱うことを確認する。
func TestIsValidStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		status string
		want   bool
	}{
		{model.StatusTodo, true},
		{model.StatusDoing, true},
		{model.StatusDone, true},
		{"archived", false},
		{"", false},
		{"TODO", false},
	}

	for _, tt := range tests {
		t.Run(tt.status, func(t *testing.T) {
			t.Parallel()

			if got := model.IsValidStatus(tt.status); got != tt.want {
				t.Errorf("IsValidStatus(%q) = %v, want %v", tt.status, got, tt.want)
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
| --- | --- |
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

テスト対象は、Chapter 05 で `internal/model/task.go` に移した `CreateTaskInput` の `Normalize()` と `Validate()`、定数 `MaxTitleLength`（100）。

#### 実行

Step 1 と同じ `internal/model/task_test.go` の末尾（`TestIsValidStatus` の後ろ）に追記する。package と import は Step 1 のままでよい。

まず、日本語のタイトルで「100 文字は OK、101 文字は NG」を確認するテスト。

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

続けて、同じファイルに `Validate()` の網羅テストを追記する。空文字・空白のみ・長すぎるタイトル・不正な Priority・Priority 省略時の既定値を、Step 1 と同じ Table Driven Test で並べる。

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
> 「100 文字は OK、101 文字は NG」という境界のテストがあれば、`len([]rune(...))` を `len(...)` に書き換えた時点で落ちる。仕様が壊れたことに一番早く気づけるのはこういう境界のテスト。

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

Part 2 では Repository を Fake に差し替えた。Part 3 では何も差し替えず、HTTP から DB までを実際に通す。

```mermaid
flowchart LR
    T[Test 関数] -->|"client.mustStatus()<br/>HTTP リクエスト"| S["httptest.Server<br/>app.New()"]
    S --> DB[(kanban_test)]
    T -->|"assertXxx()<br/>pool で直接 SELECT"| DB
```

テストは2つの経路で結果を確かめる。HTTP のレスポンスはアプリを通して、DB の中身はアプリを通さずに読む。

| | Service Test（Part 2） | Integration Test（Part 3） |
| --- | --- | --- |
| Repository | Fake | 実 PostgreSQL |
| HTTP | 通らない | Handler・Middleware・Cookie まで通る |
| `t.Parallel()` | 付ける | 付けない（[理由](#なぜ-tparallel-を付けないのか)） |
| 実行 | `go test ./...` | `go test -tags=integration ./test/...` |

作るファイルは2つ。

```text
test/
├── integration_test.go   共通部品（テスト用サーバ、HTTP クライアント、DB の検証）  Step 4
└── scenario_test.go      テストシナリオ本体                                        Step 5〜6
```

### Step 4. 実 DB に対して HTTP から検証する

#### やること

テスト専用の DB を作る。そのうえで、`httptest.Server` でアプリを起動し、HTTP クライアントとして叩くための共通部品を用意する。

#### 実行

##### 1. テスト用の DB を作る

Integration Test は、各テストの最初に全テーブルを `TRUNCATE` する。開発用の `kanban` DB に向けると、Chapter 08 までに作ったデータがすべて消える。同じコンテナの中に、テスト専用の `kanban_test` を別に作る。

`go-kanban/` で実行する。

```bash
docker compose exec -T db psql -U kanban -d kanban -c "CREATE DATABASE kanban_test;"

for f in migrations/*.sql; do
  docker compose exec -T db psql -U kanban -d kanban_test -v ON_ERROR_STOP=1 < "$f"
done
```

| 指定 | 理由 |
| --- | --- |
| `-d kanban` で `CREATE DATABASE` | 接続先の DB が要るので、既存の `kanban` に接続してから新しい DB を作る |
| `migrations/*.sql` | ファイル名の番号順（001 → 004）に展開される。Chapter 06・07 で足した `task_history` と `idempotency_keys` も作られる |
| `-v ON_ERROR_STOP=1` | SQL が1つでも失敗したらそこで止める。失敗を見逃して進むと、テーブルが足りないままテストが落ち、原因を追いにくい |

期待結果（検証環境での実際の出力）。

```text
CREATE DATABASE
CREATE TABLE
CREATE TABLE
CREATE INDEX
CREATE TABLE
CREATE TABLE
CREATE INDEX
CREATE TABLE
CREATE INDEX
ALTER TABLE
CREATE INDEX
CREATE TABLE
CREATE INDEX
CREATE TABLE
```

> **NOTE**
> 2回目の `CREATE DATABASE` は `already exists` で失敗する。作り直すときは、先に `DROP DATABASE kanban_test;` を実行する。
> 同じ処理をまとめたスクリプトが [answers/chapter09/scripts/setup_test_db.sh](../../answers/chapter09/scripts/setup_test_db.sh) にある。

##### 2. テスト用サーバを起動する関数

`test/integration_test.go` を作る。package 宣言・import と、テスト用サーバを起動する関数。

```go
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
```

| コード | 理由 |
| --- | --- |
| `//go:build integration` | `-tags=integration` を付けたときだけコンパイルされる。詳しくは Step 7 の GO NOTE |
| `package test` | `internal/` の外にある別パッケージ。アプリには `app.New()` などの公開された入口からしか触れず、外から使う立場でテストする |
| 既定の DSN が `kanban_test` | 環境変数 `TEST_DATABASE_URL` を設定し忘れても、開発用 DB を消さない |
| `TRUNCATE` | 全テーブルを空にする。テストの結果が、実行の順番や前回の残りデータに左右されなくなる |
| `RESTART IDENTITY` | `BIGSERIAL` の採番を 1 に戻す。失敗したときのログに出る ID が毎回同じになり、比較しやすい |
| `CASCADE` | 外部キーで参照しているテーブルも一緒に空にする。テーブルを足したときに、ここへ書き忘れても `TRUNCATE` が失敗しない |
| `io.Discard` のロガー | アクセスログを捨てる。`go test -v` の出力にリクエストごとのログが混ざらない |
| `cfg.DebugRoutes = false` | `DefaultConfig()` の時点で `false` だが、検証用エンドポイント（Chapter 07）を登録しないことを明示する |
| `httptest.NewServer` | 空いているポートで実際に待ち受ける。リクエストは TCP を通り、Middleware・ルーティング・Cookie まで本番と同じ経路で処理される |
| `t.Helper()` | テストが失敗したとき、この関数の中ではなく呼び出し元の行番号が表示される |
| `t.Cleanup` | テストの終了時に、成功・失敗を問わず呼ばれる。`defer` と違い、ヘルパー関数の中で登録しても、呼び出し元のテストが終わるまで実行を待つ |

`app.New()` に依存を注入できる設計（Chapter 05）が、ここで効いてくる。グローバル変数の `pool` が残っていたら、テストごとに別の DB へ向けられない。

<details>
<summary>GO NOTE: <code>httptest.NewServer</code> と <code>httptest.NewRecorder</code></summary>

| | `NewServer` | `NewRecorder` |
| --- | --- | --- |
| 仕組み | 実際にポートを開き、HTTP で通信する | `Handler.ServeHTTP` を関数として直接呼ぶ |
| Cookie Jar | `http.Client` の Jar がそのまま使える | Cookie を自分でヘッダーに詰める |
| 向いている用途 | ログインから一連の操作を通すシナリオ | Handler 単体の入出力 |

この章ではログイン後の Session Cookie を引き継ぎたいので、`NewServer` を使う。

</details>

##### 3. HTTP クライアント

同じファイルに続けて書く。

```go
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
```

| コード | 理由 |
| --- | --- |
| `client` に Cookie Jar を1つずつ持たせる | alice と bob で別の `client` を作れば、別々のブラウザでログインしているのと同じ状態になる。Session Cookie が混ざらない |
| `do` | JSON への変換、`Content-Type` の設定、レスポンスの読み切りを1か所にまとめる。テスト本体には「誰が・何を送り・何が返るべきか」だけが残る |
| `mustStatus` が `Fatalf` | 登録に失敗したらログインも失敗し、以降のすべての行が失敗する。最初の失敗で止めれば、原因の行だけが表示される。失敗メッセージにレスポンスの Body を含めているので、400 の理由などもそのまま読める |
| 戻り値が `[]byte` | `decode` にそのまま渡して、作成された ID を取り出せる |
| `middleware.IdempotencyKeyHeader` | ヘッダー名を文字列で書かず、アプリ側の定数（Chapter 07）を使う。名前を変えたときにテストだけ古いまま残らない |
| `registerAndLogin` | Step 6 以降のテストは、登録とログインが前提条件にすぎない。1行にまとめて本題を読みやすくする |

##### 4. レスポンスの読み取りと DB の検証

同じファイルの末尾に書く。

```go
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
```

| コード | 理由 |
| --- | --- |
| `decode[T any]` | Generics（Go 1.18 以降）。`decode[idResponse](t, body)` のように、呼び出し側で受け取る型を指定する。型ごとに decode 関数を書かずに済む |
| `idResponse` | 作成 API のレスポンスから `id` だけを取り出す。ほかのフィールドは無視される |
| `errorResponse` | Chapter 03 のエラー形式（`{"error":{"code":...,"message":...}}`）を読む。Step 6 の `TestValidationThroughHTTP` が使う |
| `projectPath` / `taskPath` | ID の埋め込みを1か所にまとめる。`"/tasks/" + id` の書き間違いを防ぐ |
| `assertXxx` が `pool` で直接 SELECT | アプリの API で読み直すと、書き込みと読み出しの両方に同じバグがあったとき気づけない。アプリを通らない経路で DB を読む |

#### 期待結果

テストはまだないが、コンパイルが通ることを確認する。`go-kanban/` で実行する。

```bash
go vet -tags=integration ./test/...
```

何も表示されずに終われば成功。

> **NOTE**
> import の `example.com/go-kanban` は Chapter 01 の `go mod init` に合わせている。別のモジュールパスで始めた場合は、自分の `go.mod` の `module` 行に読み替える。

#### なぜ `t.Parallel()` を付けないのか

Part 1〜2 のテストにはすべて `t.Parallel()` を付けた。Integration Test には付けない。

全テストが1つの `kanban_test` を共有し、各テストの最初に `TRUNCATE` するから。並列に動かすと、あるテストが作ったデータを、別のテストの `TRUNCATE` が途中で消す。

```text
TestA: 登録 → Project 作成 ─────────────→ Task 取得 → 404（期待は 200）
TestB:                     TRUNCATE ↑
```

失敗するかどうかが実行のタイミングで変わるので、原因を追いにくい。並列にしたい場合は、テストごとに別の DB やスキーマを用意する必要がある。

---

### Step 5. 認可シナリオをテストする

#### やること

2人のユーザー（alice と bob）を作り、Chapter 04 で curl を使って確認した認証・認可を、1つのシナリオとして自動化する。

#### 実行

`test/scenario_test.go` を作る。

```go
//go:build integration

package test

import (
	"net/http"
	"strings"
	"testing"

	"example.com/go-kanban/internal/model"
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
```

`strings` は Step 6 で追記する `TestValidationThroughHTTP` が使う。先に import しておく。

bob が受け取るレスポンスを、段階ごとに並べる。

| bob の立場 | 操作 | 期待 | 確認していること |
| --- | --- | --- | --- |
| Project 外 | alice の Task を GET | 404 | 他人の Task の存在を漏らさない（Chapter 04） |
| Project 外 | 存在しない Task を GET | 404 | 上の 404 と区別がつかない |
| Project 外 | Project の Task 一覧・作成 | 403 | メンバーでなければ Project 配下を操作できない |
| Viewer | alice の Task を GET | 200 | 読める |
| Viewer | Task 作成・Status 変更 | 403 | 書けない |
| Viewer | Member 追加 | 403 | Member を追加できるのは Owner だけ |

| コード | 理由 |
| --- | --- |
| `alice` と `bob` を別の `client` にする | Cookie Jar が別なので、2人が同時にログインしている状態を作れる |
| `bobUser.ID` を使う | ID を `2` と直書きしない。今は `RESTART IDENTITY` で 2 になるが、登録の順番を変えただけで別人を指す（Chapter 04 の「ID は固定の値にしない」と同じ理由） |
| `task.ID+1000` | 確実に存在しない ID。他人の Task と同じ 404 が返ることで、「404 だから存在しない」とも「存在する」とも判断できないことを確かめる |
| 最後にログアウト | Session が DB から消え、同じ Cookie が使えなくなることを確認する |

> **NOTE**
> 同じ bob でも、Task の GET は 404、Project の一覧は 403 になる。Chapter 04 の実装では、Task は取得と認可を1つのクエリにまとめたので「見えない = 404」になる。Project 配下の操作は、先にメンバーかどうかを確認して 403 で断る。
> そのため Project ID の存在は、403 から推測できる。Project の存在も隠したい要件なら、Project 側も 404 に揃える。

Chapter 04 で curl を使って手で確認したことを、いつでも自動で再実行できるようになった。

---

### Step 6. DB の状態まで検証する

#### やること

HTTP のレスポンスだけでなく、DB に何が書かれたかを確認する。Chapter 06 の Transaction と Optimistic Lock、Chapter 07 の冪等性を対象にする。

#### 実行

`test/scenario_test.go` の末尾に追記する。

```go
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
```

リクエストごとに、Task と履歴がどう変わるかを並べる。

| リクエスト | 期待 | Task の status / version | 履歴の件数 |
| --- | --- | --- | --- |
| （作成直後） | | todo / 1 | 0 |
| todo → done（version 1） | 400 | todo / 1（変わらない） | 0 |
| todo → doing（version 1） | 200 | doing / 2 | 1 |
| doing → done（version 1、古い） | 409 | doing / 2（変わらない） | 1 |
| doing → done（version 2） | 200 | done / 3 | 2 |

> **POINT**
> 拒否されたリクエストで DB が変わっていないことまで確認している。
> 「400 が返った」だけでは、DB に書いてからロールバックしたのか、そもそも書いていないのか区別できない。
> 成功時は、Task の更新と履歴の追加が「両方」起きていることを確認する。片方だけ増えていたら、Chapter 06 の Transaction が効いていない。

409 の後に正しい version で更新できることも確認している。Optimistic Lock が「一度競合したら二度と更新できない」状態を作っていないことを確かめるため。

続けて、冪等性を検証する。同じファイルの末尾に追記する。

```go
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
```

| 送信者と Key | 期待 | Task の件数 | 確認していること |
| --- | --- | --- | --- |
| alice / `key-123` | 201 | 1 | 通常どおり作成される |
| alice / `key-123`（再送） | 201、同じ ID | 1 | 保存したレスポンスを返し、二重に作成しない |
| alice / `key-456` | 201 | 2 | Key が違えば別の処理になる |
| bob / `key-123` | 403 | 2 | Key はユーザーごとに区別される。他人の Key を使っても、alice のレスポンス（Task の ID）は手に入らない |

2回目も 201 が返るので、ステータスだけを見ると「2件作られた」のと区別がつかない。ID の一致と DB の件数で、作られたのが1件だけであることを確かめる。

最後の bob の行は、Idempotency-Key をユーザーと組にして保存していなければ失敗する。Key だけで保存していると、bob に alice のレスポンスがそのまま返ってしまう。

最後に、Validation の経路を確認する。同じファイルの末尾に追記する。

```go
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
```

| コード | 理由 |
| --- | --- |
| ケースを4つに絞る | 100 文字と 101 文字の境界などは Step 2 の Unit Test で確認済み。ここでは「Validation のエラーが HTTP の 400 と `invalid_request` になって返るか」という経路だけを見る。[テストピラミッド](#テストピラミッド)の「同じことを複数の層でテストしない」に当たる |
| `"priorty"`（綴りの誤り） | 未知のフィールドを拒否する設定（Chapter 03 の `DisallowUnknownFields`）が、HTTP 経由でも効いていることを確認する |
| サブテストに `t.Parallel()` がない | 親テストと同じ DB を使う。[Step 4](#なぜ-tparallel-を付けないのか) と同じ理由 |
| 最後の `assertTaskCount(..., 0)` | 400 を返したリクエストが、1件も Task を作っていないことを確認する |

---

### Step 7. テストを実行する

#### 実行

```bash
# Unit / Service / Notify（DB 不要）
go test ./...

# 競合状態の検出付き
go test -race ./...

# Integration（実 DB が必要。Step 4 で作った kanban_test を使う）
go test -tags=integration ./test/...
```

#### 期待結果

検証環境での実際の出力。パッケージ名の `example.com/go-kanban` は、自分の `go.mod` の `module` 行に読み替える。

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

Integration Test の内訳。`go test -v -tags=integration ./test/...` のように `-v` を付けると表示される（サブテストの行は省略）。

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
| --- | --- |
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

const BASE_URL = __ENV.BASE_URL || 'http://localhost:8980';
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

| コード | 意味 |
| --- | --- |
| VU（Virtual User） | 並行してリクエストを送る仮想ユーザー。`stages` の `target` は VU の数で、各 VU が `default` 関数を繰り返し実行する |
| `setup()` の戻り値 | 各 VU の `default` 関数に引数 `data` として渡される。ログインと Project の作成は1回だけ行い、全 VU で共有する |
| Cookie を手でヘッダーに付ける | k6 の Cookie Jar は VU ごとに分かれていて、`setup()` でログインしたときの Cookie は VU に引き継がれない。そのため Cookie の値を `data` で受け渡し、`Cookie` ヘッダーとして送る。これを消すと全リクエストが 401 になる |
| `__VU` / `__ITER` | 実行中の VU の番号と、その VU の何回目の実行かを表す。Task のタイトルが重複しないように使う |
| `check` | 条件を満たした割合を `checks` として集計する。失敗してもスクリプトは止まらない |
| `group` | 結果をグループ名ごとにまとめて表示する |
| `thresholds` | 合否の基準。1つでも超えると、k6 は結果の最後にエラーを出して終了コードを非 0 にする |

API サーバ（`go run ./cmd/api`）と DB を起動した状態で、k6 を Docker で実行する。サーバログはあとで集計に使うので、ファイルへ保存しておく。

```bash
# 別ターミナルで API を起動（ログを logs/server.log に残す）
mkdir -p logs
go run ./cmd/api > logs/server.log 2>&1

# k6 を実行（スクリプトは標準入力から渡す）
docker run --rm -i \
  -e BASE_URL=http://host.docker.internal:8980 \
  grafana/k6:2.3.0 run - < scripts/load-test.js
```

| 指定 | 理由 |
| --- | --- |
| `-e BASE_URL=http://host.docker.internal:8980` | コンテナの中の `localhost` はコンテナ自身を指す。ホストで動く API へは `host.docker.internal` で届く |
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
| --- | --- | --- | --- | --- | --- |
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
| --- | --- |
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
| --- | --- |
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
grep http_request logs/server.log | grep -E '"duration_ms":[0-9]{3,}'
```

Chapter 08 で入れた `duration_ms` が、ここで使える。

#### 負荷テストで見つかりやすい問題

| 問題 | 現れ方 |
| --- | --- |
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
| --- | --- |
| `TaskRepository` interface | Fake に差し替えて Service を DB なしでテストするため |
| `Notifier` interface | 外部 API を落とさずに失敗時の挙動をテストするため |
| `httpx` package | import cycle のコンパイルエラーを解消するため |
| `app.New()` の引数注入 | テストごとに別の設定・DB を渡すため |

いずれも「そうしないと困る」という具体的な問題があった。逆に、次のものは導入していない。

| 導入しなかったもの | 理由 |
| --- | --- |
| `ProjectService` の interface | 差し替える必要が生じていない |
| DI コンテナ | `app.New()` の 20 行で足りている |
| Repository ごとの独立した interface ファイル | 実装と並べたほうが対応を追いやすい |
| ドメインイベント / CQRS | 今の規模では複雑さだけが増える |

#### 残っている改善候補

<details>
<summary>このハンズオンで意図的に残した課題</summary>

| 課題 | 現状 | 本番で必要になること |
| --- | --- | --- |
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
| --- | --- |
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
| --- | --- | --- |
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
