package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNoStoredResponse は、その Key の結果がまだ保存されていないことを表す。
var ErrNoStoredResponse = errors.New("no stored response")

// StoredResponse は再送時にそのまま返す、前回の Response。
type StoredResponse struct {
	StatusCode int
	Body       string
}

type IdempotencyRepository struct {
	pool *pgxpool.Pool
}

func NewIdempotencyRepository(pool *pgxpool.Pool) *IdempotencyRepository {
	return &IdempotencyRepository{pool: pool}
}

// Find は (key, user_id, endpoint) で保存済みの結果を探す。
// 見つからなければ ErrNoStoredResponse。
func (r *IdempotencyRepository) Find(
	ctx context.Context,
	key string,
	userID int64,
	endpoint string,
) (StoredResponse, error) {
	var stored StoredResponse

	err := r.pool.QueryRow(
		ctx,
		`SELECT status_code, response_body
		 FROM idempotency_keys
		 WHERE key = $1 AND user_id = $2 AND endpoint = $3`,
		key, userID, endpoint,
	).Scan(&stored.StatusCode, &stored.Body)

	if errors.Is(err, pgx.ErrNoRows) {
		return StoredResponse{}, ErrNoStoredResponse
	}

	if err != nil {
		return StoredResponse{}, fmt.Errorf("query idempotency key: %w", err)
	}

	return stored, nil
}

// Save は結果を保存する。
// ON CONFLICT DO NOTHING により、同時に2つのRequestが完了しても
// 先に保存された結果が正となる。
func (r *IdempotencyRepository) Save(
	ctx context.Context,
	key string,
	userID int64,
	endpoint string,
	stored StoredResponse,
) error {
	_, err := r.pool.Exec(
		ctx,
		`INSERT INTO idempotency_keys (key, user_id, endpoint, status_code, response_body)
		 VALUES ($1, $2, $3, $4, $5)
		 ON CONFLICT (key, user_id, endpoint) DO NOTHING`,
		key, userID, endpoint, stored.StatusCode, stored.Body,
	)
	if err != nil {
		return fmt.Errorf("insert idempotency key: %w", err)
	}

	return nil
}
