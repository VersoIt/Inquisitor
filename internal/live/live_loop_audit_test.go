package live_test

import (
	"strings"
	"testing"
	"time"

	"github.com/VersoIt/Inquisitor/internal/live"
)

func TestValidateLiveLoopRunStartedRejectsUnsafeInputsTableDriven(t *testing.T) {
	tests := []struct {
		name       string
		mutate     func(*live.LiveLoopRunStarted)
		wantErrSub string
	}{
		{name: "valid", mutate: func(*live.LiveLoopRunStarted) {}},
		{name: "missing run id", mutate: func(r *live.LiveLoopRunStarted) { r.RunID = "" }, wantErrSub: "run_id"},
		{name: "untrimmed run id", mutate: func(r *live.LiveLoopRunStarted) { r.RunID = " live_loop_domain_0001 " }, wantErrSub: "trimmed"},
		{name: "missing started at", mutate: func(r *live.LiveLoopRunStarted) { r.StartedAt = time.Time{} }, wantErrSub: "started_at"},
		{name: "zero max iterations", mutate: func(r *live.LiveLoopRunStarted) { r.MaxIterations = 0 }, wantErrSub: "max_iterations"},
		{name: "zero runtime", mutate: func(r *live.LiveLoopRunStarted) { r.MaxRuntime = 0 }, wantErrSub: "max_runtime"},
		{name: "zero timeout", mutate: func(r *live.LiveLoopRunStarted) { r.IterationTimeout = 0 }, wantErrSub: "iteration_timeout"},
		{name: "timeout exceeds runtime", mutate: func(r *live.LiveLoopRunStarted) { r.IterationTimeout = 2 * time.Minute }, wantErrSub: "must not exceed"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			run := validLiveLoopRunStarted()
			tt.mutate(&run)

			err := live.ValidateLiveLoopRunStarted(run)
			if tt.wantErrSub != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("validate run started: %v", err)
			}
		})
	}
}

func TestValidateLiveLoopRunFinishedRejectsUnsafeInputsTableDriven(t *testing.T) {
	tests := []struct {
		name       string
		mutate     func(*live.LiveLoopRunFinished)
		wantErrSub string
	}{
		{name: "valid completed", mutate: func(*live.LiveLoopRunFinished) {}},
		{name: "missing run id", mutate: func(r *live.LiveLoopRunFinished) { r.RunID = "" }, wantErrSub: "run_id"},
		{name: "missing finished at", mutate: func(r *live.LiveLoopRunFinished) { r.FinishedAt = time.Time{} }, wantErrSub: "finished_at"},
		{name: "finished before started", mutate: func(r *live.LiveLoopRunFinished) { r.FinishedAt = r.StartedAt.Add(-time.Nanosecond) }, wantErrSub: "finished_at"},
		{name: "running status rejected", mutate: func(r *live.LiveLoopRunFinished) { r.Status = live.LiveLoopRunStatusRunning }, wantErrSub: "status"},
		{name: "unknown status", mutate: func(r *live.LiveLoopRunFinished) { r.Status = "BROKEN" }, wantErrSub: "status"},
		{name: "negative attempted", mutate: func(r *live.LiveLoopRunFinished) { r.IterationsAttempted = -1 }, wantErrSub: "iterations_attempted"},
		{name: "negative succeeded", mutate: func(r *live.LiveLoopRunFinished) { r.IterationsSucceeded = -1 }, wantErrSub: "iterations_succeeded"},
		{name: "succeeded exceeds attempted", mutate: func(r *live.LiveLoopRunFinished) { r.IterationsSucceeded = r.IterationsAttempted + 1 }, wantErrSub: "must not exceed"},
		{name: "completed outside bounds", mutate: func(r *live.LiveLoopRunFinished) { r.CompletedWithinBounds = false }, wantErrSub: "within bounds"},
		{name: "failed without error", mutate: func(r *live.LiveLoopRunFinished) {
			r.Status = live.LiveLoopRunStatusFailed
			r.CompletedWithinBounds = false
			r.Error = ""
		}, wantErrSub: "error"},
		{name: "untrimmed stop reason", mutate: func(r *live.LiveLoopRunFinished) { r.StopReason = " MAX_ITERATIONS " }, wantErrSub: "stop_reason"},
		{name: "untrimmed error", mutate: func(r *live.LiveLoopRunFinished) {
			r.Status = live.LiveLoopRunStatusFailed
			r.CompletedWithinBounds = false
			r.Error = " failed "
		}, wantErrSub: "error"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			run := validLiveLoopRunFinished()
			tt.mutate(&run)

			err := live.ValidateLiveLoopRunFinished(run)
			if tt.wantErrSub != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("validate run finished: %v", err)
			}
		})
	}
}

