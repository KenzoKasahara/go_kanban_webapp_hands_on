#!/usr/bin/env bash
# Chapter 05 の層分割後も Chapter 04 までの API が同じように動くかを確認する。
# サーバを DEBUG_ROUTES=1 で起動していれば、Part 2（SQL Injection）と
# Part 3（N+1）の検証用エンドポイントも確認する。
#
# 使い方（サーバを起動した状態で実行する）:
#   bash scripts/chapter05_check.sh
#
# 何度でも実行できるよう、メールアドレスには実行ごとに異なる接尾辞を付ける。
# 照合するのは Status Code と、Response に特定の文字列が含まれるかだけ。

set -u

BASE_URL=${BASE_URL:-http://localhost:8980}
SUFFIX=$(date +%s)
ALICE="alice-$SUFFIX@example.com"
BOB="bob-$SUFFIX@example.com"
BOB_SECRET="bob salary negotiation $SUFFIX"

# Cookie ファイルは一時ディレクトリに置き、終了時に消す。
WORK_DIR=$(mktemp -d)
trap 'rm -rf "$WORK_DIR"' EXIT
ALICE_COOKIE="$WORK_DIR/alice.txt"
BOB_COOKIE="$WORK_DIR/bob.txt"

PASSED=0
FAILED=0

# call METHOD PATH DATA [curlの追加オプション...]
# 結果を STATUS と BODY に入れる。DATA が空なら Body を送らない。
call() {
	local method=$1 path=$2 data=$3
	shift 3

	local out
	if [ -n "$data" ]; then
		out=$(curl -s -w '\n%{http_code}' -X "$method" "$@" \
			-H 'Content-Type: application/json' -d "$data" "$BASE_URL$path")
	else
		out=$(curl -s -w '\n%{http_code}' -X "$method" "$@" "$BASE_URL$path")
	fi

	STATUS=${out##*$'\n'}
	BODY=${out%$'\n'*}
	# json.Encoder は末尾に改行を付ける。比較しやすいよう取り除く。
	BODY=${BODY%$'\n'}
}

pass() {
	PASSED=$((PASSED + 1))
	printf 'PASS  %s\n' "$1"
}

fail() {
	FAILED=$((FAILED + 1))
	printf 'FAIL  %s\n' "$1"
	printf '      body: %s\n' "$BODY"
}

# check 名前 期待するStatus
check() {
	if [ "$STATUS" = "$2" ]; then
		pass "$1 ($STATUS)"
	else
		fail "$1 (want $2, got $STATUS)"
	fi
}

# check_body 名前 含まれるべき文字列
check_body() {
	if [[ "$BODY" == *"$2"* ]]; then
		pass "$1"
	else
		fail "$1 (body に \"$2\" が含まれない)"
	fi
}

# 後続の手順に必要な準備。失敗したら以降は意味がないので中断する。
require() {
	if [ "$STATUS" != "$2" ]; then
		printf 'ABORT %s (want %s, got %s)\n' "$1" "$2" "$STATUS"
		printf '      body: %s\n' "$BODY"
		exit 1
	fi
}

extract_id() {
	echo "$1" | sed -E 's/^\{"id":([0-9]+).*/\1/'
}

call GET /health ""
if [ "$STATUS" != "200" ]; then
	echo "サーバに接続できません: $BASE_URL (status: $STATUS)"
	echo "go run ./cmd/api を実行してから、もう一度試してください。"
	exit 1
fi

echo "== Chapter 04 までの API（回帰確認）"
call POST /users "{\"email\":\"$ALICE\",\"password\":\"alice-password-1\"}"
check "alice 登録" 201

call POST /users "{\"email\":\"$BOB\",\"password\":\"bob-password-123\"}"
require "bob 登録" 201

call POST /users "{\"email\":\"$ALICE\",\"password\":\"alice-password-1\"}"
check "同じメールで再登録" 409

call POST /users "{\"email\":\"carol-$SUFFIX@example.com\",\"password\":\"short\"}"
check "12文字未満のパスワード" 400

call POST /projects '{"name":"No Cookie"}'
check "Cookie なしで POST /projects" 401

call POST /login "{\"email\":\"$ALICE\",\"password\":\"wrong-password-1\"}"
check "誤ったパスワードでログイン" 401
WRONG_PASSWORD_BODY=$BODY

call POST /login "{\"email\":\"nobody-$SUFFIX@example.com\",\"password\":\"alice-password-1\"}"
check "存在しないメールでログイン" 401

if [ "$WRONG_PASSWORD_BODY" = "$BODY" ]; then
	pass "誤ったパスワードと存在しないメールのレスポンスが同じ"
else
	fail "誤ったパスワードと存在しないメールのレスポンスが異なる"
fi

# Chapter 05 で 200 から 204 へ変わる。
call POST /login "{\"email\":\"$ALICE\",\"password\":\"alice-password-1\"}" -c "$ALICE_COOKIE"
check "alice がログイン" 204

call POST /login "{\"email\":\"$BOB\",\"password\":\"bob-password-123\"}" -c "$BOB_COOKIE"
require "bob がログイン" 204

call POST /projects '{"name":"Alice Board"}' -b "$ALICE_COOKIE"
require "alice が Project 作成" 201
ALICE_PROJECT=$(extract_id "$BODY")

call POST "/projects/$ALICE_PROJECT/tasks" '{"title":"write docs","priority":"high"}' -b "$ALICE_COOKIE"
check "alice が Task 作成" 201
TASK_ID=$(extract_id "$BODY")

if ! [[ "$TASK_ID" =~ ^[0-9]+$ ]]; then
	echo "ABORT Task の id を取り出せませんでした: $BODY"
	exit 1
fi

call POST "/projects/$ALICE_PROJECT/tasks" '{"title":"   ","priority":"high"}' -b "$ALICE_COOKIE"
check "空白だけの title（Service の Validation）" 400

call GET "/tasks/$TASK_ID" "" -b "$ALICE_COOKIE"
check "alice が自分の Task 取得" 200

call GET "/tasks/$TASK_ID" "" -b "$BOB_COOKIE"
check "bob が alice の Task 取得（IDOR）" 404

call POST "/projects/$ALICE_PROJECT/tasks" '{"title":"intruder","priority":"low"}' -b "$BOB_COOKIE"
check "bob が alice の Project に Task 作成" 403

call GET "/projects/$ALICE_PROJECT/tasks" "" -b "$BOB_COOKIE"
check "bob が alice の Project の Task 一覧" 403

echo "== Part 2. 検索（プレースホルダ版）"
call POST /projects '{"name":"Bob Private"}' -b "$BOB_COOKIE"
require "bob が Project 作成" 201
BOB_PROJECT=$(extract_id "$BODY")

call POST "/projects/$BOB_PROJECT/tasks" "{\"title\":\"$BOB_SECRET\",\"priority\":\"high\"}" -b "$BOB_COOKIE"
require "bob が非公開 Task 作成" 201

call GET "/projects/$ALICE_PROJECT/tasks" "" -b "$ALICE_COOKIE" --get --data-urlencode "q=docs"
check "alice が q=docs で検索" 200
check_body "検索結果に write docs が含まれる" "write docs"

call GET "/projects/$ALICE_PROJECT/tasks" "" -b "$ALICE_COOKIE" --get --data-urlencode "q=%' OR project_id > 0 --"
check "攻撃文字列を安全な実装へ" 200

if [ "$BODY" = "[]" ]; then
	pass "安全な実装は 0 件を返す"
else
	fail "安全な実装が 0 件を返さない"
fi

call GET "/debug/unsafe-search/$ALICE_PROJECT" "" -b "$ALICE_COOKIE" --get --data-urlencode "q=docs"
if [ "$STATUS" = "404" ]; then
	echo "SKIP  検証用エンドポイントが無効（DEBUG_ROUTES=1 で起動すると確認できる）"
else
	echo "== Part 2. SQL Injection（文字列連結版）"
	check "通常のキーワードは危険な実装でも正常に見える" 200
	check_body "危険な実装でも write docs が返る" "write docs"

	call GET "/debug/unsafe-search/$ALICE_PROJECT" "" -b "$ALICE_COOKIE" --get --data-urlencode "q=%' OR project_id > 0 --"
	check "攻撃文字列を危険な実装へ" 200
	check_body "alice が bob の非公開 Task を読み出せてしまう" "$BOB_SECRET"

	echo "== Part 3. N+1"
	call GET "/debug/nplus1/$ALICE_PROJECT" "" -b "$ALICE_COOKIE"
	check "N+1 比較エンドポイント" 200
	check_body "JOIN 版は 1 Query" '"join_queries":1'
	check_body "両実装が同じ結果を返す" '"same_rows":true'

	call GET "/debug/nplus1/$ALICE_PROJECT" "" -b "$BOB_COOKIE"
	check "bob は alice の Project を計測できない" 403
fi

echo "== Failure Test: ログアウト後に Cookie を使い回す"
call POST /logout "" -b "$ALICE_COOKIE"
check "ログアウト" 204

call GET "/tasks/$TASK_ID" "" -b "$ALICE_COOKIE"
check "ログアウト後の Task 取得" 401

echo
echo "passed: $PASSED, failed: $FAILED"

[ "$FAILED" -eq 0 ]
