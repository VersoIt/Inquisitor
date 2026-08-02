package live_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	applive "github.com/VersoIt/Inquisitor/internal/app/live"
	"github.com/VersoIt/Inquisitor/internal/clock"
	domainlive "github.com/VersoIt/Inquisitor/internal/live"
	domainrisk "github.com/VersoIt/Inquisitor/internal/risk"
)

func TestServiceBuildLiveOpsReportTableDriven(t *testing.T) {
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name       string
		req        applive.LiveOpsReportRequest
		killSwitch domainrisk.KillSwitchState
		pending    []domainlive.PendingLiveDecision
		audit      []domainlive.LiveLoopRunAudit
		wantStatus domainlive.LiveOpsStatus
		wantCheck  string
	}{
		{
			name:       "clear without first-order review requirement",
			pending:    []domainlive.PendingLiveDecision{pendingLiveDecision("risk_decision_live_ops_0001", "BTCUSDT", now)},
			audit:      []domainlive.LiveLoopRunAudit{liveLoopAuditRun(now.Add(-time.Minute), domainlive.LiveLoopRunStatusCompleted)},
			wantStatus: domainlive.LiveOpsStatusClear,
		},
		{
			name:       "no pending decisions needs attention but does not block ops report",
			audit:      []domainlive.LiveLoopRunAudit{liveLoopAuditRun(now.Add(-time.Minute), domainlive.LiveLoopRunStatusCompleted)},
			wantStatus: domainlive.LiveOpsStatusAttention,
			wantCheck:  "pending_live_decision",
		},
		{
			name: "active kill switch blocks",
			killSwitch: domainrisk.KillSwitchState{
				Active:    true,
				Reason:    "operator stop",
				Source:    "operator",
				UpdatedAt: now,
			},
			pending:    []domainlive.PendingLiveDecision{pendingLiveDecision("risk_decision_live_ops_0001", "BTCUSDT", now)},
			audit:      []domainlive.LiveLoopRunAudit{liveLoopAuditRun(now.Add(-time.Minute), domainlive.LiveLoopRunStatusCompleted)},
			wantStatus: domainlive.LiveOpsStatusBlocked,
			wantCheck:  "kill_switch",
		},
		{
			name:       "running live loop blocks",
			pending:    []domainlive.PendingLiveDecision{pendingLiveDecision("risk_decision_live_ops_0001", "BTCUSDT", now)},
			audit:      []domainlive.LiveLoopRunAudit{liveLoopAuditRun(now.Add(-time.Minute), domainlive.LiveLoopRunStatusRunning)},
			wantStatus: domainlive.LiveOpsStatusBlocked,
			wantCheck:  "recent_live_loop_audit",
		},
		{
			name: "ready first-order review contributes pass check",
			req: applive.LiveOpsReportRequest{
				HasFirstOrderReviewArtifact: true,
				FirstOrderReviewArtifact:    validAppLiveOpsFirstOrderReviewArtifact(t, now),
			},
			pending:    []domainlive.PendingLiveDecision{pendingLiveDecision("risk_decision_live_ops_0001", "BTCUSDT", now)},
			audit:      []domainlive.LiveLoopRunAudit{liveLoopAuditRun(now.Add(-time.Minute), domainlive.LiveLoopRunStatusCompleted)},
			wantStatus: domainlive.LiveOpsStatusClear,
			wantCheck:  "first_order_review",
		},
		{
			name: "failed first-order review blocks",
			req: applive.LiveOpsReportRequest{
				HasFirstOrderReviewArtifact: true,
				FirstOrderReviewArtifact: mutateAppLiveOpsFirstOrderReviewArtifact(validAppLiveOpsFirstOrderReviewArtifact(t, now), func(a *domainlive.LiveFirstOrderReviewArtifact) {
					a.Ready = false
					a.FailedChecks = []string{"live_position_snapshot"}
					a.Summary = domainlive.LiveFirstOrderReviewArtifactSummary{Total: 4, Passed: 3, Failed: 1}
					a.Checks = []domainlive.LiveFirstOrderReviewArtifactCheck{
						{Name: "live_position_snapshot", Status: domainlive.ReadinessCheckStatusFail, Details: "missing"},
						{Name: "live_order_status", Status: domainlive.ReadinessCheckStatusPass, Details: "filled"},
						{Name: "live_order_submission", Status: domainlive.ReadinessCheckStatusPass, Details: "matched"},
						{Name: "live_order_acknowledgement", Status: domainlive.ReadinessCheckStatusPass, Details: "accepted"},
					}
				}),
			},
			pending:    []domainlive.PendingLiveDecision{pendingLiveDecision("risk_decision_live_ops_0001", "BTCUSDT", now)},
			audit:      []domainlive.LiveLoopRunAudit{liveLoopAuditRun(now.Add(-time.Minute), domainlive.LiveLoopRunStatusCompleted)},
			wantStatus: domainlive.LiveOpsStatusBlocked,
			wantCheck:  "first_order_review",
		},
		{
			name: "missing required first-order review blocks",
			req: applive.LiveOpsReportRequest{
				RequireFirstOrderReviewArtifact: true,
			},
			pending:    []domainlive.PendingLiveDecision{pendingLiveDecision("risk_decision_live_ops_0001", "BTCUSDT", now)},
			audit:      []domainlive.LiveLoopRunAudit{liveLoopAuditRun(now.Add(-time.Minute), domainlive.LiveLoopRunStatusCompleted)},
			wantStatus: domainlive.LiveOpsStatusBlocked,
			wantCheck:  "first_order_review",
		},
		{
			name: "stale first-order review blocks",
			req: applive.LiveOpsReportRequest{
				HasFirstOrderReviewArtifact:    true,
				FirstOrderReviewArtifact:       validAppLiveOpsFirstOrderReviewArtifact(t, now.Add(-time.Hour)),
				MaxFirstOrderReviewArtifactAge: 10 * time.Minute,
			},
			pending:    []domainlive.PendingLiveDecision{pendingLiveDecision("risk_decision_live_ops_0001", "BTCUSDT", now)},
			audit:      []domainlive.LiveLoopRunAudit{liveLoopAuditRun(now.Add(-time.Minute), domainlive.LiveLoopRunStatusCompleted)},
			wantStatus: domainlive.LiveOpsStatusBlocked,
			wantCheck:  "first_order_review",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := applive.NewService(
				applive.WithClock(clock.FixedClock{Time: now}),
				applive.WithKillSwitchRepository(&fakeLiveKillSwitchRepository{state: tt.killSwitch}),
				applive.WithPendingLiveDecisionReader(&fakePendingLiveDecisionReader{candidates: tt.pending}),
				applive.WithLiveLoopAuditReader(&fakeLiveLoopAuditReader{runs: tt.audit}),
			)

			got, err := service.BuildLiveOpsReport(context.Background(), tt.req)
			if err != nil {
				t.Fatalf("build live ops report: %v", err)
			}
			if got.Status != tt.wantStatus {
				t.Fatalf("status mismatch: got %s want %s checks=%#v", got.Status, tt.wantStatus, got.Checks)
			}
			if got.Summary.Total != len(got.Checks) {
				t.Fatalf("summary mismatch: %#v checks=%#v", got.Summary, got.Checks)
			}
			if tt.wantCheck != "" && !appLiveOpsHasCheck(got.Checks, tt.wantCheck) {
				t.Fatalf("expected check %q, got %#v", tt.wantCheck, got.Checks)
			}
		})
	}
}

