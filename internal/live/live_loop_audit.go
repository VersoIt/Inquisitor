package live

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

type LiveLoopRunStatus string

const (
	LiveLoopRunStatusRunning   LiveLoopRunStatus = "RUNNING"
	LiveLoopRunStatusCompleted LiveLoopRunStatus = "COMPLETED"
	LiveLoopRunStatusFailed    LiveLoopRunStatus = "FAILED"
)

type LiveLoopAuditReviewStatus string

const (
	LiveLoopAuditReviewStatusClear   LiveLoopAuditReviewStatus = "CLEAR"
	LiveLoopAuditReviewStatusReview  LiveLoopAuditReviewStatus = "REVIEW"
	LiveLoopAuditReviewStatusBlocked LiveLoopAuditReviewStatus = "BLOCKED"
)

type LiveLoopAuditIterationAction string

const (
	LiveLoopAuditIterationActionNone      LiveLoopAuditIterationAction = "NONE"
	LiveLoopAuditIterationActionSubmitted LiveLoopAuditIterationAction = "SUBMITTED"
	LiveLoopAuditIterationActionStop      LiveLoopAuditIterationAction = "STOP"
)

type LiveLoopRunStarted struct {
	RunID            string
	StartedAt        time.Time
	MaxIterations    int
	MaxRuntime       time.Duration
	IterationTimeout time.Duration
}

type LiveLoopRunFinished struct {
	RunID                 string
	StartedAt             time.Time
	FinishedAt            time.Time
	Status                LiveLoopRunStatus
	PreflightChecked      bool
	PreflightReady        bool
	IterationsAttempted   int
	IterationsSucceeded   int
	StopReason            string
	StopDetails           string
	Error                 string
	CompletedWithinBounds bool
}

type LiveLoopIterationAudit struct {
	RunID             string
	RunStartedAt      time.Time
	Iteration         int
	Action            LiveLoopAuditIterationAction
	RequestStop       bool
	Reason            string
	DecisionID        string
	SubmissionID      string
	ClientOrderID     string
	ExchangeSubmitted bool
	AlreadySubmitted  bool
	StartedAt         time.Time
	FinishedAt        time.Time
}

type LiveLoopRunAudit struct {
	RunID                 string
	StartedAt             time.Time
	MaxIterations         int
	MaxRuntime            time.Duration
	IterationTimeout      time.Duration
	Status                LiveLoopRunStatus
	FinishedAt            time.Time
	PreflightChecked      bool
	PreflightReady        bool
	IterationsAttempted   int
	IterationsSucceeded   int
	StopReason            string
	StopDetails           string
	Error                 string
	CompletedWithinBounds bool
	Iterations            []LiveLoopIterationAudit
}

type LiveLoopAuditQuery struct {
	RunID             string
	Status            LiveLoopRunStatus
	Limit             int
	IncludeIterations bool
}

type LiveLoopAuditStats struct {
	Inserted int
	Updated  int
	Skipped  int
}

type LiveLoopAuditReview struct {
	Status LiveLoopAuditReviewStatus
	RunID  string
	Reason string
}

type LiveLoopJournal interface {
	RecordLiveLoopRunStarted(ctx context.Context, run LiveLoopRunStarted) (LiveLoopAuditStats, error)
	RecordLiveLoopRunFinished(ctx context.Context, run LiveLoopRunFinished) (LiveLoopAuditStats, error)
	RecordLiveLoopIteration(ctx context.Context, iteration LiveLoopIterationAudit) (LiveLoopAuditStats, error)
}

type LiveLoopAuditReader interface {
	ListLiveLoopRunAudits(ctx context.Context, query LiveLoopAuditQuery) ([]LiveLoopRunAudit, error)
}

func (s LiveLoopAuditStats) Total() int {
	return s.Inserted + s.Updated + s.Skipped
}

func (r LiveLoopAuditReview) OperatorActionRequired() bool {
	return r.Status == LiveLoopAuditReviewStatusReview || r.Status == LiveLoopAuditReviewStatusBlocked
}

