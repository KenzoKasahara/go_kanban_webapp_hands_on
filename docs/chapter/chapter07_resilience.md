# Chapter 07: Timeout・Retry・冪等性

## この章の目的

ここまでは「DB も外部 API も正常に応答する」前提で作ってきた。実際には次のことが起きる。

- DB が遅い。Client はもう待っていないのに処理だけ残る
- 外部 API が 500 を返す、あるいは応答しない
- Response が届かず、Client が同じリクエストを再送する

この3つは**セットで考える必要がある**。Timeout を入れると Retry が必要になり、Retry を入れると冪等性が必要になる。片方だけ導入すると、別の障害を生む。

## 現在地

Webアプリ化(03-05) → **本番対応(06-08)** → Test(09)

## 完了条件

- [ ] Request 全体に Timeout が設定され、超過すると 503 が返る
- [ ] Timeout が DB のクエリまで伝わることを確認した
- [ ] 外部 API の 500 / 429 / Timeout は Retry され、400 は Retry されないことを確認した
- [ ] 通知の失敗で Task 更新が失敗しないことを確認した
- [ ] 同じ `Idempotency-Key` での再送が二重作成にならないことを確認した

## この章のコードの載せ方

ファイル名を示したコードブロックは、**すべてファイルの全文**になっている。そのファイルの中身を丸ごと置き換えればよい。

説明のために一部だけを再掲する場合は、ブロックの直前の文に「**抜粋（貼り付け不要）**」と書く。

`internal/app/app.go` は章の途中で3回書き換える（Step 2・Step 5・Step 7）。どの版も全文なので、最新の版で上書きする。

| ファイル | 操作 | Step |
| --- | --- | --- |
| `internal/middleware/observability.go` | 新規作成 | 1 |
| `internal/httpx/httpx.go` | 置き換え | 1 |
| `internal/handler/debug.go` | 新規作成 | 2 |
| `internal/app/app.go` | 置き換え | 2 / 5 / 7 |
| `internal/notify/notifier.go` | 新規作成 | 3 |
| `internal/service/task.go` | 置き換え | 5 |
| `cmd/api/main.go` | 置き換え | 5 |
| `internal/notify/notifier_test.go` | 新規作成 | 6 |
| `migrations/004_idempotency.sql` | 新規作成 | 7 |
| `internal/repository/idempotency.go` | 新規作成 | 7 |
| `internal/middleware/idempotency.go` | 新規作成 | 7 |

## 3つの関係

```mermaid
flowchart LR
    A[Timeout を入れる] -->|処理を打ち切る| B[一時的な失敗が増える]
    B --> C[Retry が必要になる]
    C -->|同じ Request が複数回届く| D[二重処理の危険]
    D --> E[冪等性が必要になる]

    style A fill:#e0f0ff,color:#000
    style C fill:#e0f0ff,color:#000
    style E fill:#e0f0ff,color:#000
```

---

## Part 1. Timeout

### Step 1. Request 全体に上限を設ける

#### やること

Middleware で Request の Context に期限を設定する。期限切れを 503 として返せるよう、エラー変換にも分類を足す。

#### 実行

`internal/middleware/observability.go` を新規作成する。

```go
package middleware

import (
	"context"
	"net/http"
	"time"
)

// このファイルには Chapter 08 で RequestID と AccessLog を追加する。

// Timeout は Request 全体の上限時間を設定する。
// Client が待っていない処理をServer側で続けないための仕組み。
func Timeout(d time.Duration, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), d)
		defer cancel()

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
```

`internal/httpx/httpx.go` を置き換える。変更点は、`RespondError` に `context.DeadlineExceeded` の case を追加したことだけ。

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
	case errors.Is(err, context.DeadlineExceeded):
		// 処理は打ち切ったが、利用者から見れば「今は使えない」状態。
		writeErrorBody(w, http.StatusServiceUnavailable, "timeout", "request timed out")
	default:
		slog.ErrorContext(r.Context(), "unexpected error", slog.String("error", err.Error()))
		writeErrorBody(w, http.StatusInternalServerError, "internal_error", "internal server error")
	}
}
```

`Timeout` を `app.go` に適用するのは Step 2 で行う。検証用エンドポイントの登録と一緒に書き換えるため。

#### Context の伝播

```text
Client
  │ 接続を切る / Timeout に達する
  X
  ↓ ctx がキャンセルされる
Middleware (Timeout)
  ↓ ctx
Handler
  ↓ ctx
Service
  ↓ ctx
Repository
  ↓ ctx
