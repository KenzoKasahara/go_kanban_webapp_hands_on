package service

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"golang.org/x/crypto/bcrypt"

	"example.com/go-kanban/internal/model"
	"example.com/go-kanban/internal/repository"
)

const sessionTTL = 24 * time.Hour

type AuthService struct {
	users repository.UserRepository
}

func NewAuthService(users repository.UserRepository) *AuthService {
	return &AuthService{users: users}
}

// Session はログインの成果物。Cookie への変換は handler が行う。
type Session struct {
	ID        string
	ExpiresAt time.Time
}

func (s *AuthService) Register(ctx context.Context, in model.Credentials) (model.User, error) {
	if err := in.Validate(); err != nil {
		return model.User{}, err
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(in.Password), bcrypt.DefaultCost)
	if err != nil {
		return model.User{}, fmt.Errorf("hash password: %w", err)
	}

	return s.users.Create(ctx, in.Email, string(hash))
}

func (s *AuthService) Login(ctx context.Context, in model.Credentials) (Session, error) {
	userID, hash, err := s.users.FindPasswordHash(ctx, in.Email)

	// 「User未登録」と「Password不一致」を区別して返さない。
	// 区別するとEmailの登録有無を外部から列挙できてしまう。
	if errors.Is(err, model.ErrNotFound) {
		return Session{}, model.ErrUnauthorized
	}

	if err != nil {
		return Session{}, err
	}

	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(in.Password)); err != nil {
		return Session{}, model.ErrUnauthorized
	}

	sessionID, err := newSessionID()
	if err != nil {
		return Session{}, err
	}

	expiresAt := time.Now().Add(sessionTTL)

	if err := s.users.CreateSession(ctx, sessionID, userID, expiresAt); err != nil {
		return Session{}, err
	}

	return Session{ID: sessionID, ExpiresAt: expiresAt}, nil
}

// Logout は Server 側の Session を消す。Cookie を消すだけでは無効化にならない。
func (s *AuthService) Logout(ctx context.Context, sessionID string) error {
	return s.users.DeleteSession(ctx, sessionID)
}

// Authenticate は Session ID から User を特定する。無効なら ErrUnauthorized。
func (s *AuthService) Authenticate(ctx context.Context, sessionID string) (model.User, error) {
	return s.users.FindBySession(ctx, sessionID)
}

// newSessionID は推測不能なSession IDを生成する。
// math/rand ではなく crypto/rand を使う。
func newSessionID() (string, error) {
	buf := make([]byte, 32)

	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate session id: %w", err)
	}

	return base64.RawURLEncoding.EncodeToString(buf), nil
}