func ValidateLiveLoopRunStarted(run LiveLoopRunStarted) error {
	var problems []string
	problems = append(problems, validateLiveLoopRunIdentity(run.RunID, run.StartedAt)...)
	if run.MaxIterations <= 0 {
		problems = append(problems, "max_iterations must be positive")
	}
	if run.MaxRuntime <= 0 {
		problems = append(problems, "max_runtime must be positive")
	}
	if run.IterationTimeout <= 0 {
		problems = append(problems, "iteration_timeout must be positive")
	}
	if run.MaxRuntime > 0 && run.IterationTimeout > run.MaxRuntime {
		problems = append(problems, "iteration_timeout must not exceed max_runtime")
	}
	if len(problems) > 0 {
		return errors.New("live loop run start validation failed: " + strings.Join(problems, "; "))
	}
	return nil
}

func ValidateLiveLoopRunFinished(run LiveLoopRunFinished) error {
	var problems []string
	problems = append(problems, validateLiveLoopRunIdentity(run.RunID, run.StartedAt)...)
	if run.FinishedAt.IsZero() {
		problems = append(problems, "finished_at is required")
	}
	if !run.StartedAt.IsZero() && !run.FinishedAt.IsZero() && run.FinishedAt.Before(run.StartedAt) {
		problems = append(problems, "finished_at must not be before started_at")
	}
	if !KnownLiveLoopRunStatus(run.Status) || run.Status == LiveLoopRunStatusRunning {
		problems = append(problems, "status must be COMPLETED or FAILED")
	}
	if run.IterationsAttempted < 0 {
		problems = append(problems, "iterations_attempted must be non-negative")
	}
	if run.IterationsSucceeded < 0 {
		problems = append(problems, "iterations_succeeded must be non-negative")
	}
	if run.IterationsSucceeded > run.IterationsAttempted {
		problems = append(problems, "iterations_succeeded must not exceed iterations_attempted")
	}
	if run.Status == LiveLoopRunStatusCompleted && !run.CompletedWithinBounds {
		problems = append(problems, "completed run must be completed within bounds")
	}
	if run.Status == LiveLoopRunStatusFailed && strings.TrimSpace(run.Error) == "" {
		problems = append(problems, "failed run requires error")
	}
	if run.StopReason != strings.TrimSpace(run.StopReason) {
		problems = append(problems, "stop_reason must be trimmed")
	}
	if run.Error != strings.TrimSpace(run.Error) {
		problems = append(problems, "error must be trimmed")
	}
	if len(problems) > 0 {
		return errors.New("live loop run finish validation failed: " + strings.Join(problems, "; "))
	}
	return nil
}

func ValidateLiveLoopIterationAudit(iteration LiveLoopIterationAudit) error {
	var problems []string
	problems = append(problems, validateLiveLoopRunIdentity(iteration.RunID, iteration.RunStartedAt)...)
	if iteration.Iteration <= 0 {
		problems = append(problems, "iteration must be positive")
	}
	if !KnownLiveLoopAuditIterationAction(iteration.Action) {
		problems = append(problems, "action must be NONE, SUBMITTED, or STOP")
	}
	if iteration.StartedAt.IsZero() {
		problems = append(problems, "started_at is required")
	}
	if iteration.FinishedAt.IsZero() {
		problems = append(problems, "finished_at is required")
	}
	if !iteration.StartedAt.IsZero() && !iteration.FinishedAt.IsZero() && iteration.FinishedAt.Before(iteration.StartedAt) {
		problems = append(problems, "finished_at must not be before started_at")
	}
	if iteration.RequestStop && strings.TrimSpace(iteration.Reason) == "" {
		problems = append(problems, "stopping iteration requires reason")
	}
	if iteration.Reason != strings.TrimSpace(iteration.Reason) {
		problems = append(problems, "reason must be trimmed")
	}
	for _, item := range []struct {
		name  string
		value string
	}{
		{"decision_id", iteration.DecisionID},
		{"submission_id", iteration.SubmissionID},
		{"client_order_id", iteration.ClientOrderID},
	} {
		if item.value != strings.TrimSpace(item.value) {
			problems = append(problems, item.name+" must be trimmed")
		}
	}
	if iteration.Action == LiveLoopAuditIterationActionSubmitted {
		for _, item := range []struct {
			name  string
			value string
		}{
			{"decision_id", iteration.DecisionID},
			{"submission_id", iteration.SubmissionID},
			{"client_order_id", iteration.ClientOrderID},
		} {
			if strings.TrimSpace(item.value) == "" {
				problems = append(problems, item.name+" is required for submitted iteration")
			}
		}
	}
	if iteration.ExchangeSubmitted && strings.TrimSpace(iteration.ClientOrderID) == "" {
		problems = append(problems, "exchange-submitted iteration requires client_order_id")
	}
	if len(problems) > 0 {
		return errors.New("live loop iteration audit validation failed: " + strings.Join(problems, "; "))
	}
	return nil
}

