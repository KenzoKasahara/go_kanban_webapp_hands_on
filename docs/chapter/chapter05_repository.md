# Chapter 05: 層分割・SQL Injection・N+1

## この章の目的

`cmd/api/` に機能を足し続けた結果、`main.go` が378行、5ファイル合計865行になった。HTTP の処理、業務ルール、SQL がすべて同じ関数に混在している。

この章では、まずコードを Handler / Service / Repository の3層に分ける。次に SQL Injection（入力値で SQL 文を書き換える攻撃）を実際に起こし、文字列連結で他人のデータまで読めてしまうことを確かめる。最後に N+1 の Query 回数と応答時間を測って比べる。

分割自体が目的ではないので、「分割しないと何が困るのか」から先に見ていく。

## 現在地

基礎(01-02) → **Webアプリ化(03-05)** → 本番対応(06-08) → Test(09)

## 完了条件

- [ ] `internal/` 配下へ層が分割され、`go build ./...` が通る
- [ ] `main.go` が起動処理だけになる
- [ ] SQL Injection で他プロジェクトの Task を読み出せることを確認した
- [ ] プレースホルダ版では同じ攻撃が通らないことを確認した
- [ ] N+1 実装と JOIN 実装の Query 数・所要時間を比較した

## この章のコードの載せ方

この章で書くファイルはすべて、本文に全文を載せている。書かれたとおりに写せば動く。

コードブロックの直前には、次のどれかの指示がある。

| 指示 | やること |
|---|---|
| 「〜を作成する」 | 新しいファイルを作り、ブロックの内容をそのまま書く |
| 「〜を次の内容に置き換える」 | 既存ファイルの中身をすべて消し、ブロックの内容にする |
| 「〜の末尾に追加する」 | 既存の内容を残したまま、ファイルの最後にブロックの内容を足す |

上の3つの指示が付いていないコードブロックは、説明のための再掲か例示で、写す必要はない。

import パスは Chapter 01 の `go mod init example.com/go-kanban` に合わせている。別のモジュールパスで始めた場合は、`example.com/go-kanban` を自分の `go.mod` の `module` 行に読み替える。

書くファイルと、Chapter 04 のどのコードを移したものかの対応は次のとおり。

| Step | 書くファイル | 移し元（Chapter 04） |
|---|---|---|
| 2 | `internal/model/errors.go` | `errors.go` の `ErrNotFound` などと `ValidationError` |
| 2 | `internal/model/task.go` | `main.go` の `Task`、`validate.go` |
| 2 | `internal/model/member.go` | `authz.go` の Role 定数と判定関数 |
| 2 | `internal/model/user.go` | `auth.go` の `User` と `credentials.validate()`、`main.go` の `Project` |
| 3 | `internal/repository/pgerror.go` | `errors.go` の `isUniqueViolation` `isForeignKeyViolation` |
| 3 | `internal/repository/task.go` | `main.go` の Task 系 Handler 内の SQL、`authz.go` の `findTaskForUser` |
| 3 | `internal/repository/project.go` | `createProjectHandler` の Transaction、`projectRole`、`addMemberHandler` の INSERT |
| 3 | `internal/repository/user.go` | `auth.go` の users / sessions への SQL |
| 4 | `internal/service/task.go` `project.go` `auth.go` | 各 Handler の認可・Validation・bcrypt・Session ID 生成 |
| 5 | `internal/httpx/httpx.go` | `writeJSON` `respondError` `decodeJSON` `pathID` |
| 5 | `internal/middleware/auth.go` | `requireAuth` |
| 5・6 | `internal/handler/*.go` | 各 Handler の JSON 読み書きの部分 |
| 7 | `internal/app/app.go` `cmd/api/main.go` | `main()` のルーティング |
| 7 | `scripts/chapter05_check.sh` | `scripts/chapter04_check.sh` |
| 8 | `internal/service/debug.go` `internal/handler/debug.go` | なし（検証専用の新規コード） |

移すときの判断基準は1つだけ。**SQL は Repository、「してよいか」の判断は Service、HTTP との変換は Handler**。

完成時点のコード全体は [answers/chapter05/](../../answers/chapter05/) にもある。写し間違いを探すときの答え合わせに使う。

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

現状の `createTaskHandler` は、この3つすべての理由で修正対象になる。どれか1つを変えるたびに、関係のない処理まで読むはめになる。

```text
Before                          After

createTaskHandler               Handler      HTTP と Go 値の変換
 ├─ Cookie から User 取得         ↓
 ├─ Path から ID 取得           Service      業務ルールの判断
 ├─ JSON Decode                  ↓
 ├─ Validation                 Repository   SQL の実行
 ├─ 認可 Query                    ↓
 ├─ INSERT                     PostgreSQL
 ├─ エラー分類
 └─ JSON 書き込み
```

---

### Step 1. ディレクトリを作る

#### やること

層ごとの package を置くディレクトリを作る。

#### 実行

`go-kanban/` で実行する。

```bash
mkdir -p internal/{model,repository,service,handler,middleware,httpx,app}
```

#### 期待結果

最終的な構成は次のようになる。Step 2 から Step 8 で、この中にファイルを作っていく。

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

Chapter 04 の `cmd/api/*.go` は Step 7 まで残しておく。`cmd/api` は `internal/` を import していないので、途中で `internal/` だけを `go vet` で確認できる。

#### 依存の向き

依存の向きは一方向にする。

下の図の矢印は import（package 間の依存）の向きを表す。`A --> B` は「A が B を import する」という意味で、Request が処理される順番ではない。

```mermaid
flowchart TD
    APP[app] --> MW[middleware]
    APP --> H[handler]
    APP -.->|組み立て用| SVC
    APP -.->|組み立て用| REPO
    MW --> HX[httpx]
    H --> HX
    MW --> SVC[service]
    H --> SVC
    H --> M
    HX --> M[model]
    SVC --> REPO[repository]
    SVC --> M
    REPO --> M
```

- `app` はすべての層を import し、各層の値を作って依存関係を組み立てる（Step 7）
- `middleware` と `handler` は互いを import しない。両方が使う処理は `httpx` に置く（Step 5）
- `model` は誰にも依存しない。だから Test で単独に検証できる

実行時の Request の流れは別物で、`app` が組み立てた呼び出し関係によって決まる。

```mermaid
flowchart LR
    REQ[Request] --> MW[middleware] --> H[handler] --> SVC[service] --> REPO[repository] --> DB[(PostgreSQL)]
```

middleware が handler を import していなくても handler を呼べるのは、次に呼ぶ処理を `http.Handler` インターフェースとして受け取っているからだ。`app` が `middleware.RequireAuth(authService, h)` の形で、両者をつなぐ。

---

### Step 2. model を作る

#### やること

ドメインの型と業務ルールを、HTTP にも SQL にも依存しない形で定義する。

#### 実行

`internal/model/errors.go` を作成する。

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

`internal/model/task.go` を作成する。

```go
package model

import "strings"

const MaxTitleLength = 100

var validPriorities = map[string]bool{
	"low":    true,
	"medium": true,
	"high":   true,
}

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

// TaskWithAssignee は Task に担当者のメールアドレスを添えたもの。
// Part 3 の N+1 計測で使う。
type TaskWithAssignee struct {
	Task
	AssigneeEmail string `json:"assignee_email"`
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

`internal/model/member.go` を作成する。

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

`internal/model/user.go` を作成する。

```go
package model

import "strings"

const MinPasswordLength = 12

type User struct {
	ID    int64  `json:"id"`
	Email string `json:"email"`
}

type Project struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// Credentials は登録・ログインの入力。
type Credentials struct {
	Email    string
	Password string
}

// Validate は登録時の入力を検査する。
// ログイン時には呼ばない。ルールを満たさない Password は、単に照合で失敗させる。
func (c Credentials) Validate() error {
	if !strings.Contains(c.Email, "@") {
		return Invalid("email must be a valid address")
	}

	if len(c.Password) < MinPasswordLength {
		return Invalid("password must be 12 characters or more")
	}

	return nil
}
```

```bash
go vet ./internal/model/
```

#### 期待結果

何も出力されなければ成功。

#### なぜ `CreateTaskInput` と `createTaskRequest` を分けるのか

Step 6 で、Handler 側に JSON タグ付きの型を別に作る（再掲）。

```go
// internal/handler/task.go
type createTaskRequest struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	Priority    string `json:"priority"`
}
```

型が2つになって冗長に見えるが、JSON のキー名を変えるという HTTP 都合の変更が、Service や Repository へ波及しなくなる。`model` に `json:"..."` タグを書くと、ドメインの型が HTTP の表現に縛られる。

---

### Step 3. repository を作る

#### やること

SQL の実行を Repository へ集約し、Service が依存する interface を定義する。

#### 実行

`internal/repository/pgerror.go` を作成する。

```go
package repository

import (
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
)

// PostgreSQL の制約違反は、Go側では単なる error として届く。
// SQLSTATE を見て業務上の意味へ翻訳しないと、すべて 500 になる。
// 翻訳は SQL を知っている Repository の仕事になる。
const (
	pgCodeUniqueViolation     = "23505"
	pgCodeForeignKeyViolation = "23503"
)

func pgErrorCode(err error) string {
	var pgErr *pgconn.PgError

	if errors.As(err, &pgErr) {
		return pgErr.Code
	}

	return ""
}

func IsUniqueViolation(err error) bool {
	return pgErrorCode(err) == pgCodeUniqueViolation
}

func IsForeignKeyViolation(err error) bool {
	return pgErrorCode(err) == pgCodeForeignKeyViolation
}
```

`internal/repository/task.go` を作成する。

```go
package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"example.com/go-kanban/internal/model"
)

