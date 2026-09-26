# Chapter 05: 層分割・SQL Injection・N+1

## この章の目的

`cmd/api/` に機能を足し続けた結果、`main.go` が378行、5ファイル合計865行になった。HTTP の処理、業務ルール、SQL がすべて同じ関数に混在している。

この章で3つのことをやる。

1. **層を分割する** — Handler / Service / Repository へ責務を分ける
2. **SQL Injection を再現する** — 文字列連結の危険を、実際に他人のデータを読み出して確認する
3. **N+1 を計測する** — Query 発行回数と応答時間を実測して比較する

分割自体が目的ではない。「分割しないと何が困るのか」を先に確認する。

## 現在地

基礎(01-02) → **Webアプリ化(03-05)** → 本番対応(06-08) → Test(09)

## 完了条件

- [ ] `internal/` 配下へ層が分割され、`go build ./...` が通る
- [ ] `main.go` が起動処理だけになる
- [ ] SQL Injection で他プロジェクトの Task を読み出せることを確認した
- [ ] プレースホルダ版では同じ攻撃が通らないことを確認した
- [ ] N+1 実装と JOIN 実装の Query 数・所要時間を比較した

---

## Part 1. 層を分割する

### なぜ分割するのか

分割の基準は「綺麗に見えるから」ではない。**変更理由が異なるコードを分ける**。

```text
このコードは、何が変わったときに修正が必要になるか？

  HTTP の仕様が変わった        → Handler
  業務ルールが変わった          → Service
  DB スキーマ・SQL が変わった   → Repository
```

現状の `createTaskHandler` は、この3つすべての理由で変更される。つまり**変更のたびに、無関係な処理まで読む必要がある**。

```text
Before                          After

createTaskHandler               Handler      HTTP と Go 値の変換
 ├─ Cookie から User 取得         ↓
 ├─ Path から ID 取得           Service      業務ルールの判断
 ├─ JSON Decode                  ↓
 ├─ Validation                 Repository   SQL の実行
 ├─ 認可クエリ                    ↓
 ├─ INSERT                     PostgreSQL
 ├─ エラー分類
 └─ JSON 書き込み
```

---

### Step 1. ディレクトリを作る

```bash
mkdir -p internal/{model,repository,service,handler,middleware,httpx,app}
```

最終的な構成。

```text
internal/
├── model/        ドメインの型と業務ルール（他層に依存しない）
├── repository/   SQL の実行（model にのみ依存）
├── service/      業務ルールの判断（model, repository に依存）
├── handler/      HTTP と Go 値の変換（model, service, httpx に依存）
├── httpx/        handler と middleware の共通処理
├── middleware/   全 Request 共通の前処理
└── app/          依存関係の組み立てとルーティング
```

依存の向きは**一方向**にする。

```mermaid
flowchart TD
    APP[app] --> MW[middleware]
    APP --> H[handler]
    MW --> HX[httpx]
    H --> HX
    MW --> SVC[service]
    H --> SVC
    HX --> M[model]
    SVC --> REPO[repository]
    SVC --> M
    REPO --> M
```

`model` は誰にも依存しない。だから Test で単独に検証できる。

---

### Step 2. model を作る

### やること

ドメインの型と業務ルールを、HTTP にも SQL にも依存しない形で定義する。

### 実行

`internal/model/errors.go`。

```go
package model

import "errors"

// 業務上の失敗の分類。HTTP Status への変換は handler 層が行う。
var (
	ErrNotFound     = errors.New("not found")
	ErrForbidden    = errors.New("forbidden")
	ErrConflict     = errors.New("conflict")
	ErrUnauthorized = errors.New("unauthorized")
)

// ValidationError は「入力が不正である」という分類を表す。
type ValidationError struct {
	Message string
}

func (e *ValidationError) Error() string {
	return e.Message
}

func Invalid(message string) error {
	return &ValidationError{Message: message}
}

// PublicError は分類と、利用者へ見せてよい文言を束ねる。
type PublicError struct {
	err     error
	message string
}

func (e *PublicError) Error() string {
	return e.message + ": " + e.err.Error()
}

func (e *PublicError) Unwrap() error {
	return e.err
}

func (e *PublicError) PublicMessage() string {
	return e.message
}

func Public(err error, message string) error {
	return &PublicError{err: err, message: message}
}
```