func TestServiceBuildLiveOpsReportRejectsUnsafeInputsTableDriven(t *testing.T) {
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name       string
		service    *applive.Service
		req        applive.LiveOpsReportRequest
		wantErrSub string
	}{
		{
			name:       "missing dependencies",
			service:    applive.NewService(),
			wantErrSub: "requires",
		},
		{
			name: "invalid pending query",
			service: applive.NewService(
				applive.WithKillSwitchRepository(&fakeLiveKillSwitchRepository{}),
				applive.WithPendingLiveDecisionReader(&fakePendingLiveDecisionReader{}),
				applive.WithLiveLoopAuditReader(&fakeLiveLoopAuditReader{}),
			),
			req:        applive.LiveOpsReportRequest{PendingLimit: 101},
			wantErrSub: "limit",
		},
		{
			name: "kill switch error",
			service: applive.NewService(
				applive.WithKillSwitchRepository(&fakeLiveKillSwitchRepository{err: errors.New("kill switch db offline")}),
				applive.WithPendingLiveDecisionReader(&fakePendingLiveDecisionReader{}),
				applive.WithLiveLoopAuditReader(&fakeLiveLoopAuditReader{}),
			),
			wantErrSub: "kill switch db offline",
		},
		{
			name: "audit reader error",
			service: applive.NewService(
				applive.WithKillSwitchRepository(&fakeLiveKillSwitchRepository{}),
				applive.WithPendingLiveDecisionReader(&fakePendingLiveDecisionReader{}),
				applive.WithLiveLoopAuditReader(&fakeLiveLoopAuditReader{err: errors.New("audit db offline")}),
			),
			wantErrSub: "audit db offline",
		},
		{
			name: "invalid first-order review artifact",
			service: applive.NewService(
				applive.WithClock(clock.FixedClock{Time: now}),
				applive.WithKillSwitchRepository(&fakeLiveKillSwitchRepository{}),
				applive.WithPendingLiveDecisionReader(&fakePendingLiveDecisionReader{candidates: []domainlive.PendingLiveDecision{
					pendingLiveDecision("risk_decision_live_ops_0001", "BTCUSDT", now),
				}}),
				applive.WithLiveLoopAuditReader(&fakeLiveLoopAuditReader{runs: []domainlive.LiveLoopRunAudit{
					liveLoopAuditRun(now.Add(-time.Minute), domainlive.LiveLoopRunStatusCompleted),
				}}),
			),
			req: applive.LiveOpsReportRequest{
				HasFirstOrderReviewArtifact: true,
				FirstOrderReviewArtifact: mutateAppLiveOpsFirstOrderReviewArtifact(validAppLiveOpsFirstOrderReviewArtifact(t, now), func(a *domainlive.LiveFirstOrderReviewArtifact) {
					a.SchemaVersion = "old"
				}),
			},
			wantErrSub: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.service.BuildLiveOpsReport(context.Background(), tt.req)
			if tt.wantErrSub == "" {
				if err != nil {
					t.Fatalf("build live ops report: %v", err)
				}
				if got.Status != domainlive.LiveOpsStatusBlocked || !appLiveOpsHasCheck(got.Checks, "first_order_review") {
					t.Fatalf("expected invalid artifact to be a blocked report, got %#v", got)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
		})
	}
}