// TaskRepository は Service が必要とする振る舞いだけを宣言する。
// Service は PostgreSQL も pgx も知らない。
type TaskRepository interface {
	Create(ctx context.Context, in model.CreateTaskInput) (model.Task, error)
	FindForUser(ctx context.Context, taskID, userID int64) (model.Task, string, error)
	ListByProject(ctx context.Context, projectID int64) ([]model.Task, error)
	Search(ctx context.Context, projectID int64, keyword string) ([]model.Task, error)
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

// collectTasks は複数行の結果を Task のスライスにする。
// 0件のときも nil ではなく空スライスを返し、JSON で null にならないようにする。
func collectTasks(rows pgx.Rows) ([]model.Task, error) {
	defer rows.Close()

	tasks := []model.Task{}

	for rows.Next() {
		task, err := scanTask(rows)
		if err != nil {
			return nil, fmt.Errorf("scan task: %w", err)
		}

		tasks = append(tasks, task)
	}

	// ループを抜けた理由がエラーでないかを確認する。
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate tasks: %w", err)
	}

	return tasks, nil
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

func (r *PgTaskRepository) ListByProject(ctx context.Context, projectID int64) ([]model.Task, error) {
	rows, err := r.pool.Query(
		ctx,
		`SELECT `+taskColumns+` FROM tasks WHERE project_id = $1 ORDER BY id`,
		projectID,
	)
	if err != nil {
		return nil, fmt.Errorf("query tasks: %w", err)
	}

	return collectTasks(rows)
}

// Search はキーワードでTaskを絞り込む。
// 値は必ずプレースホルダ($2) で渡し、SQL文と連結しない。
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
	if err != nil {
		return nil, fmt.Errorf("search tasks: %w", err)
	}

	return collectTasks(rows)
}
```

`internal/repository/project.go` を作成する。

```go
package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"example.com/go-kanban/internal/model"
)

type ProjectRepository interface {
	CreateWithOwner(ctx context.Context, name string, ownerID int64) (model.Project, error)
	RoleOf(ctx context.Context, projectID, userID int64) (string, error)
	AddMember(ctx context.Context, projectID, userID int64, role string) error
}

var _ ProjectRepository = (*PgProjectRepository)(nil)

type PgProjectRepository struct {
	pool *pgxpool.Pool
}

func NewProjectRepository(pool *pgxpool.Pool) *PgProjectRepository {
	return &PgProjectRepository{pool: pool}
}

// CreateWithOwner は Project 作成と Owner 登録を1つの Transaction で行う。
// Projectだけ作られてMemberが居ないと、作成者本人すら操作できないProjectが残る。
// Transactionの詳細は Chapter 06 で扱う。
func (r *PgProjectRepository) CreateWithOwner(ctx context.Context, name string, ownerID int64) (model.Project, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return model.Project{}, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	var project model.Project

	err = tx.QueryRow(
		ctx,
		"INSERT INTO projects (name) VALUES ($1) RETURNING id, name",
		name,
	).Scan(&project.ID, &project.Name)
	if err != nil {
		return model.Project{}, fmt.Errorf("insert project: %w", err)
	}

	_, err = tx.Exec(
		ctx,
		"INSERT INTO project_members (project_id, user_id, role) VALUES ($1, $2, $3)",
		project.ID, ownerID, model.RoleOwner,
	)
	if err != nil {
		return model.Project{}, fmt.Errorf("insert project member: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return model.Project{}, fmt.Errorf("commit: %w", err)
	}

	return project, nil
}

// RoleOf は user が project のメンバーかどうかと、その Role を返す。
// メンバーでなければ ErrForbidden。
func (r *PgProjectRepository) RoleOf(ctx context.Context, projectID, userID int64) (string, error) {
	var role string

	err := r.pool.QueryRow(
		ctx,
		"SELECT role FROM project_members WHERE project_id = $1 AND user_id = $2",
		projectID, userID,
	).Scan(&role)

	if errors.Is(err, pgx.ErrNoRows) {
		return "", model.ErrForbidden
	}

	if err != nil {
		return "", fmt.Errorf("query project role: %w", err)
	}

	return role, nil
}

func (r *PgProjectRepository) AddMember(ctx context.Context, projectID, userID int64, role string) error {
	_, err := r.pool.Exec(
		ctx,
		"INSERT INTO project_members (project_id, user_id, role) VALUES ($1, $2, $3)",
		projectID, userID, role,
	)

	// 複合主キー (project_id, user_id) の違反 = すでにメンバー。
	if IsUniqueViolation(err) {
		return model.Public(model.ErrConflict, "user is already a member")
	}

	// users への外部キー違反 = 存在しない User。
	if IsForeignKeyViolation(err) {
		return model.Public(model.ErrNotFound, "user not found")
	}

	if err != nil {
		return fmt.Errorf("insert project member: %w", err)
	}

	return nil
}
```

`internal/repository/user.go` を作成する。

```go
package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"example.com/go-kanban/internal/model"
)

// UserRepository は User と Session の永続化を扱う。
type UserRepository interface {
	Create(ctx context.Context, email, passwordHash string) (model.User, error)
	FindPasswordHash(ctx context.Context, email string) (userID int64, passwordHash string, err error)
	CreateSession(ctx context.Context, sessionID string, userID int64, expiresAt time.Time) error
	DeleteSession(ctx context.Context, sessionID string) error
	FindBySession(ctx context.Context, sessionID string) (model.User, error)
}

var _ UserRepository = (*PgUserRepository)(nil)

type PgUserRepository struct {
	pool *pgxpool.Pool
}

func NewUserRepository(pool *pgxpool.Pool) *PgUserRepository {
	return &PgUserRepository{pool: pool}
}

func (r *PgUserRepository) Create(ctx context.Context, email, passwordHash string) (model.User, error) {
	var user model.User

	err := r.pool.QueryRow(
		ctx,
		"INSERT INTO users (email, password_hash) VALUES ($1, $2) RETURNING id, email",
		email, passwordHash,
	).Scan(&user.ID, &user.Email)

	if IsUniqueViolation(err) {
		return model.User{}, model.Public(model.ErrConflict, "email is already registered")
	}

	if err != nil {
		return model.User{}, fmt.Errorf("insert user: %w", err)
	}

	return user, nil
}

// FindPasswordHash は照合用のハッシュを返す。見つからなければ ErrNotFound。
func (r *PgUserRepository) FindPasswordHash(ctx context.Context, email string) (int64, string, error) {
	var (
		userID int64
		hash   string
	)

	err := r.pool.QueryRow(
		ctx,
		"SELECT id, password_hash FROM users WHERE email = $1",
		email,
	).Scan(&userID, &hash)

	if errors.Is(err, pgx.ErrNoRows) {
		return 0, "", model.ErrNotFound
	}

	if err != nil {
		return 0, "", fmt.Errorf("query user: %w", err)
	}

	return userID, hash, nil
}

func (r *PgUserRepository) CreateSession(ctx context.Context, sessionID string, userID int64, expiresAt time.Time) error {
	_, err := r.pool.Exec(
		ctx,
		"INSERT INTO sessions (id, user_id, expires_at) VALUES ($1, $2, $3)",
		sessionID, userID, expiresAt,
	)
	if err != nil {
		return fmt.Errorf("insert session: %w", err)
	}

	return nil
}

func (r *PgUserRepository) DeleteSession(ctx context.Context, sessionID string) error {
	if _, err := r.pool.Exec(ctx, "DELETE FROM sessions WHERE id = $1", sessionID); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}

	return nil
}

// FindBySession は有効期限内の Session から User を特定する。
// 見つからなければ ErrUnauthorized。
func (r *PgUserRepository) FindBySession(ctx context.Context, sessionID string) (model.User, error) {
	var user model.User

	err := r.pool.QueryRow(
		ctx,
		`SELECT u.id, u.email
		 FROM sessions s
		 JOIN users u ON u.id = s.user_id
		 WHERE s.id = $1 AND s.expires_at > NOW()`,
		sessionID,
	).Scan(&user.ID, &user.Email)

	if errors.Is(err, pgx.ErrNoRows) {
		return model.User{}, model.ErrUnauthorized
	}

	if err != nil {
		return model.User{}, fmt.Errorf("query session: %w", err)
	}

	return user, nil
}
```

```bash
go vet ./internal/repository/
```

#### 期待結果

何も出力されなければ成功。

#### 設計のポイント

- `Search` はプレースホルダで値を渡す検索。なぜこう書くのかは Part 2 で扱う
- Status を更新するメソッドは Chapter 06 で `TaskRepository` に追加する
- `IsForeignKeyViolation` は、Chapter 03 の `isForeignKeyViolation` を `pgerror.go` へ移して公開したもの。制約違反を業務上の意味へ翻訳するのは SQL を知っている層の仕事なので、Repository に置く
- `RoleOf` の中身は Chapter 04 の `projectRole` と同じ

<details>
<summary>GO NOTE: <code>var _ TaskRepository = (*PgTaskRepository)(nil)</code> の意味</summary>

「この型は、この interface を満たしている」ことをコンパイル時に宣言する慣用句。

`var _ =` は、変数名を `_` にして「値は使わない」ことを示す。`(*PgTaskRepository)(nil)` は nil ポインタを型変換しただけで、実体は作らない。

メソッド名を1文字間違えたとき、この行でコンパイルエラーになる。この宣言がないと、エラーは遠く離れた利用箇所で「型が合わない」として出る。原因の特定に時間がかかる。

</details>

<details>
<summary>GO NOTE: interface はどちら側に置くか</summary>

Go では interface を「使う側」の package に置くのが一般的とされる。本来なら `TaskRepository` は `service` package に置くべきである。

このハンズオンでは `repository` package に置いている。理由は、実装（`PgTaskRepository`）と並べたほうが初学時に対応を追いやすいため。

どちらが正しいという話ではない。「Service が必要とする振る舞いだけを宣言する」という原則のほうが本質で、置き場所は二次的な判断になる。実務で Repository の実装が複数（PostgreSQL / インメモリ / 外部 API）になったら、使う側へ移すことを検討する。

</details>

---

### Step 4. service を作る

#### やること

業務ルールの判断を Service へ集約する。

#### 実行

`internal/service/task.go` を作成する。

```go
package service

