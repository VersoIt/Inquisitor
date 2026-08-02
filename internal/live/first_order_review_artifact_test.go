package live_test

import (
	"strings"
	"testing"
	"time"

	domainlive "github.com/VersoIt/Inquisitor/internal/live"
)

func TestBuildLiveFirstOrderReviewArtifactTableDriven(t *testing.T) {
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	valid := validLiveFirstOrderReviewArtifact(t, now)

	tests := []struct {
		name       string
		artifact   domainlive.LiveFirstOrderReviewArtifact
		mutate     func(*domainlive.LiveFirstOrderReviewArtifact)
		wantErrSub string
	}{
		{name: "valid ready artifact", artifact: valid},
		{name: "failed review artifact is valid", artifact: validFailedLiveFirstOrderReviewArtifact(t, now)},
		{name: "bad schema", artifact: valid, mutate: func(a *domainlive.LiveFirstOrderReviewArtifact) {
			a.SchemaVersion = "old"
		}, wantErrSub: "schema_version"},
		{name: "summary mismatch", artifact: valid, mutate: func(a *domainlive.LiveFirstOrderReviewArtifact) {
			a.Summary.Failed = 1
		}, wantErrSub: "summary"},
		{name: "ready mismatch", artifact: valid, mutate: func(a *domainlive.LiveFirstOrderReviewArtifact) {
			a.Ready = false
		}, wantErrSub: "ready"},
		{name: "failed checks mismatch", artifact: valid, mutate: func(a *domainlive.LiveFirstOrderReviewArtifact) {
			a.FailedChecks = []string{"live_order_status"}
		}, wantErrSub: "failed_checks"},
		{name: "invalid plan sha", artifact: valid, mutate: func(a *domainlive.LiveFirstOrderReviewArtifact) {
			a.PlanFile.SHA256 = "ABC"
		}, wantErrSub: "plan_file.sha256"},
		{name: "metadata mismatch", artifact: valid, mutate: func(a *domainlive.LiveFirstOrderReviewArtifact) {
			a.Evidence.SubmissionID = "live_submission_other"
		}, wantErrSub: "submission_id"},
		{name: "ready requires filled status", artifact: valid, mutate: func(a *domainlive.LiveFirstOrderReviewArtifact) {
			a.Evidence.LatestOrderStatus = domainlive.ExchangeOrderStatusNew
		}, wantErrSub: "latest_order_status"},
		{name: "status limit must be persisted positive", artifact: valid, mutate: func(a *domainlive.LiveFirstOrderReviewArtifact) {
			a.Evidence.StatusLimit = 0
		}, wantErrSub: "status_limit"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			artifact := tt.artifact
			artifact.Checks = append([]domainlive.LiveFirstOrderReviewArtifactCheck(nil), artifact.Checks...)
			artifact.FailedChecks = append([]string(nil), artifact.FailedChecks...)
			if tt.mutate != nil {
				tt.mutate(&artifact)
			}

			err := domainlive.ValidateLiveFirstOrderReviewArtifact(artifact)
			if tt.wantErrSub == "" {
				if err != nil {
					t.Fatalf("validate artifact: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
		})
	}
}

func TestValidateLiveFirstOrderReviewArtifactFreshnessTableDriven(t *testing.T) {
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	valid := validLiveFirstOrderReviewArtifact(t, now.Add(-time.Minute))

	tests := []struct {
		name       string
		artifact   domainlive.LiveFirstOrderReviewArtifact
		now        time.Time
		maxAge     time.Duration
		wantErrSub string
	}{
		{name: "fresh", artifact: valid, now: now, maxAge: 10 * time.Minute},
		{name: "stale", artifact: valid, now: now.Add(time.Hour), maxAge: 10 * time.Minute, wantErrSub: "stale"},
		{name: "future", artifact: validLiveFirstOrderReviewArtifact(t, now.Add(time.Minute)), now: now, maxAge: 10 * time.Minute, wantErrSub: "future"},
		{name: "bad max age", artifact: valid, now: now, maxAge: 0, wantErrSub: "max_age"},
		{name: "invalid artifact first", artifact: mutateLiveFirstOrderReviewArtifact(valid, func(a *domainlive.LiveFirstOrderReviewArtifact) {
			a.SchemaVersion = "old"
		}), now: now, maxAge: 10 * time.Minute, wantErrSub: "schema_version"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := domainlive.ValidateLiveFirstOrderReviewArtifactFreshness(tt.artifact, tt.now, tt.maxAge)
			if tt.wantErrSub == "" {
				if err != nil {
					t.Fatalf("validate freshness: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
		})
	}
}

func validLiveFirstOrderReviewArtifact(t *testing.T, now time.Time) domainlive.LiveFirstOrderReviewArtifact {
	t.Helper()
	evidence := validLiveFirstOrderReviewEvidence(t, now)
	report, err := domainlive.BuildLiveFirstOrderReviewReport(evidence)
	if err != nil {
		t.Fatalf("build report: %v", err)
	}
	artifact, err := domainlive.BuildLiveFirstOrderReviewArtifact(domainlive.BuildLiveFirstOrderReviewArtifactRequest{
		Report: report,
		Query: domainlive.LiveFirstOrderReviewEvidenceQuery{
			PlanArtifact:  evidence.PlanArtifact,
			StatusLimit:   domainlive.DefaultLiveFirstOrderReviewStatusLimit,
			PositionLimit: domainlive.DefaultLiveFirstOrderReviewPositionLimit,
		},
		CreatedAt:      now.Add(time.Minute),
		ConfigPath:     "configs/live.local.yaml",
		PlanFilePath:   "artifacts/live-order-plan.json",
		PlanFileSHA256: strings.Repeat("a", 64),
	})
	if err != nil {
		t.Fatalf("build artifact: %v", err)
	}
	return artifact
}

func mutateLiveFirstOrderReviewArtifact(
	artifact domainlive.LiveFirstOrderReviewArtifact,
	mutate func(*domainlive.LiveFirstOrderReviewArtifact),
) domainlive.LiveFirstOrderReviewArtifact {
	artifact.Checks = append([]domainlive.LiveFirstOrderReviewArtifactCheck(nil), artifact.Checks...)
	artifact.FailedChecks = append([]string(nil), artifact.FailedChecks...)
	mutate(&artifact)
	return artifact
}

func validFailedLiveFirstOrderReviewArtifact(t *testing.T, now time.Time) domainlive.LiveFirstOrderReviewArtifact {
	t.Helper()
	evidence := validLiveFirstOrderReviewEvidence(t, now)
	evidence.PositionSnapshots = nil
	report, err := domainlive.BuildLiveFirstOrderReviewReport(evidence)
	if err != nil {
		t.Fatalf("build report: %v", err)
	}
	artifact, err := domainlive.BuildLiveFirstOrderReviewArtifact(domainlive.BuildLiveFirstOrderReviewArtifactRequest{
		Report: report,
		Query: domainlive.LiveFirstOrderReviewEvidenceQuery{
			PlanArtifact: evidence.PlanArtifact,
		},
		CreatedAt:      now.Add(time.Minute),
		ConfigPath:     "configs/live.local.yaml",
		PlanFilePath:   "artifacts/live-order-plan.json",
		PlanFileSHA256: strings.Repeat("a", 64),
	})
	if err != nil {
		t.Fatalf("build failed artifact: %v", err)
	}
	return artifact
}
