#!/usr/bin/env bash
# Chapter 08 の Request ID・アクセスログ・機密情報の非出力・監査ログを API から確認する。
#
# 使い方（サーバをログファイル付きで起動した状態で実行する）:
#   go run ./cmd/api > server.log 2>&1
#   LOG_FILE=server.log bash scripts/chapter08_check.sh
#
# LOG_FILE を指定しなければ、Response ヘッダの確認だけを行う。
# DEBUG_ROUTES=1 で起動していれば、503 が ERROR レベルで記録されることも確認する。
# docker compose が使える場所（go-kanban/）を COMPOSE_DIR に指定すると、task_history も確認する。
#
# 何度でも実行できるよう、メールアドレスには実行ごとに異なる接尾辞を付ける。

set -u

BASE_URL=${BASE_URL:-http://localhost:8080}
LOG_FILE=${LOG_FILE:-}
COMPOSE_DIR=${COMPOSE_DIR:-}
SUFFIX=$(date +%s)
ALICE="alice-$SUFFIX@example.com"
ALICE_PASSWORD="alice-password-$SUFFIX"

WORK_DIR=$(mktemp -d)
trap 'rm -rf "$WORK_DIR"' EXIT
ALICE_COOKIE="$WORK_DIR/alice.txt"
HEADERS="$WORK_DIR/headers.txt"

PASSED=0
FAILED=0

# call METHOD PATH DATA [curlの追加オプション...]
# 結果を STATUS / BODY に入れ、Response Header を $HEADERS に保存する。
call() {
	local method=$1 path=$2 data=$3
	shift 3

	local out
	if [ -n "$data" ]; then
		out=$(curl -s -D "$HEADERS" -w '\n%{http_code}' -X "$method" "$@" \
			-H 'Content-Type: application/json' -d "$data" "$BASE_URL$path")
	else
		out=$(curl -s -D "$HEADERS" -w '\n%{http_code}' -X "$method" "$@" "$BASE_URL$path")
	fi

	STATUS=${out##*$'\n'}
	BODY=${out%$'\n'*}
}

# 直前の Response の X-Request-Id を返す。
response_request_id() {
	grep -i '^X-Request-Id:' "$HEADERS" | head -n 1 | sed -E 's/^[^:]+:[[:space:]]*//' | tr -d '\r'
}

pass() {
	PASSED=$((PASSED + 1))
	printf 'PASS  %s\n' "$1"
}

fail() {
	FAILED=$((FAILED + 1))
	printf 'FAIL  %s\n' "$1"
}

check() {
	if [ "$STATUS" = "$2" ]; then
		pass "$1 ($STATUS)"
	else
		fail "$1 (want $2, got $STATUS)"
		printf '      body: %s\n' "$BODY"
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

# log_line REQUEST_ID
# 指定した request_id のアクセスログを1行返す。ログは Request 完了時に書かれるため少し待つ。
log_line() {
	local i
	for i in 1 2 3 4 5; do
		if grep -F "\"request_id\":\"$1\"" "$LOG_FILE" | grep -F '"msg":"http_request"' | head -n 1 | grep -q .; then
			grep -F "\"request_id\":\"$1\"" "$LOG_FILE" | grep -F '"msg":"http_request"' | head -n 1
			return
		fi
		sleep 0.2
	done
}

# check_log NAME LINE EXPECTED_FRAGMENT
check_log() {
	if [[ "$2" == *"$3"* ]]; then
		pass "$1"
	else
		fail "$1 (ログに $3 が含まれない)"
		printf '      log: %s\n' "$2"
	fi
}

call GET /health ""
if [ "$STATUS" != "200" ]; then
	echo "サーバに接続できません: $BASE_URL (status: $STATUS)"
	echo "go run ./cmd/api > server.log 2>&1 を実行してから、もう一度試してください。"
	exit 1
fi
HEALTH_ID=$(response_request_id)

echo "== 準備"
call POST /users "{\"email\":\"$ALICE\",\"password\":\"$ALICE_PASSWORD\"}"
require "alice 登録" 201

call POST /login "{\"email\":\"$ALICE\",\"password\":\"$ALICE_PASSWORD\"}" -c "$ALICE_COOKIE"
require "alice がログイン" 204
SESSION_ID=$(awk '$6 == "kanban_session" { print $7 }' "$ALICE_COOKIE" | tail -n 1)

call POST /projects '{"name":"Observability Board"}' -b "$ALICE_COOKIE"
require "alice が Project 作成" 201
PROJECT=$(extract_id "$BODY")

call POST "/projects/$PROJECT/tasks" '{"title":"trace me","priority":"high"}' -b "$ALICE_COOKIE"
require "Task 作成" 201
TASK_ID=$(extract_id "$BODY")

echo "== Part 1. Request ID"
call GET "/tasks/$TASK_ID" "" -b "$ALICE_COOKIE"
check "Task 取得" 200
TASK_REQUEST_ID=$(response_request_id)
if [[ "$TASK_REQUEST_ID" =~ ^[0-9a-f]{16}$ ]]; then
	pass "X-Request-Id が自動生成される ($TASK_REQUEST_ID)"
else
	fail "X-Request-Id が 16 桁の hex ではない ($TASK_REQUEST_ID)"
fi

TRACE_ID="my-trace-$SUFFIX"
call GET "/tasks/$TASK_ID" "" -b "$ALICE_COOKIE" -H "X-Request-Id: $TRACE_ID"
if [ "$(response_request_id)" = "$TRACE_ID" ]; then
	pass "Request ヘッダの ID を引き継ぐ ($TRACE_ID)"
else
	fail "Request ヘッダの ID が引き継がれない (got $(response_request_id))"
fi

call GET /health "" -H 'X-Request-Id: bad id with spaces'
BAD_ID=$(response_request_id)
if [ -n "$BAD_ID" ] && [ "$BAD_ID" != "bad id with spaces" ]; then
	pass "不正な形式の ID は新しい ID に置き換える ($BAD_ID)"
else
	fail "不正な形式の ID がそのまま使われた ($BAD_ID)"
fi

SLOW_ID=""
call GET "/debug/slow?seconds=3" "" -b "$ALICE_COOKIE"
if [ "$STATUS" = "404" ]; then
	echo "SKIP  検証用エンドポイントが無効（DEBUG_ROUTES=1 で起動すると 503 の記録も確認できる）"
else
	check "Timeout" 503
	SLOW_ID=$(response_request_id)
fi

# Status 変更を2回行い、task_history に2行残す。
call PATCH "/tasks/$TASK_ID/status" '{"status":"doing","version":1}' -b "$ALICE_COOKIE"
check "todo -> doing" 200
call PATCH "/tasks/$TASK_ID/status" '{"status":"done","version":2}' -b "$ALICE_COOKIE"
check "doing -> done" 200

echo "== Part 1. アクセスログ"
if [ -z "$LOG_FILE" ]; then
	echo "SKIP  LOG_FILE が未指定（LOG_FILE=server.log で実行するとログも確認できる）"
elif [ ! -f "$LOG_FILE" ]; then
	fail "LOG_FILE が見つからない ($LOG_FILE)"
else
	LINE=$(log_line "$TASK_REQUEST_ID")
	check_log "Response の ID でアクセスログを検索できる" "$LINE" "\"request_id\":\"$TASK_REQUEST_ID\""
	check_log "認証済み Request に user_id が付く" "$LINE" '"user_id":'
	check_log "status が記録される" "$LINE" '"status":200'
	check_log "INFO レベル" "$LINE" '"level":"INFO"'

	LINE=$(log_line "$TRACE_ID")
	check_log "引き継いだ ID でも検索できる" "$LINE" "\"request_id\":\"$TRACE_ID\""

	LINE=$(log_line "$HEALTH_ID")
	if [ -n "$LINE" ] && [[ "$LINE" != *'"user_id"'* ]]; then
		pass "/health には user_id が付かない"
	else
		fail "/health のログが無い、または user_id が付いている"
		printf '      log: %s\n' "$LINE"
	fi

	if [ -n "$SLOW_ID" ]; then
		LINE=$(log_line "$SLOW_ID")
		check_log "503 は ERROR レベル" "$LINE" '"level":"ERROR"'
	fi

	echo "== Part 2. ログに出してはいけないもの"
	LEAKS=$(grep -icE 'kanban_session|password|set-cookie' "$LOG_FILE")
	if [ "$LEAKS" = "0" ]; then
		pass "kanban_session / password / set-cookie が 0 件"
	else
		fail "機密情報らしき文字列が $LEAKS 行ある"
	fi

	if [ -n "$SESSION_ID" ] && grep -qF "$SESSION_ID" "$LOG_FILE"; then
		fail "Session ID の値がログに含まれる"
	else
		pass "Session ID の値がログに含まれない"
	fi

	if grep -qF "$ALICE_PASSWORD" "$LOG_FILE"; then
		fail "パスワードの値がログに含まれる"
	else
		pass "パスワードの値がログに含まれない"
	fi
fi

echo "== Part 3. Audit Log"
if [ -z "$COMPOSE_DIR" ]; then
	echo "SKIP  COMPOSE_DIR が未指定（COMPOSE_DIR=../../go-kanban で実行すると task_history も確認できる）"
else
	HISTORY=$(cd "$COMPOSE_DIR" && docker compose exec -T db psql -U kanban -d kanban -At -F ' ' -c \
		"SELECT h.user_id = u.id, h.action, h.old_value, h.new_value
		   FROM task_history h JOIN users u ON u.email = '$ALICE'
		  WHERE h.task_id = $TASK_ID ORDER BY h.id;")
	EXPECTED=$'t status_changed todo doing\nt status_changed doing done'

	if [ "$HISTORY" = "$EXPECTED" ]; then
		pass "task_history に「誰が・何を・何から何へ」が2行残る"
	else
		fail "task_history の内容が期待と違う"
		printf '      got:\n%s\n' "$HISTORY"
	fi
fi

echo
echo "passed: $PASSED, failed: $FAILED"

[ "$FAILED" -eq 0 ]
