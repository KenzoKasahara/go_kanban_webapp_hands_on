# Chapter 02: 雑な CRUD を作る

## この章の目的

PostgreSQL を起動し、Task を作成・取得できる API を作る。ただし意図的に雑に作る。

Validation は最小限、認証なし、Handler から直接 SQL を呼ぶ。そのうえで「この実装の何が問題なのか」を実際のレスポンスとして観測する。ここで観測した問題が、Chapter 03 以降の改善対象になる。

## 現在地

準備(00) → **基礎(01-02)** → Webアプリ化(03-05) → 本番対応(06-08) → Test(09)

## 完了条件

- [ ] `docker compose up -d` で PostgreSQL が起動する
- [ ] Migration でテーブルが作られる
- [ ] `POST /projects/1/tasks` で Task を作成できる
- [ ] 空の title が 201 で保存されてしまうことを確認した
- [ ] 存在しない Task の取得が 500 になることを確認した

## この章の構造

```text
curl
 │
 ▼
Handler        ← HTTP も Validation も SQL も全部ここに書く（あえて）
 │ pgxpool
 ▼
PostgreSQL
```

---

## Step 1. PostgreSQL を起動する

### やること

Docker Compose で PostgreSQL 17 を起動する。

### 実行

`docker-compose.yml` を作る。

```yaml
services:
  db:
    image: postgres:17
    environment:
      POSTGRES_DB: kanban
      POSTGRES_USER: kanban
      POSTGRES_PASSWORD: local-dev-password
    ports:
      - "5432:5432"
    volumes:
      - kanban-db:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U kanban -d kanban"]
      interval: 3s
      timeout: 3s
      retries: 20

volumes:
  kanban-db:
```

```bash
docker compose up -d --wait
docker compose ps
```

`--wait` を付けると、`healthcheck` が通って `healthy` になるまでコマンドが戻らない。

### 期待結果

`healthy` になるまで数秒かかる。

```text
NAME             STATUS
go-kanban-db-1   Up 55 seconds (healthy)
```

> **WARNING**
> `local-dev-password` はローカルハンズオン専用。本番環境で、コードや Compose ファイルに固定パスワードを書かない。接続情報は環境変数やシークレット管理の仕組みから渡す。

### なぜ行うのか

`healthcheck` を入れておくと、「起動したがまだ接続を受け付けていない」状態を区別できる。`--wait` なしの `docker compose up -d` はコンテナを起動した時点で戻るので、すぐ次の Migration を流すと接続エラーで落ちることがある。

---

## Step 2. テーブルを作る

### やること

Project と Task のテーブルを作る。

### 実行

`migrations/001_init.sql` を作る。

```sql
CREATE TABLE projects (
    id BIGSERIAL PRIMARY KEY,
    name TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE tasks (
    id BIGSERIAL PRIMARY KEY,
    project_id BIGINT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    title TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    priority TEXT NOT NULL DEFAULT 'medium',
    status TEXT NOT NULL DEFAULT 'todo',
    version INTEGER NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_tasks_project_id ON tasks(project_id);
```

適用する。

```bash
docker compose exec -T db \
  psql -U kanban -d kanban -v ON_ERROR_STOP=1 < migrations/001_init.sql
```

確認する。

```bash
docker compose exec db psql -U kanban -d kanban -c '\dt'
```

### 期待結果

```text
CREATE TABLE
CREATE TABLE
CREATE INDEX

         List of relations
 Schema |   Name   | Type  | Owner
--------+----------+-------+--------
 public | projects | table | kanban
 public | tasks    | table | kanban
(2 rows)
```

### なぜ行うのか

`-v ON_ERROR_STOP=1` を付けると、途中の SQL が失敗した時点で止まる。これがないと、エラーを出しながら最後まで走り切り、中途半端なスキーマが残る。

<details>
<summary>この時点のスキーマ設計で決めていること</summary>