func TestValidateLiveLoopIterationAuditRejectsUnsafeInputsTableDriven(t *testing.T) {
	tests := []struct {
		name       string
		mutate     func(*live.LiveLoopIterationAudit)
		wantErrSub string
	}{
		{name: "valid submitted", mutate: func(*live.LiveLoopIterationAudit) {}},
		{name: "missing run id", mutate: func(i *live.LiveLoopIterationAudit) { i.RunID = "" }, wantErrSub: "run_id"},
		{name: "missing run started at", mutate: func(i *live.LiveLoopIterationAudit) { i.RunStartedAt = time.Time{} }, wantErrSub: "started_at"},
		{name: "zero iteration", mutate: func(i *live.LiveLoopIterationAudit) { i.Iteration = 0 }, wantErrSub: "iteration"},
		{name: "unknown action", mutate: func(i *live.LiveLoopIterationAudit) { i.Action = "TRADE" }, wantErrSub: "action"},
		{name: "missing started at", mutate: func(i *live.LiveLoopIterationAudit) { i.StartedAt = time.Time{} }, wantErrSub: "started_at"},
		{name: "missing finished at", mutate: func(i *live.LiveLoopIterationAudit) { i.FinishedAt = time.Time{} }, wantErrSub: "finished_at"},
		{name: "finished before started", mutate: func(i *live.LiveLoopIterationAudit) { i.FinishedAt = i.StartedAt.Add(-time.Nanosecond) }, wantErrSub: "finished_at"},
		{name: "stop without reason", mutate: func(i *live.LiveLoopIterationAudit) {
			i.Action = live.LiveLoopAuditIterationActionStop
			i.RequestStop = true
			i.Reason = ""
		}, wantErrSub: "reason"},
		{name: "untrimmed decision", mutate: func(i *live.LiveLoopIterationAudit) { i.DecisionID = " risk_decision_live_0001 " }, wantErrSub: "decision_id"},
		{name: "submitted missing decision", mutate: func(i *live.LiveLoopIterationAudit) { i.DecisionID = "" }, wantErrSub: "decision_id"},
		{name: "exchange submitted missing client id", mutate: func(i *live.LiveLoopIterationAudit) {
			i.Action = live.LiveLoopAuditIterationActionNone
			i.ClientOrderID = ""
			i.ExchangeSubmitted = true
		}, wantErrSub: "client_order_id"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			iteration := validLiveLoopIterationAudit()
			tt.mutate(&iteration)

			err := live.ValidateLiveLoopIterationAudit(iteration)
			if tt.wantErrSub != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("validate iteration audit: %v", err)
			}
		})
	}
}

func TestLiveLoopAuditStatsAndStatusHelpers(t *testing.T) {
	if got := (live.LiveLoopAuditStats{Inserted: 1, Updated: 2, Skipped: 3}).Total(); got != 6 {
		t.Fatalf("stats total mismatch: %d", got)
	}
	if live.LiveLoopRunStatusFromError(true, nil) != live.LiveLoopRunStatusCompleted {
		t.Fatal("nil error within bounds should be completed")
	}
	if live.LiveLoopRunStatusFromError(false, nil) != live.LiveLoopRunStatusFailed {
		t.Fatal("outside bounds should be failed")
	}
	if live.LiveLoopAuditError(nil) != "" || live.LiveLoopAuditError(assertErr(" failure ")) != "failure" {
		t.Fatal("audit error formatting mismatch")
	}
}

type assertErr string

func (e assertErr) Error() string {
	return string(e)
}

func validLiveLoopRunStarted() live.LiveLoopRunStarted {
	startedAt := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	return live.LiveLoopRunStarted{
		RunID:            "live_loop_domain_0001",
		StartedAt:        startedAt,
		MaxIterations:    3,
		MaxRuntime:       time.Minute,
		IterationTimeout: 5 * time.Second,
	}
}

func validLiveLoopRunFinished() live.LiveLoopRunFinished {
	startedAt := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	return live.LiveLoopRunFinished{
		RunID:                 "live_loop_domain_0001",
		StartedAt:             startedAt,
		FinishedAt:            startedAt.Add(3 * time.Second),
		Status:                live.LiveLoopRunStatusCompleted,
		PreflightChecked:      true,
		PreflightReady:        true,
		IterationsAttempted:   3,
		IterationsSucceeded:   3,
		StopReason:            "MAX_ITERATIONS",
		StopDetails:           "max live loop iterations reached",
		CompletedWithinBounds: true,
	}
}

func validLiveLoopIterationAudit() live.LiveLoopIterationAudit {
	startedAt := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	return live.LiveLoopIterationAudit{
		RunID:             "live_loop_domain_0001",
		RunStartedAt:      startedAt,
		Iteration:         1,
		Action:            live.LiveLoopAuditIterationActionSubmitted,
		RequestStop:       true,
		Reason:            "live_order_submitted",
		DecisionID:        "risk_decision_live_0001",
		SubmissionID:      "live_submission_0001",
		ClientOrderID:     "live_client_0001",
		ExchangeSubmitted: true,
		StartedAt:         startedAt.Add(time.Second),
		FinishedAt:        startedAt.Add(2 * time.Second),
	}
}
