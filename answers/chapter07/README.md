# Chapter 07 完成時点のコード

[Chapter 07: Timeout・Retry・冪等性](../../docs/chapter/chapter07_resilience.md) を終えた時点の `go-kanban` です。本文では抜粋しか載せていないファイルの全体を確認できます。Chapter 06 の変更（Status 変更・楽観ロック・履歴の Transaction）も含みます。

自分のコードが動かないときの答え合わせに使ってください。

## 構成

```text
chapter07/
├── cmd/api/main.go                  起動処理（NOTIFY_URL / DEBUG_ROUTES を読む）
├── internal/
│   ├── model/                       型と業務ルール（Status 遷移は Chapter 06）
│   ├── repository/
│   │   ├── task.go                  UpdateStatusWithHistory（Chapter 06）
│   │   └── idempotency.go           Idempotency-Key の保存と検索          ← Chapter 07
│   ├── service/task.go              ChangeStatus と通知失敗の切り離し     ← Chapter 07
│   ├── notify/
│   │   ├── notifier.go              外部 API 呼び出し・Retry・Backoff    ← Chapter 07
│   │   └── notifier_test.go         httptest.Server による Retry の検証  ← Chapter 07
│   ├── httpx/httpx.go               Timeout を 503 に分類               ← Chapter 07
│   ├── middleware/
│   │   ├── auth.go
│   │   ├── observability.go         Timeout                            ← Chapter 07
│   │   └── idempotency.go           Idempotency                        ← Chapter 07
│   ├── handler/
│   │   ├── task.go                  PATCH /tasks/{id}/status（Chapter 06）
│   │   └── debug.go                 GET /debug/slow（Timeout の検証用）  ← Chapter 07
│   └── app/app.go                   Middleware と Notifier の組み立て     ← Chapter 07
├── migrations/003_history.sql       task_history（06）+ idempotency_keys（07）
└── scripts/chapter07_check.sh
```

Chapter 05 の検証用コード（`/debug/unsafe-search`、`/debug/nplus1`、`DebugTaskQueries`）は、本文の指示どおり削除済みです。

## 動かし方

DB は `go-kanban/` の `docker compose` をそのまま使います。`003_history.sql` を適用していなければ、`go-kanban/` で次を実行します。Chapter 06 で `task_history` を作成済みでも、`IF NOT EXISTS` を付けているので再実行できます。

```bash
docker compose exec -T db psql -U kanban -d kanban -v ON_ERROR_STOP=1 < ../answers/chapter07/migrations/003_history.sql
```

```bash
cd answers/chapter07
go build ./...
go vet ./...

# Retry の Unit Test（DB 不要）
go test ./internal/notify/ -v

# 通常の起動
go run ./cmd/api

# Timeout 検証用の GET /debug/slow を有効にし、
# 通知先を「誰も待ち受けていない」ポートにして起動する
DEBUG_ROUTES=1 NOTIFY_URL=http://localhost:9999 go run ./cmd/api
```

別のターミナルで確認スクリプトを実行します。

```bash
bash scripts/chapter07_check.sh
```

| 確認する内容 | 条件 |
|---|---|
| 上限 2 秒・DB 3 秒で 503、3 秒待たずに打ち切られる | `DEBUG_ROUTES=1` で起動したとき |
| Status 変更が 400 / 200 / 409 を返す | 常に |
| 通知が失敗しても Status 変更は 200 | `NOTIFY_URL` を応答しない宛先にしたとき。サーバログに `notification retry` と `notification failed` が出る |
| Key なしの再送で 2 件、Key ありの再送で 1 件・同じ id・`Idempotent-Replay: true` | 常に |
| 400 になった Key は保存されず、同じ Key で再送できる | 常に |

## 本文の抜粋との違い

| 箇所 | このコード | 理由 |
|---|---|---|
| Middleware の順序 | `Timeout -> mux` のみ | 本文の `RequestID` と `AccessLog` は Chapter 08 で作る。この時点ではまだ存在しない |
| Timeout の判定 | `err` に加えて Request の Context も見る | Driver が `context.DeadlineExceeded` を別の error に包み直した場合でも 503 にするため |
| `/debug/slow` | `DEBUG_ROUTES=1` のときだけ登録。`seconds` は 0〜10 に制限 | 任意の時間 DB 接続を占有できる Endpoint を既定で公開しないため |
| 通知先 | `NOTIFY_URL` が空なら Notifier を作らない | 本文は通知先の設定方法を扱っていない。未設定でも起動できるようにした |
| 通知の URL と Body | `POST {NOTIFY_URL}/task-status-changed`、`task_id` / `project_id` / `old_status` / `new_status` / `version` | 本文では省略されている部分。架空の通知 API を想定している |
| `notifier_test.go` | `testConfig` / `discardLogger` / `TestRetrySucceedsAfterTransientFailure` を含む全体 | 本文は抜粋のみ |
| `003_history.sql` | `IF NOT EXISTS` 付き | Chapter 06 で適用済みの環境でも、そのまま流せるようにした |
