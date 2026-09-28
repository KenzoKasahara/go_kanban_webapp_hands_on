package model

import "errors"

// 業務上の失敗の分類。HTTP Status への変換は handler 層が行う。
var (
	ErrNotFound     = errors.New("not found")
	ErrForbidden    = errors.New("forbidden")
	ErrConflict     = errors.New("conflict")
	ErrUnauthorized = errors.New("unauthorized")
)

// ValidationError は「入力が不正である」という分類を表す。
type ValidationError struct {
	Message string
}

func (e *ValidationError) Error() string {
	return e.Message
}

func Invalid(message string) error {
	return &ValidationError{Message: message}
}

// PublicError は分類と、利用者へ見せてよい文言を束ねる。
type PublicError struct {
	err     error
	message string
}

func (e *PublicError) Error() string {
	return e.message + ": " + e.err.Error()
}

func (e *PublicError) Unwrap() error {
	return e.err
}

func (e *PublicError) PublicMessage() string {
	return e.message
}

func Public(err error, message string) error {
	return &PublicError{err: err, message: message}
}