`internal/model/task.go`（抜粋。状態遷移ルールは Chapter 06 で使う）。

```go
package model

import "strings"

const MaxTitleLength = 100

type Task struct {
	ID          int64  `json:"id"`
	ProjectID   int64  `json:"project_id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Priority    string `json:"priority"`
	Status      string `json:"status"`
	Version     int    `json:"version"`
	AssigneeID  *int64 `json:"assignee_id"`
}

// CreateTaskInput は Service への入力。HTTP や JSON には依存しない。
type CreateTaskInput struct {
	ProjectID   int64
	Title       string
	Description string
	Priority    string
}

func (in *CreateTaskInput) Normalize() {
	in.Title = strings.TrimSpace(in.Title)
	in.Description = strings.TrimSpace(in.Description)

	if in.Priority == "" {
		in.Priority = "medium"
	}
}

func (in CreateTaskInput) Validate() error {
	if in.Title == "" {
		return Invalid("title is required")
	}

	if len([]rune(in.Title)) > MaxTitleLength {
		return Invalid("title must be 100 characters or fewer")
	}

	if !validPriorities[in.Priority] {
		return Invalid("priority must be one of: low, medium, high")
	}

	return nil
}
```

`internal/model/member.go`。

```go
package model

// Role は Project 内での権限。
const (
	RoleOwner  = "owner"
	RoleMember = "member"
	RoleViewer = "viewer"
)

func IsValidRole(role string) bool {
	switch role {
	case RoleOwner, RoleMember, RoleViewer:
		return true
	default:
		return false
	}
}

// CanWriteTask は Task を作成・更新できる Role かを判断する。
func CanWriteTask(role string) bool {
	return role == RoleOwner || role == RoleMember
}

// CanManageMembers は Member を追加・変更できる Role かを判断する。
func CanManageMembers(role string) bool {
	return role == RoleOwner
}
```

### なぜ `CreateTaskInput` と `createTaskRequest` を分けるのか

Handler 側には JSON タグ付きの型を別に置く。

```go
// internal/handler/task.go
type createTaskRequest struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	Priority    string `json:"priority"`
}
```

型が2つになって冗長に見えるが、**JSON のキー名を変える**という HTTP 都合の変更が、Service や Repository へ波及しなくなる。`model` に `json:"..."` タグを書くと、ドメインの型が HTTP の表現に縛られる。

---

### Step 3. repository を作る

### やること

SQL の実行を Repository へ集約し、Service が依存する interface を定義する。

### 実行

`internal/repository/task.go`（抜粋）。

```go
// TaskRepository は Service が必要とする振る舞いだけを宣言する。
// Service は PostgreSQL も pgx も知らない。
type TaskRepository interface {
	Create(ctx context.Context, in model.CreateTaskInput) (model.Task, error)
	FindForUser(ctx context.Context, taskID, userID int64) (model.Task, string, error)
	ListByProject(ctx context.Context, projectID int64) ([]model.Task, error)
	Search(ctx context.Context, projectID int64, keyword string) ([]model.Task, error)
	UpdateStatusWithHistory(
		ctx context.Context,
		taskID, userID int64,
		oldStatus, newStatus string,
		version int,
	) (model.Task, error)
}

// コンパイル時に「PgTaskRepository は TaskRepository を満たすか」を確認する。
// 満たさなくなったら、利用箇所ではなくここで失敗する。
var _ TaskRepository = (*PgTaskRepository)(nil)