func validAppLiveOpsFirstOrderReviewArtifact(t *testing.T, now time.Time) domainlive.LiveFirstOrderReviewArtifact {
	t.Helper()
	evidence := validAppFirstOrderReviewEvidence(t, now.Add(-time.Minute))
	report, err := domainlive.BuildLiveFirstOrderReviewReport(evidence)
	if err != nil {
		t.Fatalf("build first-order report: %v", err)
	}
	artifact, err := domainlive.BuildLiveFirstOrderReviewArtifact(domainlive.BuildLiveFirstOrderReviewArtifactRequest{
		Report: report,
		Query: domainlive.LiveFirstOrderReviewEvidenceQuery{
			PlanArtifact: evidence.PlanArtifact,
		},
		CreatedAt:      now,
		ConfigPath:     "configs/live.local.yaml",
		PlanFilePath:   "artifacts/live-first-order/live-order-plan.json",
		PlanFileSHA256: strings.Repeat("a", 64),
	})
	if err != nil {
		t.Fatalf("build first-order artifact: %v", err)
	}
	return artifact
}

func mutateAppLiveOpsFirstOrderReviewArtifact(
	artifact domainlive.LiveFirstOrderReviewArtifact,
	mutate func(*domainlive.LiveFirstOrderReviewArtifact),
) domainlive.LiveFirstOrderReviewArtifact {
	artifact.Checks = append([]domainlive.LiveFirstOrderReviewArtifactCheck(nil), artifact.Checks...)
	artifact.FailedChecks = append([]string(nil), artifact.FailedChecks...)
	mutate(&artifact)
	return artifact
}

func appLiveOpsHasCheck(checks []domainlive.ReadinessCheck, name string) bool {
	for _, check := range checks {
		if check.Name == name {
			return true
		}
	}
	return false
}