pgx  ──→  PostgreSQL へキャンセルを送る
```

Context を引き回すのは、この連鎖を成立させるため。どこか1か所で `context.Background()` に差し替えると、**そこから先はキャンセルが伝わらない**。

> **WARNING**
> Request 処理の中で `context.Background()` を作らない。Client が切断しても DB のクエリだけが走り続け、接続プールを食い潰す。
> ただし**Commit 後の後処理**（ログの永続化など、途中でやめると困る処理）は例外で、あえて独立した Context を使うことがある。その場合も理由をコメントに残す。

---

### Step 2. Timeout を実際に発生させる

#### やること

DB 側で意図的に遅い処理を実行し、Timeout の挙動を確認する。

#### 実行

検証用のエンドポイントを用意する。`internal/handler/debug.go` を新規作成する。

```go
package handler

import (
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"example.com/go-kanban/internal/httpx"
)

// SlowQuery は Timeout を再現するための検証用Endpoint。
// pg_sleep により、指定秒数だけDB側で待つ。
// 検証が終わったら、app.go のルーティングと一緒に削除する。
func SlowQuery(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		seconds := r.URL.Query().Get("seconds")
		if seconds == "" {
			seconds = "3"
		}

		start := time.Now()

		var result int

		// Context は Handler から Repository、さらに DB Driver まで伝播する。
		// Timeout すると pgx が Query をキャンセルする。
		err := pool.QueryRow(r.Context(), "SELECT pg_sleep($1::float8), 1", seconds).
			Scan(nil, &result)
		if err != nil {
			httpx.RespondError(w, r, err)
			return
		}

		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"result":      result,
			"elapsed_ms":  time.Since(start).Milliseconds(),
			"slept_for_s": seconds,
		})
	}
}
```

`internal/app/app.go` を置き換える。Chapter 06 の版からの変更点は次の3つ。

- `Config` に `RequestTimeout`（既定 2 秒）を追加した
- `DEBUG_ROUTES=1` のときだけ `GET /debug/slow` を登録する
- `mux` を `middleware.Timeout` で包んで返す

```go
package app

import (
	"log/slog"
	"net/http"
	"time"

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

	// DebugRoutes は検証用エンドポイント（GET /debug/slow）を登録するか。
	// 既定では登録しないので、消し忘れても公開されない。
	DebugRoutes bool

	// RequestTimeout は Request 全体の上限時間。
	RequestTimeout time.Duration
}

