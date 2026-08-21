package service

import (
	"context"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/authz"
	"github.com/robfig/cron/v3"
)

var scheduledTestCronParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)

// ScheduledTestService provides CRUD operations for scheduled test plans and results.
type ScheduledTestService struct {
	planRepo   ScheduledTestPlanRepository
	resultRepo ScheduledTestResultRepository
}

// NewScheduledTestService creates a new ScheduledTestService.
func NewScheduledTestService(
	planRepo ScheduledTestPlanRepository,
	resultRepo ScheduledTestResultRepository,
) *ScheduledTestService {
	return &ScheduledTestService{
		planRepo:   planRepo,
		resultRepo: resultRepo,
	}
}

// AdminCreatePlan is the authenticated admin facade for creating a plan.
func (s *ScheduledTestService) AdminCreatePlan(ctx context.Context, actor authz.Actor, plan *ScheduledTestPlan) (*ScheduledTestPlan, error) {
	if err := ValidateAdminResourceActor(actor); err != nil {
		return nil, err
	}
	return s.CreatePlan(ctx, plan)
}

// AdminGetPlan is the authenticated admin facade for retrieving a plan.
func (s *ScheduledTestService) AdminGetPlan(ctx context.Context, actor authz.Actor, id int64) (*ScheduledTestPlan, error) {
	if err := ValidateAdminResourceActor(actor); err != nil {
		return nil, err
	}
	return s.GetPlan(ctx, id)
}

// AdminListPlansByAccount is the authenticated admin facade for account plans.
func (s *ScheduledTestService) AdminListPlansByAccount(ctx context.Context, actor authz.Actor, accountID int64) ([]*ScheduledTestPlan, error) {
	if err := ValidateAdminResourceActor(actor); err != nil {
		return nil, err
	}
	return s.ListPlansByAccount(ctx, accountID)
}

// AdminUpdatePlan is the authenticated admin facade for updating a plan.
func (s *ScheduledTestService) AdminUpdatePlan(ctx context.Context, actor authz.Actor, plan *ScheduledTestPlan) (*ScheduledTestPlan, error) {
	if err := ValidateAdminResourceActor(actor); err != nil {
		return nil, err
	}
	return s.UpdatePlan(ctx, plan)
}

// AdminDeletePlan is the authenticated admin facade for deleting a plan.
func (s *ScheduledTestService) AdminDeletePlan(ctx context.Context, actor authz.Actor, id int64) error {
	if err := ValidateAdminResourceActor(actor); err != nil {
		return err
	}
	return s.DeletePlan(ctx, id)
}

// AdminListResults is the authenticated admin facade for plan results.
func (s *ScheduledTestService) AdminListResults(ctx context.Context, actor authz.Actor, planID int64, limit int) ([]*ScheduledTestResult, error) {
	if err := ValidateAdminResourceActor(actor); err != nil {
		return nil, err
	}
	return s.ListResults(ctx, planID, limit)
}

// CreatePlan validates the cron expression, computes next_run_at, and persists the plan.
func (s *ScheduledTestService) CreatePlan(ctx context.Context, plan *ScheduledTestPlan) (*ScheduledTestPlan, error) {
	nextRun, err := computeNextRun(plan.CronExpression, time.Now())
	if err != nil {
		return nil, fmt.Errorf("invalid cron expression: %w", err)
	}
	plan.NextRunAt = &nextRun

	if plan.MaxResults <= 0 {
		plan.MaxResults = 50
	}

	return s.planRepo.Create(ctx, plan)
}

// GetPlan retrieves a plan by ID.
func (s *ScheduledTestService) GetPlan(ctx context.Context, id int64) (*ScheduledTestPlan, error) {
	return s.planRepo.GetByID(ctx, id)
}

// ListPlansByAccount returns all plans for a given account.
func (s *ScheduledTestService) ListPlansByAccount(ctx context.Context, accountID int64) ([]*ScheduledTestPlan, error) {
	return s.planRepo.ListByAccountID(ctx, accountID)
}

// UpdatePlan validates cron and updates the plan.
func (s *ScheduledTestService) UpdatePlan(ctx context.Context, plan *ScheduledTestPlan) (*ScheduledTestPlan, error) {
	nextRun, err := computeNextRun(plan.CronExpression, time.Now())
	if err != nil {
		return nil, fmt.Errorf("invalid cron expression: %w", err)
	}
	plan.NextRunAt = &nextRun

	return s.planRepo.Update(ctx, plan)
}

// DeletePlan removes a plan and its results (via CASCADE).
func (s *ScheduledTestService) DeletePlan(ctx context.Context, id int64) error {
	return s.planRepo.Delete(ctx, id)
}

// ListResults returns the most recent results for a plan.
func (s *ScheduledTestService) ListResults(ctx context.Context, planID int64, limit int) ([]*ScheduledTestResult, error) {
	if limit <= 0 {
		limit = 50
	}
	return s.resultRepo.ListByPlanID(ctx, planID, limit)
}

// SaveResult inserts a result and prunes old entries beyond maxResults.
func (s *ScheduledTestService) SaveResult(ctx context.Context, planID int64, maxResults int, result *ScheduledTestResult) error {
	result.PlanID = planID
	if _, err := s.resultRepo.Create(ctx, result); err != nil {
		return err
	}
	return s.resultRepo.PruneOldResults(ctx, planID, maxResults)
}

func computeNextRun(cronExpr string, from time.Time) (time.Time, error) {
	sched, err := scheduledTestCronParser.Parse(cronExpr)
	if err != nil {
		return time.Time{}, err
	}
	return sched.Next(from), nil
}