import (
	"context"
	"log/slog"

	"example.com/go-kanban/internal/model"
	"example.com/go-kanban/internal/repository"
)

// Notifier は外部通知の契約。Service は HTTP も Retry も知らない。
// 実装は Chapter 07 で作る。それまでは nil を渡す。
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

// Get はアクセス可能な Task だけを返す。
// 他人の Task は存在しない Task と同じ ErrNotFound になる。
func (s *TaskService) Get(ctx context.Context, userID, taskID int64) (model.Task, error) {
	task, _, err := s.tasks.FindForUser(ctx, taskID, userID)
	return task, err
}

// List は Project の Task 一覧を返す。keyword があればタイトルで絞り込む。
// メンバーかどうかだけを確認する。Viewer でも一覧は読める。
func (s *TaskService) List(ctx context.Context, userID, projectID int64, keyword string) ([]model.Task, error) {
	if _, err := s.projects.RoleOf(ctx, projectID, userID); err != nil {
		return nil, err
	}

	if keyword == "" {
		return s.tasks.ListByProject(ctx, projectID)
	}

	return s.tasks.Search(ctx, projectID, keyword)
}
```

`internal/service/project.go` を作成する。

```go
package service

import (
	"context"

	"example.com/go-kanban/internal/model"
	"example.com/go-kanban/internal/repository"
)

type ProjectService struct {
	projects repository.ProjectRepository
}

func NewProjectService(projects repository.ProjectRepository) *ProjectService {
	return &ProjectService{projects: projects}
}

// Create は Project を作り、作成者を Owner として登録する。
func (s *ProjectService) Create(ctx context.Context, userID int64, name string) (model.Project, error) {
	return s.projects.CreateWithOwner(ctx, name, userID)
}

// AddMember は Owner だけが実行できる。
func (s *ProjectService) AddMember(ctx context.Context, userID, projectID, targetUserID int64, role string) error {
	current, err := s.projects.RoleOf(ctx, projectID, userID)
	if err != nil {
		return err
	}

	if !model.CanManageMembers(current) {
		return model.ErrForbidden
	}

	// DB の CHECK 制約でも弾けるが、その場合は 500 になる。先に 400 として返す。
	if !model.IsValidRole(role) {
		return model.Invalid("role must be one of owner, member, viewer")
	}

	return s.projects.AddMember(ctx, projectID, targetUserID, role)
}
```

`internal/service/auth.go` を作成する。

```go
package service

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"golang.org/x/crypto/bcrypt"

	"example.com/go-kanban/internal/model"
	"example.com/go-kanban/internal/repository"
)

const sessionTTL = 24 * time.Hour

type AuthService struct {
	users repository.UserRepository
}

func NewAuthService(users repository.UserRepository) *AuthService {
	return &AuthService{users: users}
}

// Session はログインの成果物。Cookie への変換は handler が行う。
type Session struct {
	ID        string
	ExpiresAt time.Time
}

func (s *AuthService) Register(ctx context.Context, in model.Credentials) (model.User, error) {
	if err := in.Validate(); err != nil {
		return model.User{}, err
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(in.Password), bcrypt.DefaultCost)
	if err != nil {
		return model.User{}, fmt.Errorf("hash password: %w", err)
	}

	return s.users.Create(ctx, in.Email, string(hash))
}

func (s *AuthService) Login(ctx context.Context, in model.Credentials) (Session, error) {
	userID, hash, err := s.users.FindPasswordHash(ctx, in.Email)

	// 「User未登録」と「Password不一致」を区別して返さない。
	// 区別するとEmailの登録有無を外部から列挙できてしまう。
	if errors.Is(err, model.ErrNotFound) {
		return Session{}, model.ErrUnauthorized
	}

	if err != nil {
		return Session{}, err
	}

	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(in.Password)); err != nil {
		return Session{}, model.ErrUnauthorized
	}

	sessionID, err := newSessionID()
	if err != nil {
		return Session{}, err
	}

	expiresAt := time.Now().Add(sessionTTL)

	if err := s.users.CreateSession(ctx, sessionID, userID, expiresAt); err != nil {
		return Session{}, err
	}

	return Session{ID: sessionID, ExpiresAt: expiresAt}, nil
}

// Logout は Server 側の Session を消す。Cookie を消すだけでは無効化にならない。
func (s *AuthService) Logout(ctx context.Context, sessionID string) error {
	return s.users.DeleteSession(ctx, sessionID)
}

// Authenticate は Session ID から User を特定する。無効なら ErrUnauthorized。
func (s *AuthService) Authenticate(ctx context.Context, sessionID string) (model.User, error) {
	return s.users.FindBySession(ctx, sessionID)
}

// newSessionID は推測不能なSession IDを生成する。
// math/rand ではなく crypto/rand を使う。
func newSessionID() (string, error) {
	buf := make([]byte, 32)

	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate session id: %w", err)
	}

	return base64.RawURLEncoding.EncodeToString(buf), nil
}
```

```bash
go vet ./internal/service/
```

#### 期待結果

何も出力されなければ成功。

`Notifier` の実装は Chapter 07 で作る。この章では `nil` を渡すので、`notifier` と `logger` はまだ使わない。引数の形だけ先に決めておくと、Chapter 07 で `NewTaskService` の呼び出し側を変えずに済む。

#### なぜ Validation が Service にあるのか

Chapter 03 では Handler で Validation していた。層を分けたので、Service へ移す。

```text
Handler の Validation        「JSON として読めるか」「数値として解釈できるか」
                             → HTTP / 形式の話

Service の Validation        「title は必須」「priority は3種類のいずれか」
                             → 業務ルールの話
```

業務ルールを Handler に置くと、CLI やバッチから同じ処理を呼んだときにルールが抜ける。入口が増えるたびに、ルールをコピーして回ることになる。

---

### Step 5. import cycle に遭遇する

#### やること

`middleware` と `handler` を素直に書くと、コンパイルエラーになる。まずそのエラーを起こし、共通部分を `httpx` へ切り出して解消する。

#### 実行（1）: 素直に書く

素直に考えると、こう書きたくなる。

- `middleware/auth.go` は、エラー応答に `handler.RespondError()` を使いたい
- `handler/health.go` は、ログイン中の User を `middleware.CurrentUser()` で取り出したい

`internal/handler/health.go` を作成する。

```go
package handler

import (
	"encoding/json"
	"errors"
	"net/http"

	"example.com/go-kanban/internal/middleware"
	"example.com/go-kanban/internal/model"
)

func Health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(body)
}

// RespondError は middleware からも使いたい。
func RespondError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, model.ErrUnauthorized) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
}

// currentUser は middleware が Context に載せた User を取り出したい。
func currentUser(w http.ResponseWriter, r *http.Request) (model.User, bool) {
	user, ok := middleware.CurrentUser(r.Context())
	if !ok {
		RespondError(w, r, model.ErrUnauthorized)
	}

	return user, ok
}
```

`internal/middleware/auth.go` を作成する。

```go
package middleware

import (
	"context"
	"net/http"

	"example.com/go-kanban/internal/handler"
	"example.com/go-kanban/internal/model"
	"example.com/go-kanban/internal/service"
)

type contextKey string

const userContextKey contextKey = "user"

func CurrentUser(ctx context.Context) (model.User, bool) {
	user, ok := ctx.Value(userContextKey).(model.User)
	return user, ok
}

func RequireAuth(auth *service.AuthService, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("kanban_session")
		if err != nil {
			handler.RespondError(w, r, model.ErrUnauthorized)
			return
		}

		user, err := auth.Authenticate(r.Context(), cookie.Value)
		if err != nil {
			handler.RespondError(w, r, err)
			return
		}

		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userContextKey, user)))
	})
}
```

```bash
go vet ./internal/...
```

#### 期待結果（1）

検証環境での実際の出力。

```text
package example.com/go-kanban/internal/handler
	imports example.com/go-kanban/internal/middleware from health.go
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

#### 実行（2）: httpx へ切り出す

両方が必要とする共通部分を、第三の package へ切り出す。

```mermaid
flowchart TD
    H[handler] --> X[httpx<br/>WriteJSON / RespondError<br/>Context アクセサ]
    M[middleware] --> X
    X --> MO[model]
```

`internal/httpx/httpx.go` を作成する。