func DefaultConfig() Config {
	return Config{
		SecureCookie:   false,
		DebugRoutes:    false,
		RequestTimeout: 2 * time.Second,
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
	// Notifier は Step 5 でつなぐ。それまでは nil を渡す。
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
	mux.Handle("PATCH /tasks/{id}/status", requireAuth(taskHandler.ChangeStatus))

	// 検証用。DEBUG_ROUTES=1 で起動したときだけ登録する。
	if cfg.DebugRoutes {
		mux.Handle("GET /debug/slow", requireAuth(handler.SlowQuery(pool)))
	}

	// Middleware は外側から順に適用される。
	// Chapter 08 で、Timeout のさらに外側へ RequestID と AccessLog を追加する。
	var h http.Handler = mux
	h = middleware.Timeout(cfg.RequestTimeout, h)

	return h
}
```

ビルドを確認し、`DEBUG_ROUTES=1` を付けてサーバを起動し直す。

```bash
go build ./...
go vet ./...
DEBUG_ROUTES=1 go run ./cmd/api      # 別のターミナルで
```

Chapter 04 で保存した Session Cookie は有効期限が 24 時間なので、期限が切れていると `/debug/slow` は 401 を返す。先にログインし直して Cookie を上書きしておく。

```bash
# Session を取り直す（Chapter 04 と同じディレクトリで実行する）
mkdir -p cookie
curl -s -w '\n%{http_code}\n' -c ./cookie/alice.txt -X POST localhost:8080/login -H 'Content-Type: application/json' \
  -d '{"email":"alice@example.com","password":"alice-password-1"}'    # → 200

# Server の上限は 2秒。DB を 3秒 待たせる
curl -s -w 'status=%{http_code} time=%{time_total}s\n' \
  -b ./cookie/alice.txt 'localhost:8080/debug/slow?seconds=3'

# 上限内（1秒）なら成功する
curl -s -w 'status=%{http_code} time=%{time_total}s\n' \
  -b ./cookie/alice.txt 'localhost:8080/debug/slow?seconds=1'
```

> **NOTE**
> `status=401` が返ったら、Session の期限切れか、Cookie ファイルのパスの間違いを疑う。`-b` に存在しないファイルを渡しても curl はエラーを出さず、Cookie を付けずに送信する。

#### 期待結果

検証環境での実際の出力。

レスポンスボディの次の行に、`-w` で指定した Status と所要時間が出る。

```text
{"error":{"code":"timeout","message":"request timed out"}}
status=503 time=2.002815s
```

```text
{"elapsed_ms":1001,"result":1,"slept_for_s":"1"}
status=200 time=1.026585s
```

| 確認項目 | 結果 |
| --- | --- |
| 打ち切りのタイミング | **2.00 秒**（3秒待たずに終了） |
| Status | 503 Service Unavailable |
| 上限内のリクエスト | 1.03 秒で 200 |

**DB のクエリまでキャンセルが届いている。** アプリが応答を返しただけで DB が 3 秒走り続けているわけではない。

<details>
<summary>Timeout 値をどう決めるか</summary>

| 対象 | 目安 | 根拠 |
| --- | --- | --- |
| Request 全体 | 2〜30 秒 | Client（ブラウザ、ロードバランサ）の Timeout より**短く**する |
| DB クエリ | Request 全体より短く | 遅いクエリを特定しやすくする |
| 外部 API（1回の試行） | 0.5〜3 秒 | Retry の回数 × Timeout が Request 全体を超えないようにする |

> **WARNING**
> Request 全体の Timeout が、ロードバランサの Timeout より長いと意味がない。LB が先に切断し、アプリ側は処理を続ける。**外側から内側へ向かって短くなる**ように設計する。

本ハンズオンでは既定 2 秒にしている。実際のアプリでは、エンドポイントごとに変えることが多い（一覧取得は短く、レポート生成は長く）。

</details>

---

## Part 2. Retry

### Step 3. 外部 API 呼び出しを作る

#### やること

Task の Status 変更時に、外部の通知 API へ POST する。

#### 外部 API から返ってくるもの

```text
Task 更新
  ↓
Notification API
  ├─ 200 OK           成功
  ├─ 400 Bad Request  こちらの Request が不正
  ├─ 429 Too Many     レート制限
  ├─ 500 / 503        相手側の障害
  ├─ Timeout          応答がない
  └─ 接続エラー        相手が落ちている
```

**これらを同じ扱いにしてはいけない。**

| 応答 | Retry すべきか | 理由 |
| --- | --- | --- |
| 2xx | 不要 | 成功 |
| 400 / 404 / 422 | **しない** | Request 自体が不正。何度送っても同じ結果になる |
| 401 / 403 | **しない** | 認証・権限の問題。時間では解決しない |
| 429 | する | レート制限。時間を置けば通る。`Retry-After` があれば従う |
| 5xx | する | 相手側の一時障害の可能性がある |
| Timeout | する | 一時的な遅延の可能性がある |
| 接続エラー | する | 相手が再起動中などの可能性がある |

#### 実行

`internal/notify/notifier.go` を新規作成する。Step 4 で説明する `backoff` もこのファイルに含まれている。

```go
// Package notify は外部の通知 API を呼び出す。
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

	"example.com/go-kanban/internal/model"
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
```

各関数の役割。

| 関数 | 役割 |
| --- | --- |
| `TaskStatusChanged` | 送る本文を組み立て、`postWithRetry` に渡す。`service.Notifier` interface を満たす |
| `postWithRetry` | 試行回数の上限まで `post` を繰り返す。`ErrRetryable` でない失敗はその場で諦める |
| `post` | 1回だけ送り、応答を「成功」「Retry する失敗」「Retry しない失敗」に分類する |
| `backoff` | 次の試行までの待ち時間を決める（Step 4） |

```bash
go build ./...
go vet ./...
```

この時点ではまだ誰も `notify` を呼んでいない。呼び出しは Step 5 でつなぐ。

### Step 4. Backoff と Jitter

#### 実装

Step 3 の `notifier.go` に含まれている `backoff` を再掲する。以下は**抜粋（貼り付け不要）**。

```go
func (n *HTTPNotifier) backoff(attempt int) time.Duration {
	base := n.cfg.BaseDelay * time.Duration(1<<(attempt-1))
	jitter := time.Duration(rand.Int64N(int64(base)))

	return base/2 + jitter
}
```

#### なぜ間隔を広げるのか

```text
固定間隔（1秒ごと）で Retry

相手が過負荷 → 全 Client が1秒ごとに再送 → さらに過負荷 → 永久に復旧しない


指数バックオフ

1回目失敗 →  0.1秒待つ
2回目失敗 →  0.2秒待つ
3回目失敗 →  0.4秒待つ
            └─ 相手に回復する時間を与える
```

#### なぜ Jitter が必要なのか

```mermaid
flowchart TD
    subgraph W["Jitter なし: 同期して殺到する"]
        A1[Client 1] -->|t=1.0s| S1[相手サーバ]
        A2[Client 2] -->|t=1.0s| S1
        A3[Client 3] -->|t=1.0s| S1
        A4[... 1000台] -->|t=1.0s| S1
    end
    subgraph G["Jitter あり: 時間が分散する"]
        B1[Client 1] -->|t=0.6s| S2[相手サーバ]
        B2[Client 2] -->|t=1.3s| S2
        B3[Client 3] -->|t=0.9s| S2
        B4[... 1000台] -->|t=0.5-1.5s| S2
    end
    style S1 fill:#ffe0e0,color:#000
```

障害は多くの場合**同時に**起きる。全 Client が同じタイミングで失敗すれば、Retry のタイミングも揃う。復旧しかけた相手に 1000 件が同時に届けば、また落ちる。これを **Retry Storm** または **Thundering Herd** と呼ぶ。

`base/2 + rand(base)` により、待機時間が `base/2` から `base*1.5` の範囲にばらける。

---

### Step 5. 通知の失敗で本処理を失敗させない

#### やること

Status 変更の後に通知を送る。通知が失敗しても、Task の更新は成功として扱う。

#### 実行

`internal/service/task.go` を置き換える。変更点は次の2つ。

- `ChangeStatus` の末尾（`UpdateStatusWithHistory` の後）に通知処理を追加した
- `Notifier` interface のコメントを実装の場所に合わせて直した

```go
package service

import (
	"context"
	"log/slog"

	"example.com/go-kanban/internal/model"
	"example.com/go-kanban/internal/repository"
)

// Notifier は外部通知の契約。Service は HTTP も Retry も知らない。
// 実装は internal/notify にある。通知しない場合は nil を渡す。
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

// ChangeStatus は Status 変更の業務ルールをまとめて適用する。
//
// Handler ではなく Service に置く理由:
// CLI・Batch・別APIから同じ操作を行っても、同じルールが適用されるようにするため。
func (s *TaskService) ChangeStatus(
	ctx context.Context,
	userID, taskID int64,
	newStatus string,
	version int,
) (model.Task, error) {
	if !model.IsValidStatus(newStatus) {
		return model.Task{}, model.Invalid("status must be one of: todo, doing, done")
	}

	// 認可: アクセスできないTaskは 404 として扱われる。
	current, role, err := s.tasks.FindForUser(ctx, taskID, userID)
	if err != nil {
		return model.Task{}, err
	}

	if !model.CanWriteTask(role) {
		return model.Task{}, model.ErrForbidden
	}

	// 競合検出は業務ルール判定より先に行う。
	//
	// 逆順にすると、他Requestが先に doing へ変えた直後の Request は
	// 「doing から doing へは遷移できない」という 400 になる。
	// 利用者にとっての事実は「手元の情報が古い」なので 409 を返し、
	// 再読み込みを促す。
	if current.Version != version {
		return model.Task{}, model.Public(model.ErrConflict,
			"task was updated by another request; reload and retry")
	}

	// 業務ルール: 許可された遷移かどうか。
	if !model.CanTransition(current.Status, newStatus) {
		return model.Task{}, model.Invalid(
			"cannot change status from " + current.Status + " to " + newStatus)
	}

	// 同時更新検出: 読んだ version のまま更新できるか。
	updated, err := s.tasks.UpdateStatusWithHistory(
		ctx, taskID, userID, current.Status, newStatus, version)
	if err != nil {
		return model.Task{}, err
	}

	// 通知の失敗で Task 更新を失敗にしない。
	// DBは既に Commit 済みで、ここで error を返すと利用者は
	// 「失敗した」と判断して再送し、二重処理の原因になる。
	if s.notifier != nil {
		if err := s.notifier.TaskStatusChanged(ctx, updated, current.Status); err != nil {
			s.logger.WarnContext(ctx, "notification failed",
				slog.Int64("task_id", updated.ID),
				slog.String("error", err.Error()),
			)
		}
	}

	return updated, nil
}
```

`internal/app/app.go` を置き換える。Step 2 の版からの変更点は次の2つ。

- `Config` に `NotifyURL` を追加した。空なら通知しない
- `NotifyURL` があるときだけ `HTTPNotifier` を作り、`NewTaskService` に渡す

```go
package app

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"example.com/go-kanban/internal/handler"
	"example.com/go-kanban/internal/middleware"
	"example.com/go-kanban/internal/notify"
	"example.com/go-kanban/internal/repository"
	"example.com/go-kanban/internal/service"
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

	mux.Handle("POST /projects", requireAuth(projectHandler.Create))
	mux.Handle("POST /projects/{id}/members", requireAuth(projectHandler.AddMember))
	mux.Handle("POST /projects/{id}/tasks", requireAuth(taskHandler.Create))
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

	return h
}
```

`cmd/api/main.go` を置き換える。変更点は、環境変数 `NOTIFY_URL` を `cfg.NotifyURL` に読み込む2行と、`DEBUG_ROUTES` のコメント。

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
	// 検証用エンドポイントを使うときだけ DEBUG_ROUTES=1 で起動する。
	cfg.DebugRoutes = os.Getenv("DEBUG_ROUTES") == "1"
	// 通知 API の URL。未設定なら通知しない。
	cfg.NotifyURL = os.Getenv("NOTIFY_URL")

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

```bash
go build ./...
go vet ./...
```

#### なぜこうするのか

```mermaid
flowchart TD
    A[Task を COMMIT] --> B[通知を送る]
    B -->|失敗| C{どうする?}
    C -->|error を返す| D["利用者: 失敗したのか<br/>→ 再送する<br/>→ しかし Task は更新済み<br/>→ 二重処理"]
    C -->|ログに記録して成功を返す| E["利用者: 更新できた<br/>通知は後から追跡・再送できる"]

    style D fill:#ffe0e0,color:#000
    style E fill:#e0ffe0,color:#000