| 決定 | 理由 |
|---|---|
| `project_id` に `REFERENCES ... ON DELETE CASCADE` | Project が消えたら Task も消す。孤児レコードを作らない |
| `version INTEGER NOT NULL DEFAULT 1` | Chapter 06 の楽観ロックで使う。後から列を足すと既存行の扱いに困るので最初から入れる |
| `description` は `NOT NULL DEFAULT ''` | NULL と空文字の両方が存在する状態を避ける。判定が1つ減る |
| `idx_tasks_project_id` | `WHERE project_id = $1` が頻出するため |
| `TIMESTAMPTZ` | UTC に正規化して保存し、表示時にセッションのタイムゾーンへ変換する。`TIMESTAMP` はタイムゾーンを持たないので、どの時刻を指すかが曖昧になる |

</details>

---

## Step 3. DB ドライバを入れる

### やること

Go から PostgreSQL へ接続するためのドライバを追加する。

### 実行

```bash
go get github.com/jackc/pgx/v5/pgxpool
```

### 期待結果

`go.mod` に依存が追加される。検証環境では `v5.11.0` が入った。

### なぜ行うのか

Go だけでは PostgreSQL 固有の通信方法を知らない。

```text
Go Application
      ↓
     pgx              ← Go の関数呼び出しを PostgreSQL の通信形式へ変換する
      ↓
PostgreSQL Protocol
      ↓
PostgreSQL
```

`pgxpool` は接続プール付きの API を提供する。リクエストごとに新しい接続を張ると、接続確立のコストと DB 側の接続数上限がすぐ問題になる。プールは接続を使い回す。

---

## Step 4. 雑な CRUD を書く

### やること

Handler から直接 SQL を呼ぶ CRUD を書く。

### 実行

`cmd/api/main.go` を書き換える。

```go
package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strconv"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Task struct {
	ID          int64  `json:"id"`
	ProjectID   int64  `json:"project_id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Priority    string `json:"priority"`
	Status      string `json:"status"`
	Version     int    `json:"version"`
}

type Project struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

var pool *pgxpool.Pool

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	if err := json.NewEncoder(w).Encode(body); err != nil {
		log.Printf("encode response: %v", err)
	}
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func createProjectHandler(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name string `json:"name"`
	}

	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var project Project

	err := pool.QueryRow(
		r.Context(),
		"INSERT INTO projects (name) VALUES ($1) RETURNING id, name",
		input.Name,
	).Scan(&project.ID, &project.Name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusCreated, project)
}

func createTaskHandler(w http.ResponseWriter, r *http.Request) {
	projectID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var input struct {
		Title       string `json:"title"`
		Description string `json:"description"`
		Priority    string `json:"priority"`
	}

	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if input.Priority == "" {
		input.Priority = "medium"
	}

	var task Task

	err = pool.QueryRow(
		r.Context(),
		`INSERT INTO tasks (project_id, title, description, priority)
		 VALUES ($1, $2, $3, $4)
		 RETURNING id, project_id, title, description, priority, status, version`,
		projectID, input.Title, input.Description, input.Priority,
	).Scan(
		&task.ID, &task.ProjectID, &task.Title,
		&task.Description, &task.Priority, &task.Status, &task.Version,
	)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusCreated, task)
}

func listTasksHandler(w http.ResponseWriter, r *http.Request) {
	projectID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	rows, err := pool.Query(
		r.Context(),
		`SELECT id, project_id, title, description, priority, status, version
		 FROM tasks WHERE project_id = $1 ORDER BY id`,
		projectID,
	)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	tasks := []Task{}

	for rows.Next() {
		var task Task

		if err := rows.Scan(
			&task.ID, &task.ProjectID, &task.Title,
			&task.Description, &task.Priority, &task.Status, &task.Version,
		); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		tasks = append(tasks, task)
	}

	writeJSON(w, http.StatusOK, tasks)
}

