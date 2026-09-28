package middleware

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"example.com/go-kanban/internal/httpx"
)

// accessLogLine は AccessLog が出した1行を読むための型。
type accessLogLine struct {
	Level     string `json:"level"`
	Msg       string `json:"msg"`
	RequestID string `json:"request_id"`
	Path      string `json:"path"`
	Status    int    `json:"status"`
	Bytes     int    `json:"bytes"`
	UserID    *int64 `json:"user_id"`
}

// serve は RequestID -> AccessLog -> inner の順に組み立てて1回呼び、
// Response とログの1行を返す。
func serve(t *testing.T, req *http.Request, inner http.Handler) (*httptest.ResponseRecorder, accessLogLine, string) {
	t.Helper()

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	h := RequestID(AccessLog(logger, inner))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	raw := strings.TrimSpace(buf.String())

	var line accessLogLine
	if err := json.Unmarshal([]byte(raw), &line); err != nil {
		t.Fatalf("log is not a single JSON line: %v\n%s", err, raw)
	}

	return rec, line, raw
}

func TestAccessLogRecordsStatusAndRequestID(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("hello"))
	})

	rec, line, _ := serve(t, httptest.NewRequest(http.MethodPost, "/tasks?token=secret", nil), inner)

	responseID := rec.Header().Get(RequestIDHeader)
	if responseID == "" {
		t.Fatal("X-Request-Id is not set on the response")
	}

	if line.RequestID != responseID {
		t.Errorf("request_id = %q, want %q (same as response header)", line.RequestID, responseID)
	}

	if line.Msg != "http_request" || line.Level != "INFO" {
		t.Errorf("msg/level = %q/%q, want http_request/INFO", line.Msg, line.Level)
	}

	if line.Status != http.StatusCreated || line.Bytes != 5 {
		t.Errorf("status/bytes = %d/%d, want 201/5", line.Status, line.Bytes)
	}

	// クエリ文字列はログに出さない。
	if line.Path != "/tasks" {
		t.Errorf("path = %q, want /tasks", line.Path)
	}

	if line.UserID != nil {
		t.Errorf("user_id = %d, want absent for anonymous request", *line.UserID)
	}
}

func TestAccessLogDefaultsTo200WithoutWriteHeader(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})

	_, line, _ := serve(t, httptest.NewRequest(http.MethodGet, "/health", nil), inner)

	if line.Status != http.StatusOK {
		t.Errorf("status = %d, want 200", line.Status)
	}
}

func TestAccessLogUsesErrorLevelFor5xx(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})

	_, line, _ := serve(t, httptest.NewRequest(http.MethodGet, "/debug/slow", nil), inner)

	if line.Level != "ERROR" {
		t.Errorf("level = %q, want ERROR for 503", line.Level)
	}
}

// Step 5 の再現。内側で WithValue した Context は外側に届かないが、
// LogFields のポインタ経由なら user_id が外側の AccessLog に届く。
func TestAccessLogReceivesUserIDFromInnerMiddleware(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fields, ok := httpx.LogFieldsFrom(r.Context()); ok {
			fields.UserID = 42
		}

		w.WriteHeader(http.StatusOK)
	})

	_, line, _ := serve(t, httptest.NewRequest(http.MethodGet, "/tasks/1", nil), inner)

	if line.UserID == nil || *line.UserID != 42 {
		t.Errorf("user_id = %v, want 42", line.UserID)
	}
}

func TestRequestIDInheritsValidHeader(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/tasks/1", nil)
	req.Header.Set(RequestIDHeader, "my-trace-123")

	rec, line, _ := serve(t, req, http.NotFoundHandler())

	if got := rec.Header().Get(RequestIDHeader); got != "my-trace-123" {
		t.Errorf("response X-Request-Id = %q, want my-trace-123", got)
	}

	if line.RequestID != "my-trace-123" {
		t.Errorf("request_id = %q, want my-trace-123", line.RequestID)
	}
}

func TestRequestIDReplacesInvalidHeader(t *testing.T) {
	for _, id := range []string{
		"has space",
		"line\nbreak",
		strings.Repeat("a", maxRequestIDLength+1),
	} {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set(RequestIDHeader, id)

		rec, _, _ := serve(t, req, http.NotFoundHandler())

		got := rec.Header().Get(RequestIDHeader)
		if got == id || got == "" {
			t.Errorf("invalid id %q was not replaced (got %q)", id, got)
		}
	}
}

func TestAccessLogDoesNotLeakSecrets(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(`{"password":"p@ss"}`))
	req.Header.Set("Cookie", httpx.SessionCookieName+"=secret-session-id")
	req.Header.Set("Authorization", "Bearer secret-token")

	_, _, raw := serve(t, req, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	for _, secret := range []string{"secret-session-id", "secret-token", "p@ss", httpx.SessionCookieName} {
		if strings.Contains(raw, secret) {
			t.Errorf("log contains %q: %s", secret, raw)
		}
	}
}