```

**DB の Commit は既に終わっている。** この時点で error を返すと、利用者から見れば「失敗した」としか見えず、再送してくる。しかし Task は更新済みなので、再送は別の問題（version 不一致で 409、あるいは二重処理）を引き起こす。

通知は「送れなかったこと」をログに残し、別の仕組み（再送キュー、定期バッチ）で回収する。

> **POINT**
> 「主処理」と「副作用」を区別する。**主処理が成功したかどうかを、副作用の成否で上書きしない。**
> どちらが主でどちらが副かは要件次第で、例えば決済の通知は主処理に含めるべきかもしれない。その判断自体を意識的に行う。

---

### Step 6. Retry の挙動をテストで確認する

#### やること

外部 API を `httptest.Server` で再現し、Retry の挙動を検証する。

#### 実行

`internal/notify/notifier_test.go` を新規作成する。

```go
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

// testConfig は待ち時間を短くした Test 用の設定。
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

// 一時障害が途中で直れば、残りの試行で成功することを確認する。
func TestRetrySucceedsAfterTransientFailure(t *testing.T) {
	t.Parallel()

	var attempts int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 2回目までは失敗し、3回目で復旧する。
		if atomic.AddInt32(&attempts, 1) < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}

		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	notifier := notify.NewHTTPNotifier(testConfig(server.URL), discardLogger())

	if err := notifier.TaskStatusChanged(context.Background(), model.Task{ID: 1}, "todo"); err != nil {
		t.Fatalf("expected success after retry, got %v", err)
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
```

| テスト | 確かめること |
| --- | --- |
| `TestRetryBehavior` | 応答の Status ごとに、Retry するかしないか |
| `TestRetrySucceedsAfterTransientFailure` | 途中で相手が復旧すれば成功で終わる |
| `TestTimeoutIsRetryable` | 相手が応答しないとき、Client 側の Timeout で打ち切って Retry する |
| `TestRetryStopsOnContextCancel` | 待機中に Context がキャンセルされたら、すぐに諦める |

```bash
go test ./internal/notify/ -v
```

#### 期待結果

検証環境での実際の出力。

```text
=== RUN   TestRetryBehavior
=== RUN   TestRetryBehavior/success_on_first_attempt
=== RUN   TestRetryBehavior/400_is_not_retried
=== RUN   TestRetryBehavior/404_is_not_retried
=== RUN   TestRetryBehavior/429_is_retried
=== RUN   TestRetryBehavior/500_is_retried
=== RUN   TestRetryBehavior/503_is_retried
--- PASS: TestRetryBehavior (0.00s)
--- PASS: TestRetrySucceedsAfterTransientFailure (0.04s)
--- PASS: TestRetryStopsOnContextCancel (0.10s)
--- PASS: TestTimeoutIsRetryable (0.91s)
PASS
ok  	example.com/go-kanban/internal/notify	1.501s
```

| 検証した挙動 | 結果 |
| --- | --- |
| 200 → 試行 1 回 | ○ |
| 400 / 404 → 試行 1 回で諦める | ○ |
| 429 / 500 / 503 → 試行 3 回 | ○ |
| 3回目で復旧 → 成功して終了 | ○ |
| 相手が 500ms 応答しない → 200ms で打ち切り、3回試行、合計 0.91 秒 | ○ |
| Context キャンセル → 2秒待たずに即終了 | ○ |

`TestTimeoutIsRetryable` が示すのは、**相手の応答を待たずに自分から打ち切れている**こと。相手の sleep が 500ms × 3 = 1.5 秒でも、こちらは 0.91 秒で終わっている。

---

## Part 3. 冪等性（Idempotency）

### 問題の構造

```mermaid
sequenceDiagram
    participant C as Client
    participant S as Server
    participant DB as PostgreSQL

    C->>S: POST /projects/1/tasks
    S->>DB: INSERT INTO tasks
    DB-->>S: id = 1
    S--xC: Response が届かない（通信切断 / Timeout）

    Note over C: 成功したのか失敗したのか分からない

    C->>S: POST /projects/1/tasks（再送）
    S->>DB: INSERT INTO tasks
    DB-->>S: id = 2
    S-->>C: 201 Created

    rect rgb(255, 224, 224)
    Note over DB: Task が 2 件できた
    end
```

**Client には区別がつかない。** 「Response が届かなかった」は、「処理されなかった」を意味しない。

### HTTP メソッドと冪等性

| メソッド | 仕様上の冪等性 | 補足 |
| --- | --- | --- |
| GET | あり | 何度読んでも状態は変わらない |
| PUT | あり | 同じ値で上書きするだけ |
| DELETE | あり | 2回目は「すでに無い」だけ |
| **POST** | **なし** | 呼ぶたびに新しい資源が作られる |
| PATCH | 実装次第 | 今回は version で保護しているため、2回目は 409 になる |

**POST だけ対策が必要**になる。

> **NOTE**
> 今回の `PATCH /tasks/{id}/status` は、Chapter 06 の楽観ロックによって既に保護されている。再送しても version が合わず 409 になるため、二重適用されない。**楽観ロックは冪等性の一形態としても機能する。**

---

### Step 7. Idempotency-Key を実装する

#### やること

Client が発行した一意なキーで、処理済みのリクエストを識別する。

#### 実行

`migrations/004_idempotency.sql` を新規作成する。

```sql
CREATE TABLE idempotency_keys (
    key TEXT NOT NULL,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    endpoint TEXT NOT NULL,
    status_code INTEGER NOT NULL,
    response_body TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (key, user_id, endpoint)
);
```

```bash
docker compose exec -T db \
  psql -U kanban -d kanban -v ON_ERROR_STOP=1 < migrations/004_idempotency.sql
```

主キーが `(key, user_id, endpoint)` の3つである点が重要になる。

| 含める理由 | |
| --- | --- |
| `user_id` | キーを利用者ごとに分離する。共有すると、他人のキーを指定して**他人のレスポンスを読める** |
| `endpoint` | 同じキーで別の API を叩いたとき、前の結果が返るのを防ぐ |

`internal/repository/idempotency.go` を新規作成する。保存は `ON CONFLICT DO NOTHING` にする。

```go
package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNoStoredResponse は、その Key の結果がまだ保存されていないことを表す。
var ErrNoStoredResponse = errors.New("no stored response")

// StoredResponse は再送時にそのまま返す、前回の Response。
type StoredResponse struct {
	StatusCode int
	Body       string
}

type IdempotencyRepository struct {
	pool *pgxpool.Pool
}

func NewIdempotencyRepository(pool *pgxpool.Pool) *IdempotencyRepository {
	return &IdempotencyRepository{pool: pool}
}

// Find は保存済みの結果を返す。無ければ ErrNoStoredResponse。
func (r *IdempotencyRepository) Find(
	ctx context.Context,
	key string,
	userID int64,
	endpoint string,
) (StoredResponse, error) {
	var stored StoredResponse

	err := r.pool.QueryRow(
		ctx,
		`SELECT status_code, response_body FROM idempotency_keys
		 WHERE key = $1 AND user_id = $2 AND endpoint = $3`,
		key, userID, endpoint,
	).Scan(&stored.StatusCode, &stored.Body)

	if errors.Is(err, pgx.ErrNoRows) {
		return StoredResponse{}, ErrNoStoredResponse
	}

	if err != nil {
		return StoredResponse{}, fmt.Errorf("query idempotency key: %w", err)
	}

	return stored, nil
}

// Save は結果を保存する。
// ON CONFLICT DO NOTHING により、同時に2つのRequestが完了しても
// 先に保存された結果が正となる。
func (r *IdempotencyRepository) Save(
	ctx context.Context,
	key string,
	userID int64,
	endpoint string,
	stored StoredResponse,
) error {
	_, err := r.pool.Exec(
		ctx,
		`INSERT INTO idempotency_keys (key, user_id, endpoint, status_code, response_body)
		 VALUES ($1, $2, $3, $4, $5)
		 ON CONFLICT (key, user_id, endpoint) DO NOTHING`,
		key, userID, endpoint, stored.StatusCode, stored.Body,
	)
	if err != nil {
		return fmt.Errorf("insert idempotency key: %w", err)
	}

	return nil
}
```

`internal/middleware/idempotency.go` を新規作成する。

```go
package middleware

import (
	"bytes"
	"errors"
	"log/slog"
	"net/http"

	"example.com/go-kanban/internal/httpx"
	"example.com/go-kanban/internal/model"
	"example.com/go-kanban/internal/repository"
)

const IdempotencyKeyHeader = "Idempotency-Key"

// captureWriter は Response を記録しつつ Client へも書き込む。
type captureWriter struct {
	http.ResponseWriter
	status int
	body   bytes.Buffer
}

func (w *captureWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *captureWriter) Write(b []byte) (int, error) {
	w.body.Write(b)
	return w.ResponseWriter.Write(b)
}

// Idempotency は同じ Idempotency-Key の再送に対して、
// 処理をやり直さず前回の結果を返す。
func Idempotency(
	repo *repository.IdempotencyRepository,
	logger *slog.Logger,
	next http.Handler,
) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get(IdempotencyKeyHeader)
		if key == "" {
			next.ServeHTTP(w, r)
			return
		}

		user, ok := httpx.CurrentUser(r.Context())
		if !ok {
			httpx.RespondError(w, r, model.ErrUnauthorized)
			return
		}

		// Key は利用者ごとに分離する。
		// 共有すると、他人のKeyを指定して他人のResponseを読めてしまう。
		endpoint := r.Method + " " + r.URL.Path

		stored, err := repo.Find(r.Context(), key, user.ID, endpoint)

		switch {
		case err == nil:
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Idempotent-Replay", "true")
			w.WriteHeader(stored.StatusCode)

			if _, err := w.Write([]byte(stored.Body)); err != nil {
				logger.ErrorContext(r.Context(), "write replayed response",
					slog.String("error", err.Error()))
			}

			return
		case !errors.Is(err, repository.ErrNoStoredResponse):
			httpx.RespondError(w, r, err)
			return
		}

		capture := &captureWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(capture, r)

		// 成功した結果だけ保存する。
		// 失敗まで保存すると、一時障害による失敗が永続化される。
		if capture.status >= 200 && capture.status < 300 {
			if err := repo.Save(r.Context(), key, user.ID, endpoint, repository.StoredResponse{
				StatusCode: capture.status,
				Body:       capture.body.String(),
			}); err != nil {
				logger.ErrorContext(r.Context(), "save idempotency key",
					slog.String("error", err.Error()))
			}
		}
	})
}
```

`internal/app/app.go` を置き換える。これがこの章の最終版になる。Step 5 の版からの変更点は次の3つ。

- `IdempotencyRepository` を作る
- 作成系の API 用に `idempotent` を用意する
- `POST /projects/{id}/tasks` を `requireAuth` から `idempotent` に替える

```go
package app

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"example.com/go-kanban/internal/handler"
	"example.com/go-kanban/internal/middleware"
	"example.com/go-kanban/internal/notify"
	"example.com/go-kanban/internal/repository"
	"example.com/go-kanban/internal/service"
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

	return h
}
```

```bash
go build ./...
go vet ./...
```

`Idempotency` は `RequireAuth` の内側に置く。`CurrentUser` で利用者を特定してからでないと、キーを利用者ごとに分離できないため。

#### 判断の流れ

```mermaid
flowchart TD
    A[POST + Idempotency-Key] --> B{Key がある?}
    B -->|なし| C[通常処理]
    B -->|あり| D{保存済み?}
    D -->|あり| E["前回の Status と Body を返す<br/>Idempotent-Replay: true"]
    D -->|なし| F[通常処理]
    F --> G{2xx?}
    G -->|はい| H[結果を保存]
    G -->|いいえ| I[保存しない<br/>再送で再試行できる]
    H --> J[レスポンス]
    I --> J

    style E fill:#e0ffe0,color:#000