// taskColumns は Scan 順と SELECT 順のズレを防ぐために1か所へまとめる。
const taskColumns = `id, project_id, title, description, priority, status, version, assignee_id`

type PgTaskRepository struct {
	pool *pgxpool.Pool
}

func NewTaskRepository(pool *pgxpool.Pool) *PgTaskRepository {
	return &PgTaskRepository{pool: pool}
}

func scanTask(row pgx.Row) (model.Task, error) {
	var task model.Task

	err := row.Scan(
		&task.ID, &task.ProjectID, &task.Title, &task.Description,
		&task.Priority, &task.Status, &task.Version, &task.AssigneeID,
	)

	return task, err
}

func (r *PgTaskRepository) Create(ctx context.Context, in model.CreateTaskInput) (model.Task, error) {
	row := r.pool.QueryRow(
		ctx,
		`INSERT INTO tasks (project_id, title, description, priority)
		 VALUES ($1, $2, $3, $4)
		 RETURNING `+taskColumns,
		in.ProjectID, in.Title, in.Description, in.Priority,
	)

	task, err := scanTask(row)

	if IsForeignKeyViolation(err) {
		return model.Task{}, model.Public(model.ErrNotFound, "project not found")
	}

	if err != nil {
		return model.Task{}, fmt.Errorf("insert task: %w", err)
	}

	return task, nil
}

// FindForUser は「存在するか」ではなく
// 「このUserから見てアクセス可能か」を1つのQueryで判断する。
func (r *PgTaskRepository) FindForUser(ctx context.Context, taskID, userID int64) (model.Task, string, error) {
	var (
		task model.Task
		role string
	)

	err := r.pool.QueryRow(
		ctx,
		`SELECT t.id, t.project_id, t.title, t.description,
		        t.priority, t.status, t.version, t.assignee_id, pm.role
		 FROM tasks t
		 JOIN project_members pm ON pm.project_id = t.project_id
		 WHERE t.id = $1 AND pm.user_id = $2`,
		taskID, userID,
	).Scan(
		&task.ID, &task.ProjectID, &task.Title, &task.Description,
		&task.Priority, &task.Status, &task.Version, &task.AssigneeID, &role,
	)

	// 存在しないTaskと他人のTaskを区別せず ErrNotFound にする。
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Task{}, "", model.ErrNotFound
	}

	if err != nil {
		return model.Task{}, "", fmt.Errorf("query task for user: %w", err)
	}

	return task, role, nil
}
```

<details>
<summary>GO NOTE: <code>var _ TaskRepository = (*PgTaskRepository)(nil)</code> の意味</summary>

「この型は、この interface を満たしている」ことをコンパイル時に宣言する慣用句。

- `var _ =` — 変数名を `_` にして「値は使わない」ことを示す
- `(*PgTaskRepository)(nil)` — nil ポインタを型変換しただけ。実体は作られない

メソッド名を1文字間違えたとき、**この行でコンパイルエラーになる**。この宣言がないと、エラーは遠く離れた利用箇所で「型が合わない」として出る。原因の特定に時間がかかる。

</details>

<details>
<summary>GO NOTE: interface はどちら側に置くか</summary>

Go では **interface を「使う側」の package に置く**のが一般的とされる。本来なら `TaskRepository` は `service` package に置くべきだ。

このハンズオンでは `repository` package に置いている。理由は、実装（`PgTaskRepository`）と並べたほうが初学時に対応を追いやすいため。

どちらが正しいという話ではなく、**「Service が必要とする振る舞いだけを宣言する」という原則のほうが本質**で、置き場所は二次的な判断になる。実務で Repository の実装が複数（PostgreSQL / インメモリ / 外部 API）になったら、使う側へ移すことを検討する。

</details>

---

### Step 4. service を作る

### やること

業務ルールの判断を Service へ集約する。

### 実行

`internal/service/task.go`（抜粋）。

```go
// Notifier は外部通知の契約。Service は HTTP も Retry も知らない。
type Notifier interface {
	TaskStatusChanged(ctx context.Context, task model.Task, oldStatus string) error
}

