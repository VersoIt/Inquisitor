package live_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	applive "github.com/VersoIt/Inquisitor/internal/app/live"
	domainlive "github.com/VersoIt/Inquisitor/internal/live"
)

func TestServiceBuildLiveLoopAuditReportSummarizesRuns(t *testing.T) {
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	reader := &fakeLiveLoopAuditReader{runs: []domainlive.LiveLoopRunAudit{
		liveLoopAuditRun(now, domainlive.LiveLoopRunStatusCompleted),
		liveLoopAuditRun(now.Add(-time.Minute), domainlive.LiveLoopRunStatusFailed),
		liveLoopAuditRun(now.Add(-2*time.Minute), domainlive.LiveLoopRunStatusRunning),
	}}
	service := applive.NewService(applive.WithLiveLoopAuditReader(reader))

	got, err := service.BuildLiveLoopAuditReport(context.Background(), applive.LiveLoopAuditReportRequest{
		Status:            domainlive.LiveLoopRunStatusCompleted,
		IncludeIterations: true,
	})
	if err != nil {
		t.Fatalf("build live loop audit report: %v", err)
	}

	if reader.calls != 1 {
		t.Fatalf("reader calls mismatch: %d", reader.calls)
	}
	if reader.query.Limit != 10 || reader.query.Status != domainlive.LiveLoopRunStatusCompleted || !reader.query.IncludeIterations {
		t.Fatalf("query mismatch: %#v", reader.query)
	}
	if got.Summary.Total != 3 || got.Summary.Completed != 1 || got.Summary.Failed != 1 || got.Summary.Running != 1 {
		t.Fatalf("summary mismatch: %#v", got.Summary)
	}
	if len(got.Runs) != 3 || got.Runs[0].RunID == "" {
		t.Fatalf("runs mismatch: %#v", got.Runs)
	}
}

func TestServiceBuildLiveLoopAuditReportRejectsUnsafeInputsTableDriven(t *testing.T) {
	tests := []struct {
		name       string
		service    *applive.Service
		req        applive.LiveLoopAuditReportRequest
		wantErrSub string
	}{
		{
			name:       "missing reader",
			service:    applive.NewService(),
			req:        applive.LiveLoopAuditReportRequest{},
			wantErrSub: "audit reader",
		},
		{
			name:       "invalid status",
			service:    applive.NewService(applive.WithLiveLoopAuditReader(&fakeLiveLoopAuditReader{})),
			req:        applive.LiveLoopAuditReportRequest{Status: "BROKEN"},
			wantErrSub: "status",
		},
		{
			name:       "reader error",
			service:    applive.NewService(applive.WithLiveLoopAuditReader(&fakeLiveLoopAuditReader{err: errors.New("db unavailable")})),
			req:        applive.LiveLoopAuditReportRequest{},
			wantErrSub: "db unavailable",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.service.BuildLiveLoopAuditReport(context.Background(), tt.req)
			if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
		})
	}
}

type fakeLiveLoopAuditReader struct {
	query domainlive.LiveLoopAuditQuery
	runs  []domainlive.LiveLoopRunAudit
	calls int
	err   error
}

func (r *fakeLiveLoopAuditReader) ListLiveLoopRunAudits(_ context.Context, query domainlive.LiveLoopAuditQuery) ([]domainlive.LiveLoopRunAudit, error) {
	r.calls++
	r.query = query
	if r.err != nil {
		return nil, r.err
	}
	return append([]domainlive.LiveLoopRunAudit(nil), r.runs...), nil
}

func liveLoopAuditRun(startedAt time.Time, status domainlive.LiveLoopRunStatus) domainlive.LiveLoopRunAudit {
	run := domainlive.LiveLoopRunAudit{
		RunID:                 "live_loop_audit_app_0001",
		StartedAt:             startedAt,
		MaxIterations:         1,
		MaxRuntime:            time.Minute,
		IterationTimeout:      5 * time.Second,
		Status:                status,
		PreflightChecked:      true,
		PreflightReady:        true,
		IterationsAttempted:   1,
		IterationsSucceeded:   1,
		StopReason:            "ITERATION_REQUESTED",
		StopDetails:           "live_order_submitted",
		CompletedWithinBounds: true,
	}
	switch status {
	case domainlive.LiveLoopRunStatusRunning:
		run.PreflightChecked = false
		run.PreflightReady = false
		run.IterationsAttempted = 0
		run.IterationsSucceeded = 0
		run.StopReason = ""
		run.StopDetails = ""
		run.CompletedWithinBounds = false
	case domainlive.LiveLoopRunStatusFailed:
		run.FinishedAt = startedAt.Add(time.Second)
		run.IterationsSucceeded = 0
		run.CompletedWithinBounds = false
		run.Error = "live loop failed"
	default:
		run.FinishedAt = startedAt.Add(time.Second)
	}
	if status != domainlive.LiveLoopRunStatusRunning {
		run.Iterations = []domainlive.LiveLoopIterationAudit{{
			RunID:             run.RunID,
			RunStartedAt:      run.StartedAt,
			Iteration:         1,
			Action:            domainlive.LiveLoopAuditIterationActionSubmitted,
			RequestStop:       true,
			Reason:            "live_order_submitted",
			DecisionID:        "risk_decision_live_audit_0001",
			SubmissionID:      "live_submission_audit_0001",
			ClientOrderID:     "live_client_audit_0001",
			ExchangeSubmitted: true,
			StartedAt:         run.StartedAt.Add(100 * time.Millisecond),
			FinishedAt:        run.StartedAt.Add(200 * time.Millisecond),
		}}
	}
	return run
}
