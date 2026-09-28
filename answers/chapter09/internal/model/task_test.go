package model_test

import (
	"testing"

	"example.com/go-kanban/internal/model"
)

// Table Driven Test:
// 条件をsliceへまとめ、同じ検証処理を繰り返す。
// 条件の追加が1行で済み、どの条件が落ちたかも名前で分かる。
func TestCanTransition(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		from string
		to   string
		want bool
	}{
		{"todo to doing", model.StatusTodo, model.StatusDoing, true},
		{"doing to done", model.StatusDoing, model.StatusDone, true},
		{"doing back to todo", model.StatusDoing, model.StatusTodo, true},
		{"done back to doing", model.StatusDone, model.StatusDoing, true},
		{"todo to done is not allowed", model.StatusTodo, model.StatusDone, false},
		{"done to todo is not allowed", model.StatusDone, model.StatusTodo, false},
		{"todo to todo is not a transition", model.StatusTodo, model.StatusTodo, false},
		{"doing to doing is not a transition", model.StatusDoing, model.StatusDoing, false},
		{"done to done is not a transition", model.StatusDone, model.StatusDone, false},
		{"unknown source status", "archived", model.StatusTodo, false},
		{"unknown target status", model.StatusTodo, "archived", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := model.CanTransition(tt.from, tt.to); got != tt.want {
				t.Errorf("CanTransition(%q, %q) = %v, want %v", tt.from, tt.to, got, tt.want)
			}
		})
	}
}

func TestIsValidStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		status string
		want   bool
	}{
		{model.StatusTodo, true},
		{model.StatusDoing, true},
		{model.StatusDone, true},
		{"archived", false},
		{"", false},
		{"TODO", false},
	}

	for _, tt := range tests {
		t.Run(tt.status, func(t *testing.T) {
			t.Parallel()

			if got := model.IsValidStatus(tt.status); got != tt.want {
				t.Errorf("IsValidStatus(%q) = %v, want %v", tt.status, got, tt.want)
			}
		})
	}
}

// 日本語など、1文字が複数byteになる入力でも
// 「100文字」で判定できることを確認する。
func TestTitleLengthCountsRunesNotBytes(t *testing.T) {
	t.Parallel()

	input := model.CreateTaskInput{Title: "", Priority: "low"}
	for range model.MaxTitleLength {
		input.Title += "あ" // 3 bytes / 1 rune
	}

	input.Normalize()

	if err := input.Validate(); err != nil {
		t.Fatalf("100 runes should be valid, got %v", err)
	}

	input.Title += "あ"

	if err := input.Validate(); err == nil {
		t.Fatal("101 runes should be rejected")
	}
}

func TestCreateTaskInputValidate(t *testing.T) {
	t.Parallel()

	longTitle := ""
	for range model.MaxTitleLength + 1 {
		longTitle += "a"
	}

	tests := []struct {
		name    string
		input   model.CreateTaskInput
		wantErr bool
	}{
		{"valid", model.CreateTaskInput{Title: "write docs", Priority: "high"}, false},
		{"empty title", model.CreateTaskInput{Title: "", Priority: "high"}, true},
		{"whitespace title", model.CreateTaskInput{Title: "   ", Priority: "high"}, true},
		{"title too long", model.CreateTaskInput{Title: longTitle, Priority: "low"}, true},
		{"invalid priority", model.CreateTaskInput{Title: "ok", Priority: "SUPER_HIGH"}, true},
		{"empty priority defaults to medium", model.CreateTaskInput{Title: "ok"}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			input := tt.input
			input.Normalize()

			err := input.Validate()

			if tt.wantErr && err == nil {
				t.Fatalf("expected error, got nil")
			}

			if !tt.wantErr && err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
		})
	}
}