type TaskService struct {
	tasks    repository.TaskRepository
	projects repository.ProjectRepository
	notifier Notifier
	logger   *slog.Logger
}

func NewTaskService(
	tasks repository.TaskRepository,
	projects repository.ProjectRepository,
	notifier Notifier,
	logger *slog.Logger,
) *TaskService {
	return &TaskService{tasks: tasks, projects: projects, notifier: notifier, logger: logger}
}

func (s *TaskService) Create(ctx context.Context, userID int64, in model.CreateTaskInput) (model.Task, error) {
	in.Normalize()

	if err := in.Validate(); err != nil {
		return model.Task{}, err
	}

	role, err := s.projects.RoleOf(ctx, in.ProjectID, userID)
	if err != nil {
		return model.Task{}, err
	}

	if !model.CanWriteTask(role) {
		return model.Task{}, model.ErrForbidden
	}

	return s.tasks.Create(ctx, in)
}
```

### なぜ Validation が Service にあるのか

Chapter 03 では Handler で Validation していた。層を分けたので、**Service へ移す**。

```text
Handler の Validation        「JSON として読めるか」「数値として解釈できるか」
                             → HTTP / 形式の話

Service の Validation        「title は必須」「priority は3種類のいずれか」
                             → 業務ルールの話
```

業務ルールを Handler に置くと、CLI やバッチから同じ処理を呼んだときにルールが抜ける。**API の入口が増えるたびにルールをコピーする**ことになる。

---

### Step 5. import cycle に遭遇する

### やること

`middleware` と `handler` を書くと、必ずコンパイルエラーになる。これを解消する。

### 実行

素直に書くと、こうなる。

- `middleware/auth.go` は `handler.RespondError()` を呼びたい
- `handler/task.go` は `middleware.CurrentUser()` を呼びたい

```bash
go vet ./internal/...
```

### 期待結果

実際に出たエラー。

```text
package example.com/go-kanban/internal/handler
	imports example.com/go-kanban/internal/middleware from auth.go
	imports example.com/go-kanban/internal/handler from auth.go: import cycle not allowed
```

Go は package 間の循環参照を許さない。

```mermaid
flowchart LR
    H[handler] -->|CurrentUser| M[middleware]
    M -->|RespondError| H
    style H fill:#ffe0e0,color:#000
    style M fill:#ffe0e0,color:#000
```

### 解決方法

**両方が必要とする共通部分を、第三の package へ切り出す。**

```mermaid
flowchart TD
    H[handler] --> X[httpx<br/>WriteJSON / RespondError<br/>Context アクセサ]
    M[middleware] --> X
    X --> MO[model]
```

`internal/httpx/httpx.go`（抜粋）。

```go
// --- Request Context ---------------------------------------------------
//
// handler も middleware も、この package を経由して Context を読み書きする。
// middleware が handler を import すると、handler -> middleware との間で
// import cycle になるため、共有部分をここへ集約する。

const SessionCookieName = "kanban_session"

type contextKey string

const (
	userContextKey      contextKey = "user"
	requestIDContextKey contextKey = "request_id"
)

func WithUser(ctx context.Context, user model.User) context.Context {
	return context.WithValue(ctx, userContextKey, user)
}

