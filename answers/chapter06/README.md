# Chapter 06 完成時点のコード

[Chapter 06: Transaction と同時更新](../../docs/chapter/chapter06_transaction.md) を終えた時点の `go-kanban` と、Part 1 で使う実験用ディレクトリ `goroutine-lab` です。本文では抜粋しか載せていないファイルの全体を確認できます。

自分のコードが動かないときの答え合わせに使ってください。Step 9 の判定順序（version チェックを遷移チェックより先に置く）は、並列で叩いて 400 が返るのを一度見てから読むほうが納得しやすいはずです。

## 構成

```text
chapter06/
├── cmd/api/main.go
├── internal/
│   ├── model/task.go         Status の定数と遷移規則（CanTransition / IsValidStatus）を追加
│   ├── repository/task.go    UpdateStatusWithHistory（楽観ロック + 履歴を1つの Transaction で）を追加
│   ├── service/task.go       ChangeStatus（判定順序）を追加
│   ├── handler/task.go       PATCH /tasks/{id}/status の Handler を追加
│   ├── app/app.go            ルーティングを追加
│   └── ...                   その他は Chapter 05 と同じ
├── migrations/003_history.sql
├── scripts/chapter06_check.sh
└── goroutine-lab/            Part 1 の実験用（別モジュール）
    ├── go.mod
    ├── demo/main.go          Step 1
    ├── waitgroup/main.go     Step 2
    ├── counter_race_test.go  Step 3（Data Race 版）
    └── counter_test.go       Step 4（Mutex 版）
```

Chapter 05 の Step 11 で消した検証用コード（`debug.go`、`SearchUnsafe`、`DEBUG_ROUTES` など）は含みません。

## 動かし方

### Part 1. goroutine-lab

`goroutine-lab/` は独立したモジュールです。`chapter06/` で `go build ./...` を実行しても対象になりません。

```bash
cd answers/chapter06/goroutine-lab

go run ./demo
go run ./waitgroup

# Step 4（Mutex 版）。警告なしで counter=1000 になる
go test -race -run TestCounterRace -v -count=3 .
go test -race ./...

# Step 3（Data Race 版）。-tags racedemo を付けたときだけビルドされる
go test -tags racedemo -run TestCounterRace -v -count=3 .
go test -tags racedemo -race -run TestCounterRace -v .
```

### Part 3・Part 4. API

DB は `go-kanban/` の `docker compose` をそのまま使います。先に `task_history` テーブルを作ります。

```bash
cd go-kanban
docker compose exec -T db \
  psql -U kanban -d kanban -v ON_ERROR_STOP=1 < ../answers/chapter06/migrations/003_history.sql
```

```bash
cd answers/chapter06
go build ./...
go vet ./...
go run ./cmd/api
```

別のターミナルで確認スクリプトを実行します。

```bash
cd answers/chapter06
bash scripts/chapter06_check.sh
```

`passed: 31, failed: 0` になれば、本文の Step 10〜12 と同じ結果が出ています。

| 区分 | 確認すること |
|---|---|
| 回帰確認 | Task の作成・取得・検索、メンバー以外への 404 |
| Step 10 | 存在しない Status と不正な遷移が 400、Viewer が 403、メンバー外が 404、古い version が 409、成功時に version が 1 → 2 → 3、履歴は成功した2件だけ |
| Step 11 | 8並列・同一 version で 200 が1件、409 が7件。version と履歴は1回分だけ |
| Step 12 | 履歴の INSERT を CHECK 制約で失敗させると 500。Task は更新前のまま、履歴も増えない。制約を外すと 200 |

DB の中身は `docker compose exec` 経由の psql で確認します。`go-kanban/` 以外の compose ファイルや DB 名を使っている場合は、環境変数で指定します。

```bash
COMPOSE_FILE=/path/to/docker-compose.yml DB_NAME=kanban bash scripts/chapter06_check.sh
```

docker compose に接続できない場合、DB を直接見る項目（履歴の件数と Step 12）は SKIP します。

## 本文との違い

| 箇所 | このコード | 理由 |
|---|---|---|
| Step 3 のテスト | `counter_race_test.go` に分け、`//go:build racedemo` を付けた | 本文では `counter_test.go` を書き換える。両方を残すため Build Tag で切り替える。Data Race 版が既定でビルドされると `go test -race ./...` が必ず失敗する |
| Race Detector の行番号 | Build Tag とコメントの4行分、本文の出力とずれる | 本文の `counter_test.go:16`（`counter++`）と `:21`（`t.Logf`）は、ここでは `counter_race_test.go:20` と `:25` になる |
| Step 5・11・12 の Task id | スクリプトでは毎回新しい Task を作る | 本文の id=2・206 は検証環境の値。既存データに依存せず何度でも実行できるようにした |
| Step 12 の制約名 | `reject_done_<実行時刻>` | 途中で中断しても `trap` で必ず外す。他の実行と名前が衝突しないようにした |
| `ChangeStatus` の Handler | 本文に掲載なし。`Create` と同じ形で書いた | JSON は `{"status": "...", "version": N}`。未知の field は Chapter 03 の `DecodeJSON` により 400 |
| Part 2（Lost Update の再現） | コードなし | psql だけで行う手順のため。本文の SQL をそのまま使う |

## 検証した環境

Go 1.27.1（`go.mod` の `go` 行は Chapter 05 と同じ `1.26.0`）、PostgreSQL 17、Windows 11 の Git Bash、gcc 15.1.0（MinGW、Race Detector 用）。`go-kanban/` のコンテナ内に一時 DB を作り、001〜003 のマイグレーションを適用して確認スクリプトを2回実行し、2回とも `passed: 31, failed: 0` でした。
