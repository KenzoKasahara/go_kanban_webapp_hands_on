package repository

import (
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
)

// PostgreSQL の制約違反は、Go側では単なる error として届く。
// SQLSTATE を見て業務上の意味へ翻訳しないと、すべて 500 になる。
// 翻訳は SQL を知っている Repository の仕事になる。
const (
	pgCodeUniqueViolation     = "23505"
	pgCodeForeignKeyViolation = "23503"
)

func pgErrorCode(err error) string {
	var pgErr *pgconn.PgError

	if errors.As(err, &pgErr) {
		return pgErr.Code
	}

	return ""
}

func IsUniqueViolation(err error) bool {
	return pgErrorCode(err) == pgCodeUniqueViolation
}

func IsForeignKeyViolation(err error) bool {
	return pgErrorCode(err) == pgCodeForeignKeyViolation
}
