# Chapter 03 完成時点のコード

[Chapter 03: Validation と Error Handling](../../docs/chapter/chapter03_validation.md) を終えた時点の `go-kanban` です。

自分のコードが動かないときの答え合わせに使ってください。`errors.go` と `validate.go` は本文の Step 1・Step 2 に全文が載っています。`main.go` は本文では書き換えた部分だけを載せているので、ファイル全体はここで確認できます。

## 構成

```text
chapter03/
├── cmd/api/
│   ├── main.go              decodeJSON / pathID と、書き換えた Task の Handler
│   ├── errors.go            sentinel error、ValidationError、respondError、SQLSTATE の翻訳
│   └── validate.go          CreateTaskRequest の Normalize と Validate
├── docker-compose.yml       PostgreSQL 17（Chapter 02 と同じ）
├── migrations/001_init.sql  projects / tasks テーブル（Chapter 02 と同じ）
├── go.mod
├── go.sum
└── scripts/chapter03_check.sh
```

`createProjectHandler` はこの章では書き換えません。本文でも触れておらず、Chapter 04 で丸ごと置き換えるためです。空の `name` を送ると 201 になりますし、エラー時は `http.Error` のままです。

`errors.go` の `ErrForbidden`、`ErrUnauthorized`、`publicError`、`isUniqueViolation` などは、この章ではまだ使いません。Chapter 04 以降で使います。Go は使っていないパッケージレベルの関数を許すので、`go vet` も通ります。

## 動かし方

`answers/chapter03` で実行します。テーブルは Chapter 02 から変わりません。Chapter 02 の DB が残っていれば、`docker compose up` と Migration は飛ばしてください。

```bash
cd answers/chapter03

docker compose up -d --wait
docker compose exec -T db \
  psql -U kanban -d kanban -v ON_ERROR_STOP=1 < migrations/001_init.sql

go vet ./...
go run ./cmd/api
```

別のターミナルで確認スクリプトを実行します。

```bash
bash scripts/chapter03_check.sh
```

本文 Step 4 のケースに、いくつか確認を足しています。

| 確認内容 | 期待する Status | 照合する文字列 |
|---|---|---|
| title が空 / 空白だけ | 400 | `title is required` |
| priority が不正 | 400 | `priority must be one of: low, medium, high` |
| title が 101 文字 | 400 | `title must be 100 characters or fewer` |
| 日本語の title が 100 文字ちょうど | 201 | `len([]rune(...))` で数えていれば通る |
| フィールド名の typo | 400 | `unknown field \"titel\"` |
| 壊れた JSON | 400 | `request body is not valid JSON` |
| id が `abc` / `0` | 400 | `id must be a positive integer` |
| 存在しない Task | 404 | `resource not found`。`no rows in result set` が含まれないこと |
| 存在しない Project への Task 作成 | 404 | `resource not found`。`SQLSTATE`・`tasks_project_id_fkey` が含まれないこと |
| 存在しない Project に不正な入力 | 400 | Validation が DB より先に動くこと |
| 正常系（作成・取得・一覧） | 201 / 200 | 前後の空白が除去された `"title":"write docs"` |

スクリプトは実行のたびに新しい Project を作るので、何度実行しても結果は変わりません。

Step 5（500 のときの挙動）はスクリプトでは確認しません。本文のとおり、DB 側で必ず失敗する状況は Chapter 06 で作ります。

### ポートが使われているとき

[Chapter 02 の README](../chapter02/README.md#ポートが使われているとき) と同じです。`go-kanban/` の DB を使う場合、後の章の Migration で追加される列は NULL を許すので、この章のコードでも読み書きできます。

### Windows で日本語を送るとき

Windows の curl は、`-d '{"title":"あいう"}'` のように引数で渡した文字列を ANSI コードページ（日本語環境なら CP932）へ変換します。サーバには不正な UTF-8 が届き、Go の JSON Decoder はそれを1バイトずつ `U+FFFD` に置き換えます。文字数が増えるので、100 文字ちょうどの日本語 title でも 400 になります。

確認スクリプトは Body を標準入力から `--data-binary @-` で渡しているので、この影響を受けません。手で試すときも、ファイルに書いて `--data-binary @body.json` で送ってください。

## 本文との違い

| 箇所 | このコード | 理由 |
|---|---|---|
| `go.mod` の `go` 行 | `1.25.0` | `pgx v5.11.0` の要求による。Chapter 02 と同じ |
| `go.mod` の依存 | `pgx v5.11.0` に固定 | 本文の検証環境に合わせた。`pgconn` は `pgx/v5` に含まれるので、依存は増えない |