func ValidateLiveLoopRunAudit(run LiveLoopRunAudit) error {
	var problems []string
	problems = append(problems, validateLiveLoopRunIdentity(run.RunID, run.StartedAt)...)
	if run.MaxIterations <= 0 {
		problems = append(problems, "max_iterations must be positive")
	}
	if run.MaxRuntime <= 0 {
		problems = append(problems, "max_runtime must be positive")
	}
	if run.IterationTimeout <= 0 {
		problems = append(problems, "iteration_timeout must be positive")
	}
	if run.MaxRuntime > 0 && run.IterationTimeout > run.MaxRuntime {
		problems = append(problems, "iteration_timeout must not exceed max_runtime")
	}
	if !KnownLiveLoopRunStatus(run.Status) {
		problems = append(problems, "status must be RUNNING, COMPLETED, or FAILED")
	}
	if run.Status == LiveLoopRunStatusRunning {
		if !run.FinishedAt.IsZero() {
			problems = append(problems, "running run must not include finished_at")
		}
		if run.CompletedWithinBounds {
			problems = append(problems, "running run must not be completed within bounds")
		}
	} else {
		if run.FinishedAt.IsZero() {
			problems = append(problems, "finished_at is required for finished run")
		}
		if !run.StartedAt.IsZero() && !run.FinishedAt.IsZero() && run.FinishedAt.Before(run.StartedAt) {
			problems = append(problems, "finished_at must not be before started_at")
		}
	}
	if run.Status == LiveLoopRunStatusCompleted && !run.CompletedWithinBounds {
		problems = append(problems, "completed run must be completed within bounds")
	}
	if run.Status == LiveLoopRunStatusFailed && strings.TrimSpace(run.Error) == "" {
		problems = append(problems, "failed run requires error")
	}
	if run.IterationsAttempted < 0 {
		problems = append(problems, "iterations_attempted must be non-negative")
	}
	if run.IterationsSucceeded < 0 {
		problems = append(problems, "iterations_succeeded must be non-negative")
	}
	if run.IterationsSucceeded > run.IterationsAttempted {
		problems = append(problems, "iterations_succeeded must not exceed iterations_attempted")
	}
	if run.StopReason != strings.TrimSpace(run.StopReason) {
		problems = append(problems, "stop_reason must be trimmed")
	}
	if run.Error != strings.TrimSpace(run.Error) {
		problems = append(problems, "error must be trimmed")
	}
	for index, iteration := range run.Iterations {
		if err := ValidateLiveLoopIterationAudit(iteration); err != nil {
			problems = append(problems, fmt.Sprintf("iteration[%d]: %s", index, err.Error()))
		}
		if iteration.RunID != run.RunID {
			problems = append(problems, fmt.Sprintf("iteration[%d] run_id must match run", index))
		}
		if !iteration.RunStartedAt.Equal(run.StartedAt) {
			problems = append(problems, fmt.Sprintf("iteration[%d] run_started_at must match run", index))
		}
	}
	if len(problems) > 0 {
		return errors.New("live loop run audit validation failed: " + strings.Join(problems, "; "))
	}
	return nil
}

func ValidateLiveLoopRunAudits(runs []LiveLoopRunAudit) error {
	for index, run := range runs {
		if err := ValidateLiveLoopRunAudit(run); err != nil {
			return fmt.Errorf("live_loop_run_audit[%d]: %w", index, err)
		}
	}
	return nil
}

