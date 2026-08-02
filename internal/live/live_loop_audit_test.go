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

func TestSummarizeLiveLoopAuditReviewTableDriven(t *testing.T) {
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	completed := liveLoopRunAuditWithStatus("live_loop_review_completed_0001", now, live.LiveLoopRunStatusCompleted)
	failed := liveLoopRunAuditWithStatus("live_loop_review_failed_0001", now.Add(-time.Minute), live.LiveLoopRunStatusFailed)
	running := liveLoopRunAuditWithStatus("live_loop_review_running_0001", now.Add(-2*time.Minute), live.LiveLoopRunStatusRunning)
	invalid := completed
	invalid.RunID = " live_loop_review_invalid_0001 "

	tests := []struct {
		name           string
		runs           []live.LiveLoopRunAudit
		wantStatus     live.LiveLoopAuditReviewStatus
		wantRunID      string
		wantReasonSub  string
		wantAction     bool
		wantErrSub     string
		wantKnownState bool
	}{
		{
			name:           "empty audit is clear",
			wantStatus:     live.LiveLoopAuditReviewStatusClear,
			wantReasonSub:  "no recent",
			wantKnownState: true,
		},
		{
			name:           "completed runs are clear",
			runs:           []live.LiveLoopRunAudit{completed},
			wantStatus:     live.LiveLoopAuditReviewStatusClear,
			wantReasonSub:  "no running or failed",
			wantKnownState: true,
		},
		{
			name:           "failed run requires review",
			runs:           []live.LiveLoopRunAudit{completed, failed},
			wantStatus:     live.LiveLoopAuditReviewStatusReview,
			wantRunID:      failed.RunID,
			wantReasonSub:  "FAILED: live loop failed",
			wantAction:     true,
			wantKnownState: true,
		},
		{
			name:           "running run blocks before failed review",
			runs:           []live.LiveLoopRunAudit{failed, running},
			wantStatus:     live.LiveLoopAuditReviewStatusBlocked,
			wantRunID:      running.RunID,
			wantReasonSub:  "still RUNNING",
			wantAction:     true,
			wantKnownState: true,
		},
		{
			name:       "invalid audit run fails closed",
			runs:       []live.LiveLoopRunAudit{invalid},
			wantErrSub: "run_id",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := live.SummarizeLiveLoopAuditReview(tt.runs)
			if tt.wantErrSub != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("summarize review: %v", err)
			}
			if got.Status != tt.wantStatus || got.RunID != tt.wantRunID || got.OperatorActionRequired() != tt.wantAction {
				t.Fatalf("review mismatch: got %#v want status=%s run=%s action=%t", got, tt.wantStatus, tt.wantRunID, tt.wantAction)
			}
			if !strings.Contains(got.Reason, tt.wantReasonSub) {
				t.Fatalf("expected reason to contain %q, got %q", tt.wantReasonSub, got.Reason)
			}
			if live.KnownLiveLoopAuditReviewStatus(got.Status) != tt.wantKnownState {
				t.Fatalf("known status mismatch for %s", got.Status)
			}
		})
	}
	if live.KnownLiveLoopAuditReviewStatus("BROKEN") {
		t.Fatal("BROKEN must not be a known live-loop audit review status")
	}
}

