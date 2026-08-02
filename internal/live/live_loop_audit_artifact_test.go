package live_test

import (
	"strings"
	"testing"
	"time"

	domainlive "github.com/VersoIt/Inquisitor/internal/live"
)

func TestValidateLiveLoopAuditArtifactTableDriven(t *testing.T) {
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	completed := liveLoopAuditArtifactRun("live_loop_artifact_completed_0001", now.Add(-time.Minute), domainlive.LiveLoopRunStatusCompleted)
	failed := liveLoopAuditArtifactRun("live_loop_artifact_failed_0001", now.Add(-2*time.Minute), domainlive.LiveLoopRunStatusFailed)
	running := liveLoopAuditArtifactRun("live_loop_artifact_running_0001", now.Add(-3*time.Minute), domainlive.LiveLoopRunStatusRunning)

	tests := []struct {
		name       string
		artifact   domainlive.LiveLoopAuditArtifact
		mutate     func(*domainlive.LiveLoopAuditArtifact)
		wantErrSub string
	}{
		{name: "valid completed audit artifact", artifact: validLiveLoopAuditArtifact(t, now, []domainlive.LiveLoopRunAudit{completed}, true)},
		{name: "valid empty audit artifact", artifact: validLiveLoopAuditArtifact(t, now, nil, true)},
		{name: "valid failed audit artifact requires review", artifact: validLiveLoopAuditArtifact(t, now, []domainlive.LiveLoopRunAudit{failed}, true)},
		{name: "valid running audit artifact is blocked", artifact: validLiveLoopAuditArtifact(t, now, []domainlive.LiveLoopRunAudit{running}, true)},
		{name: "bad schema", artifact: validLiveLoopAuditArtifact(t, now, []domainlive.LiveLoopRunAudit{completed}, true), mutate: func(a *domainlive.LiveLoopAuditArtifact) {
			a.SchemaVersion = "old"
		}, wantErrSub: "schema_version"},
		{name: "missing created at", artifact: validLiveLoopAuditArtifact(t, now, []domainlive.LiveLoopRunAudit{completed}, true), mutate: func(a *domainlive.LiveLoopAuditArtifact) {
			a.CreatedAt = time.Time{}
		}, wantErrSub: "created_at"},
		{name: "untrimmed config path", artifact: validLiveLoopAuditArtifact(t, now, []domainlive.LiveLoopRunAudit{completed}, true), mutate: func(a *domainlive.LiveLoopAuditArtifact) {
			a.ConfigPath = " configs/live.local.yaml "
		}, wantErrSub: "config_path"},
		{name: "query run id mismatch", artifact: validLiveLoopAuditArtifact(t, now, []domainlive.LiveLoopRunAudit{completed}, true), mutate: func(a *domainlive.LiveLoopAuditArtifact) {
			a.Query.RunID = "live_loop_other_0001"
		}, wantErrSub: "query.run_id"},
		{name: "query status mismatch", artifact: validLiveLoopAuditArtifact(t, now, []domainlive.LiveLoopRunAudit{completed}, true), mutate: func(a *domainlive.LiveLoopAuditArtifact) {
			a.Query.Status = domainlive.LiveLoopRunStatusFailed
		}, wantErrSub: "query.status"},
		{name: "iterations rejected when query excludes them", artifact: validLiveLoopAuditArtifact(t, now, []domainlive.LiveLoopRunAudit{completed}, true), mutate: func(a *domainlive.LiveLoopAuditArtifact) {
			a.Query.IncludeIterations = false
		}, wantErrSub: "include_iterations"},
		{name: "summary total mismatch", artifact: validLiveLoopAuditArtifact(t, now, []domainlive.LiveLoopRunAudit{completed}, true), mutate: func(a *domainlive.LiveLoopAuditArtifact) {
			a.Summary.Total = 2
		}, wantErrSub: "summary.total"},
		{name: "summary review mismatch", artifact: validLiveLoopAuditArtifact(t, now, []domainlive.LiveLoopRunAudit{failed}, true), mutate: func(a *domainlive.LiveLoopAuditArtifact) {
			a.Summary.ReviewStatus = domainlive.LiveLoopAuditReviewStatusClear
		}, wantErrSub: "summary.review_status"},
		{name: "summary action mismatch", artifact: validLiveLoopAuditArtifact(t, now, []domainlive.LiveLoopRunAudit{failed}, true), mutate: func(a *domainlive.LiveLoopAuditArtifact) {
			a.Summary.OperatorActionRequired = false
		}, wantErrSub: "summary.operator_action_required"},
		{name: "invalid duration", artifact: validLiveLoopAuditArtifact(t, now, []domainlive.LiveLoopRunAudit{completed}, true), mutate: func(a *domainlive.LiveLoopAuditArtifact) {
			a.Runs[0].MaxRuntime = "soon"
		}, wantErrSub: "max_runtime"},
		{name: "invalid run validation fails closed", artifact: validLiveLoopAuditArtifact(t, now, []domainlive.LiveLoopRunAudit{running}, true), mutate: func(a *domainlive.LiveLoopAuditArtifact) {
			finishedAt := now
			a.Runs[0].FinishedAt = &finishedAt
		}, wantErrSub: "running run must not include finished_at"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			artifact := cloneLiveLoopAuditArtifact(tt.artifact)
			if tt.mutate != nil {
				tt.mutate(&artifact)
			}
			err := domainlive.ValidateLiveLoopAuditArtifact(artifact)
			if tt.wantErrSub != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("validate audit artifact: %v", err)
			}
		})
	}
}

