package model

import "strings"

const MinPasswordLength = 12

type User struct {
	ID    int64  `json:"id"`
	Email string `json:"email"`
}

type Project struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// Credentials は登録・ログインの入力。
type Credentials struct {
	Email    string
	Password string
}

// Validate は登録時の入力を検査する。
// ログイン時には呼ばない。ルールを満たさない Password は、単に照合で失敗させる。
func (c Credentials) Validate() error {
	if !strings.Contains(c.Email, "@") {
		return Invalid("email must be a valid address")
	}

	if len(c.Password) < MinPasswordLength {
		return Invalid("password must be 12 characters or more")
	}

	return nil
}
