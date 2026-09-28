package model

import "strings"

const MaxTitleLength = 100

var validPriorities = map[string]bool{
	"low":    true,
	"medium": true,
	"high":   true,
}

type Task struct {
	ID          int64  `json:"id"`
	ProjectID   int64  `json:"project_id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Priority    string `json:"priority"`
	Status      string `json:"status"`
	Version     int    `json:"version"`
	AssigneeID  *int64 `json:"assignee_id"`
}

// TaskWithAssignee は Task に担当者のメールアドレスを添えたもの。
// Part 3 の N+1 計測で使う。
type TaskWithAssignee struct {
	Task
	AssigneeEmail string `json:"assignee_email"`
}

// CreateTaskInput は Service への入力。HTTP や JSON には依存しない。
type CreateTaskInput struct {
	ProjectID   int64
	Title       string
	Description string
	Priority    string
}

func (in *CreateTaskInput) Normalize() {
	in.Title = strings.TrimSpace(in.Title)
	in.Description = strings.TrimSpace(in.Description)

	if in.Priority == "" {
		in.Priority = "medium"
	}
}

func (in CreateTaskInput) Validate() error {
	if in.Title == "" {
		return Invalid("title is required")
	}

	if len([]rune(in.Title)) > MaxTitleLength {
		return Invalid("title must be 100 characters or fewer")
	}

	if !validPriorities[in.Priority] {
		return Invalid("priority must be one of: low, medium, high")
	}

	return nil
}
