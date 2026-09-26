package main

import "strings"

const maxTitleLength = 100

var allowedPriorities = map[string]bool{
	"low":    true,
	"medium": true,
	"high":   true,
}

type CreateTaskRequest struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	Priority    string `json:"priority"`
}

// Normalize は Validation の前に入力を正規化する。
// 「前後の空白だけの title」を空として扱うために必要。
func (input *CreateTaskRequest) Normalize() {
	input.Title = strings.TrimSpace(input.Title)
	input.Description = strings.TrimSpace(input.Description)

	if input.Priority == "" {
		input.Priority = "medium"
	}
}

func (input CreateTaskRequest) Validate() error {
	if input.Title == "" {
		return &ValidationError{Message: "title is required"}
	}

	if len(input.Title) > maxTitleLength {
		return &ValidationError{Message: "title must be 100 characters or fewer"}
	}

	if !allowedPriorities[input.Priority] {
		return &ValidationError{Message: "priority must be one of: low, medium, high"}
	}

	return nil
}
