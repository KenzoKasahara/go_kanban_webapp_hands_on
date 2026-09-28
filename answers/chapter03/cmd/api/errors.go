package main

import (
	"errors"
	"log"
	"net/http"

	"github.com/jackc/pgx/v5/pgconn"
)

// 業務上の失敗の分類。sentinel error（比較の目印として使う、あらかじめ用意した error 値）と呼ばれる。
var (
	ErrNotFound     = errors.New("not found")
	ErrForbidden    = errors.New("forbidden")
	ErrConflict     = errors.New("conflict")
	ErrUnauthorized = errors.New("unauthorized")
)

// ValidationError は「入力が不正である」という分類を表す。
// 利用者へ返してよいメッセージだけを Message に入れる。
type ValidationError struct {
	Message string
}

func (e *ValidationError) Error() string {
	return e.Message
}

type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type errorEnvelope struct {
	Error errorBody `json:"error"`
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, errorEnvelope{
		Error: errorBody{Code: code, Message: message},
	})
}

// PublicError は「分類(err)」と「利用者へ見せてよい文言(message)」を束ねる。
// 分類だけでは "email is already registered" のような具体的な案内を返せない。
type PublicError struct {
	err     error
	message string
}

func (e *PublicError) Error() string {
	return e.message + ": " + e.err.Error()
}

// Unwrap があるため errors.Is(err, ErrConflict) は引き続き成立する。
func (e *PublicError) Unwrap() error {
	return e.err
}

func publicError(err error, message string) error {
	return &PublicError{err: err, message: message}
}

// publicMessage は err に利用者向け文言が付いていればそれを、
// 無ければ fallback を返す。
func publicMessage(err error, fallback string) string {
	var publicErr *PublicError

	if errors.As(err, &publicErr) {
		return publicErr.message
	}

	return fallback
}

// respondError は内部 error を HTTP Status へ変換する唯一の場所。
// 分類できない error は詳細をログへ残し、利用者へは一般的な文言だけ返す。
func respondError(w http.ResponseWriter, err error) {
	var validationErr *ValidationError

	switch {
	case errors.As(err, &validationErr):
		writeError(w, http.StatusBadRequest, "invalid_request", validationErr.Message)
	case errors.Is(err, ErrUnauthorized):
		writeError(w, http.StatusUnauthorized, "unauthorized", "authentication required")
	case errors.Is(err, ErrForbidden):
		writeError(w, http.StatusForbidden, "forbidden", "operation not allowed")
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found",
			publicMessage(err, "resource not found"))
	case errors.Is(err, ErrConflict):
		writeError(w, http.StatusConflict, "conflict",
			publicMessage(err, "resource was updated by another request"))
	default:
		log.Printf("unexpected error: %v", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "internal server error")
	}
}

// PostgreSQL の制約違反は、Go側では単なる error として届く。
// SQLSTATE（PostgreSQL がエラーの種類ごとに返す5桁のコード）を見て
// 業務上の意味へ翻訳しないと、すべて 500 になる。
const (
	pgCodeUniqueViolation     = "23505"
	pgCodeForeignKeyViolation = "23503"
	pgCodeCheckViolation      = "23514"
)

func pgErrorCode(err error) string {
	var pgErr *pgconn.PgError

	if errors.As(err, &pgErr) {
		return pgErr.Code
	}

	return ""
}

func isUniqueViolation(err error) bool {
	return pgErrorCode(err) == pgCodeUniqueViolation
}

func isForeignKeyViolation(err error) bool {
	return pgErrorCode(err) == pgCodeForeignKeyViolation
}

func isCheckViolation(err error) bool {
	return pgErrorCode(err) == pgCodeCheckViolation
}