func validLiveLoopAuditArtifact(
	t *testing.T,
	createdAt time.Time,
	runs []domainlive.LiveLoopRunAudit,
	includeIterations bool,
) domainlive.LiveLoopAuditArtifact {
	t.Helper()

	review, err := domainlive.SummarizeLiveLoopAuditReview(runs)
	if err != nil {
		t.Fatalf("review: %v", err)
	}
	artifact := domainlive.LiveLoopAuditArtifact{
		SchemaVersion: domainlive.LiveLoopAuditArtifactSchemaVersion,
		CreatedAt:     createdAt,
		ConfigPath:    "configs/live.local.yaml",
		Query: domainlive.LiveLoopAuditArtifactQuery{
			Limit:             10,
			IncludeIterations: includeIterations,
		},
		Summary: domainlive.LiveLoopAuditArtifactSummary{
			Total:                  len(runs),
			ReviewStatus:           review.Status,
			ReviewRunID:            review.RunID,
			ReviewReason:           review.Reason,
			OperatorActionRequired: review.OperatorActionRequired(),
		},
	}
	for _, run := range runs {
		switch run.Status {
		case domainlive.LiveLoopRunStatusRunning:
			artifact.Summary.Running++
		case domainlive.LiveLoopRunStatusCompleted:
			artifact.Summary.Completed++
		case domainlive.LiveLoopRunStatusFailed:
			artifact.Summary.Failed++
		}
		artifact.Runs = append(artifact.Runs, liveLoopAuditArtifactRunFromDomain(run, includeIterations))
	}
	return artifact
}