```

> **POINT**
> **失敗した結果を保存しない。** 保存すると、一時的な DB 障害による 500 が「このキーは永久に 500」として固定される。
> 利用者は同じキーで再送でき、次は成功できる状態を保つ。

---

### Step 8. 動かして確認する

#### 実行

サーバを起動し直してから実行する。Session は DB に保存しているので、再起動しても Cookie は使える。401 が返った場合は、Step 2 と同じ手順でログインし直す（有効期限は 24 時間）。

まず alice の Project を作り、その ID を控える。

```bash
PROJECT_ID=$(curl -s -b ./cookie/alice.txt -X POST localhost:8080/projects \
  -H 'Content-Type: application/json' -d '{"name":"Chapter07 Board"}' \
  | sed -E 's/^\{"id":([0-9]+).*/\1/')
echo "PROJECT_ID=$PROJECT_ID"
```

`PROJECT_ID=` の後ろに数字だけが表示されれば成功。JSON が表示された場合は Session が切れているので、ログインからやり直す。

```bash
# Key なしで同じリクエストを2回
curl -s -o /dev/null -w '1st: %{http_code}\n' -b ./cookie/alice.txt -X POST localhost:8080/projects/$PROJECT_ID/tasks \
  -H 'Content-Type: application/json' -d '{"title":"duplicate me","priority":"low"}'
