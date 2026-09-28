# Chapter 02 完成時点のコード

[Chapter 02: 雑な CRUD を作る](../../docs/chapter/chapter02_crud.md) を終えた時点の `go-kanban` です。

自分のコードが動かないときの答え合わせに使ってください。この章のコードは本文の Step 1・Step 2・Step 4 に全文が載っています。違いが出るとすれば `go.mod` と `go.sum` です。

このコードは意図的に雑に作っています。Validation がない、DB のエラー文をそのまま返す、といった問題は Chapter 03 以降で直すので、ここでは直さないでください。

## 構成

```text
chapter02/
├── cmd/api/main.go          Handler から直接 SQL を呼ぶ CRUD
├── docker-compose.yml       PostgreSQL 17
├── migrations/001_init.sql  projects / tasks テーブル
├── go.mod
├── go.sum
└── scripts/chapter02_check.sh
```

## 動かし方

`answers/chapter02` で実行します。

```bash
cd answers/chapter02

docker compose up -d --wait
docker compose exec -T db \
  psql -U kanban -d kanban -v ON_ERROR_STOP=1 < migrations/001_init.sql

go vet ./...
go run ./cmd/api
```

別のターミナルで確認スクリプトを実行します。

```bash
bash scripts/chapter02_check.sh
```

次の8つを確認します。

| 確認内容 | 期待する Status | 照合する文字列 |
|---|---|---|
| Project を作る | 201 | `"name":"Kanban Hands-on"` |
| Task を作る | 201 | `"status":"todo"`、`"version":1` など |
| 作った Task を取得する | 200 | `"title":"write docs"` |
| Failure Test 1: 空の title と定義外の priority | 201 | `"title":""`、`"priority":"SUPER_HIGH"` |
| Failure Test 1: 不正データが一覧に出る | 200 | `"priority":"SUPER_HIGH"` |
| Failure Test 2: 存在しない Task | 500 | `no rows in result set` |
| Failure Test 3: 存在しない Project に Task を作る | 500 | `tasks_project_id_fkey`、`SQLSTATE 23503` |
| Failure Test 4: `titel` と typo する | 201 | `"title":""` |

Failure Test がすべて PASS する状態が、この章の完成です。スクリプトは実行のたびに新しい Project を作るので、何度実行しても結果は変わりません。

### ポートが使われているとき

5432 番ポートを `go-kanban/` の DB が使っていると、`docker compose up` がポートの競合で失敗します。どちらかを選んでください。

| 方法 | 手順 |
|---|---|
| `go-kanban/` の DB を止める | `go-kanban/` で `docker compose stop` を実行してから上の手順を行う。データは volume に残る |
| `go-kanban/` の DB をそのまま使う | `docker compose up` と Migration を飛ばして `go run ./cmd/api` だけ実行する。後の章の Migration で `tasks` に追加される列は NULL を許すので、この章のコードでも読み書きできる |

後者の場合、既存のデータがあるので本文の出力例とは `id` の値が変わります。

8080 番ポートを別のサーバが使っていると、`server started on :8080` のあとに `listen tcp :8080: bind: ...` で終了します。後の章の `go-kanban` を起動したままにしていないか確認してください。

## 本文との違い

| 箇所 | このコード | 理由 |
|---|---|---|
| `go.mod` の `go` 行 | `1.25.0` | `pgx v5.11.0` の要求による。`go get` が自動で書き換える |
| `go.mod` の依存 | `pgx v5.11.0` に固定 | 本文の検証環境に合わせた。`go get` を実行した時期によっては新しいバージョンが入るが、この章の範囲では動作は変わらない |