func liveLoopAuditArtifactRun(runID string, startedAt time.Time, status domainlive.LiveLoopRunStatus) domainlive.LiveLoopRunAudit {
	run := domainlive.LiveLoopRunAudit{
		RunID:                 runID,
		StartedAt:             startedAt,
		MaxIterations:         1,
		MaxRuntime:            15 * time.Second,
		IterationTimeout:      10 * time.Second,
		Status:                status,
		FinishedAt:            startedAt.Add(2 * time.Second),
		PreflightChecked:      true,
		PreflightReady:        true,
		IterationsAttempted:   1,
		IterationsSucceeded:   1,
		StopReason:            "ITERATION_REQUESTED",
		StopDetails:           "live_order_submitted",
		CompletedWithinBounds: true,
		Iterations: []domainlive.LiveLoopIterationAudit{{
			RunID:             runID,
			RunStartedAt:      startedAt,
			Iteration:         1,
			Action:            domainlive.LiveLoopAuditIterationActionSubmitted,
			RequestStop:       true,
			Reason:            "live_order_submitted",
			DecisionID:        "risk_decision_live_audit_artifact_0001",
			SubmissionID:      "live_submission_audit_artifact_0001",
			ClientOrderID:     "live_client_audit_artifact_0001",
			ExchangeSubmitted: true,
			StartedAt:         startedAt.Add(time.Second),
			FinishedAt:        startedAt.Add(2 * time.Second),
		}},
	}
	switch status {
	case domainlive.LiveLoopRunStatusRunning:
		run.FinishedAt = time.Time{}
		run.PreflightChecked = false
		run.PreflightReady = false
		run.IterationsAttempted = 0
		run.IterationsSucceeded = 0
		run.StopReason = ""
		run.StopDetails = ""
		run.CompletedWithinBounds = false
		run.Iterations = nil
	case domainlive.LiveLoopRunStatusFailed:
		run.CompletedWithinBounds = false
		run.IterationsSucceeded = 0
		run.Error = "live loop failed"
	}
	return run
}

func liveLoopAuditArtifactRunFromDomain(
	run domainlive.LiveLoopRunAudit,
	includeIterations bool,
) domainlive.LiveLoopAuditArtifactRun {
	artifactRun := domainlive.LiveLoopAuditArtifactRun{
		RunID:                 run.RunID,
		StartedAt:             run.StartedAt,
		Status:                run.Status,
		MaxIterations:         run.MaxIterations,
		MaxRuntime:            run.MaxRuntime.String(),
		IterationTimeout:      run.IterationTimeout.String(),
		PreflightChecked:      run.PreflightChecked,
		PreflightReady:        run.PreflightReady,
		IterationsAttempted:   run.IterationsAttempted,
		IterationsSucceeded:   run.IterationsSucceeded,
		StopReason:            run.StopReason,
		StopDetails:           run.StopDetails,
		Error:                 run.Error,
		CompletedWithinBounds: run.CompletedWithinBounds,
	}
	if !run.FinishedAt.IsZero() {
		finishedAt := run.FinishedAt
		artifactRun.FinishedAt = &finishedAt
	}
	if includeIterations {
		for _, iteration := range run.Iterations {
			artifactRun.Iterations = append(artifactRun.Iterations, domainlive.LiveLoopAuditArtifactIteration{
				Iteration:         iteration.Iteration,
				Action:            iteration.Action,
				RequestStop:       iteration.RequestStop,
				Reason:            iteration.Reason,
				DecisionID:        iteration.DecisionID,
				SubmissionID:      iteration.SubmissionID,
				ClientOrderID:     iteration.ClientOrderID,
				ExchangeSubmitted: iteration.ExchangeSubmitted,
				AlreadySubmitted:  iteration.AlreadySubmitted,
				StartedAt:         iteration.StartedAt,
				FinishedAt:        iteration.FinishedAt,
			})
		}
	}
	return artifactRun
}

func cloneLiveLoopAuditArtifact(artifact domainlive.LiveLoopAuditArtifact) domainlive.LiveLoopAuditArtifact {
	artifact.Runs = append([]domainlive.LiveLoopAuditArtifactRun(nil), artifact.Runs...)
	for index := range artifact.Runs {
		if artifact.Runs[index].FinishedAt != nil {
			finishedAt := *artifact.Runs[index].FinishedAt
			artifact.Runs[index].FinishedAt = &finishedAt
		}
		artifact.Runs[index].Iterations = append([]domainlive.LiveLoopAuditArtifactIteration(nil), artifact.Runs[index].Iterations...)
	}
	return artifact
}
