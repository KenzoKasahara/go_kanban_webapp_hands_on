#!/usr/bin/env bash
# Chapter 07 の Timeout・通知失敗の切り離し・Idempotency-Key を API から確認する。
#
# 使い方（サーバを起動した状態で実行する）:
#   bash scripts/chapter07_check.sh
#
# サーバを DEBUG_ROUTES=1 で起動していれば、Timeout（GET /debug/slow）も確認する。
# NOTIFY_URL を応答しない宛先（例: http://localhost:9999）にして起動すると、
# 通知が失敗しても Status 変更が 200 になることを確認できる。
#
# 何度でも実行できるよう、メールアドレスと Idempotency-Key には実行ごとに異なる接尾辞を付ける。

set -u

BASE_URL=${BASE_URL:-http://localhost:8080}
SUFFIX=$(date +%s)
ALICE="alice-$SUFFIX@example.com"

WORK_DIR=$(mktemp -d)
trap 'rm -rf "$WORK_DIR"' EXIT
ALICE_COOKIE="$WORK_DIR/alice.txt"
HEADERS="$WORK_DIR/headers.txt"

PASSED=0
FAILED=0

# call METHOD PATH DATA [curlの追加オプション...]
# 結果を STATUS / BODY / TIME に入れ、Response Header を $HEADERS に保存する。
call() {
	local method=$1 path=$2 data=$3
	shift 3

	local out
	if [ -n "$data" ]; then
		out=$(curl -s -D "$HEADERS" -w '\n%{http_code} %{time_total}' -X "$method" "$@" \
			-H 'Content-Type: application/json' -d "$data" "$BASE_URL$path")
	else
		out=$(curl -s -D "$HEADERS" -w '\n%{http_code} %{time_total}' -X "$method" "$@" "$BASE_URL$path")
	fi

	local last=${out##*$'\n'}
	STATUS=${last%% *}
	TIME=${last##* }
	BODY=${out%$'\n'*}
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

check() {
	if [ "$STATUS" = "$2" ]; then
		pass "$1 ($STATUS)"
	else
		fail "$1 (want $2, got $STATUS)"
	fi
}

check_body() {
	if [[ "$BODY" == *"$2"* ]]; then
		pass "$1"
	else
		fail "$1 (body に \"$2\" が含まれない)"
	fi
}

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

count_tasks() {
	call GET "/projects/$PROJECT/tasks" "" -b "$ALICE_COOKIE" --get --data-urlencode "q=$1"
	echo "$BODY" | grep -o '"id":' | wc -l | tr -d ' '
}

call GET /health ""
if [ "$STATUS" != "200" ]; then
	echo "サーバに接続できません: $BASE_URL (status: $STATUS)"
	echo "go run ./cmd/api を実行してから、もう一度試してください。"
	exit 1
fi

echo "== 準備"
call POST /users "{\"email\":\"$ALICE\",\"password\":\"alice-password-1\"}"
require "alice 登録" 201

call POST /login "{\"email\":\"$ALICE\",\"password\":\"alice-password-1\"}" -c "$ALICE_COOKIE"
require "alice がログイン" 204

call POST /projects '{"name":"Resilience Board"}' -b "$ALICE_COOKIE"
require "alice が Project 作成" 201
PROJECT=$(extract_id "$BODY")

echo "== Part 1. Timeout"
call GET "/debug/slow?seconds=1" "" -b "$ALICE_COOKIE"
if [ "$STATUS" = "404" ]; then
	echo "SKIP  検証用エンドポイントが無効（DEBUG_ROUTES=1 で起動すると確認できる）"
else
	check "上限内（DB 1秒）" 200
	check_body "pg_sleep の結果が返る" '"result":1'

	call GET "/debug/slow?seconds=3" "" -b "$ALICE_COOKIE"
	check "上限超過（Server 2秒 / DB 3秒）" 503
	check_body "timeout として分類される" '"code":"timeout"'

	# 3秒待たずに打ち切られていれば、DB のクエリまでキャンセルが届いている。
	if awk -v t="$TIME" 'BEGIN { exit !(t < 2.8) }'; then
		pass "3秒待たずに打ち切られた (${TIME}s)"
	else
		fail "打ち切りまでの時間が長い (${TIME}s)"
	fi
fi

echo "== Part 2. Status 変更（通知の失敗で本処理を失敗させない）"
call POST "/projects/$PROJECT/tasks" '{"title":"notify me","priority":"high"}' -b "$ALICE_COOKIE"
require "Task 作成" 201
TASK_ID=$(extract_id "$BODY")

call PATCH "/tasks/$TASK_ID/status" '{"status":"done","version":1}' -b "$ALICE_COOKIE"
check "todo -> done は許可されない" 400

call PATCH "/tasks/$TASK_ID/status" '{"status":"doing","version":1}' -b "$ALICE_COOKIE"
check "todo -> doing（NOTIFY_URL が失敗しても 200）" 200
check_body "version が 2 になる" '"version":2'

call PATCH "/tasks/$TASK_ID/status" '{"status":"done","version":1}' -b "$ALICE_COOKIE"
check "古い version で更新" 409

echo "== Part 3. Idempotency-Key"
NO_KEY_TITLE="duplicate me $SUFFIX"
call POST "/projects/$PROJECT/tasks" "{\"title\":\"$NO_KEY_TITLE\",\"priority\":\"low\"}" -b "$ALICE_COOKIE"
check "Key なし 1回目" 201
call POST "/projects/$PROJECT/tasks" "{\"title\":\"$NO_KEY_TITLE\",\"priority\":\"low\"}" -b "$ALICE_COOKIE"
check "Key なし 2回目" 201

ROWS=$(count_tasks "$NO_KEY_TITLE")
if [ "$ROWS" = "2" ]; then
	pass "Key なしでは 2 件できる"
else
	fail "Key なしの件数 (want 2, got $ROWS)"
fi

KEY="key-$SUFFIX"
KEY_TITLE="idempotent task $SUFFIX"

call POST "/projects/$PROJECT/tasks" "{\"title\":\"$KEY_TITLE\",\"priority\":\"low\"}" \
	-b "$ALICE_COOKIE" -H "Idempotency-Key: $KEY"
check "Key あり 1回目" 201
FIRST_ID=$(extract_id "$BODY")

call POST "/projects/$PROJECT/tasks" "{\"title\":\"$KEY_TITLE\",\"priority\":\"low\"}" \
	-b "$ALICE_COOKIE" -H "Idempotency-Key: $KEY"
check "Key あり 再送" 201
SECOND_ID=$(extract_id "$BODY")

if [ "$FIRST_ID" = "$SECOND_ID" ]; then
	pass "再送で同じ id が返る ($FIRST_ID)"
else
	fail "再送で別の id が返った ($FIRST_ID != $SECOND_ID)"
fi

if grep -qi '^Idempotent-Replay: true' "$HEADERS"; then
	pass "再送に Idempotent-Replay: true が付く"
else
	fail "再送に Idempotent-Replay ヘッダが無い"
fi

ROWS=$(count_tasks "$KEY_TITLE")
if [ "$ROWS" = "1" ]; then
	pass "Key ありでは 1 件だけ"
else
	fail "Key ありの件数 (want 1, got $ROWS)"
fi

call POST "/projects/$PROJECT/tasks" "{\"title\":\"$KEY_TITLE\",\"priority\":\"low\"}" \
	-b "$ALICE_COOKIE" -H "Idempotency-Key: $KEY-other"
check "別の Key は別の処理" 201

ROWS=$(count_tasks "$KEY_TITLE")
if [ "$ROWS" = "2" ]; then
	pass "別の Key で 2 件目が作成される"
else
	fail "別の Key の件数 (want 2, got $ROWS)"
fi

# 失敗した結果は保存しない。同じ Key で直した Request を送れば成功できる。
FAIL_KEY="fail-key-$SUFFIX"
call POST "/projects/$PROJECT/tasks" '{"title":"","priority":"low"}' \
	-b "$ALICE_COOKIE" -H "Idempotency-Key: $FAIL_KEY"
check "Key あり・不正な入力" 400

call POST "/projects/$PROJECT/tasks" "{\"title\":\"retry after 400 $SUFFIX\",\"priority\":\"low\"}" \
	-b "$ALICE_COOKIE" -H "Idempotency-Key: $FAIL_KEY"
check "同じ Key で直した Request は処理される（400 は保存されない）" 201

echo
echo "passed: $PASSED, failed: $FAILED"

[ "$FAILED" -eq 0 ]
