#!/usr/bin/env bash
# Chapter 04 Step 6 の「期待結果」を順に実行し、Status Code を照合する。
#
# 使い方（go-kanban ディレクトリで、サーバを起動した状態で実行する）:
#   bash scripts/chapter04_check.sh
#
# 何度でも実行できるよう、メールアドレスには実行ごとに異なる接尾辞を付ける。
# そのため Response の id はドキュメントの表と一致しない。照合するのは Status Code だけ。

set -u

BASE_URL=${BASE_URL:-http://localhost:8980}
SUFFIX=$(date +%s)
ALICE="alice-$SUFFIX@example.com"
BOB="bob-$SUFFIX@example.com"

# Cookie ファイルは一時ディレクトリに置き、終了時に消す。
WORK_DIR=$(mktemp -d)
trap 'rm -rf "$WORK_DIR"' EXIT
ALICE_COOKIE="$WORK_DIR/cookie/alice.txt"
BOB_COOKIE="$WORK_DIR/cookie/bob.txt"
mkdir -p "$WORK_DIR/cookie"

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
}

# check 番号 操作 期待するStatus
check() {
	local no=$1 name=$2 want=$3

	if [ "$STATUS" = "$want" ]; then
		PASSED=$((PASSED + 1))
		printf 'PASS  #%-3s %s (%s)\n' "$no" "$name" "$STATUS"
	else
		FAILED=$((FAILED + 1))
		printf 'FAIL  #%-3s %s (want %s, got %s)\n' "$no" "$name" "$want" "$STATUS"
		printf '      body: %s\n' "$BODY"
	fi
}

# 後続の手順に必要な準備。失敗したら以降は意味がないので中断する。
require() {
	local name=$1 want=$2

	if [ "$STATUS" != "$want" ]; then
		printf 'ABORT %s (want %s, got %s)\n' "$name" "$want" "$STATUS"
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
	echo "go-kanban ディレクトリで go run ./cmd/api を実行してから、もう一度試してください。"
	exit 1
fi

echo "== ユーザー登録"
call POST /users "{\"email\":\"$ALICE\",\"password\":\"alice-password-1\"}"
check 1 "alice 登録" 201

call POST /users "{\"email\":\"$BOB\",\"password\":\"bob-password-123\"}"
require "bob 登録" 201

call POST /users "{\"email\":\"$ALICE\",\"password\":\"alice-password-1\"}"
check 2 "同じメールで再登録" 409

call POST /users "{\"email\":\"carol-$SUFFIX@example.com\",\"password\":\"short\"}"
check 3 "12文字未満のパスワード" 400

echo "== 認証"
call POST /projects '{"name":"No Cookie"}'
check 4 "Cookie なしで POST /projects" 401

call POST /login "{\"email\":\"$ALICE\",\"password\":\"wrong-password-1\"}"
check 5 "誤ったパスワードでログイン" 401
WRONG_PASSWORD_BODY=$BODY

call POST /login "{\"email\":\"nobody-$SUFFIX@example.com\",\"password\":\"alice-password-1\"}"
check 6 "存在しないメールでログイン" 401
UNKNOWN_EMAIL_BODY=$BODY

# CHECK: 5 と 6 が同じレスポンスであること。
if [ "$WRONG_PASSWORD_BODY" = "$UNKNOWN_EMAIL_BODY" ]; then
	PASSED=$((PASSED + 1))
	echo "PASS  5 と 6 のレスポンスが同じ"
else
	FAILED=$((FAILED + 1))
	echo "FAIL  5 と 6 のレスポンスが異なる"
	printf '      5: %s\n      6: %s\n' "$WRONG_PASSWORD_BODY" "$UNKNOWN_EMAIL_BODY"
fi

call POST /login "{\"email\":\"$ALICE\",\"password\":\"alice-password-1\"}" -c "$ALICE_COOKIE"
check 7 "alice がログイン" 200

call POST /login "{\"email\":\"$BOB\",\"password\":\"bob-password-123\"}" -c "$BOB_COOKIE"
require "bob がログイン" 200

echo "== 認可・IDOR"
call POST /projects '{"name":"Alice Board"}' -b "$ALICE_COOKIE"
require "alice が Project 作成" 201
PROJECT_ID=$(extract_id "$BODY")

call POST "/projects/$PROJECT_ID/tasks" '{"title":"secret task","priority":"high"}' -b "$ALICE_COOKIE"
check 8 "alice が Task 作成" 201
TASK_ID=$(extract_id "$BODY")

if ! [[ "$TASK_ID" =~ ^[0-9]+$ ]]; then
	echo "ABORT Task の id を取り出せませんでした: $BODY"
	exit 1
fi

call GET "/tasks/$TASK_ID" "" -b "$ALICE_COOKIE"
check 9 "alice が自分の Task 取得" 200

call GET "/tasks/$TASK_ID" "" -b "$BOB_COOKIE"
check 10 "bob が alice の Task 取得（IDOR）" 404

call POST "/projects/$PROJECT_ID/tasks" '{"title":"intruder","priority":"low"}' -b "$BOB_COOKIE"
check 11 "bob が alice の Project に Task 作成" 403

call GET "/projects/$PROJECT_ID/tasks" "" -b "$BOB_COOKIE"
check 12 "bob が alice の Project の Task 一覧" 403

echo "== Failure Test: ログアウト後に Cookie を使い回す"
call POST /logout "" -b "$ALICE_COOKIE"
check F1 "ログアウト" 204

call GET "/tasks/$TASK_ID" "" -b "$ALICE_COOKIE"
check F2 "ログアウト後の Task 取得" 401

echo
echo "passed: $PASSED, failed: $FAILED"

[ "$FAILED" -eq 0 ]