func getTaskHandler(w http.ResponseWriter, r *http.Request) {
	taskID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var task Task

	err = pool.QueryRow(
		r.Context(),
		`SELECT id, project_id, title, description, priority, status, version
		 FROM tasks WHERE id = $1`,
		taskID,
	).Scan(
		&task.ID, &task.ProjectID, &task.Title,
		&task.Description, &task.Priority, &task.Status, &task.Version,
	)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, task)
}

func main() {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://kanban:local-dev-password@localhost:5432/kanban"
	}

	var err error

	pool, err = pgxpool.New(context.Background(), dsn)
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()

	if err := pool.Ping(context.Background()); err != nil {
		log.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", healthHandler)
	mux.HandleFunc("POST /projects", createProjectHandler)
	mux.HandleFunc("POST /projects/{id}/tasks", createTaskHandler)
	mux.HandleFunc("GET /projects/{id}/tasks", listTasksHandler)
	mux.HandleFunc("GET /tasks/{id}", getTaskHandler)

	log.Println("server started on :8080")

	if err := http.ListenAndServe(":8080", mux); err != nil {
		log.Fatal(err)
	}
}
```

```bash
go mod tidy
go vet ./...
go run ./cmd/api
```

| コマンド | 役割 |
|---|---|
| `go mod tidy` | `import` と `go.mod` / `go.sum` を突き合わせ、足りない依存を追加し、使っていない依存を削除する |
| `go vet ./...` | コンパイルは通るがバグの可能性が高い書き方（フォーマット指定と引数の型の不一致など）を検出する。何も出なければ問題なし |
| `go run ./cmd/api` | `cmd/api` をビルドしてそのまま起動する。実行ファイルは残らない。サーバは `Ctrl + C` で停止する |

`go-kanban/`（`go.mod` があるディレクトリ）で実行する。

### 期待結果

サーバが起動し、`server started on :8080` が出る。`pool.Ping()` が通らなければ DB 接続の問題なので、ここで気づける。

### なぜこの書き方をするのか（あえて）

| 雑な点 | 本来どうすべきか | 直す章 |
|---|---|---|
| Handler が直接 SQL を実行している | Repository へ分離する | Chapter 05 |
| Validation が `priority` の既定値設定だけ | 必須・長さ・値域を検証する | Chapter 03 |
| `http.Error(w, err.Error(), 500)` で error をそのまま返す | 分類して安全な文言に変換する | Chapter 03 |
| 認証も認可もない | Session と Role で制御する | Chapter 04 |
| `pool` がグローバル変数 | 依存として注入する | Chapter 05 |

後で「なぜ改善が必要なのか」を比較するために、先に問題のある状態を作って動かす。

<details>
<summary>GO NOTE: <code>$1</code> と <code>PathValue</code></summary>

`$1`, `$2` は PostgreSQL のプレースホルダ。値を SQL 文と別物としてサーバへ渡す。文字列連結で SQL を組み立てるとどうなるかは Chapter 05 で実演する。

`r.PathValue("id")` は Go 1.22 以降の機能で、`"POST /projects/{id}/tasks"` の `{id}` 部分を取り出す。戻り値は常に文字列なので、`strconv.ParseInt` で数値へ変換する。

</details>

<details>
<summary>GO NOTE: <code>Decode(&amp;input)</code> や <code>Scan(&amp;task.ID)</code> に <code>&amp;</code> が付く理由</summary>

ポインタの基本は [Chapter 01「ポインタを使う場面と使わない場面」](./chapter01_http.md#ポインタを使う場面と使わない場面) で扱った。ここでは、`Decode` がどう動くかを図で追う。

#### 結論

`Decode` は、受け取った変数に JSON の値を書き込む関数である。書き込む先を教えるために、`input` の場所（アドレス）を `&input` で渡す。

![Go で Decode(&input) や Scan(&task.ID) に & が付く理由](../images/chapter02_crud/Goのアドレス・ポインタを使用する理由.png)

#### `&` なしだとコピーに書き込まれて捨てられる

Go は関数に値を渡すときにコピーを作る。`Decode(input)` と書くと、`Decode` が受け取るのは `input` の複製になる。仮に書き込めても複製のほうに入り、関数が終わると捨てられる。

実際には、`Decode` は書き込み先がないと判断してエラーを返す。

#### `&` ありなら本体に書き込まれる

`&input` で渡すのは `input` の場所だけで、`Decode` はその場所をたどって呼び出し元の `input` に直接書き込む。

#### 実行して確かめる

`Decode` に `input` と `&input` をそれぞれ渡すと、次の結果になる。

```go
package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

