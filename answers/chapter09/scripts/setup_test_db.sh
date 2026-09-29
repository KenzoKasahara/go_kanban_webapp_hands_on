#!/usr/bin/env bash
# Integration Test 用の DB（kanban_test）を作り、Migration を流す。
#
# Integration Test は毎回全テーブルを TRUNCATE する。
# 開発用の kanban DB を消さないよう、同じコンテナの中に別の DB を用意する。
#
# 使い方（answers/chapter09 で実行する）:
#   bash scripts/setup_test_db.sh
#
# 作り直すときは、先に DB を消してから実行する。
#   docker compose -f ../../go-kanban/docker-compose.yml exec -T db \
#     psql -U kanban -d kanban -c 'DROP DATABASE kanban_test;'

set -euo pipefail

SCRIPT_DIR=$(cd "$(dirname "$0")" && pwd)
ROOT_DIR=$(cd "$SCRIPT_DIR/.." && pwd)
COMPOSE_FILE=${COMPOSE_FILE:-$ROOT_DIR/../../go-kanban/docker-compose.yml}
MIGRATIONS_DIR=$ROOT_DIR/../../go-kanban/migrations
TEST_DB=kanban_test

psql_in() {
	docker compose -f "$COMPOSE_FILE" exec -T db psql -U kanban -v ON_ERROR_STOP=1 "$@"
}

EXISTS=$(psql_in -d kanban -tAc "SELECT 1 FROM pg_database WHERE datname = '$TEST_DB'")
if [ "$EXISTS" = "1" ]; then
	echo "$TEST_DB は作成済みです。作り直す場合はファイル先頭のコメントを参照してください。"
	exit 0
fi

psql_in -d kanban -c "CREATE DATABASE $TEST_DB;"

psql_in -d "$TEST_DB" < "$MIGRATIONS_DIR/001_init.sql"
psql_in -d "$TEST_DB" < "$MIGRATIONS_DIR/002_auth.sql"
psql_in -d "$TEST_DB" < "$ROOT_DIR/migrations/003_history.sql"
psql_in -d "$TEST_DB" < "$ROOT_DIR/migrations/004_idempotency.sql"

echo "$TEST_DB を作成しました。"