```go
// Package httpx は handler と middleware が共有する HTTP の共通処理を置く。
package httpx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"

	"example.com/go-kanban/internal/model"
)

// --- Request Context ---------------------------------------------------
//
// handler も middleware も、この package を経由して Context を読み書きする。
// middleware が handler を import すると、handler -> middleware との間で
// import cycle になるため、共有部分をここへ集約する。

const SessionCookieName = "kanban_session"

// contextKey は他packageのkeyと衝突しないよう独自型にする。
type contextKey string

const userContextKey contextKey = "user"

func WithUser(ctx context.Context, user model.User) context.Context {
	return context.WithValue(ctx, userContextKey, user)
}

// CurrentUser は認証middlewareが載せた User を取り出す。
func CurrentUser(ctx context.Context) (model.User, bool) {
	user, ok := ctx.Value(userContextKey).(model.User)
	return user, ok
}

// --- Request -----------------------------------------------------------

// DecodeJSON は未知のfieldを拒否する。
// typoした field 名が黙って無視されると、利用者は「送ったのに反映されない」状態になる。
func DecodeJSON(r *http.Request, dst any) error {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(dst); err != nil {
		return model.Invalid("request body is not valid JSON: " + err.Error())
	}

	return nil
}

func PathID(r *http.Request, name string) (int64, error) {
	raw := r.PathValue(name)

	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		return 0, model.Invalid(fmt.Sprintf("%s must be a positive integer", name))
	}

	return id, nil
}

// --- Response ----------------------------------------------------------

func WriteJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	if err := json.NewEncoder(w).Encode(body); err != nil {
		slog.Error("encode response", slog.String("error", err.Error()))
	}
}

type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type errorEnvelope struct {
	Error errorBody `json:"error"`
}

func writeErrorBody(w http.ResponseWriter, status int, code, message string) {
	WriteJSON(w, status, errorEnvelope{
		Error: errorBody{Code: code, Message: message},
	})
}

// publicMessage は err に利用者向け文言が付いていればそれを、
// 無ければ fallback を返す。
func publicMessage(err error, fallback string) string {
	var publicErr *model.PublicError

	if errors.As(err, &publicErr) {
		return publicErr.PublicMessage()
	}

	return fallback
}

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
	default:
		slog.ErrorContext(r.Context(), "unexpected error", slog.String("error", err.Error()))
		writeErrorBody(w, http.StatusInternalServerError, "internal_error", "internal server error")
	}
}
```

`internal/middleware/auth.go` を次の内容に置き換える。

```go
package middleware

import (
	"net/http"

	"example.com/go-kanban/internal/httpx"
	"example.com/go-kanban/internal/model"
	"example.com/go-kanban/internal/service"
)

// RequireAuth はCookieのSessionからUserを特定し、Contextへ載せる。
//
// next を http.Handler として受け取るので、handler package を import しなくても
// handler を呼べる。どの handler を渡すかは app が決める。
func RequireAuth(auth *service.AuthService, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(httpx.SessionCookieName)
		if err != nil {
			httpx.RespondError(w, r, model.ErrUnauthorized)
			return
		}

		user, err := auth.Authenticate(r.Context(), cookie.Value)
		if err != nil {
			httpx.RespondError(w, r, err)
			return
		}

		next.ServeHTTP(w, r.WithContext(httpx.WithUser(r.Context(), user)))
	})
}
```

`internal/handler/health.go` を次の内容に置き換える。

```go
package handler

import (
	"net/http"

	"example.com/go-kanban/internal/httpx"
	"example.com/go-kanban/internal/model"
)

func Health(w http.ResponseWriter, r *http.Request) {
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// currentUser は RequireAuth が載せた User を取り出す。
// RequireAuth を通っていない経路で呼ばれたら、認証されていないものとして扱う。
func currentUser(w http.ResponseWriter, r *http.Request) (model.User, bool) {
	user, ok := httpx.CurrentUser(r.Context())
	if !ok {
		httpx.RespondError(w, r, model.ErrUnauthorized)
	}

	return user, ok
}
```

```bash
go vet ./internal/...
```

#### 期待結果（2）

何も出力されなければ成功。import cycle が消えた。

#### Chapter 03・04 から変わった点

`RespondError` は Chapter 03 との違いが2つある。`log.Printf` を `slog.ErrorContext` に変えたことと、引数に `r *http.Request` を足したこと。Chapter 08 で、Request ごとの ID をこの Context からログへ載せる。

`WriteJSON` `DecodeJSON` `PathID` は、Chapter 03・04 のものを公開名にして `httpx` へ移した。エラーは `model.Invalid` で作るようにした。

> **POINT**
> import cycle は「設計の問題をコンパイラが教えてくれている」状態と考える。無理に回避せず、何が共通概念なのかを考えて切り出す。
> ここでは「HTTP レイヤの共通語彙」が `httpx` として分離できた。

---

### Step 6. handler を作る

#### やること

残りの Handler を書く。Handler の仕事は、HTTP の Request を Go の値に変換して Service を呼び、結果を HTTP の Response に変換することだけにする。

#### 実行

`internal/handler/auth.go` を作成する。

```go
package handler

import (
	"net/http"

	"example.com/go-kanban/internal/httpx"
	"example.com/go-kanban/internal/model"
	"example.com/go-kanban/internal/service"
)

type AuthHandler struct {
	auth         *service.AuthService
	secureCookie bool
}

// NewAuthHandler の secureCookie は、本番(HTTPS)では true にする。
func NewAuthHandler(auth *service.AuthService, secureCookie bool) *AuthHandler {
	return &AuthHandler{auth: auth, secureCookie: secureCookie}
}

type credentialsRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (req credentialsRequest) toInput() model.Credentials {
	return model.Credentials{Email: req.Email, Password: req.Password}
}

func (h *AuthHandler) Register(w http.ResponseWriter, r *http.Request) {
	var req credentialsRequest

	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.RespondError(w, r, err)
		return
	}

	user, err := h.auth.Register(r.Context(), req.toInput())
	if err != nil {
		httpx.RespondError(w, r, err)
		return
	}

	httpx.WriteJSON(w, http.StatusCreated, user)
}

func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	var req credentialsRequest

	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.RespondError(w, r, err)
		return
	}

	session, err := h.auth.Login(r.Context(), req.toInput())
	if err != nil {
		httpx.RespondError(w, r, err)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     httpx.SessionCookieName,
		Value:    session.ID,
		Path:     "/",
		Expires:  session.ExpiresAt,
		HttpOnly: true,                 // JavaScriptから読めなくする
		Secure:   h.secureCookie,       // 本番(HTTPS)では true にする
		SameSite: http.SameSiteLaxMode, // 他サイトからの自動送信を抑制する
	})

	// ログインの成果物は Body ではなく Set-Cookie ヘッダにある。
	w.WriteHeader(http.StatusNoContent)
}

func (h *AuthHandler) Logout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(httpx.SessionCookieName); err == nil {
		if err := h.auth.Logout(r.Context(), cookie.Value); err != nil {
			httpx.RespondError(w, r, err)
			return
		}
	}

	http.SetCookie(w, &http.Cookie{
		Name:     httpx.SessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   h.secureCookie,
		SameSite: http.SameSiteLaxMode,
	})

	w.WriteHeader(http.StatusNoContent)
}
```

`internal/handler/project.go` を作成する。

```go
package handler

import (
	"net/http"

	"example.com/go-kanban/internal/httpx"
	"example.com/go-kanban/internal/service"
)

type ProjectHandler struct {
	projects *service.ProjectService
}

func NewProjectHandler(projects *service.ProjectService) *ProjectHandler {
	return &ProjectHandler{projects: projects}
}

type createProjectRequest struct {
	Name string `json:"name"`
}

func (h *ProjectHandler) Create(w http.ResponseWriter, r *http.Request) {
	user, ok := currentUser(w, r)
	if !ok {
		return
	}

	var req createProjectRequest

	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.RespondError(w, r, err)
		return
	}

	project, err := h.projects.Create(r.Context(), user.ID, req.Name)
	if err != nil {
		httpx.RespondError(w, r, err)
		return
	}

	httpx.WriteJSON(w, http.StatusCreated, project)
}

type addMemberRequest struct {
	UserID int64  `json:"user_id"`
	Role   string `json:"role"`
}

func (h *ProjectHandler) AddMember(w http.ResponseWriter, r *http.Request) {
	user, ok := currentUser(w, r)
	if !ok {
		return
	}

	projectID, err := httpx.PathID(r, "id")
	if err != nil {
		httpx.RespondError(w, r, err)
		return
	}

	var req addMemberRequest

	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.RespondError(w, r, err)
		return
	}

	if err := h.projects.AddMember(r.Context(), user.ID, projectID, req.UserID, req.Role); err != nil {
		httpx.RespondError(w, r, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
```

`internal/handler/task.go` を作成する。

```go
package handler

import (
	"net/http"

	"example.com/go-kanban/internal/httpx"
	"example.com/go-kanban/internal/model"
	"example.com/go-kanban/internal/service"
)

type TaskHandler struct {
	tasks *service.TaskService
}

func NewTaskHandler(tasks *service.TaskService) *TaskHandler {
	return &TaskHandler{tasks: tasks}
}

// createTaskRequest は HTTP の表現。model.CreateTaskInput とは分ける。
// JSON のキー名を変えても、Service や Repository へ波及しない。
type createTaskRequest struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	Priority    string `json:"priority"`
}

func (h *TaskHandler) Create(w http.ResponseWriter, r *http.Request) {
	user, ok := currentUser(w, r)
	if !ok {
		return
	}

	projectID, err := httpx.PathID(r, "id")
	if err != nil {
		httpx.RespondError(w, r, err)
		return
	}

	var req createTaskRequest

	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.RespondError(w, r, err)
		return
	}

	task, err := h.tasks.Create(r.Context(), user.ID, model.CreateTaskInput{
		ProjectID:   projectID,
		Title:       req.Title,
		Description: req.Description,
		Priority:    req.Priority,
	})
	if err != nil {
		httpx.RespondError(w, r, err)
		return
	}

	httpx.WriteJSON(w, http.StatusCreated, task)
}

// ListByProject は ?q= があればタイトルで絞り込む。
func (h *TaskHandler) ListByProject(w http.ResponseWriter, r *http.Request) {
	user, ok := currentUser(w, r)
	if !ok {
		return
	}

	projectID, err := httpx.PathID(r, "id")
	if err != nil {
		httpx.RespondError(w, r, err)
		return
	}

	tasks, err := h.tasks.List(r.Context(), user.ID, projectID, r.URL.Query().Get("q"))
	if err != nil {
		httpx.RespondError(w, r, err)
		return
	}

	httpx.WriteJSON(w, http.StatusOK, tasks)
}

func (h *TaskHandler) Get(w http.ResponseWriter, r *http.Request) {
	user, ok := currentUser(w, r)
	if !ok {
		return
	}

	taskID, err := httpx.PathID(r, "id")
	if err != nil {
		httpx.RespondError(w, r, err)
		return
	}

	task, err := h.tasks.Get(r.Context(), user.ID, taskID)
	if err != nil {
		httpx.RespondError(w, r, err)
		return
	}

	httpx.WriteJSON(w, http.StatusOK, task)
}
```