curl -s -o /dev/null -w '2nd: %{http_code}\n' -b ./cookie/alice.txt -X POST localhost:8080/projects/$PROJECT_ID/tasks \
  -H 'Content-Type: application/json' -d '{"title":"duplicate me","priority":"low"}'

# 同じタイトルの Task が何件できたか数える
curl -s -b ./cookie/alice.txt localhost:8080/projects/$PROJECT_ID/tasks | grep -o '"title":"duplicate me"' | wc -l

# Key ありで同じリクエストを2回
KEY=$(openssl rand -hex 16)
curl -i -b ./cookie/alice.txt -X POST localhost:8080/projects/$PROJECT_ID/tasks \
  -H 'Content-Type: application/json' -H "Idempotency-Key: $KEY" \
  -d '{"title":"idempotent task","priority":"low"}'
curl -i -b ./cookie/alice.txt -X POST localhost:8080/projects/$PROJECT_ID/tasks \
  -H 'Content-Type: application/json' -H "Idempotency-Key: $KEY" \
  -d '{"title":"idempotent task","priority":"low"}'

# 同じタイトルの Task が何件できたか数える
curl -s -b ./cookie/alice.txt localhost:8080/projects/$PROJECT_ID/tasks | grep -o '"title":"idempotent task"' | wc -l
```

#### 期待結果

検証環境での実際の出力。

**Key なし。**

```text
1st: 201
2nd: 201
2      ← Task が2件できた
```

**Key あり。** `id` と `project_id` の値は環境によって変わる。

```http
HTTP/1.1 201 Created

