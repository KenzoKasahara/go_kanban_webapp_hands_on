# Chapter 09 完成時点のコード

[Chapter 09: Test と Refactoring](../../docs/chapter/chapter09_test.md) を終えた時点の `go-kanban`、つまりハンズオン全体の完成形です。本文では抜粋しか載せていないファイルの全体を確認できます。

[Chapter 07 の答え](../chapter07/)に、Chapter 08 の変更（JSON ログ・Request ID・アクセスログ）と Chapter 09 のテストを加えています。

## 構成

```text
chapter09/
├── cmd/api/main.go                  JSON ログで起動                      ← Chapter 08
├── internal/
│   ├── model/
│   │   └── task_test.go             状態遷移・境界値の Unit Test          ← Chapter 09
│   ├── service/
│   │   └── task_test.go             Fake Repository による Service Test  ← Chapter 09
│   ├── notify/notifier_test.go      Retry の Test（Chapter 07）
│   ├── httpx/httpx.go               RequestID と LogFields の受け渡し    ← Chapter 08
│   ├── middleware/
│   │   ├── observability.go         RequestID / AccessLog / Timeout     ← Chapter 08
│   │   └── auth.go                  LogFields へ user_id を書き込む      ← Chapter 08
│   ├── app/app.go                   RequestID -> AccessLog -> Timeout   ← Chapter 08
│   └── （repository / handler は Chapter 07 から変更なし）
├── test/
│   ├── integration_test.go          Test Server・HTTP Client・DB 検証の補助 ← Chapter 09
│   └── scenario_test.go             4 つのシナリオ                         ← Chapter 09
├── migrations/003_history.sql
└── scripts/
    ├── setup_test_db.sh             Integration Test 用 DB の作成          ← Chapter 09
    └── load-test.js                 k6 の負荷テスト                        ← Chapter 09
```

## 動かし方

### Unit / Service / Notify（DB 不要）

```bash
cd answers/chapter09
go test ./...
go test -race ./...
```

`-race` には cgo と C コンパイラが必要です（[Chapter 06](../../docs/chapter/chapter06_transaction.md) の NOTE を参照）。

### Integration（実 DB が必要）

Integration Test は、各 Test の最初に全テーブルを `TRUNCATE` します。開発用の `kanban` DB を消さないよう、同じコンテナに `kanban_test` DB を作って使います。

```bash
# 初回だけ。go-kanban の docker compose が起動している状態で実行する
bash scripts/setup_test_db.sh

go test -tags=integration ./test/... -v
```

別の DB を使うときは `TEST_DATABASE_URL` で接続先を指定します。

### Load Test（k6）

```bash
# 別ターミナルで API を起動（ログを server.log に残す）
go run ./cmd/api > server.log 2>&1

docker run --rm -i \
  -e BASE_URL=http://host.docker.internal:8080 \
  grafana/k6:2.3.0 run - < scripts/load-test.js
```

負荷テストは開発用の `kanban` DB に Task を大量に作ります。後片付けは本文の NOTE を参照してください。

## 検証結果

| 実行したもの | 結果 |
|---|---|
| `go test ./...` | model / notify / service すべて PASS |
| `go test -race ./...` | すべて PASS |
| `go vet -tags=integration ./...` | 指摘なし |
| `go test -tags=integration ./test/...` | 4 シナリオすべて PASS（0.99 秒） |
| JSON アクセスログ | `request_id`・`user_id` が出る。`X-Request-Id: my-trace-123` を引き継ぐ。503 は ERROR |
| ログの機密情報 | `grep -icE 'kanban_session\|password\|set-cookie'` が 0 |
| k6 | 未実行（Docker イメージの取得が必要なため） |

## 本文の抜粋との違い

| 箇所 | このコード | 理由 |
|---|---|---|
| Integration Test の接続先 | 既定は `kanban_test` DB | 本文の既定は開発用の `kanban` DB。毎回 `TRUNCATE` するので、手元で作ったデータが消えないようにした |
| `TestAuthorizationScenario` の bob の id | 登録時の Response から取得 | 本文は `user_id: 2` と固定。`RESTART IDENTITY` に頼らずに済む |
| `TestAuthorizationScenario` | Viewer の Status 変更、Owner 以外の Member 追加、存在しない Task の 404 を追加 | 本文の抜粋に、同じシナリオで確認できる項目を足した |
| `TestChangeStatus` | 不正な Status、古い version（409）のケースを追加 | Chapter 06 で見つけた「version チェックを遷移チェックより先に行う」順序を固定するため |
| `TestChangeStatusDoesNotNotifyWhenUpdateFails` | 追加 | 通知は Commit に成功したときだけ送ることを固定するため |
| `TestIdempotentTaskCreation` | 他人が同じ Key を使っても alice の Response が返らないことを追加 | 主キーに `user_id` を含めた理由（Chapter 07）を固定するため |
| `TestValidationThroughHTTP` | 本文に中身がないため、この内容で作成 | 400 と `invalid_request` が返り、DB に書かれないことを確認する |
| `unexpected error` / `request timed out` のログ | `request_id` を付けて出す | Chapter 08 の「障害調査の流れ」で、Request ID からエラーの詳細まで辿れるようにするため |