func main() {
	var input struct {
		Name string `json:"name"`
	}

	err := json.NewDecoder(strings.NewReader(`{"name":"買い物"}`)).Decode(input)
	fmt.Printf("& なし: err=%v, input.Name=%q\n", err, input.Name)

	err = json.NewDecoder(strings.NewReader(`{"name":"買い物"}`)).Decode(&input)
	fmt.Printf("& あり: err=%v, input.Name=%q\n", err, input.Name)
}
```

```text
& なし: err=json: Unmarshal(non-pointer struct { Name string "json:\"name\"" }), input.Name=""
& あり: err=<nil>, input.Name="買い物"
```

`& なし` はコンパイルエラーにはならない（`Decode` の引数は `any` 型なので何でも受け付ける）。ただし `go vet` を実行すると `call of Decode passes non-pointer` と警告される。

#### Python との違い

| | Python | Go |
|---|---|---|
| 関数に渡されるもの | オブジェクトへの参照 | 値のコピー |
| 関数の中で書き換えたとき | list・dict などは呼び出し元に反映される | 構造体・数値・文字列は反映されない |
| 呼び出し元に反映させる方法 | そのまま渡す | `&` で場所を渡す |

Python の感覚で「渡せば中身を埋めてもらえる」と考えると、Go では期待どおりに動かない。

#### `&` を付けるかどうかの判断

「変数には必ず `&` を付ける」わけではない。関数に書き込んでほしいときに付ける。

| 書き方 | `&` | 理由 |
|---|---|---|
| `Decode(&input)` | 付ける | JSON の値を `input` に書き込んでもらう |
| `Scan(&project.ID, &project.Name)` | 付ける | DB から読んだ値を各フィールドに書き込んでもらう |
| `writeJSON(w, http.StatusCreated, project)` | 付けない | `project` を読んで JSON にするだけ |
| `input.Name` を SQL の引数に渡す | 付けない | 値を読むだけ |

迷ったら、関数の引数の型を見る。`*T` なら `&` を付ける。`Decode(v any)` のように型から分からない関数は、ドキュメントに「ポインタを渡す」と書かれている。

</details>

---

## Step 5. 動かして確認する

### 実行

```bash
# Project を作る
curl -X POST localhost:8080/projects \
  -H 'Content-Type: application/json' \
  -d '{"name":"Kanban Hands-on"}'

# Task を作る
curl -X POST localhost:8080/projects/1/tasks \
  -H 'Content-Type: application/json' \
  -d '{"title":"write docs","description":"","priority":"high"}'
```

### 期待結果

実際の出力。

```json
{"id":1,"name":"Kanban Hands-on"}
```

```json
{"id":1,"project_id":1,"title":"write docs","description":"","priority":"high","status":"todo","version":1}
```

`status` と `version` は送っていないが、DB の `DEFAULT` が入っている。

---

## Step 6. 壊して観察する（この章の本題）

この実装の何が問題なのかを、レスポンスとして目に見える形で確認する。

### Failure Test 1: 空の title と存在しない priority

```bash
curl -i -X POST localhost:8080/projects/1/tasks \
  -H 'Content-Type: application/json' \
  -d '{"title":"","priority":"SUPER_HIGH"}'