// CurrentUser は認証middlewareが載せた User を取り出す。
func CurrentUser(ctx context.Context) (model.User, bool) {
	user, ok := ctx.Value(userContextKey).(model.User)
	return user, ok
}
```

`RespondError` も `httpx` へ移す。

```go
// RespondError は内部 error を HTTP Status へ変換する唯一の場所。
func RespondError(w http.ResponseWriter, r *http.Request, err error) {
	var validationErr *model.ValidationError

	switch {
	case errors.As(err, &validationErr):
		writeErrorBody(w, http.StatusBadRequest, "invalid_request", validationErr.Message)
	case errors.Is(err, model.ErrUnauthorized):
		writeErrorBody(w, http.StatusUnauthorized, "unauthorized", "authentication required")
	case errors.Is(err, model.ErrForbidden):
		writeErrorBody(w, http.StatusForbidden, "forbidden", "operation not allowed")
	case errors.Is(err, model.ErrNotFound):
		writeErrorBody(w, http.StatusNotFound, "not_found",
			publicMessage(err, "resource not found"))
	case errors.Is(err, model.ErrConflict):
		writeErrorBody(w, http.StatusConflict, "conflict",
			publicMessage(err, "resource was updated by another request"))
	case errors.Is(err, context.DeadlineExceeded):
		// 処理は打ち切ったが、利用者から見れば「今は使えない」状態。
		writeErrorBody(w, http.StatusServiceUnavailable, "timeout", "request timed out")
	default:
		slog.ErrorContext(r.Context(), "unexpected error", slog.String("error", err.Error()))
		writeErrorBody(w, http.StatusInternalServerError, "internal_error", "internal server error")
	}
}
```

> **POINT**
> import cycle は「設計の問題をコンパイラが教えてくれている」状態と考える。無理に回避せず、**何が共通概念なのかを考えて切り出す**。
> ここでは「HTTP レイヤの共通語彙」が `httpx` として分離できた。

---

### Step 6. app で組み立てる

### やること

依存関係の組み立てとルーティングを1か所へ集める。

### 実行

`internal/app/app.go`（抜粋）。

```go
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

	mux.Handle("POST /projects", requireAuth(projectHandler.Create))
	mux.Handle("POST /projects/{id}/members", requireAuth(projectHandler.AddMember))
	mux.Handle("GET /projects/{id}/tasks", requireAuth(taskHandler.ListByProject))
	mux.Handle("GET /tasks/{id}", requireAuth(taskHandler.Get))
	mux.Handle("PATCH /tasks/{id}/status", requireAuth(taskHandler.UpdateStatus))

	return mux
}
```

`cmd/api/main.go` は起動処理だけになる。

```go
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

	server := &http.Server{
		Addr:              ":8080",
		Handler:           app.New(pool, logger, app.DefaultConfig()),
		ReadHeaderTimeout: 5 * time.Second,
	}

	serverErr := make(chan error, 1)

	go func() {
		logger.Info("server started", slog.String("addr", server.Addr))

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
```

> **POINT**
> `pool` のグローバル変数が消えた。各層は `New(...)` の引数で依存を受け取る。
> これにより Chapter 09 で、DB を Fake に差し替えたテストが書けるようになる。

<details>
<summary>この移行で <code>POST /login</code> のレスポンスが 200 から 204 へ変わる</summary>

Chapter 04 の実装は、ログイン成功時に 200 と `{"user_id":1}` を返していた。層分割にあわせて 204 No Content へ変更する。

```go
// internal/handler/auth.go
	http.SetCookie(w, &http.Cookie{ /* ... */ })

	w.WriteHeader(http.StatusNoContent)
```

理由は、**このレスポンスの本体に意味のある情報がない**こと。`user_id` は Client が送った email に対応する値で、Client 側の処理に必要なら `GET /me` のような専用エンドポイントで取得すべきものになる。ログインの成果物は Body ではなく `Set-Cookie` ヘッダにある。

Chapter 09 の Integration Test は、この 204 を前提に書かれている。

</details>

### 期待結果

```bash
go build ./...
go vet ./...
```

両方とも何も出力しなければ成功。Chapter 04 までの API が同じように動くことも確認する。

---

## Part 2. SQL Injection を再現する

### 危険な実装

```go
// SearchUnsafe は SQL Injection を再現するための実装。
// 絶対に本番コードへ持ち込まない。
func (r *PgTaskRepository) SearchUnsafe(
	ctx context.Context,
	projectID int64,
	keyword string,
) ([]model.Task, error) {
	query := fmt.Sprintf(
		`SELECT %s FROM tasks WHERE project_id = %d AND title LIKE '%%%s%%' ORDER BY id`,
		taskColumns, projectID, keyword,
	)

	rows, err := r.pool.Query(ctx, query)
	// ...
}
```

利用者の入力 `keyword` が、SQL 文の一部として組み立てられている。

### 安全な実装

```go
// Search はキーワードでTaskを絞り込む。
// 値は必ず Placeholder($2) で渡し、SQL文と連結しない。
func (r *PgTaskRepository) Search(
	ctx context.Context,
	projectID int64,
	keyword string,
) ([]model.Task, error) {
	rows, err := r.pool.Query(
		ctx,
		`SELECT `+taskColumns+`
		 FROM tasks
		 WHERE project_id = $1 AND title ILIKE '%' || $2 || '%'
		 ORDER BY id`,
		projectID, keyword,
	)
	// ...
}
```

```text
安全な実装

  SQL 文   SELECT ... WHERE project_id = $1 AND title ILIKE '%' || $2 || '%'
  値       [1, "攻撃文字列"]
             ↑
           別々に DB へ送られる。値が構文として解釈されることはない
```

---

### Step 7. 攻撃を実行する

### やること

alice と bob の Project を用意し、alice が bob の非公開 Task を読み出せるか試す。

### 実行

検証用のエンドポイント（`GET /debug/unsafe-search/{id}`）を一時的に用意して比較する。

```bash
# 準備: bob が非公開の Project と Task を作る
curl -b bob.txt -X POST localhost:8080/projects \
  -H 'Content-Type: application/json' -d '{"name":"Bob Private"}'
curl -b bob.txt -X POST localhost:8080/projects/2/tasks \
  -H 'Content-Type: application/json' -d '{"title":"bob salary negotiation","priority":"high"}'

# alice が自分の Project を普通に検索
curl -b alice.txt 'localhost:8080/projects/1/tasks?q=docs'

# 攻撃文字列を、安全な実装へ投げる
curl -b alice.txt --get --data-urlencode "q=%' OR project_id > 0 --" \
  localhost:8080/projects/1/tasks

# 同じ攻撃文字列を、危険な実装へ投げる
curl -b alice.txt --get --data-urlencode "q=%' OR project_id > 0 --" \
  localhost:8080/debug/unsafe-search/1
```

`--data-urlencode` を使うと、`'` や空白を含む文字列を安全に URL へ載せられる。

### 期待結果

検証環境での実際の出力。

**安全な実装（プレースホルダ）。**

```json
[]
```

攻撃文字列は「そういう名前のタスクを探す」として扱われた。該当なしで 0 件。

**危険な実装（文字列連結）。**

```json
{"_warn":"this endpoint is intentionally vulnerable","count":2,
 "tasks":[
   {"id":1,"project_id":1,"title":"write docs",...},
   {"id":2,"project_id":2,"title":"bob salary negotiation",...}
 ]}
```

> **alice が bob の非公開 Task を読み出せた。**
> `project_id = 1` の絞り込みも、Chapter 04 で作った認可も、**SQL の構文ごと書き換えられたため無効化された**。

### 何が起きたのか

組み立てられた SQL を展開すると。

```sql
-- 意図した形
SELECT ... FROM tasks
WHERE project_id = 1 AND title LIKE '%<keyword>%'
ORDER BY id;

-- keyword = "%' OR project_id > 0 --" を埋め込んだ結果
SELECT ... FROM tasks
WHERE project_id = 1 AND title LIKE '%%' OR project_id > 0 --%'
ORDER BY id;
                                      ^^^^^^^^^^^^^^^^^^ ^^
                                      条件を追加         以降をコメントアウト
```

`OR project_id > 0` が全行にマッチし、`--` 以降がコメントになって `ORDER BY` も消えた。

> **POINT**
> 通常のキーワード（`q=docs`）では、危険な実装も**正常に動作して見える**。
> テストが正常系しかないと、この脆弱性は発見できない。

### 対策の原則

| やること | 理由 |
|---|---|
| 値は必ずプレースホルダ（`$1`）で渡す | SQL 文と値が別経路で送られる |
| 「この入力は安全だから」で例外を作らない | 安全性の判断は将来の変更で崩れる |
| エスケープ関数を自作しない | 網羅漏れが必ず出る |
| テーブル名・列名を動的にしたい場合は許可リストで照合する | プレースホルダは識別子に使えない |

```go
// 識別子を動的にする場合（プレースホルダが使えない）
var allowedSortColumns = map[string]string{
    "created": "created_at",
    "title":   "title",
}

column, ok := allowedSortColumns[userInput]
if !ok {
    return model.Invalid("invalid sort key")
}
// column は自分が定義した文字列なので、連結してよい
```

---

## Part 3. N+1 を計測する

### 問題の構造

「各 Task の担当者メールアドレスも返したい」という要件を、素直に実装すると。

```text
Task 一覧を取得          1 Query
 ↓
Task 1 の担当者を取得     1 Query
Task 2 の担当者を取得     1 Query
Task 3 の担当者を取得     1 Query
...
Task 200 の担当者を取得   1 Query
                       ─────────
                        201 Query
```

Task が N 件なら N+1 回。これを **N+1 問題**と呼ぶ。

### 実装の比較

**N+1 版。**

```go
func (r *PgTaskRepository) ListWithAssigneeNaive(
	ctx context.Context,
	projectID int64,
) ([]TaskWithAssignee, int, error) {
	tasks, err := r.ListByProject(ctx, projectID)
	if err != nil {
		return nil, 0, err
	}

	queries := 1
	result := make([]TaskWithAssignee, 0, len(tasks))

	for _, task := range tasks {
		item := TaskWithAssignee{Task: task}

		if task.AssigneeID != nil {
			queries++

			err := r.pool.QueryRow(
				ctx,
				"SELECT email FROM users WHERE id = $1",
				*task.AssigneeID,
			).Scan(&item.AssigneeEmail)
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return nil, queries, fmt.Errorf("query assignee: %w", err)
			}
		}

		result = append(result, item)
	}

	return result, queries, nil
}
```

**JOIN 版。**

```go
func (r *PgTaskRepository) ListWithAssigneeJoin(
	ctx context.Context,
	projectID int64,
) ([]TaskWithAssignee, int, error) {
	rows, err := r.pool.Query(
		ctx,
		`SELECT t.id, t.project_id, t.title, t.description,
		        t.priority, t.status, t.version, t.assignee_id,
		        COALESCE(u.email, '')
		 FROM tasks t
		 LEFT JOIN users u ON u.id = t.assignee_id
		 WHERE t.project_id = $1
		 ORDER BY t.id`,
		projectID,
	)
	// ... Scan して返す
}
```

`LEFT JOIN` を使うのは、担当者が未設定（`assignee_id IS NULL`）の Task も結果に含めるため。`INNER JOIN` だと担当者なしの Task が消える。

---

### Step 8. 計測する

### やること

同じ Project に 200 件の Task を入れ、両実装の Query 数と所要時間を比較する。

### 実行

```bash
# テストデータを投入
docker compose exec -T db psql -U kanban -d kanban -c \
  "INSERT INTO tasks (project_id, title, assignee_id)
   SELECT 1, 'task ' || g, (g % 2) + 1 FROM generate_series(1, 200) g;"

# 比較用エンドポイントを3回叩く
for i in 1 2 3; do curl -s -b alice.txt localhost:8080/debug/nplus1/1; echo; done
```

### 期待結果

検証環境での実際の出力（Task 201 件）。

```json
{"join_ms":1,"join_queries":1,"naive_ms":65,"naive_queries":201,"rows":201,"same_rows":true}
{"join_ms":0,"join_queries":1,"naive_ms":64,"naive_queries":201,"rows":201,"same_rows":true}
{"join_ms":1,"join_queries":1,"naive_ms":70,"naive_queries":201,"rows":201,"same_rows":true}
```

| 実装 | Query 数 | 所要時間 | 返す結果 |
|---|---:|---:|---|
| N+1（ループ内 Query） | **201** | **64〜70 ms** | 同じ |
| JOIN（1 Query） | **1** | **0〜1 ms** | 同じ |

**同じ結果を返すのに、65 倍以上の差が出た。**

これはローカルの Docker（ネットワーク遅延ほぼゼロ）での測定値になる。DB が別ホストにあり 1 Query あたり 1ms の往復遅延がある環境なら、N+1 版は 200ms 以上追加でかかる。

### なぜ遅いのか

```mermaid
flowchart LR
    subgraph N["N+1: 往復が件数分発生する"]
        direction TB
        A1[App] -->|Query 1| D1[(DB)]
        D1 -->|結果| A1
        A1 -->|Query 2| D1
        D1 -->|結果| A1
        A1 -->|... 201 回| D1
    end
    subgraph J["JOIN: 往復は1回"]
        direction TB
        A2[App] -->|Query| D2[(DB)]
        D2 -->|201 行| A2
    end
```

SQL の実行そのものより、**通信の往復回数**が支配的になる。

### JOIN が常に正解ではない

> **POINT**
> N+1 は「JOIN を使えば常に解決」という話ではない。

| 状況 | 適した手法 |
|---|---|
| 1対1、または 1対少数の関連 | JOIN |
| 1対多で、親の列が大量に複製される | 2 Query に分けて、アプリ側で組み立てる |
| 関連先の種類が多く JOIN が複雑になる | `WHERE id = ANY($1)` によるバッチ取得 |
| 関連データが実はほぼ不要 | そもそも取得しない（必要なときだけ取る） |

**バッチ取得の例。**

```go
// ループで N 回引く代わりに、ID をまとめて1回で引く
rows, err := r.pool.Query(ctx,
    "SELECT id, email FROM users WHERE id = ANY($1)", assigneeIDs)
```

Query 数・転送データ量・可読性の3つを比較して判断する。

### 発見の仕方

N+1 はコードを読むだけでは気づきにくい。ループの中の関数呼び出しが、数階層下で Query を発行していることがある。

| 方法 | やり方 |
|---|---|
| Query 数を数える | 今回のように計測用の仕組みを入れる。pgx なら `Tracer` を使える |
| PostgreSQL 側で数える | `pg_stat_statements` 拡張で `calls` を見る |
| スロークエリログ | 個々は速いので**引っかからない**。N+1 の発見には向かない |
| 負荷テスト | Chapter 09 で実施。件数を増やしたときの劣化として現れる |

---

## この章のまとめ

| やったこと | 得られたもの |
|---|---|
| 層の分割 | 変更理由ごとにコードが分かれ、Test で差し替え可能になった |
| `httpx` の切り出し | import cycle を、共通概念の抽出として解決した |
| 依存の注入 | グローバル変数 `pool` が消えた |
| SQL Injection の再現 | 文字列連結が**認可すら無効化する**ことを実データで確認した |
| N+1 の計測 | 201 Query / 65ms 対 1 Query / 1ms という実測値を得た |

> **WARNING**
> 「綺麗に見えるから」という理由だけで interface や package を増やさない。
> 今回 `httpx` を作ったのは**コンパイルエラーという具体的な問題**があったからで、`TaskRepository` を interface にしたのは**Test で差し替えたい**という具体的な目的があったから。

次は [Chapter 06: Transaction と同時更新](./chapter06_transaction.md)。複数の更新を原子化し、2人が同時に同じ Task を更新したときに何が起きるかを再現する。
