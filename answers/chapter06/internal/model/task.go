package model

import "strings"

const MaxTitleLength = 100

// Status
const (
	StatusTodo  = "todo"
	StatusDoing = "doing"
	StatusDone  = "done"
)

// allowedTransitions は Status の遷移規則。
// 「どの状態からどの状態へ行けるか」をデータとして持つと、
// テストでも表として書け、分岐の書き漏らしに気づきやすい。
var allowedTransitions = map[string][]string{
	StatusTodo:  {StatusDoing},
	StatusDoing: {StatusTodo, StatusDone},
	StatusDone:  {StatusDoing},
}

// CanTransition は Status 遷移が業務上許可されるかを判断する。
func CanTransition(from, to string) bool {
	if from == to {
		return false
	}

	for _, allowed := range allowedTransitions[from] {
		if allowed == to {
			return true
		}
	}

	return false
}

func IsValidStatus(status string) bool {
	_, ok := allowedTransitions[status]
	return ok
}

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