```bash
go vet ./internal/...
```

#### 期待結果

何も出力されなければ成功。

<details>
<summary>この移行で <code>POST /login</code> のレスポンスが 200 から 204 へ変わる</summary>

Chapter 04 の実装は、ログイン成功時に 200 と `{"user_id":1}` を返していた。上の `Login` は、Cookie を設定したあと `w.WriteHeader(http.StatusNoContent)` で 204 No Content を返す。

このレスポンスの本体には、意味のある情報がないからだ。`user_id` は Client が送った email に対応する値で、Client 側の処理に必要なら `GET /me` のような専用エンドポイントで取得すべきものになる。ログインの成果物は Body ではなく `Set-Cookie` ヘッダにある。

Chapter 09 の Integration Test は、この 204 を前提に書かれている。

</details>

---

### Step 7. app で組み立てる

#### やること

依存関係の組み立てとルーティングを1か所へ集め、`main.go` を起動処理だけにする。

#### 実行

`internal/app/app.go` を作成する。

```go
package app

import (
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"example.com/go-kanban/internal/handler"
	"example.com/go-kanban/internal/middleware"
	"example.com/go-kanban/internal/repository"
	"example.com/go-kanban/internal/service"
)

// Config は環境によって変わる設定。
type Config struct {
	// SecureCookie は Cookie に Secure 属性を付けるか。本番(HTTPS)では true にする。
	SecureCookie bool

	// DebugRoutes は Part 2・Part 3 の検証用エンドポイントを登録するか。
	// SQL Injection が通る実装を含むので、検証が終わったら削除する。
	DebugRoutes bool
}

func DefaultConfig() Config {
	return Config{
		SecureCookie: false,
		DebugRoutes:  false,
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

	// Service
	// Notifier の実装は Chapter 07 で作る。それまでは nil を渡す。
	authService := service.NewAuthService(userRepo)
	taskService := service.NewTaskService(taskRepo, projectRepo, nil, logger)
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
	mux.Handle("POST /projects/{id}/tasks", requireAuth(taskHandler.Create))
	mux.Handle("GET /projects/{id}/tasks", requireAuth(taskHandler.ListByProject))
	mux.Handle("GET /tasks/{id}", requireAuth(taskHandler.Get))

	return mux
}
```

`Config.DebugRoutes` は Step 8 で使う。この時点ではまだ参照されない。

`cmd/api/main.go` を次の内容に置き換える。

```go
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
	// ログの形式は Chapter 08 で JSON に変える。
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
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
	// Part 2・Part 3 の検証中だけ DEBUG_ROUTES=1 で起動する。
	cfg.DebugRoutes = os.Getenv("DEBUG_ROUTES") == "1"

	server := &http.Server{
		Addr:              ":8080",
		Handler:           app.New(pool, logger, cfg),
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

Chapter 04 の残りのファイルは、中身がすべて `internal/` へ移ったので削除する。

```bash
rm cmd/api/auth.go cmd/api/authz.go cmd/api/errors.go cmd/api/validate.go
ls cmd/api
```

`ls` の結果が `main.go` だけになっていればよい。

`golang.org/x/crypto` を `cmd/api` ではなく `internal/service` が使うようになったので、`go.mod` を整える。

```bash
go mod tidy
go build ./...
go vet ./...
```

#### 期待結果

`go build ./...` と `go vet ./...` が何も出力しなければ成功。

`pool` のグローバル変数が消え、各層は `New(...)` の引数で依存を受け取るようになった。Chapter 09 では、この形を使って DB を Fake に差し替えた Test を書く。

#### 動作確認

Chapter 04 までの API が同じように動くことを確認する。Chapter 04 の `scripts/chapter04_check.sh` はログインの 200 を期待しているので、#7 が FAIL になる。204 を期待するように直した確認スクリプトを作る。

`scripts/chapter05_check.sh` を作成する。

```bash
#!/usr/bin/env bash
# Chapter 05 の層分割後も Chapter 04 までの API が同じように動くかを確認する。
# サーバを DEBUG_ROUTES=1 で起動していれば、Part 2（SQL Injection）と
# Part 3（N+1）の検証用エンドポイントも確認する。
#
# 使い方（サーバを起動した状態で実行する）:
#   bash scripts/chapter05_check.sh
#
# 何度でも実行できるよう、メールアドレスには実行ごとに異なる接尾辞を付ける。
# 照合するのは Status Code と、Response に特定の文字列が含まれるかだけ。

set -u

