#!/usr/bin/env bash
# Chapter 02 の完了条件を確認する。
#   - Project と Task を作成・取得できる
#   - 空の title と定義外の priority が 201 で保存されてしまう
#   - 存在しない Task の取得が 500 になる
#   - 存在しない Project への Task 作成が 500 になり、DB のエラー文がそのまま返る
#   - typo したフィールドが無視され、title が空の Task が作られる
#
# 使い方（サーバを起動した状態で実行する）:
#   bash scripts/chapter02_check.sh
#
# 何度でも実行できるよう、Project は実行のたびに新しく作り、その ID を使う。
# 照合するのは Status Code と、Body に特定の文字列が含まれるかだけ。

set -u

BASE_URL=${BASE_URL:-http://localhost:8980}

# 存在しない ID として使う値。BIGSERIAL がここまで進むことはない。
MISSING_ID=999999999

PASSED=0
FAILED=0

# call METHOD PATH [DATA]
# 結果を STATUS と BODY に入れる。DATA がなければ Body を送らない。
call() {
	local method=$1 path=$2 data=${3:-}

	local out
	if [ -n "$data" ]; then
		out=$(curl -s -w '\n%{http_code}' -X "$method" \
			-H 'Content-Type: application/json' -d "$data" "$BASE_URL$path")
	else
		out=$(curl -s -w '\n%{http_code}' -X "$method" "$BASE_URL$path")
	fi

	STATUS=${out##*$'\n'}
	BODY=${out%$'\n'*}
	# json.Encoder と http.Error は末尾に改行を付ける。比較しやすいよう取り除く。
	BODY=${BODY%$'\n'}
}

# expect NAME WANT_STATUS [Body に含まれるべき文字列 ...]
expect() {
	local name=$1 want=$2
	shift 2

	local ok=1
	[ "$STATUS" = "$want" ] || ok=0

	local s
	for s in "$@"; do
		case "$BODY" in
		*"$s"*) ;;
		*) ok=0 ;;
		esac
	done

	if [ $ok -eq 1 ]; then
		echo "PASS  $name ($STATUS)"
		PASSED=$((PASSED + 1))
	else
		echo "FAIL  $name: want $want, got ${STATUS:-no response}"
		printf '      body: %s\n' "$BODY"
		FAILED=$((FAILED + 1))
	fi
}

# 直前の BODY の先頭にある "id" の値を取り出す。
first_id() {
	printf '%s\n' "$BODY" | sed -n 's/^{"id":\([0-9]*\).*/\1/p'
}

if ! curl -s -o /dev/null "$BASE_URL/health"; then
	echo "サーバに接続できません: $BASE_URL"
	echo "別のターミナルで go run ./cmd/api を実行してから再度実行してください。"
	exit 1
fi

echo "== Step 5. 動かして確認する"
call POST /projects '{"name":"Kanban Hands-on"}'
expect "Project を作る" 201 '"name":"Kanban Hands-on"'
PROJECT_ID=$(first_id)

if [ -z "$PROJECT_ID" ]; then
	echo "Project の ID を取得できないため中断します。"
	exit 1
fi

call POST "/projects/$PROJECT_ID/tasks" '{"title":"write docs","description":"","priority":"high"}'
expect "Task を作る" 201 "\"project_id\":$PROJECT_ID" '"title":"write docs"' '"priority":"high"' '"status":"todo"' '"version":1'
TASK_ID=$(first_id)

call GET "/tasks/$TASK_ID"
expect "作った Task を取得する" 200 "\"id\":$TASK_ID" '"title":"write docs"'

echo "== Step 6. 壊して観察する"
call POST "/projects/$PROJECT_ID/tasks" '{"title":"","priority":"SUPER_HIGH"}'
expect "Failure Test 1: 空の title と定義外の priority が保存される" 201 '"title":""' '"priority":"SUPER_HIGH"'

call GET "/projects/$PROJECT_ID/tasks"
expect "Failure Test 1: 不正データが一覧にも出る" 200 '"title":"write docs"' '"priority":"SUPER_HIGH"'

call GET "/tasks/$MISSING_ID"
expect "Failure Test 2: 存在しない Task が 500 になる" 500 'no rows in result set'

call POST "/projects/$MISSING_ID/tasks" '{"title":"orphan","priority":"low"}'
expect "Failure Test 3: DB のエラー文がそのまま返る" 500 'tasks_project_id_fkey' 'SQLSTATE 23503'

call POST "/projects/$PROJECT_ID/tasks" '{"titel":"typo","priority":"low"}'
expect "Failure Test 4: typo したフィールドが無視される" 201 '"title":""' '"priority":"low"'

echo
echo "passed: $PASSED, failed: $FAILED"
[ $FAILED -eq 0 ]
