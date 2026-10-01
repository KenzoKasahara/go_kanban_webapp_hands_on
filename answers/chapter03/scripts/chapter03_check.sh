#!/usr/bin/env bash
# Chapter 03 の完了条件を確認する。
#   - 空 title / 空白だけの title / 範囲外 priority / 長すぎる title が 400 になる
#   - 未知の JSON フィールドと壊れた JSON が 400 になる
#   - 数値でない Path Parameter が 400 になる
#   - 存在しない Task と、存在しない Project への Task 作成が 404 になる
#   - 404 の Body に DB の内部情報（SQLSTATE や制約名）が含まれない
#   - 正常系は Chapter 02 と同じ Status を返す
#
# 使い方（サーバを起動した状態で実行する）:
#   bash scripts/chapter03_check.sh
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
		# Body は標準入力から渡す。Windows の curl は -d の引数を ANSI コードページへ
		# 変換するため、日本語が不正な UTF-8 になって文字数が変わってしまう。
		out=$(printf '%s' "$data" | curl -s -w '\n%{http_code}' -X "$method" \
			-H 'Content-Type: application/json' --data-binary @- "$BASE_URL$path")
	else
		out=$(curl -s -w '\n%{http_code}' -X "$method" "$BASE_URL$path")
	fi

	STATUS=${out##*$'\n'}
	BODY=${out%$'\n'*}
	# json.Encoder は末尾に改行を付ける。比較しやすいよう取り除く。
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

	report "$name" "$want" $ok
}

# expect_absent NAME [Body に含まれてはいけない文字列 ...]
# 直前の call の Body だけを見る。Status は expect で確認済みの前提。
expect_absent() {
	local name=$1
	shift

	local ok=1 s
	for s in "$@"; do
		case "$BODY" in
		*"$s"*) ok=0 ;;
		esac
	done

	report "$name" "$STATUS" $ok
}

report() {
	local name=$1 want=$2 ok=$3

	if [ "$ok" -eq 1 ]; then
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

# repeat_str STR N: STR を N 回つなげた文字列を出力する。
repeat_str() {
	local s=$1 n=$2 out=""
	local i
	for ((i = 0; i < n; i++)); do
		out+=$s
	done
	printf '%s' "$out"
}

if ! curl -s -o /dev/null "$BASE_URL/health"; then
	echo "サーバに接続できません: $BASE_URL"
	echo "別のターミナルで go run ./cmd/api を実行してから再度実行してください。"
	exit 1
fi

echo "== 準備"
call POST /projects '{"name":"Kanban Hands-on"}'
expect "Project を作る" 201 '"name":"Kanban Hands-on"'
PROJECT_ID=$(first_id)

if [ -z "$PROJECT_ID" ]; then
	echo "Project の ID を取得できないため中断します。"
	exit 1
fi

TASKS="/projects/$PROJECT_ID/tasks"
INVALID='"code":"invalid_request"'

echo "== Step 4. Validation"
call POST "$TASKS" '{"title":"","priority":"high"}'
expect "title が空" 400 "$INVALID" '"message":"title is required"'

call POST "$TASKS" '{"title":"   ","priority":"high"}'
expect "title が空白だけ" 400 "$INVALID" '"message":"title is required"'

call POST "$TASKS" '{"title":"ok","priority":"SUPER_HIGH"}'
expect "priority が不正" 400 "$INVALID" '"message":"priority must be one of: low, medium, high"'

call POST "$TASKS" "{\"title\":\"$(repeat_str a 101)\"}"
expect "title が 101 文字" 400 "$INVALID" '"message":"title must be 100 characters or fewer"'

# len(s) で判定していると 300 バイトになり弾かれる。rune 数で判定していれば通る。
call POST "$TASKS" "{\"title\":\"$(repeat_str あ 100)\"}"
expect "日本語の title が 100 文字ちょうど" 201 '"priority":"medium"'

echo "== Step 4. JSON Decode"
call POST "$TASKS" '{"title":"ok","titel":"typo"}'
expect "フィールド名の typo" 400 "$INVALID" 'unknown field \"titel\"'

call POST "$TASKS" '{"title":'
expect "壊れた JSON" 400 "$INVALID" 'request body is not valid JSON'

echo "== Step 4. Path Parameter"
call GET /tasks/abc
expect "id が数値でない" 400 "$INVALID" '"message":"id must be a positive integer"'

call GET /tasks/0
expect "id が 0" 400 "$INVALID" '"message":"id must be a positive integer"'

call GET /projects/abc/tasks
expect "project の id が数値でない" 400 "$INVALID"

echo "== Step 4. Not Found"
call GET "/tasks/$MISSING_ID"
expect "存在しない Task" 404 '"code":"not_found"' '"message":"resource not found"'
expect_absent "存在しない Task: DB の文言が漏れない" 'no rows in result set'

call POST "/projects/$MISSING_ID/tasks" '{"title":"ok","priority":"high"}'
expect "存在しない Project" 404 '"code":"not_found"' '"message":"resource not found"'
expect_absent "存在しない Project: DB の文言が漏れない" 'SQLSTATE' 'tasks_project_id_fkey'

# Validation は DB より先に動くので、Project が無くても不正な入力は 400 になる。
call POST "/projects/$MISSING_ID/tasks" '{"title":""}'
expect "存在しない Project でも不正な入力は 400" 400 "$INVALID"

echo "== Step 4. 正常系"
call POST "$TASKS" '{"title":"  write docs  ","priority":"high"}'
expect "Task を作る（前後の空白は除去される）" 201 "\"project_id\":$PROJECT_ID" '"title":"write docs"' '"priority":"high"' '"status":"todo"' '"version":1'
TASK_ID=$(first_id)

call GET "/tasks/$TASK_ID"
expect "作った Task を取得する" 200 "\"id\":$TASK_ID" '"title":"write docs"'

call GET "$TASKS"
expect "一覧を取得する" 200 '"title":"write docs"'
expect_absent "一覧に不正データが無い" '"title":""' 'SUPER_HIGH'

echo
echo "passed: $PASSED, failed: $FAILED"
[ $FAILED -eq 0 ]
