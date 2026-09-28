package service

import (
	"context"
	"time"

	"github.com/KenzoKasahara/go_kanban_webapp_hands_on/go-kanban/internal/model"
	"github.com/KenzoKasahara/go_kanban_webapp_hands_on/go-kanban/internal/repository"
)

// DebugService は Part 2（SQL Injection）と Part 3（N+1）の検証専用。
// 検証が終わったら、app.go のルーティングと一緒に削除する。
type DebugService struct {
	tasks    repository.DebugTaskQueries
	projects repository.ProjectRepository
}

func NewDebugService(tasks repository.DebugTaskQueries, projects repository.ProjectRepository) *DebugService {
	return &DebugService{tasks: tasks, projects: projects}
}

// SearchUnsafe は認可を通したうえで、文字列連結版の検索を呼ぶ。
// 認可を通しても SQL Injection は防げないことを確かめるため。
func (s *DebugService) SearchUnsafe(ctx context.Context, userID, projectID int64, keyword string) ([]model.Task, error) {
	if _, err := s.projects.RoleOf(ctx, projectID, userID); err != nil {
		return nil, err
	}

	return s.tasks.SearchUnsafe(ctx, projectID, keyword)
}

// NPlusOneResult は N+1 版と JOIN 版の計測結果。
type NPlusOneResult struct {
	NaiveQueries  int
	NaiveDuration time.Duration
	JoinQueries   int
	JoinDuration  time.Duration
	Rows          int
	SameRows      bool
}

func (s *DebugService) CompareNPlusOne(ctx context.Context, userID, projectID int64) (NPlusOneResult, error) {
	if _, err := s.projects.RoleOf(ctx, projectID, userID); err != nil {
		return NPlusOneResult{}, err
	}

	start := time.Now()

	naive, naiveQueries, err := s.tasks.ListWithAssigneeNaive(ctx, projectID)
	if err != nil {
		return NPlusOneResult{}, err
	}

	naiveDuration := time.Since(start)
	start = time.Now()

	joined, joinQueries, err := s.tasks.ListWithAssigneeJoin(ctx, projectID)
	if err != nil {
		return NPlusOneResult{}, err
	}

	joinDuration := time.Since(start)

	return NPlusOneResult{
		NaiveQueries:  naiveQueries,
		NaiveDuration: naiveDuration,
		JoinQueries:   joinQueries,
		JoinDuration:  joinDuration,
		Rows:          len(joined),
		SameRows:      sameRows(naive, joined),
	}, nil
}

// sameRows は2つの実装が同じ結果を返したかを確認する。
// 速くても結果が違えば比較の意味がない。
func sameRows(a, b []model.TaskWithAssignee) bool {
	if len(a) != len(b) {
		return false
	}

	for i := range a {
		if a[i].ID != b[i].ID || a[i].AssigneeEmail != b[i].AssigneeEmail {
			return false
		}
	}

	return true
}