{"id":205,"project_id":12,"title":"idempotent task",...}
```

再送。

```http
HTTP/1.1 201 Created
Idempotent-Replay: true

{"id":205,"project_id":12,"title":"idempotent task",...}
```

```text
1      ← Task は1件だけ
```

| 確認項目 | Key なし | Key あり |
| --- | --- | --- |
| 作成された Task | **2 件** | **1 件** |
| 2回目の id | 別の id | **同じ id（205）** |
| `Idempotent-Replay` ヘッダ | なし | **true** |

異なるキーを使えば、別の処理として扱われる（検証済み。`key-456` で 2 件目が作成される）。

<details>
<summary>本番運用で追加で考えること</summary>

| 項目 | 検討事項 |
| --- | --- |
| キーの有効期限 | 無期限に保存するとテーブルが肥大する。24時間〜7日程度で削除するバッチが必要 |
| 処理中の再送 | 1回目がまだ処理中に再送が来ると、両方が処理を開始しうる。厳密には「処理中」レコードを先に INSERT して排他する |
| リクエスト本文の検証 | 同じキーで**異なる本文**が来たら 422 を返すべき。今回は未実装 |
| Body のサイズ | 大きなレスポンスをそのまま保存すると DB を圧迫する |
| キーの生成 | Client 側で UUID などを生成する。サーバが生成すると、それを受け取るための往復が必要になり意味がない |

</details>

---

## この章のまとめ

| 導入したもの | 防いだ問題 |
| --- | --- |
| Request 全体の Timeout | Client が待っていない処理の滞留 |
| Context の伝播 | DB クエリがキャンセルされず接続を占有する |
| `http.Client{Timeout: ...}` | 応答しない相手への接続が残り続ける |
| Retry の可否判定 | 400 を延々と再送する無駄 |
| 指数バックオフ | 固定間隔の Retry が相手を殺し続ける |
| Jitter | Retry Storm（同時再送による二次障害） |
| Context を見る待機（`select`） | キャンセル済みなのに待ち続ける |
| 通知失敗を主処理から切り離す | 副作用の失敗が主処理の失敗になり、再送で二重処理を招く |
| Idempotency-Key | Response 紛失による再送での二重作成 |
| `(key, user_id, endpoint)` の複合主キー | 他人のレスポンスの読み取り |
| 成功時のみ保存 | 一時障害による失敗の永続化 |

| 得られた検証データ | 値 |
| --- | --- |
| Timeout（上限 2 秒、DB 3 秒） | 2.00 秒で 503 |
| Retry（400 / 404） | 試行 1 回で終了 |
| Retry（429 / 500 / 503） | 試行 3 回 |
| Client Timeout（相手 500ms 無応答） | 3 回試行して合計 0.91 秒 |
| Idempotency-Key ありの再送 | Task 1 件、同じ id、`Idempotent-Replay: true` |

次は [Chapter 08: Logging と Audit Log](./chapter08_observability.md)。ここまでで作った仕組みが障害時に追跡できるよう、ログを整える。
