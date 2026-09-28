# Chapter 01 完成時点のコード

[Chapter 01: Go と HTTP の基礎](../../docs/chapter/chapter01_http.md) を終えた時点の `go-kanban` です。

自分のコードが動かないときの答え合わせに使ってください。この章のコードは本文の Step 2 に全文が載っているので、違いが出るとすれば `go.mod` くらいです。

## 構成

```text
chapter01/
├── cmd/api/main.go          /health を返す最小の HTTP サーバ
├── go.mod
└── scripts/chapter01_check.sh
```

## 動かし方

DB は使いません。

```bash
cd answers/chapter01
go fmt ./...
go vet ./...

go run ./cmd/api
```

別のターミナルで確認スクリプトを実行します。

```bash
bash scripts/chapter01_check.sh
```

次の3つを確認します。

| 確認内容 | 期待する Status | 照合する文字列 |
|---|---|---|
| `GET /health` | 200 | `Content-Type: application/json`、`{"status":"ok"}` |
| `GET /not-found` | 404 | `404 page not found` |
| `POST /health` | 405 | `Allow: GET, HEAD`、`Method Not Allowed` |

8080 番ポートを別のサーバ（後の章の `go-kanban` など）が使っていると、`server started on :8080` のあとに `listen tcp :8080: bind: ...` というエラーで終了します（`bind:` 以降の文言は OS によって異なります）。この状態でスクリプトを実行すると、別のサーバの応答を確認してしまうので注意してください。

## 本文との違い

| 箇所 | このコード | 理由 |
|---|---|---|
| `go.mod` の `go` 行 | `1.22` | `"GET /health"` のようにメソッドを含むパターンは Go 1.22 以降で使える。`go mod init` は手元の Go のバージョンを書き込むので、自分の `go.mod` と数字が違っていても問題ない |
