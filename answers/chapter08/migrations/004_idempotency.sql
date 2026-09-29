-- Idempotency-Key で処理済みの Response
-- 主キーに user_id を含め、他人の Key で他人の Response を読めないようにする。
-- endpoint を含め、同じ Key で別の API を叩いたときに前の結果が返るのを防ぐ。
CREATE TABLE idempotency_keys (
    key TEXT NOT NULL,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    endpoint TEXT NOT NULL,
    status_code INTEGER NOT NULL,
    response_body TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (key, user_id, endpoint)
);