func ValidateLiveLoopAuditQuery(query LiveLoopAuditQuery) error {
	var problems []string
	if query.RunID != strings.TrimSpace(query.RunID) {
		problems = append(problems, "run_id must be trimmed")
	}
	if query.Status != "" && !KnownLiveLoopRunStatus(query.Status) {
		problems = append(problems, "status must be RUNNING, COMPLETED, or FAILED")
	}
	if query.Limit < 0 {
		problems = append(problems, "limit must be greater than or equal to zero")
	}
	if query.Limit > 100 {
		problems = append(problems, "limit must be no more than 100")
	}
	if len(problems) > 0 {
		return errors.New("live loop audit query validation failed: " + strings.Join(problems, "; "))
	}
	return nil
}

func SummarizeLiveLoopAuditReview(runs []LiveLoopRunAudit) (LiveLoopAuditReview, error) {
	if err := ValidateLiveLoopRunAudits(runs); err != nil {
		return LiveLoopAuditReview{}, err
	}
	for _, run := range runs {
		if run.Status == LiveLoopRunStatusRunning {
			return LiveLoopAuditReview{
				Status: LiveLoopAuditReviewStatusBlocked,
				RunID:  run.RunID,
				Reason: fmt.Sprintf("live-loop run %s is still RUNNING", FormatLiveLoopRunKey(run.RunID, run.StartedAt)),
			}, nil
		}
	}
	for _, run := range runs {
		if run.Status == LiveLoopRunStatusFailed {
			reason := fmt.Sprintf("live-loop run %s FAILED", FormatLiveLoopRunKey(run.RunID, run.StartedAt))
			if strings.TrimSpace(run.Error) != "" {
				reason += ": " + strings.TrimSpace(run.Error)
			}
			return LiveLoopAuditReview{
				Status: LiveLoopAuditReviewStatusReview,
				RunID:  run.RunID,
				Reason: reason,
			}, nil
		}
	}
	if len(runs) == 0 {
		return LiveLoopAuditReview{
			Status: LiveLoopAuditReviewStatusClear,
			Reason: "no recent live-loop audit runs found",
		}, nil
	}
	return LiveLoopAuditReview{
		Status: LiveLoopAuditReviewStatusClear,
		Reason: "recent live-loop audit has no running or failed runs",
	}, nil
}

func KnownLiveLoopRunStatus(status LiveLoopRunStatus) bool {
	switch status {
	case LiveLoopRunStatusRunning, LiveLoopRunStatusCompleted, LiveLoopRunStatusFailed:
		return true
	default:
		return false
	}
}

func KnownLiveLoopAuditReviewStatus(status LiveLoopAuditReviewStatus) bool {
	switch status {
	case LiveLoopAuditReviewStatusClear, LiveLoopAuditReviewStatusReview, LiveLoopAuditReviewStatusBlocked:
		return true
	default:
		return false
	}
}

func KnownLiveLoopAuditIterationAction(action LiveLoopAuditIterationAction) bool {
	switch action {
	case LiveLoopAuditIterationActionNone, LiveLoopAuditIterationActionSubmitted, LiveLoopAuditIterationActionStop:
		return true
	default:
		return false
	}
}

func validateLiveLoopRunIdentity(runID string, startedAt time.Time) []string {
	var problems []string
	if strings.TrimSpace(runID) == "" {
		problems = append(problems, "run_id is required")
	}
	if runID != strings.TrimSpace(runID) {
		problems = append(problems, "run_id must be trimmed")
	}
	if startedAt.IsZero() {
		problems = append(problems, "started_at is required")
	}
	return problems
}

func LiveLoopRunStatusFromError(completedWithinBounds bool, err error) LiveLoopRunStatus {
	if err == nil && completedWithinBounds {
		return LiveLoopRunStatusCompleted
	}
	return LiveLoopRunStatusFailed
}

func LiveLoopAuditError(err error) string {
	if err == nil {
		return ""
	}
	return strings.TrimSpace(err.Error())
}

func LiveLoopAuditDurationNanos(value time.Duration) int64 {
	return int64(value)
}

func FormatLiveLoopRunKey(runID string, startedAt time.Time) string {
	return fmt.Sprintf("%s@%s", strings.TrimSpace(runID), startedAt.UTC().Format(time.RFC3339Nano))
}