BASE_URL=${BASE_URL:-http://localhost:8080}
SUFFIX=$(date +%s)
ALICE="alice-$SUFFIX@example.com"
BOB="bob-$SUFFIX@example.com"
BOB_SECRET="bob salary negotiation $SUFFIX"

# Cookie ファイルは一時ディレクトリに置き、終了時に消す。
WORK_DIR=$(mktemp -d)
trap 'rm -rf "$WORK_DIR"' EXIT
ALICE_COOKIE="$WORK_DIR/alice.txt"
BOB_COOKIE="$WORK_DIR/bob.txt"

PASSED=0
FAILED=0

# call METHOD PATH DATA [curlの追加オプション...]
# 結果を STATUS と BODY に入れる。DATA が空なら Body を送らない。
call() {
	local method=$1 path=$2 data=$3
	shift 3

	local out
	if [ -n "$data" ]; then
		out=$(curl -s -w '\n%{http_code}' -X "$method" "$@" \
			-H 'Content-Type: application/json' -d "$data" "$BASE_URL$path")
	else
		out=$(curl -s -w '\n%{http_code}' -X "$method" "$@" "$BASE_URL$path")
	fi

	STATUS=${out##*$'\n'}
	BODY=${out%$'\n'*}
	# json.Encoder は末尾に改行を付ける。比較しやすいよう取り除く。
	BODY=${BODY%$'\n'}
}

pass() {
	PASSED=$((PASSED + 1))
	printf 'PASS  %s\n' "$1"
}

fail() {
	FAILED=$((FAILED + 1))
	printf 'FAIL  %s\n' "$1"
	printf '      body: %s\n' "$BODY"
}

# check 名前 期待するStatus
check() {
	if [ "$STATUS" = "$2" ]; then
		pass "$1 ($STATUS)"
	else
		fail "$1 (want $2, got $STATUS)"
	fi
}

# check_body 名前 含まれるべき文字列
check_body() {
	if [[ "$BODY" == *"$2"* ]]; then
		pass "$1"
	else
		fail "$1 (body に \"$2\" が含まれない)"
	fi
}

# 後続の手順に必要な準備。失敗したら以降は意味がないので中断する。
require() {
	if [ "$STATUS" != "$2" ]; then
		printf 'ABORT %s (want %s, got %s)\n' "$1" "$2" "$STATUS"
		printf '      body: %s\n' "$BODY"
		exit 1
	fi
}

extract_id() {
	echo "$1" | sed -E 's/^\{"id":([0-9]+).*/\1/'
}

call GET /health ""
if [ "$STATUS" != "200" ]; then
	echo "サーバに接続できません: $BASE_URL (status: $STATUS)"
	echo "go run ./cmd/api を実行してから、もう一度試してください。"
	exit 1
fi

echo "== Chapter 04 までの API（回帰確認）"
call POST /users "{\"email\":\"$ALICE\",\"password\":\"alice-password-1\"}"
check "alice 登録" 201

call POST /users "{\"email\":\"$BOB\",\"password\":\"bob-password-123\"}"
require "bob 登録" 201

call POST /users "{\"email\":\"$ALICE\",\"password\":\"alice-password-1\"}"
check "同じメールで再登録" 409

call POST /users "{\"email\":\"carol-$SUFFIX@example.com\",\"password\":\"short\"}"
check "12文字未満のパスワード" 400

call POST /projects '{"name":"No Cookie"}'
check "Cookie なしで POST /projects" 401

call POST /login "{\"email\":\"$ALICE\",\"password\":\"wrong-password-1\"}"
check "誤ったパスワードでログイン" 401
WRONG_PASSWORD_BODY=$BODY

call POST /login "{\"email\":\"nobody-$SUFFIX@example.com\",\"password\":\"alice-password-1\"}"
check "存在しないメールでログイン" 401

if [ "$WRONG_PASSWORD_BODY" = "$BODY" ]; then
	pass "誤ったパスワードと存在しないメールのレスポンスが同じ"
else
	fail "誤ったパスワードと存在しないメールのレスポンスが異なる"
fi

# Chapter 05 で 200 から 204 へ変わる。
call POST /login "{\"email\":\"$ALICE\",\"password\":\"alice-password-1\"}" -c "$ALICE_COOKIE"
check "alice がログイン" 204

call POST /login "{\"email\":\"$BOB\",\"password\":\"bob-password-123\"}" -c "$BOB_COOKIE"
require "bob がログイン" 204

call POST /projects '{"name":"Alice Board"}' -b "$ALICE_COOKIE"
require "alice が Project 作成" 201
ALICE_PROJECT=$(extract_id "$BODY")

call POST "/projects/$ALICE_PROJECT/tasks" '{"title":"write docs","priority":"high"}' -b "$ALICE_COOKIE"
check "alice が Task 作成" 201
TASK_ID=$(extract_id "$BODY")

if ! [[ "$TASK_ID" =~ ^[0-9]+$ ]]; then
	echo "ABORT Task の id を取り出せませんでした: $BODY"
	exit 1
fi

call POST "/projects/$ALICE_PROJECT/tasks" '{"title":"   ","priority":"high"}' -b "$ALICE_COOKIE"
check "空白だけの title（Service の Validation）" 400

call GET "/tasks/$TASK_ID" "" -b "$ALICE_COOKIE"
check "alice が自分の Task 取得" 200

call GET "/tasks/$TASK_ID" "" -b "$BOB_COOKIE"
check "bob が alice の Task 取得（IDOR）" 404

call POST "/projects/$ALICE_PROJECT/tasks" '{"title":"intruder","priority":"low"}' -b "$BOB_COOKIE"
check "bob が alice の Project に Task 作成" 403

call GET "/projects/$ALICE_PROJECT/tasks" "" -b "$BOB_COOKIE"
check "bob が alice の Project の Task 一覧" 403

echo "== Part 2. 検索（プレースホルダ版）"
call POST /projects '{"name":"Bob Private"}' -b "$BOB_COOKIE"
require "bob が Project 作成" 201
BOB_PROJECT=$(extract_id "$BODY")

call POST "/projects/$BOB_PROJECT/tasks" "{\"title\":\"$BOB_SECRET\",\"priority\":\"high\"}" -b "$BOB_COOKIE"
require "bob が非公開 Task 作成" 201

call GET "/projects/$ALICE_PROJECT/tasks" "" -b "$ALICE_COOKIE" --get --data-urlencode "q=docs"
check "alice が q=docs で検索" 200
check_body "検索結果に write docs が含まれる" "write docs"

call GET "/projects/$ALICE_PROJECT/tasks" "" -b "$ALICE_COOKIE" --get --data-urlencode "q=%' OR project_id > 0 --"
check "攻撃文字列を安全な実装へ" 200

if [ "$BODY" = "[]" ]; then
	pass "安全な実装は 0 件を返す"
else
	fail "安全な実装が 0 件を返さない"
fi

call GET "/debug/unsafe-search/$ALICE_PROJECT" "" -b "$ALICE_COOKIE" --get --data-urlencode "q=docs"
if [ "$STATUS" = "404" ]; then
	echo "SKIP  検証用エンドポイントが無効（DEBUG_ROUTES=1 で起動すると確認できる）"
else
	echo "== Part 2. SQL Injection（文字列連結版）"
	check "通常のキーワードは危険な実装でも正常に見える" 200
	check_body "危険な実装でも write docs が返る" "write docs"

	call GET "/debug/unsafe-search/$ALICE_PROJECT" "" -b "$ALICE_COOKIE" --get --data-urlencode "q=%' OR project_id > 0 --"
	check "攻撃文字列を危険な実装へ" 200
	check_body "alice が bob の非公開 Task を読み出せてしまう" "$BOB_SECRET"

	echo "== Part 3. N+1"
	call GET "/debug/nplus1/$ALICE_PROJECT" "" -b "$ALICE_COOKIE"
	check "N+1 比較エンドポイント" 200
	check_body "JOIN 版は 1 Query" '"join_queries":1'
	check_body "両実装が同じ結果を返す" '"same_rows":true'

	call GET "/debug/nplus1/$ALICE_PROJECT" "" -b "$BOB_COOKIE"
	check "bob は alice の Project を計測できない" 403
fi

echo "== Failure Test: ログアウト後に Cookie を使い回す"
call POST /logout "" -b "$ALICE_COOKIE"
check "ログアウト" 204

call GET "/tasks/$TASK_ID" "" -b "$ALICE_COOKIE"
check "ログアウト後の Task 取得" 401

echo
echo "passed: $PASSED, failed: $FAILED"

[ "$FAILED" -eq 0 ]
```

Chapter 04 のサーバが動いていたら Ctrl+C で止めてから、新しいサーバを起動する。

```bash
go run ./cmd/api                     # 別のターミナルで
bash scripts/chapter05_check.sh
```

#### 期待結果（動作確認）

最後が `passed: 20, failed: 0` なら成功。検証用エンドポイントはまだ無いので、Part 2・Part 3 の項目は `SKIP` と表示される。

---

## Part 2. SQL Injection を再現する

### 危険な実装

Step 8 で書く、文字列連結で SQL を組み立てる検索（再掲）。

```go
// SearchUnsafe は SQL Injection を再現するための実装。
// 絶対に本番コードへ持ち込まない。
func (r *PgTaskRepository) SearchUnsafe(
	ctx context.Context,
	projectID int64,
	keyword string,
) ([]model.Task, error) {
	query := fmt.Sprintf(
		`SELECT %s FROM tasks WHERE project_id = %d AND title ILIKE '%%%s%%' ORDER BY id`,
		taskColumns, projectID, keyword,
	)

	rows, err := r.pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("search tasks unsafe: %w", err)
	}

	return collectTasks(rows)
}
```

利用者の入力 `keyword` が、SQL 文の一部として組み立てられている。

### 安全な実装

Step 3 で書いた `Search`（再掲）。

```go
// Search はキーワードでTaskを絞り込む。
// 値は必ずプレースホルダ($2) で渡し、SQL文と連結しない。
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
	if err != nil {
		return nil, fmt.Errorf("search tasks: %w", err)
	}

	return collectTasks(rows)
}
```

```text
安全な実装

  SQL 文   SELECT ... WHERE project_id = $1 AND title ILIKE '%' || $2 || '%'
  値       [1, "攻撃文字列"]
             ↑
           pgx が SQL 文と値を別々に DB へ送る。DB は値を構文として解釈しない
```

`Search` は `GET /projects/{id}/tasks?q=...` から呼ばれる。Step 6 の `TaskHandler.ListByProject` が `r.URL.Query().Get("q")` を渡し、Step 4 の `TaskService.List` が `keyword` の有無で `ListByProject` と `Search` を振り分ける。

---

### Step 8. 検証用エンドポイントを作る

#### やること

Part 2・Part 3 の比較に使う検証用コードを書き、`DEBUG_ROUTES=1` のときだけ登録する。

| エンドポイント | 呼び出す実装 | 使う場所 |
|---|---|---|
| `GET /debug/unsafe-search/{id}?q=...` | `SearchUnsafe`（文字列連結） | Step 9 |
| `GET /debug/nplus1/{id}` | `ListWithAssigneeNaive` と `ListWithAssigneeJoin` | Step 10 |

どちらも `RequireAuth` の後ろに置き、メンバーでない Project は 403 にする。

#### 実行

`internal/repository/task.go` の末尾に追加する。import の追加は不要。

```go
// --- 検証用（Part 2・Part 3）。検証が終わったら、ここから下を削除する ---

// DebugTaskQueries は Part 2・Part 3 の検証だけで使う Query。
// 本番の経路から呼ばれないよう、TaskRepository とは分けて宣言する。
type DebugTaskQueries interface {
	SearchUnsafe(ctx context.Context, projectID int64, keyword string) ([]model.Task, error)
	ListWithAssigneeNaive(ctx context.Context, projectID int64) ([]model.TaskWithAssignee, int, error)
	ListWithAssigneeJoin(ctx context.Context, projectID int64) ([]model.TaskWithAssignee, int, error)
}

var _ DebugTaskQueries = (*PgTaskRepository)(nil)

// SearchUnsafe は SQL Injection を再現するための実装。
// 絶対に本番コードへ持ち込まない。
func (r *PgTaskRepository) SearchUnsafe(
	ctx context.Context,
	projectID int64,
	keyword string,
) ([]model.Task, error) {
	query := fmt.Sprintf(
		`SELECT %s FROM tasks WHERE project_id = %d AND title ILIKE '%%%s%%' ORDER BY id`,
		taskColumns, projectID, keyword,
	)

	rows, err := r.pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("search tasks unsafe: %w", err)
	}

	return collectTasks(rows)
}

