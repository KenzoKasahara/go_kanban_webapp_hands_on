package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"example.com/go-kanban/internal/model"
)

// UserRepository は User と Session の永続化を扱う。
type UserRepository interface {
	Create(ctx context.Context, email, passwordHash string) (model.User, error)
	FindPasswordHash(ctx context.Context, email string) (userID int64, passwordHash string, err error)
	CreateSession(ctx context.Context, sessionID string, userID int64, expiresAt time.Time) error
	DeleteSession(ctx context.Context, sessionID string) error
	FindBySession(ctx context.Context, sessionID string) (model.User, error)
}

var _ UserRepository = (*PgUserRepository)(nil)

type PgUserRepository struct {
	pool *pgxpool.Pool
}

func NewUserRepository(pool *pgxpool.Pool) *PgUserRepository {
	return &PgUserRepository{pool: pool}
}

func (r *PgUserRepository) Create(ctx context.Context, email, passwordHash string) (model.User, error) {
	var user model.User

	err := r.pool.QueryRow(
		ctx,
		"INSERT INTO users (email, password_hash) VALUES ($1, $2) RETURNING id, email",
		email, passwordHash,
	).Scan(&user.ID, &user.Email)

	if IsUniqueViolation(err) {
		return model.User{}, model.Public(model.ErrConflict, "email is already registered")
	}

	if err != nil {
		return model.User{}, fmt.Errorf("insert user: %w", err)
	}

	return user, nil
}

// FindPasswordHash は照合用のハッシュを返す。見つからなければ ErrNotFound。
func (r *PgUserRepository) FindPasswordHash(ctx context.Context, email string) (int64, string, error) {
	var (
		userID int64
		hash   string
	)

	err := r.pool.QueryRow(
		ctx,
		"SELECT id, password_hash FROM users WHERE email = $1",
		email,
	).Scan(&userID, &hash)

	if errors.Is(err, pgx.ErrNoRows) {
		return 0, "", model.ErrNotFound
	}

	if err != nil {
		return 0, "", fmt.Errorf("query user: %w", err)
	}

	return userID, hash, nil
}

func (r *PgUserRepository) CreateSession(ctx context.Context, sessionID string, userID int64, expiresAt time.Time) error {
	_, err := r.pool.Exec(
		ctx,
		"INSERT INTO sessions (id, user_id, expires_at) VALUES ($1, $2, $3)",
		sessionID, userID, expiresAt,
	)
	if err != nil {
		return fmt.Errorf("insert session: %w", err)
	}

	return nil
}

func (r *PgUserRepository) DeleteSession(ctx context.Context, sessionID string) error {
	if _, err := r.pool.Exec(ctx, "DELETE FROM sessions WHERE id = $1", sessionID); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}

	return nil
}

// FindBySession は有効期限内の Session から User を特定する。
// 見つからなければ ErrUnauthorized。
func (r *PgUserRepository) FindBySession(ctx context.Context, sessionID string) (model.User, error) {
	var user model.User

	err := r.pool.QueryRow(
		ctx,
		`SELECT u.id, u.email
		 FROM sessions s
		 JOIN users u ON u.id = s.user_id
		 WHERE s.id = $1 AND s.expires_at > NOW()`,
		sessionID,
	).Scan(&user.ID, &user.Email)

	if errors.Is(err, pgx.ErrNoRows) {
		return model.User{}, model.ErrUnauthorized
	}

	if err != nil {
		return model.User{}, fmt.Errorf("query session: %w", err)
	}

	return user, nil
}
