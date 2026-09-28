# Chapter 05 完成時点のコード

[Chapter 05: 層分割・SQL Injection・N+1](../../docs/chapter/chapter05_repository.md) を終えた時点の `go-kanban` です。本文では抜粋しか載せていないファイルの全体を確認できます。

自分のコードが動かないときの答え合わせに使ってください。最初からここを写すと、import cycle に自分で遭遇する機会（Step 5）を失います。

## 構成

```text
chapter05/
├── cmd/api/main.go          起動処理だけ
├── internal/
│   ├── model/               型と業務ルール（errors / task / member / user）
│   ├── repository/          SQL の実行（task / project / user / pgerror）
│   ├── service/             業務ルールの判断（task / project / auth / debug）
│   ├── httpx/               handler と middleware の共通処理
│   ├── middleware/          RequireAuth
│   ├── handler/             HTTP と Go 値の変換（health / auth / project / task / debug）
│   └── app/                 依存関係の組み立てとルーティング
└── scripts/chapter05_check.sh
```

`debug` が付くファイルは Part 2・Part 3 の検証専用です。検証が終わったら削除します。

## 動かし方

DB は `go-kanban/` の `docker compose` をそのまま使います。テーブルは Chapter 04 から変わりません。

```bash
cd answers/chapter05
go build ./...
go vet ./...

# 通常の起動
go run ./cmd/api

# Part 2・Part 3 の検証用エンドポイントも有効にする
DEBUG_ROUTES=1 go run ./cmd/api
```

別のターミナルで確認スクリプトを実行します。

```bash
bash scripts/chapter05_check.sh
```

Chapter 04 までの API が同じ Status を返すこと、`POST /login` が 204 になったことを確認します。`DEBUG_ROUTES=1` で起動していれば、SQL Injection と N+1 の検証も行います。

## 本文の抜粋との違い

| 箇所 | このコード | 理由 |
|---|---|---|
| モジュールパス | `example.com/go-kanban` | Chapter 01 の `go mod init` に合わせた。自分のコードに移すときは import パスを読み替える |
| `go.mod` の `go` 行 | `1.26.0` | `golang.org/x/crypto` の要求による |
| 検証用エンドポイント | `DEBUG_ROUTES=1` のときだけ登録 | 本文では「一時的に登録して削除する」。消し忘れても既定では公開されないようにした |