// ListWithAssigneeNaive は N+1 を再現するための実装。
// Task 一覧で1回、担当者ごとに1回ずつ Query を発行する。
func (r *PgTaskRepository) ListWithAssigneeNaive(
	ctx context.Context,
	projectID int64,
) ([]model.TaskWithAssignee, int, error) {
	tasks, err := r.ListByProject(ctx, projectID)
	if err != nil {
		return nil, 0, err
	}

	queries := 1
	result := make([]model.TaskWithAssignee, 0, len(tasks))

	for _, task := range tasks {
		item := model.TaskWithAssignee{Task: task}

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

// ListWithAssigneeJoin は同じ結果を1回の Query で取得する。
// 担当者が未設定の Task も残すため LEFT JOIN を使う。
func (r *PgTaskRepository) ListWithAssigneeJoin(
	ctx context.Context,
	projectID int64,
) ([]model.TaskWithAssignee, int, error) {
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
	if err != nil {
		return nil, 1, fmt.Errorf("query tasks with assignee: %w", err)
	}
	defer rows.Close()

	result := []model.TaskWithAssignee{}

	for rows.Next() {
		var item model.TaskWithAssignee

		if err := rows.Scan(
			&item.ID, &item.ProjectID, &item.Title, &item.Description,
			&item.Priority, &item.Status, &item.Version, &item.AssigneeID,
			&item.AssigneeEmail,
		); err != nil {
			return nil, 1, fmt.Errorf("scan task with assignee: %w", err)
		}

		result = append(result, item)
	}

	if err := rows.Err(); err != nil {
		return nil, 1, fmt.Errorf("iterate tasks with assignee: %w", err)
	}

	return result, 1, nil
}
```

`internal/service/debug.go` を作成する。

```go
package service

import (
	"context"
	"time"

	"example.com/go-kanban/internal/model"
	"example.com/go-kanban/internal/repository"
)

// DebugService は Part 2（SQL Injection）と Part 3（N+1）の検証専用。
// 検証が終わったら、app.go のルーティングと一緒に削除する。
type DebugService struct {
	tasks    repository.DebugTaskQueries
	projects repository.ProjectRepository
}

func NewDebugService(tasks repository.DebugTaskQueries, projects repository.ProjectRepository) *DebugService {
	return &DebugService{tasks: tasks, projects: projects}
}

// SearchUnsafe は認可を通したうえで、文字列連結版の検索を呼ぶ。
// 認可を通しても SQL Injection は防げないことを確かめるため。
func (s *DebugService) SearchUnsafe(ctx context.Context, userID, projectID int64, keyword string) ([]model.Task, error) {
	if _, err := s.projects.RoleOf(ctx, projectID, userID); err != nil {
		return nil, err
	}

	return s.tasks.SearchUnsafe(ctx, projectID, keyword)
}

// NPlusOneResult は N+1 版と JOIN 版の計測結果。
type NPlusOneResult struct {
	NaiveQueries  int
	NaiveDuration time.Duration
	JoinQueries   int
	JoinDuration  time.Duration
	Rows          int
	SameRows      bool
}

func (s *DebugService) CompareNPlusOne(ctx context.Context, userID, projectID int64) (NPlusOneResult, error) {
	if _, err := s.projects.RoleOf(ctx, projectID, userID); err != nil {
		return NPlusOneResult{}, err
	}

	start := time.Now()

	naive, naiveQueries, err := s.tasks.ListWithAssigneeNaive(ctx, projectID)
	if err != nil {
		return NPlusOneResult{}, err
	}

	naiveDuration := time.Since(start)
	start = time.Now()

	joined, joinQueries, err := s.tasks.ListWithAssigneeJoin(ctx, projectID)
	if err != nil {
		return NPlusOneResult{}, err
	}

	joinDuration := time.Since(start)

	return NPlusOneResult{
		NaiveQueries:  naiveQueries,
		NaiveDuration: naiveDuration,
		JoinQueries:   joinQueries,
		JoinDuration:  joinDuration,
		Rows:          len(joined),
		SameRows:      sameRows(naive, joined),
	}, nil
}

// sameRows は2つの実装が同じ結果を返したかを確認する。
// 速くても結果が違えば比較の意味がない。
func sameRows(a, b []model.TaskWithAssignee) bool {
	if len(a) != len(b) {
		return false
	}

	for i := range a {
		if a[i].ID != b[i].ID || a[i].AssigneeEmail != b[i].AssigneeEmail {
			return false
		}
	}

	return true
}
```

`internal/handler/debug.go` を作成する。

```go
package handler

import (
	"net/http"

	"example.com/go-kanban/internal/httpx"
	"example.com/go-kanban/internal/service"
)

// DebugHandler は Part 2・Part 3 の検証専用。
// 検証が終わったら、app.go のルーティングと一緒に削除する。
type DebugHandler struct {
	debug *service.DebugService
}

func NewDebugHandler(debug *service.DebugService) *DebugHandler {
	return &DebugHandler{debug: debug}
}

// UnsafeSearch は GET /debug/unsafe-search/{id}?q=...
// 文字列連結で SQL を組み立てる検索を呼ぶ。
func (h *DebugHandler) UnsafeSearch(w http.ResponseWriter, r *http.Request) {
	user, ok := currentUser(w, r)
	if !ok {
		return
	}

	projectID, err := httpx.PathID(r, "id")
	if err != nil {
		httpx.RespondError(w, r, err)
		return
	}

	tasks, err := h.debug.SearchUnsafe(r.Context(), user.ID, projectID, r.URL.Query().Get("q"))
	if err != nil {
		httpx.RespondError(w, r, err)
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"_warn": "this endpoint is intentionally vulnerable",
		"count": len(tasks),
		"tasks": tasks,
	})
}

// NPlusOne は GET /debug/nplus1/{id}
// N+1 版と JOIN 版の Query 数と所要時間を返す。
func (h *DebugHandler) NPlusOne(w http.ResponseWriter, r *http.Request) {
	user, ok := currentUser(w, r)
	if !ok {
		return
	}

	projectID, err := httpx.PathID(r, "id")
	if err != nil {
		httpx.RespondError(w, r, err)
		return
	}

	result, err := h.debug.CompareNPlusOne(r.Context(), user.ID, projectID)
	if err != nil {
		httpx.RespondError(w, r, err)
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"naive_queries": result.NaiveQueries,
		"naive_ms":      result.NaiveDuration.Milliseconds(),
		"join_queries":  result.JoinQueries,
		"join_ms":       result.JoinDuration.Milliseconds(),
		"rows":          result.Rows,
		"same_rows":     result.SameRows,
	})
}
```

`internal/app/app.go` を次の内容に置き換える。Step 7 との違いは、`return mux` の直前に検証用のブロックが入ったことだけ。

```go
package app

import (
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"example.com/go-kanban/internal/handler"
	"example.com/go-kanban/internal/middleware"
	"example.com/go-kanban/internal/repository"
	"example.com/go-kanban/internal/service"
)

// Config は環境によって変わる設定。
type Config struct {
	// SecureCookie は Cookie に Secure 属性を付けるか。本番(HTTPS)では true にする。
	SecureCookie bool

	// DebugRoutes は Part 2・Part 3 の検証用エンドポイントを登録するか。
	// SQL Injection が通る実装を含むので、検証が終わったら削除する。
	DebugRoutes bool
}

