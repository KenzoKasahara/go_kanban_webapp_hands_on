# Chapter 04 完成時点のコード

[Chapter 04: 認証・認可・IDOR](../../docs/chapter/chapter04_auth.md) を終えた時点の `go-kanban` です。

自分のコードが動かないときの答え合わせに使ってください。`auth.go`、`authz.go`、`002_auth.sql`、確認スクリプトは本文に全文が載っています。`main.go` は本文では書き換えた Handler とルーティングだけを載せているので、ファイル全体はここで確認できます。

## 構成

```text
chapter04/
├── cmd/api/
│   ├── main.go              Handler とルーティング（Step 5 で認証・認可を組み込んだもの）
│   ├── auth.go              ユーザー登録、ログイン、ログアウト、requireAuth、currentUser
│   ├── authz.go             Role の定義、projectRole、findTaskForUser
│   ├── errors.go            Chapter 03 と同じ
│   └── validate.go          Chapter 03 と同じ
├── docker-compose.yml       PostgreSQL 17（Chapter 02 と同じ）
├── migrations/
│   ├── 001_init.sql         projects / tasks テーブル（Chapter 02 と同じ）
│   └── 002_auth.sql         users / sessions / project_members と tasks.assignee_id
├── go.mod
├── go.sum
└── scripts/chapter04_check.sh
```

Chapter 03 から `main.go` で変わったのは次の Handler です。

| 対象 | 変更 |
|---|---|
| `createProjectHandler` | 丸ごと置き換え。Transaction で Project 作成と Owner 登録を同時に行う |
| `addMemberHandler` | 新規追加。Owner だけが Member を追加できる |
| `createTaskHandler` | 先頭に認証と認可（`canWriteTask`）を追加 |
| `listTasksHandler` | 先頭に認証とメンバー確認を追加 |
| `getTaskHandler` | 丸ごと置き換え。`findTaskForUser` で取得と認可を1クエリにまとめる |
| `main()` | ルーティングを認証不要・認証必須に分けた |

`errors`・`pgx` の import は `main.go` から消えています。`pgx.ErrNoRows` を見る処理が `getTaskHandler` から `authz.go` の `findTaskForUser` へ移ったためです。

`createProjectHandler` は本文どおり `name` の Validation をしていません。空の `name` を送っても 201 になります。

## 動かし方

`answers/chapter04` で実行します。Chapter 03 の DB が残っていれば、`001_init.sql` は飛ばして `002_auth.sql` だけ流してください。

```bash
cd answers/chapter04

docker compose up -d --wait
docker compose exec -T db \
  psql -U kanban -d kanban -v ON_ERROR_STOP=1 < migrations/001_init.sql
docker compose exec -T db \
  psql -U kanban -d kanban -v ON_ERROR_STOP=1 < migrations/002_auth.sql

go vet ./...
go run ./cmd/api
```

別のターミナルで確認スクリプトを実行します。

```bash
bash scripts/chapter04_check.sh
```

本文 Step 6 の #1〜#12 と、ログアウト後の Failure Test を確認します。すべて通れば最後に `failed: 0` と表示されます。

### スクリプトで確認しないもの

`POST /projects/{id}/members` と Viewer の権限は、本文の手順にもスクリプトにも含まれていません。手で試すと次の結果になります。

| 操作 | Status | Response |
|---|---|---|
| Owner が未定義の role（`admin`）で追加 | 400 | `role must be one of owner, member, viewer` |
| Owner が Viewer を追加 | 204 | Body なし |
| 同じ User をもう一度追加 | 409 | `user is already a member` |
| 存在しない `user_id` を追加 | 404 | `user not found` |
| Viewer が Member を追加 | 403 | `operation not allowed` |
| Viewer が Task 一覧を取得 | 200 | Task の配列 |
| Viewer が Task を作成 | 403 | `operation not allowed` |

### ポートが使われているとき

[Chapter 02 の README](../chapter02/README.md#ポートが使われているとき) と同じです。

## 本文との違い

| 箇所 | このコード | 理由 |
|---|---|---|
| `go.mod` の `go` 行 | `1.26.0` | `go get golang.org/x/crypto/bcrypt` で `golang.org/x/crypto v0.57.0` が入り、その要求で上がる |
| `go.mod` の依存 | `pgx v5.11.0`、`golang.org/x/crypto v0.57.0` に固定 | 本文の検証環境（Chapter 05 の答え）に合わせた |