```

実際の出力。

```http
HTTP/1.1 201 Created
```

```json
{"id":2,"project_id":1,"title":"","description":"","priority":"SUPER_HIGH","status":"todo","version":1}
```

> **観測された問題**
> title が空でも、priority が定義外の値でも 201 Created で保存される。
> DB に `title TEXT NOT NULL` と書いてあるので弾かれそうに見えるが、空文字は NULL ではないので制約に引っかからない。

一度保存されると、この不正データは一覧 API（`curl localhost:8080/projects/1/tasks`）にもそのまま出てくる。

```json
[{"id":1,...,"title":"write docs",...},
 {"id":2,...,"title":"","priority":"SUPER_HIGH",...}]
```

### Failure Test 2: 存在しない Task を取得する

```bash
curl -i localhost:8080/tasks/9999
```

実際の出力。

```http
HTTP/1.1 500 Internal Server Error

no rows in result set
```

> **観測された問題**
> 「そんな Task はない」は利用者の入力に起因する話であって、サーバの不具合ではない。404 を返すべきところで 500 を返している。
> クライアントから見ると「サーバが壊れた」と区別がつかず、リトライしても無駄になる。

### Failure Test 3: 存在しない Project に Task を作る

```bash
curl -i -X POST localhost:8080/projects/9999/tasks \
  -H 'Content-Type: application/json' \
  -d '{"title":"orphan","priority":"low"}'
```

実際の出力。

```http
HTTP/1.1 500 Internal Server Error

ERROR: insert or update on table "tasks" violates foreign key constraint "tasks_project_id_fkey" (SQLSTATE 23503)
```

> **観測された問題**
> `http.Error(w, err.Error(), 500)` は、DB が返したエラーメッセージをそのまま利用者へ返す。
> 本文にテーブル名 `tasks`、制約名 `tasks_project_id_fkey`、SQLSTATE `23503` が出ている。攻撃者にとってはスキーマ構造の手がかりになる。

### Failure Test 4: JSON のフィールド名を打ち間違える

```bash
curl -i -X POST localhost:8080/projects/1/tasks \
  -H 'Content-Type: application/json' \
  -d '{"titel":"typo","priority":"low"}'
```

`json.Decoder` は構造体にない `titel`（typo）を黙って無視するので、title が空の Task が 201 で作られる。利用者には「送ったのに反映されない」としか見えない。

---

## What Happened?

この章で観測した問題を整理する。

```mermaid
flowchart TD
    A["POST /projects/1/tasks<br/>{title: '', priority: 'SUPER_HIGH'}"] --> B[JSON Decode]
    B --> C{Validation}
    C -->|存在しない| D[そのまま INSERT]
    D --> E["201 Created<br/>不正データが保存される"]

    F["GET /tasks/9999"] --> G[SELECT]
    G --> H{結果が0件}
    H --> I["pgx.ErrNoRows"]
    I --> J["err != nil なので一律 500<br/>+ 内部メッセージを露出"]

    style E fill:#ffe0e0,color:#000
    style J fill:#ffe0e0,color:#000
```

| 観測した事象 | 根本原因 | 対応する章 |
|---|---|---|
| 空 title が 201 で保存される | 入力検証がない | Chapter 03 |
| typo したフィールドが無視される | JSON Decode が未知フィールドを許している | Chapter 03 |
| 存在しない Task が 500 | `pgx.ErrNoRows` を業務上の意味へ翻訳していない | Chapter 03 |
| DB の内部情報が漏れる | error を分類せずそのまま返している | Chapter 03 |
| 誰でも全 Task を読める | 認証・認可がない | Chapter 04 |

どれも正常系の curl では「動いているように見える」ので、わざと壊すテストをしないと気づけない。

> **POINT**
> 正解コードを写して終わりにしない。一度問題を観測してから改善すると、その改善が何を防いでいるのかが分かる。

次は [Chapter 03: Validation と Error Handling](./chapter03_validation.md) で、表のうち Chapter 03 が担当する4つの問題を実際に潰す。
