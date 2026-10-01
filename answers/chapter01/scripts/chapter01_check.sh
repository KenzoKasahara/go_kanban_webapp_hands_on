#!/usr/bin/env bash
# Chapter 01 の完了条件を確認する。
#   - GET /health が 200 と {"status":"ok"} を返す
#   - 存在しない Path が 404 を返す
#   - 登録していないメソッドが 405 と Allow ヘッダを返す
#
# 使い方（サーバを起動した状態で実行する）:
#   bash scripts/chapter01_check.sh
#
# 照合するのは Status Code と、ヘッダ・Body に特定の文字列が含まれるかだけ。

set -u

BASE_URL=${BASE_URL:-http://localhost:8980}

PASSED=0
FAILED=0

# call METHOD PATH
# 結果を STATUS・HEADERS・BODY に入れる。
call() {
	local method=$1 path=$2

	local out
	out=$(curl -s -i -X "$method" "$BASE_URL$path" | tr -d '\r')
	STATUS=$(printf '%s\n' "$out" | head -n 1 | awk '{print $2}')
	HEADERS=$(printf '%s\n' "$out" | sed '/^$/q')
	BODY=$(printf '%s\n' "$out" | sed '1,/^$/d')
}

# expect NAME WANT_STATUS [含まれるべき文字列 ...]
# 文字列はヘッダと Body のどちらかに含まれていればよい。
expect() {
	local name=$1 want=$2
	shift 2

	local ok=1
	[ "$STATUS" = "$want" ] || ok=0

	local s
	for s in "$@"; do
		case "$HEADERS$BODY" in
		*"$s"*) ;;
		*) ok=0 ;;
		esac
	done

	if [ $ok -eq 1 ]; then
		echo "PASS  $name ($STATUS)"
		PASSED=$((PASSED + 1))
	else
		echo "FAIL  $name: want $want, got ${STATUS:-no response}"
		printf '%s\n' "$HEADERS" "$BODY" | sed 's/^/      /'
		FAILED=$((FAILED + 1))
	fi
}

if ! curl -s -o /dev/null "$BASE_URL/health"; then
	echo "サーバに接続できません: $BASE_URL"
	echo "別のターミナルで go run ./cmd/api を実行してから再度実行してください。"
	exit 1
fi

echo "== Step 4. 正常系"
call GET /health
expect "GET /health" 200 'Content-Type: application/json' '{"status":"ok"}'

echo "== Step 5. 壊して観察する"
call GET /not-found
expect "存在しない Path" 404 '404 page not found'

call POST /health
expect "登録していないメソッド" 405 'Allow: GET, HEAD' 'Method Not Allowed'

echo
echo "passed: $PASSED, failed: $FAILED"
[ $FAILED -eq 0 ]
