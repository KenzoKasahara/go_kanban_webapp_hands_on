package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"
)

const (
	sessionCookieName = "kanban_session"
	sessionTTL        = 24 * time.Hour
)

type User struct {
	ID    int64  `json:"id"`
	Email string `json:"email"`
}

// contextKey は他packageのkeyと衝突しないよう独自型にする。
type contextKey string

const userContextKey contextKey = "user"

// newSessionID は推測不能なSession IDを生成する。
// math/rand ではなく crypto/rand を使う。
func newSessionID() (string, error) {
	buf := make([]byte, 32)

	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate session id: %w", err)
	}

	return base64.RawURLEncoding.EncodeToString(buf), nil
}

type credentials struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (c credentials) validate() error {
	if !strings.Contains(c.Email, "@") {
		return &ValidationError{Message: "email must be a valid address"}
	}

	if len(c.Password) < 12 {
		return &ValidationError{Message: "password must be 12 characters or more"}
	}

	return nil
}

func createUserHandler(w http.ResponseWriter, r *http.Request) {
	var input credentials

	if err := decodeJSON(r, &input); err != nil {
		respondError(w, err)
		return
	}

	if err := input.validate(); err != nil {
		respondError(w, err)
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(input.Password), bcrypt.DefaultCost)
	if err != nil {
		respondError(w, fmt.Errorf("hash password: %w", err))
		return
	}

	var user User

	err = pool.QueryRow(
		r.Context(),
		"INSERT INTO users (email, password_hash) VALUES ($1, $2) RETURNING id, email",
		input.Email, string(hash),
	).Scan(&user.ID, &user.Email)

	if isUniqueViolation(err) {
		respondError(w, publicError(ErrConflict, "email is already registered"))
		return
	}

	if err != nil {
		respondError(w, fmt.Errorf("insert user: %w", err))
		return
	}

	writeJSON(w, http.StatusCreated, user)
}

func loginHandler(w http.ResponseWriter, r *http.Request) {
	var input credentials

	if err := decodeJSON(r, &input); err != nil {
		respondError(w, err)
		return
	}

	var (
		userID int64
		hash   string
	)

	err := pool.QueryRow(
		r.Context(),
		"SELECT id, password_hash FROM users WHERE email = $1",
		input.Email,
	).Scan(&userID, &hash)

	// 「User未登録」と「Password不一致」を区別して返さない。
	// 区別するとEmailの登録有無を外部から列挙できてしまう。
	if errors.Is(err, pgx.ErrNoRows) {
		respondError(w, ErrUnauthorized)
		return
	}

	if err != nil {
		respondError(w, fmt.Errorf("query user: %w", err))
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(input.Password)); err != nil {
		respondError(w, ErrUnauthorized)
		return
	}

	sessionID, err := newSessionID()
	if err != nil {
		respondError(w, err)
		return
	}

	expiresAt := time.Now().Add(sessionTTL)

	_, err = pool.Exec(
		r.Context(),
		"INSERT INTO sessions (id, user_id, expires_at) VALUES ($1, $2, $3)",
		sessionID, userID, expiresAt,
	)
	if err != nil {
		respondError(w, fmt.Errorf("insert session: %w", err))
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    sessionID,
		Path:     "/",
		Expires:  expiresAt,
		HttpOnly: true,                 // JavaScriptから読めなくする
		Secure:   false,                // 本番(HTTPS)では true にする
		SameSite: http.SameSiteLaxMode, // 他サイトからの自動送信を抑制する
	})

	writeJSON(w, http.StatusOK, map[string]int64{"user_id": userID})
}

func logoutHandler(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie(sessionCookieName)
	if err == nil {
		// Server側のSessionを消す。Cookieを消すだけでは無効化にならない。
		if _, err := pool.Exec(r.Context(), "DELETE FROM sessions WHERE id = $1", cookie.Value); err != nil {
			respondError(w, fmt.Errorf("delete session: %w", err))
			return
		}
	}

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})

	w.WriteHeader(http.StatusNoContent)
}

// requireAuth はCookieのSessionからUserを特定し、Contextへ載せる。
func requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(sessionCookieName)
		if err != nil {
			respondError(w, ErrUnauthorized)
			return
		}

		var user User

		err = pool.QueryRow(
			r.Context(),
			`SELECT u.id, u.email
			 FROM sessions s
			 JOIN users u ON u.id = s.user_id
			 WHERE s.id = $1 AND s.expires_at > NOW()`,
			cookie.Value,
		).Scan(&user.ID, &user.Email)

		if errors.Is(err, pgx.ErrNoRows) {
			respondError(w, ErrUnauthorized)
			return
		}

		if err != nil {
			respondError(w, fmt.Errorf("query session: %w", err))
			return
		}

		ctx := context.WithValue(r.Context(), userContextKey, user)
		next(w, r.WithContext(ctx))
	}
}

func currentUser(ctx context.Context) (User, bool) {
	user, ok := ctx.Value(userContextKey).(User)
	return user, ok
}