func TestValidateLiveLoopRunAuditRejectsUnsafeInputsTableDriven(t *testing.T) {
	tests := []struct {
		name       string
		mutate     func(*live.LiveLoopRunAudit)
		wantErrSub string
	}{
		{name: "valid completed", mutate: func(*live.LiveLoopRunAudit) {}},
		{name: "valid running", mutate: func(r *live.LiveLoopRunAudit) {
			r.Status = live.LiveLoopRunStatusRunning
			r.FinishedAt = time.Time{}
			r.CompletedWithinBounds = false
			r.IterationsAttempted = 0
			r.IterationsSucceeded = 0
			r.StopReason = ""
			r.Iterations = nil
		}},
		{name: "missing run id", mutate: func(r *live.LiveLoopRunAudit) { r.RunID = "" }, wantErrSub: "run_id"},
		{name: "unknown status", mutate: func(r *live.LiveLoopRunAudit) { r.Status = "BROKEN" }, wantErrSub: "status"},
		{name: "running with finish", mutate: func(r *live.LiveLoopRunAudit) {
			r.Status = live.LiveLoopRunStatusRunning
			r.CompletedWithinBounds = false
		}, wantErrSub: "finished_at"},
		{name: "completed outside bounds", mutate: func(r *live.LiveLoopRunAudit) { r.CompletedWithinBounds = false }, wantErrSub: "within bounds"},
		{name: "failed without error", mutate: func(r *live.LiveLoopRunAudit) {
			r.Status = live.LiveLoopRunStatusFailed
			r.CompletedWithinBounds = false
			r.Error = ""
		}, wantErrSub: "error"},
		{name: "iteration run mismatch", mutate: func(r *live.LiveLoopRunAudit) { r.Iterations[0].RunID = "other_run" }, wantErrSub: "run_id must match"},
		{name: "iteration started mismatch", mutate: func(r *live.LiveLoopRunAudit) {
			r.Iterations[0].RunStartedAt = r.StartedAt.Add(time.Second)
		}, wantErrSub: "run_started_at"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			run := validLiveLoopRunAudit()
			tt.mutate(&run)

			err := live.ValidateLiveLoopRunAudit(run)
			if tt.wantErrSub != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("validate run audit: %v", err)
			}
		})
	}
}

func TestValidateLiveLoopAuditQueryRejectsUnsafeInputsTableDriven(t *testing.T) {
	tests := []struct {
		name       string
		query      live.LiveLoopAuditQuery
		wantErrSub string
	}{
		{name: "valid empty"},
		{name: "valid status", query: live.LiveLoopAuditQuery{Status: live.LiveLoopRunStatusCompleted, Limit: 10}},
		{name: "untrimmed run id", query: live.LiveLoopAuditQuery{RunID: " live_loop_domain_0001 "}, wantErrSub: "run_id"},
		{name: "unknown status", query: live.LiveLoopAuditQuery{Status: "BROKEN"}, wantErrSub: "status"},
		{name: "negative limit", query: live.LiveLoopAuditQuery{Limit: -1}, wantErrSub: "limit"},
		{name: "limit above max", query: live.LiveLoopAuditQuery{Limit: 101}, wantErrSub: "limit"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := live.ValidateLiveLoopAuditQuery(tt.query)
			if tt.wantErrSub != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("validate audit query: %v", err)
			}
		})
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

func validLiveLoopRunAudit() live.LiveLoopRunAudit {
	startedAt := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	iteration := validLiveLoopIterationAudit()
	return live.LiveLoopRunAudit{
		RunID:                 "live_loop_domain_0001",
		StartedAt:             startedAt,
		MaxIterations:         3,
		MaxRuntime:            time.Minute,
		IterationTimeout:      5 * time.Second,
		Status:                live.LiveLoopRunStatusCompleted,
		FinishedAt:            startedAt.Add(3 * time.Second),
		PreflightChecked:      true,
		PreflightReady:        true,
		IterationsAttempted:   1,
		IterationsSucceeded:   1,
		StopReason:            "ITERATION_REQUESTED",
		StopDetails:           "live_order_submitted",
		CompletedWithinBounds: true,
		Iterations:            []live.LiveLoopIterationAudit{iteration},
	}
}

func liveLoopRunAuditWithStatus(runID string, startedAt time.Time, status live.LiveLoopRunStatus) live.LiveLoopRunAudit {
	run := validLiveLoopRunAudit()
	run.RunID = runID
	run.StartedAt = startedAt
	run.FinishedAt = startedAt.Add(3 * time.Second)
	run.Status = status
	for index := range run.Iterations {
		run.Iterations[index].RunID = runID
		run.Iterations[index].RunStartedAt = startedAt
		run.Iterations[index].StartedAt = startedAt.Add(time.Second)
		run.Iterations[index].FinishedAt = startedAt.Add(2 * time.Second)
	}
	switch status {
	case live.LiveLoopRunStatusRunning:
		run.FinishedAt = time.Time{}
		run.CompletedWithinBounds = false
		run.IterationsAttempted = 0
		run.IterationsSucceeded = 0
		run.StopReason = ""
		run.StopDetails = ""
		run.Iterations = nil
	case live.LiveLoopRunStatusFailed:
		run.CompletedWithinBounds = false
		run.IterationsSucceeded = 0
		run.Error = "live loop failed"
	}
	return run
}
