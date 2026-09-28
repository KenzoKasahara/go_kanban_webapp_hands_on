#!/usr/bin/env bash
# Chapter 06 の PATCH /tasks/{id}/status を確認する。
#   - Chapter 05 までの API が同じように動くか（回帰確認）
#   - Step 10: 不正な遷移 400 / 正常 200 / 古い version 409 / 履歴
#   - Step 11: 8並列・同一 version で成功が1件だけか
#   - Step 12: 履歴の INSERT を失敗させると Task の更新も Rollback されるか
#
# 使い方（サーバを起動した状態で、answers/chapter06 から実行する）:
#   bash scripts/chapter06_check.sh
#
# DB の中身は go-kanban/ の docker compose 経由で psql から確認する。
# docker compose が使えない場合、DB を直接見る項目は SKIP する。
#
# 何度でも実行できるよう、メールアドレスには実行ごとに異なる接尾辞を付ける。

set -u

BASE_URL=${BASE_URL:-http://localhost:8080}
SCRIPT_DIR=$(cd "$(dirname "$0")" && pwd)
COMPOSE_FILE=${COMPOSE_FILE:-$SCRIPT_DIR/../../../go-kanban/docker-compose.yml}
DB_NAME=${DB_NAME:-kanban}

SUFFIX=$(date +%s)
ALICE="alice-$SUFFIX@example.com"
BOB="bob-$SUFFIX@example.com"
CAROL="carol-$SUFFIX@example.com"

# Cookie ファイルは一時ディレクトリに置き、終了時に消す。
WORK_DIR=$(mktemp -d)
ALICE_COOKIE="$WORK_DIR/alice.txt"
BOB_COOKIE="$WORK_DIR/bob.txt"
CAROL_COOKIE="$WORK_DIR/carol.txt"

# Step 12 で追加する制約。途中で中断しても必ず外す。
CONSTRAINT="reject_done_$SUFFIX"
CONSTRAINT_ADDED=0

PASSED=0
FAILED=0

# psql SQL
# 結果を装飾なし（-tA）で返す。
psql_q() {
	docker compose -f "$COMPOSE_FILE" exec -T db \
		psql -U kanban -d "$DB_NAME" -tA -v ON_ERROR_STOP=1 -c "$1"
}

cleanup() {
	if [ "$CONSTRAINT_ADDED" -eq 1 ]; then
		psql_q "ALTER TABLE task_history DROP CONSTRAINT IF EXISTS $CONSTRAINT;" >/dev/null
	fi
	rm -rf "$WORK_DIR"
}
trap cleanup EXIT

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

# check_eq 名前 期待値 実際の値
check_eq() {
	if [ "$2" = "$3" ]; then
		pass "$1 ($3)"
	else
		FAILED=$((FAILED + 1))
		printf 'FAIL  %s (want %s, got %s)\n' "$1" "$2" "$3"
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

# patch_status TASK_ID STATUS VERSION COOKIE
patch_status() {
	call PATCH "/tasks/$1/status" "{\"status\":\"$2\",\"version\":$3}" -b "$4"
}

call GET /health ""
if [ "$STATUS" != "200" ]; then
	echo "サーバに接続できません: $BASE_URL (status: $STATUS)"
	echo "go run ./cmd/api を実行してから、もう一度試してください。"
	exit 1
fi

DB_OK=0
if psql_q "SELECT 1 FROM task_history LIMIT 1;" >/dev/null 2>&1; then
	DB_OK=1
else
	echo "SKIP  DB を直接確認する項目（docker compose で task_history を参照できない）"
	echo "      migrations/003_history.sql の適用と COMPOSE_FILE / DB_NAME を確認してください"
fi

echo "== 準備"
call POST /users "{\"email\":\"$ALICE\",\"password\":\"alice-password-1\"}"
require "alice 登録" 201

call POST /users "{\"email\":\"$BOB\",\"password\":\"bob-password-123\"}"
require "bob 登録" 201
BOB_ID=$(extract_id "$BODY")

call POST /users "{\"email\":\"$CAROL\",\"password\":\"carol-password-1\"}"
require "carol 登録" 201

call POST /login "{\"email\":\"$ALICE\",\"password\":\"alice-password-1\"}" -c "$ALICE_COOKIE"
require "alice がログイン" 204

call POST /login "{\"email\":\"$BOB\",\"password\":\"bob-password-123\"}" -c "$BOB_COOKIE"
require "bob がログイン" 204

call POST /login "{\"email\":\"$CAROL\",\"password\":\"carol-password-1\"}" -c "$CAROL_COOKIE"
require "carol がログイン" 204

call POST /projects '{"name":"Alice Board"}' -b "$ALICE_COOKIE"
require "alice が Project 作成" 201
PROJECT=$(extract_id "$BODY")

# bob は Viewer として参加させる。carol はメンバーではない。
call POST "/projects/$PROJECT/members" "{\"user_id\":$BOB_ID,\"role\":\"viewer\"}" -b "$ALICE_COOKIE"
require "bob を Viewer として追加" 204

# create_task TITLE → TASK_ID に id を入れる
create_task() {
	call POST "/projects/$PROJECT/tasks" "{\"title\":\"$1\",\"priority\":\"high\"}" -b "$ALICE_COOKIE"
	require "Task 作成: $1" 201
	TASK_ID=$(extract_id "$BODY")
}

echo "== Chapter 05 までの API（回帰確認）"
create_task "write docs"
T1=$TASK_ID
check_body "作成直後は todo / version 1" '"status":"todo","version":1'

call GET "/tasks/$T1" "" -b "$ALICE_COOKIE"
check "alice が Task 取得" 200

call GET "/tasks/$T1" "" -b "$CAROL_COOKIE"
check "メンバーでない carol が Task 取得" 404

call GET "/projects/$PROJECT/tasks" "" -b "$ALICE_COOKIE" --get --data-urlencode "q=docs"
check "q=docs で検索" 200
check_body "検索結果に write docs が含まれる" "write docs"

echo "== Step 10. 判定順序（400 / 404 / 403 / 409 / 200）"
patch_status "$T1" "archived" 1 "$ALICE_COOKIE"
check "存在しない Status" 400
check_body "Status の候補を案内する" "status must be one of: todo, doing, done"

call PATCH "/tasks/$T1/status" '{"status":"doing","version":1,"extra":true}' -b "$ALICE_COOKIE"
check "未知の field を含む JSON" 400

patch_status "$T1" "doing" 1 "$CAROL_COOKIE"
check "メンバーでない carol が変更" 404

patch_status "$T1" "doing" 1 "$BOB_COOKIE"
check "Viewer の bob が変更" 403

patch_status "$T1" "done" 1 "$ALICE_COOKIE"
check "todo -> done（不正な遷移）" 400
check_body "遷移できない理由を返す" "cannot change status from todo to done"

patch_status "$T1" "todo" 1 "$ALICE_COOKIE"
check "todo -> todo（同じ Status）" 400

patch_status "$T1" "doing" 1 "$ALICE_COOKIE"
check "todo -> doing（正常）" 200
check_body "version が 2 になる" '"status":"doing","version":2'

patch_status "$T1" "done" 1 "$ALICE_COOKIE"
check "古い version:1 で更新" 409
check_body "再読み込みを促す" "task was updated by another request; reload and retry"

patch_status "$T1" "done" 2 "$ALICE_COOKIE"
check "正しい version:2 で更新" 200
check_body "version が 3 になる" '"status":"done","version":3'

call PATCH "/tasks/$T1/status" '{"status":"doing","version":3}' -b "$CAROL_COOKIE"
check "Cookie が別人なら version が正しくても 404" 404

if [ "$DB_OK" -eq 1 ]; then
	HISTORY=$(psql_q "SELECT string_agg(old_value || '->' || new_value, ',' ORDER BY id) FROM task_history WHERE task_id = $T1;")
	check_eq "履歴は成功した2件だけ" "todo->doing,doing->done" "$HISTORY"
fi

echo "== Step 11. 8並列・同一 version"
create_task "parallel target"
T2=$TASK_ID

for i in 1 2 3 4 5 6 7 8; do
	(curl -s -o /dev/null -w "%{http_code}\n" -b "$ALICE_COOKIE" \
		-X PATCH "$BASE_URL/tasks/$T2/status" \
		-H 'Content-Type: application/json' \
		-d '{"status":"doing","version":1}' >"$WORK_DIR/code_$i.txt") &
done
wait

CODES=$(cat "$WORK_DIR"/code_*.txt | sort | uniq -c | tr -s ' ' | sed 's/^ //' | paste -sd ',' -)
echo "      $CODES"
check_eq "200 は1件だけ" 1 "$(grep -c '^200$' "$WORK_DIR"/code_*.txt | awk -F: '{s+=$2} END {print s}')"
check_eq "残り7件は 409" 7 "$(grep -c '^409$' "$WORK_DIR"/code_*.txt | awk -F: '{s+=$2} END {print s}')"

call GET "/tasks/$T2" "" -b "$ALICE_COOKIE"
check_body "version は 1 -> 2（1回だけ増えた）" '"status":"doing","version":2'

if [ "$DB_OK" -eq 1 ]; then
	check_eq "履歴は1行だけ" 1 "$(psql_q "SELECT count(*) FROM task_history WHERE task_id = $T2;")"
fi

echo "== Step 12. 履歴の INSERT 失敗で Rollback"
if [ "$DB_OK" -eq 0 ]; then
	echo "SKIP  制約の追加に DB 接続が必要"
else
	create_task "rollback target"
	T3=$TASK_ID

	patch_status "$T3" "doing" 1 "$ALICE_COOKIE"
	require "todo -> doing" 200

	BEFORE=$(psql_q "SELECT count(*) FROM task_history WHERE task_id = $T3;")

	psql_q "ALTER TABLE task_history ADD CONSTRAINT $CONSTRAINT CHECK (new_value <> 'done') NOT VALID;" >/dev/null
	CONSTRAINT_ADDED=1

	patch_status "$T3" "done" 2 "$ALICE_COOKIE"
	check "履歴だけ失敗する更新" 500
	check_body "Client には一般的なメッセージだけ" '{"error":{"code":"internal_error","message":"internal server error"}}'

	call GET "/tasks/$T3" "" -b "$ALICE_COOKIE"
	check_body "Task は doing / version 2 のまま" '"status":"doing","version":2'
	check_eq "履歴の行数は増えない" "$BEFORE" "$(psql_q "SELECT count(*) FROM task_history WHERE task_id = $T3;")"

	psql_q "ALTER TABLE task_history DROP CONSTRAINT $CONSTRAINT;" >/dev/null
	CONSTRAINT_ADDED=0

	patch_status "$T3" "done" 2 "$ALICE_COOKIE"
	check "制約を外して再実行" 200
	check_body "version が 3 になる" '"status":"done","version":3'
fi

echo
echo "passed: $PASSED, failed: $FAILED"

[ "$FAILED" -eq 0 ]