func DefaultConfig() Config {
	return Config{
		SecureCookie: false,
		DebugRoutes:  false,
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

	// Service
	// Notifier の実装は Chapter 07 で作る。それまでは nil を渡す。
	authService := service.NewAuthService(userRepo)
	taskService := service.NewTaskService(taskRepo, projectRepo, nil, logger)
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
	mux.Handle("POST /projects/{id}/tasks", requireAuth(taskHandler.Create))
	mux.Handle("GET /projects/{id}/tasks", requireAuth(taskHandler.ListByProject))
	mux.Handle("GET /tasks/{id}", requireAuth(taskHandler.Get))

	// 検証用（Part 2・Part 3）。検証が終わったらこのブロックごと削除する。
	if cfg.DebugRoutes {
		debugHandler := handler.NewDebugHandler(service.NewDebugService(taskRepo, projectRepo))

		mux.Handle("GET /debug/unsafe-search/{id}", requireAuth(debugHandler.UnsafeSearch))
		mux.Handle("GET /debug/nplus1/{id}", requireAuth(debugHandler.NPlusOne))
	}

	return mux
}
```

```bash
go build ./...
go vet ./...
```

動いているサーバを Ctrl+C で止め、`DEBUG_ROUTES=1` を付けて起動し直す。

```bash
DEBUG_ROUTES=1 go run ./cmd/api      # 別のターミナルで
bash scripts/chapter05_check.sh
```

#### 期待結果

`go build` と `go vet` が何も出力せず、確認スクリプトの最後が `passed: 28, failed: 0` なら成功。Step 7 で `SKIP` だった Part 2（SQL Injection）と Part 3（N+1）の項目が `PASS` になる。

`DEBUG_ROUTES` を付けずに起動すると検証用エンドポイントは登録されず、404 になる。消し忘れても既定では公開されないようにするためだ。

---

### Step 9. 攻撃を実行する

#### やること

alice と bob の Project を用意し、alice が bob の非公開 Task を読み出せるか試す。

#### 実行

サーバは Step 8 のとおり `DEBUG_ROUTES=1` で起動しておく。

別のターミナルで `go-kanban/` から実行する。Cookie は Chapter 04 で保存した `./cookie/` のものを使う。Project の id は環境によって変わるので、レスポンスから取り出して変数に控える。

```bash
# 準備: alice が自分の Project と、検索で当たる Task を作る
ALICE_PROJECT=$(curl -s -b ./cookie/alice.txt -X POST localhost:8080/projects \
  -H 'Content-Type: application/json' -d '{"name":"Alice Search"}' \
  | sed -E 's/^\{"id":([0-9]+).*/\1/')
curl -s -b ./cookie/alice.txt -X POST localhost:8080/projects/$ALICE_PROJECT/tasks \
  -H 'Content-Type: application/json' -d '{"title":"write docs","priority":"high"}'

# 準備: bob が非公開の Project と Task を作る
BOB_PROJECT=$(curl -s -b ./cookie/bob.txt -X POST localhost:8080/projects \
  -H 'Content-Type: application/json' -d '{"name":"Bob Private"}' \
  | sed -E 's/^\{"id":([0-9]+).*/\1/')
curl -s -b ./cookie/bob.txt -X POST localhost:8080/projects/$BOB_PROJECT/tasks \
  -H 'Content-Type: application/json' -d '{"title":"bob salary negotiation","priority":"high"}'

# alice が自分の Project を普通に検索
curl -s -b ./cookie/alice.txt "localhost:8080/projects/$ALICE_PROJECT/tasks?q=docs"

# 攻撃文字列を、安全な実装へ投げる
curl -s -b ./cookie/alice.txt --get --data-urlencode "q=%' OR project_id > 0 --" \
  localhost:8080/projects/$ALICE_PROJECT/tasks

# 同じ攻撃文字列を、危険な実装へ投げる
curl -s -b ./cookie/alice.txt --get --data-urlencode "q=%' OR project_id > 0 --" \
  localhost:8080/debug/unsafe-search/$ALICE_PROJECT
```

`--data-urlencode` を使うと、`'` や空白を含む文字列を安全に URL へ載せられる。

Chapter 04 でログアウトまで試した場合、`./cookie/alice.txt` の Session は無効になっている。401 が返ったら、Chapter 04 Step 6 のログインをやり直す（レスポンスは 204 になる）。

#### 期待結果

検証環境での実際の出力。

安全な実装（プレースホルダ）に投げた結果。

```json
[]
```

DB は攻撃文字列を、ただの検索語として扱った。そんな名前の Task はないので 0 件になる。

危険な実装（文字列連結）に投げた結果。空の DB から始めて、alice の Project が 1、bob の Project が 2 になった場合の出力。

```json
{"_warn":"this endpoint is intentionally vulnerable","count":2,
 "tasks":[
   {"id":1,"project_id":1,"title":"write docs",...},
   {"id":2,"project_id":2,"title":"bob salary negotiation",...}
 ]}
```

Chapter 04 の Task なども DB に残っていれば、それらもすべて返るので `count` はもっと大きくなる。確かめるべき点は、`bob salary negotiation` が含まれていることだ。

> **alice が bob の非公開 Task を読み出せた。**
> alice の入力が SQL の構文そのものを書き換え、`project_id = 1` の絞り込みも、Chapter 04 で作った認可も効かなくなった。`DebugService` は検索の前に `RoleOf` で alice が Project のメンバーであることを確認している。それでも防げない。

### 何が起きたのか

組み立てた SQL を展開してみる（alice の Project が 1 の場合）。実際のコードでは SQL 全体が1行なので、ここでも1行で書く（`WHERE` 以降だけ示す）。

```sql
-- 意図した形
WHERE project_id = 1 AND title ILIKE '%<keyword>%' ORDER BY id

-- keyword = "%' OR project_id > 0 --" を埋め込んだ結果
WHERE project_id = 1 AND title ILIKE '%%' OR project_id > 0 --%' ORDER BY id
                                          ^^^^^^^^^^^^^^^^^ ^^^^^^^^^^^^^^^^
                                          条件を追加        ここから行末までコメント
```

`OR project_id > 0` が全行にマッチし、`--` から行末までがコメントになって `ORDER BY` も消えた。

通常のキーワード（`q=docs`）では、危険な実装も正常に動いて見える。Test が正常系しかないと、この脆弱性には気づけない。

### 対策の原則

| やること | 理由 |
|---|---|
| 値は必ずプレースホルダ（`$1`）で渡す | SQL 文と値が別経路で送られる |
| 「この入力は安全だから」で例外を作らない | 安全性の判断は将来の変更で崩れる |
| エスケープ関数を自作しない | 網羅漏れが必ず出る |
| テーブル名・列名を動的にしたい場合は許可リストで照合する | プレースホルダは識別子に使えない |

許可リストで照合する書き方の例。この章のコードには入れない。

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

「各 Task の担当者メールアドレスも返したい」という要件を素直に実装すると、Query は次の回数だけ発行される。

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

Task が N 件なら N+1 回。これを N+1 問題と呼ぶ。

### 実装の比較

Step 8 で `repository/task.go` に追加した2つのメソッドを見比べる（再掲）。

N+1 版。`ListByProject` で1回、ループの中で担当者ごとに1回ずつ Query を発行する。

```go
func (r *PgTaskRepository) ListWithAssigneeNaive(
	ctx context.Context,
	projectID int64,
) ([]model.TaskWithAssignee, int, error) {
	tasks, err := r.ListByProject(ctx, projectID)
	if err != nil {
		return nil, 0, err
	}

	queries := 1
	result := make([]model.TaskWithAssignee, 0, len(tasks))

	for _, task := range tasks {
		item := model.TaskWithAssignee{Task: task}

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

JOIN 版。担当者のメールアドレスを同じ Query で取得する。

```go
func (r *PgTaskRepository) ListWithAssigneeJoin(
	ctx context.Context,
	projectID int64,
) ([]model.TaskWithAssignee, int, error) {
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
	if err != nil {
		return nil, 1, fmt.Errorf("query tasks with assignee: %w", err)
	}
	defer rows.Close()

	result := []model.TaskWithAssignee{}

	for rows.Next() {
		var item model.TaskWithAssignee

		if err := rows.Scan(
			&item.ID, &item.ProjectID, &item.Title, &item.Description,
			&item.Priority, &item.Status, &item.Version, &item.AssigneeID,
			&item.AssigneeEmail,
		); err != nil {
			return nil, 1, fmt.Errorf("scan task with assignee: %w", err)
		}

		result = append(result, item)
	}

	if err := rows.Err(); err != nil {
		return nil, 1, fmt.Errorf("iterate tasks with assignee: %w", err)
	}

	return result, 1, nil
}
```

`LEFT JOIN` を使うのは、担当者が未設定（`assignee_id IS NULL`）の Task も結果に含めるため。`INNER JOIN` だと担当者なしの Task が消える。`COALESCE(u.email, '')` は、担当者がいないときに NULL ではなく空文字を返させ、`string` へ Scan できるようにする。

---

### Step 10. 計測する

#### やること

同じ Project に 200 件の Task を追加し、両実装の Query 数と所要時間を比較する。比較用エンドポイント（`GET /debug/nplus1/{id}`）は、Step 8 のとおり `DEBUG_ROUTES=1` で起動していれば使える。

#### 実行

Step 9 と同じターミナルで実行する（`$ALICE_PROJECT` を使う）。担当者には、id が 1 と 2 の User を交互に割り当てる。

```bash
# テストデータを投入
docker compose exec -T db psql -U kanban -d kanban -c \
  "INSERT INTO tasks (project_id, title, assignee_id)
   SELECT $ALICE_PROJECT, 'task ' || g, (g % 2) + 1 FROM generate_series(1, 200) g;"

# 比較用エンドポイントを3回叩く
for i in 1 2 3; do curl -s -b ./cookie/alice.txt localhost:8080/debug/nplus1/$ALICE_PROJECT; echo; done
```

#### 期待結果

検証環境での実際の出力。Step 9 で作った1件と合わせて、Task は 201 件になる。

```json
{"join_ms":1,"join_queries":1,"naive_ms":65,"naive_queries":201,"rows":201,"same_rows":true}
{"join_ms":0,"join_queries":1,"naive_ms":64,"naive_queries":201,"rows":201,"same_rows":true}
{"join_ms":1,"join_queries":1,"naive_ms":70,"naive_queries":201,"rows":201,"same_rows":true}
```

| 実装 | Query 数 | 所要時間 | 返す結果 |
|---|---:|---:|---|
| N+1（ループ内 Query） | 201 | 64〜70 ms | 同じ |
| JOIN（1 Query） | 1 | 0〜1 ms | 同じ |

同じ結果を返すのに、N+1 版は JOIN 版の 60 倍以上かかった。

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

所要時間の大半は、SQL の実行そのものではなく通信の往復が占める。

### JOIN が常に正解ではない

| 状況 | 適した手法 |
|---|---|
| 1対1、または 1対少数の関連 | JOIN |
| 1対多で、親の列が大量に複製される | 2 Query に分けて、アプリ側で組み立てる |
| 関連先の種類が多く JOIN が複雑になる | `WHERE id = ANY($1)` によるバッチ取得 |
| 関連データが実はほぼ不要 | そもそも取得しない（必要なときだけ取る） |

バッチ取得の書き方の例。この章のコードには入れない。

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
| スロー Query ログ | 個々の Query は速いので引っかからない。N+1 の発見には向かない |
| 負荷テスト | Chapter 09 で実施。件数を増やしたときの劣化として現れる |

---

### Step 11. 検証用コードを削除する

#### やること

`DEBUG_ROUTES` で隠していても、SQL Injection が通るコードはリポジトリに残さない。Step 8 で足したものをすべて消す。

#### 実行

1. `internal/repository/task.go` の `// --- 検証用（Part 2・Part 3）。検証が終わったら、ここから下を削除する ---` の行から末尾までを削除する
2. `internal/app/app.go` を Step 7 の内容に置き換える（`if cfg.DebugRoutes { ... }` のブロックを削除する）
3. 検証用の Service と Handler を削除する

```bash
rm internal/service/debug.go internal/handler/debug.go
go build ./...
go vet ./...
grep -rn "SearchUnsafe\|DebugTaskQueries\|debug" internal/ || echo "検証用コードは残っていない"
```

#### 期待結果

`go build` と `go vet` が何も出力せず、最後に `検証用コードは残っていない` と表示されれば成功。

サーバを `go run ./cmd/api` で起動し直して `bash scripts/chapter05_check.sh` を実行すると、Step 7 と同じく `passed: 20, failed: 0` になり、Part 2・Part 3 の項目は `SKIP` になる。

---

> **WARNING**
> 「綺麗に見えるから」という理由だけで interface や package を増やさない。
> 今回 `httpx` を作ったのはコンパイルエラーという具体的な問題があったからで、`TaskRepository` を interface にしたのは Test で差し替えたいという具体的な目的があったから。

次は [Chapter 06: Transaction と同時更新](./chapter06_transaction.md)。複数の更新を原子化し、2人が同時に同じ Task を更新したときに何が起きるかを再現する。
