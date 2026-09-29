# Chapter 08 完成時点のコード

[Chapter 08: Logging と Audit Log](../../docs/chapter/chapter08_observability.md) を終えた時点の `go-kanban` です。本文では抜粋しか載せていないファイルの全体を確認できます。Chapter 06・07 の変更（Status 変更と履歴、Timeout、Retry、Idempotency-Key）も含みます。

自分のコードが動かないときの答え合わせに使ってください。Step 5 の「`user_id` がログに出ない」問題は、一度自分で踏んでから `LogFields` を読むほうが理由に納得しやすいはずです。

## 構成

```text
chapter08/
├── cmd/api/main.go                     slog を JSON Handler に変更          ← Chapter 08
├── internal/
│   ├── httpx/httpx.go                  Request ID と LogFields の Context    ← Chapter 08
│   ├── middleware/
│   │   ├── observability.go            RequestID / AccessLog / Timeout     ← Chapter 08
│   │   ├── observability_test.go       Middleware の Unit Test（DB 不要）   ← Chapter 08
│   │   ├── auth.go                     LogFields へ user_id を書き込む       ← Chapter 08
│   │   └── idempotency.go
│   ├── app/app.go                      Middleware の順序を完成させる          ← Chapter 08
│   └── ...                             その他は Chapter 07 と同じ
├── migrations/
│   ├── 003_history.sql                 task_history（Chapter 06）
│   └── 004_idempotency.sql             idempotency_keys（Chapter 07）
└── scripts/chapter08_check.sh
```

Middleware は外側から次の順に適用しています。

```text
RequestID -> AccessLog -> Timeout -> mux -> RequireAuth -> (Idempotency) -> Handler
```

## 動かし方

DB は `go-kanban/` の `docker compose` をそのまま使います。この章で追加する migration はありません。Chapter 07 までの `003_history.sql` と `004_idempotency.sql` を適用済みであれば、そのまま動きます。

```bash
cd answers/chapter08
go build ./...
go vet ./...

# Middleware と Retry の Unit Test（DB 不要）
go test ./internal/middleware/ ./internal/notify/ -v

# ログをファイルに残して起動する。
# DEBUG_ROUTES=1 を付けると、503 が ERROR で記録されることも確認できる。
DEBUG_ROUTES=1 go run ./cmd/api > server.log 2>&1
```

別のターミナルで確認スクリプトを実行します。

```bash
cd answers/chapter08
LOG_FILE=server.log COMPOSE_DIR=../../go-kanban bash scripts/chapter08_check.sh
```

`LOG_FILE` と `COMPOSE_DIR` は省略できます。省略した場合、ログの確認と `task_history` の確認は SKIP になります。

| 確認する内容 | 条件 |
|---|---|
| `X-Request-Id` が自動生成され、Response ヘッダに返る | 常に |
| Request ヘッダの `X-Request-Id` を引き継ぐ。形式が不正なら新しく発行する | 常に |
| Response の ID でアクセスログを1行検索でき、`user_id` と `status` が載っている | `LOG_FILE` を指定したとき |
| `/health` のログには `user_id` が付かない | `LOG_FILE` を指定したとき |
| 503 が ERROR レベルで記録される | `LOG_FILE` を指定し、`DEBUG_ROUTES=1` で起動したとき |
| `kanban_session` / `password` / `set-cookie`、実際の Session ID とパスワードがログに無い | `LOG_FILE` を指定したとき |
| `task_history` に誰が・何を・何から何へ変えたかが2行残る | `COMPOSE_DIR` を指定したとき |

## 本文の抜粋との違い

| 箇所 | このコード | 理由 |
|---|---|---|
| `X-Request-Id` の引き継ぎ | 英数字と `-` `_` `.` のみ、128 文字以下なら使う。それ以外は新しく発行する | 外部から来た値をそのままログへ書くため。巨大な値や改行入りの値でログを汚させない |
| `unexpected error` / `request timed out` のログ | `request_id` を付ける | 本文の「障害調査の流れ」で、同じ ID からエラーの詳細まで辿れるようにするため。`slog.ErrorContext` は Context の値を自動では出力しない |
| `Timeout` | `observability.go` に同居させたまま | Chapter 07 で作ったものをそのまま使う。別 goroutine で Handler を動かす `http.TimeoutHandler` にすると、`LogFields` への書き込みが data race になる点をコメントに残した |
| `observability_test.go` | 本文には無い | `statusRecorder` の既定値 200、5xx の ERROR、`LogFields` 経由の `user_id`、機密情報の非出力を DB なしで確認できるようにした |
| 確認スクリプトの `grep` | 本文と同じパターンに加え、実際の Session ID とパスワードの値でも検索する | Cookie 名が出ていなくても、値だけが漏れるケースを検出するため |
