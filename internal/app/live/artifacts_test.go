package live_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	applive "github.com/VersoIt/Inquisitor/internal/app/live"
	"github.com/VersoIt/Inquisitor/internal/clock"
	domainlive "github.com/VersoIt/Inquisitor/internal/live"
	domainrisk "github.com/VersoIt/Inquisitor/internal/risk"
)

func TestBuildLiveOrderPlanArtifactMapsAndValidatesResult(t *testing.T) {
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	record := liveRiskDecisionAudit(now.Add(-time.Minute))
	identity, err := domainlive.NewDeterministicLiveLoopOrderIdentity(record.DecisionID, "live_loop_artifact_app_0001")
	if err != nil {
		t.Fatalf("identity: %v", err)
	}
	service := applive.NewService(
		applive.WithRiskDecisionReader(&fakeLiveRiskDecisionReader{records: []domainrisk.DecisionAuditRecord{record}}),
		applive.WithClock(clock.FixedClock{Time: now}),
	)
	plan, err := service.BuildLiveOrderPlan(context.Background(), applive.BuildLiveOrderPlanRequest{
		DecisionID:    record.DecisionID,
		SubmissionID:  identity.SubmissionID,
		ClientOrderID: identity.ClientOrderID,
		Exchange:      "bybit",
		Category:      "linear",
		Type:          domainlive.OrderTypeMarket,
		TimeInForce:   domainlive.TimeInForceIOC,
		LimitPrice:    decimal.Zero,
	})
	if err != nil {
		t.Fatalf("build live order plan: %v", err)
	}

	artifact, err := applive.BuildLiveOrderPlanArtifact(
		domainlive.LiveOrderPlanArtifactSourceDecisionID,
		"",
		identity.RunID,
		plan,
	)
	if err != nil {
		t.Fatalf("build order plan artifact: %v", err)
	}
	if artifact.DecisionID != record.DecisionID ||
		artifact.RunID != identity.RunID ||
		artifact.SubmissionID != identity.SubmissionID ||
		artifact.ClientOrderID != identity.ClientOrderID ||
		artifact.Symbol != record.Symbol ||
		artifact.SubmissionCreatedAt != now ||
		artifact.Reserved ||
		artifact.ExchangeContacted ||
		artifact.OrderSubmitted {
		t.Fatalf("artifact mismatch: %#v", artifact)
	}
}

func TestBuildLiveOrderPlanArtifactRejectsSideEffectfulResult(t *testing.T) {
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	plan := validBuildLiveOrderPlanResult(t, now)
	plan.OrderSubmitted = true

	_, err := applive.BuildLiveOrderPlanArtifact(
		domainlive.LiveOrderPlanArtifactSourceDecisionID,
		"",
		"live_loop_artifact_app_0001",
		plan,
	)
	if err == nil {
		t.Fatal("expected side-effectful plan artifact to be rejected")
	}
}

func TestBuildLiveReadinessArtifactMapsAndValidatesReport(t *testing.T) {
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	plan := validLiveReadinessPlanArtifact(t, liveRiskDecisionAudit(now.Add(-time.Minute)), domainlive.LiveOrderPlanArtifactSourceDecisionID)
	req := validLiveReadinessRequest()
	req.HasPlanArtifact = true
	req.PlanArtifact = plan
	report := validLiveReadinessArtifactReport(now, plan)

	artifact, err := applive.BuildLiveReadinessArtifact(applive.BuildLiveReadinessArtifactRequest{
		Report:         report,
		Readiness:      req,
		CreatedAt:      now,
		ConfigPath:     " configs/live.local.yaml ",
		PlanFilePath:   " artifacts/live-order-plan.json ",
		PlanFileSHA256: strings.Repeat("e", 64),
	})
	if err != nil {
		t.Fatalf("build readiness artifact: %v", err)
	}
	if artifact.ConfigPath != "configs/live.local.yaml" ||
		!artifact.Ready ||
		artifact.Pending.Limit != 1 ||
		artifact.Audit.Limit != 10 ||
		artifact.Audit.ReviewStatus != domainlive.LiveLoopAuditReviewStatusClear ||
		artifact.Audit.OperatorActionRequired ||
		artifact.Pending.NextDecisionID != plan.DecisionID ||
		artifact.Pending.OldestAt == nil ||
		artifact.Pending.NewestAt == nil ||
		artifact.PlanFile == nil ||
		artifact.PlanFile.SHA256 != strings.Repeat("e", 64) ||
		artifact.PlanFile.MaxAge != domainlive.DefaultLiveOrderPlanArtifactMaxAge.String() ||
		artifact.PlanFile.DecisionID != plan.DecisionID {
		t.Fatalf("readiness artifact mismatch: %#v", artifact)
	}
}

func TestBuildLiveLoopAuditArtifactMapsAndValidatesReport(t *testing.T) {
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	run := liveLoopAuditRun(now.Add(-time.Minute), domainlive.LiveLoopRunStatusCompleted)
	report := validLiveLoopAuditArtifactReport(t, []domainlive.LiveLoopRunAudit{run}, domainlive.LiveLoopAuditQuery{
		RunID:             run.RunID,
		Status:            domainlive.LiveLoopRunStatusCompleted,
		Limit:             5,
		IncludeIterations: true,
	})

	artifact, err := applive.BuildLiveLoopAuditArtifact(applive.BuildLiveLoopAuditArtifactRequest{
		Report:     report,
		CreatedAt:  now,
		ConfigPath: " configs/live.local.yaml ",
	})
	if err != nil {
		t.Fatalf("build audit artifact: %v", err)
	}
	if artifact.SchemaVersion != domainlive.LiveLoopAuditArtifactSchemaVersion ||
		!artifact.CreatedAt.Equal(now) ||
		artifact.ConfigPath != "configs/live.local.yaml" ||
		artifact.Query.RunID != run.RunID ||
		artifact.Query.Status != domainlive.LiveLoopRunStatusCompleted ||
		artifact.Query.Limit != 5 ||
		!artifact.Query.IncludeIterations {
		t.Fatalf("audit artifact metadata mismatch: %#v", artifact)
	}
	if artifact.Summary.Total != 1 ||
		artifact.Summary.Completed != 1 ||
		artifact.Summary.ReviewStatus != domainlive.LiveLoopAuditReviewStatusClear ||
		artifact.Summary.OperatorActionRequired {
		t.Fatalf("audit artifact summary mismatch: %#v", artifact.Summary)
	}
	if len(artifact.Runs) != 1 ||
		artifact.Runs[0].RunID != run.RunID ||
		artifact.Runs[0].MaxRuntime != run.MaxRuntime.String() ||
		artifact.Runs[0].FinishedAt == nil ||
		len(artifact.Runs[0].Iterations) != 1 ||
		artifact.Runs[0].Iterations[0].DecisionID == "" {
		t.Fatalf("audit artifact runs mismatch: %#v", artifact.Runs)
	}
}

func TestBuildLiveLoopAuditArtifactRejectsInconsistentReport(t *testing.T) {
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	run := liveLoopAuditRun(now.Add(-time.Minute), domainlive.LiveLoopRunStatusCompleted)
	report := validLiveLoopAuditArtifactReport(t, []domainlive.LiveLoopRunAudit{run}, domainlive.LiveLoopAuditQuery{
		Limit:             5,
		IncludeIterations: true,
	})
	report.Summary.Total = 2

	_, err := applive.BuildLiveLoopAuditArtifact(applive.BuildLiveLoopAuditArtifactRequest{
		Report:     report,
		CreatedAt:  now,
		ConfigPath: "configs/live.local.yaml",
	})
	if err == nil || !strings.Contains(err.Error(), "summary.total") {
		t.Fatalf("expected summary validation error, got %v", err)
	}
}

func TestBuildLiveOpsReportArtifactMapsAndValidatesReport(t *testing.T) {
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	firstOrderReview := validAppLiveOpsFirstOrderReviewArtifact(t, now.Add(-time.Minute))
	service := applive.NewService(
		applive.WithClock(clock.FixedClock{Time: now}),
		applive.WithKillSwitchRepository(&fakeLiveKillSwitchRepository{}),
		applive.WithPendingLiveDecisionReader(&fakePendingLiveDecisionReader{candidates: []domainlive.PendingLiveDecision{
			pendingLiveDecision("risk_decision_live_ops_0001", "BTCUSDT", now.Add(-2*time.Minute)),
		}}),
		applive.WithLiveLoopAuditReader(&fakeLiveLoopAuditReader{runs: []domainlive.LiveLoopRunAudit{
			liveLoopAuditRun(now.Add(-time.Minute), domainlive.LiveLoopRunStatusCompleted),
		}}),
	)
	report, err := service.BuildLiveOpsReport(context.Background(), applive.LiveOpsReportRequest{
		PendingSymbol:               "BTCUSDT",
		HasFirstOrderReviewArtifact: true,
		FirstOrderReviewArtifact:    firstOrderReview,
	})
	if err != nil {
		t.Fatalf("build ops report: %v", err)
	}

	artifact, err := applive.BuildLiveOpsReportArtifact(applive.BuildLiveOpsReportArtifactRequest{
		Report:                     report,
		CreatedAt:                  now,
		ConfigPath:                 " configs/live.local.yaml ",
		FirstOrderReviewFilePath:   " artifacts/live-first-order/live-first-order-review.json ",
		FirstOrderReviewFileSHA256: strings.Repeat("c", 64),
	})
	if err != nil {
		t.Fatalf("build ops artifact: %v", err)
	}
	if artifact.SchemaVersion != domainlive.LiveOpsReportArtifactSchemaVersion ||
		!artifact.CreatedAt.Equal(now) ||
		artifact.ConfigPath != "configs/live.local.yaml" ||
		artifact.Status != domainlive.LiveOpsStatusClear ||
		artifact.Summary.Failed != 0 ||
		artifact.Pending.Symbol != "BTCUSDT" ||
		artifact.Pending.Limit != 10 ||
		artifact.Pending.Total != 1 ||
		artifact.Audit.Limit != 10 ||
		artifact.Audit.ReviewStatus != domainlive.LiveLoopAuditReviewStatusClear ||
		artifact.KillSwitch.Active {
		t.Fatalf("ops artifact summary mismatch: %#v", artifact)
	}
	if artifact.FirstOrderReview == nil ||
		artifact.FirstOrderReview.Path != "artifacts/live-first-order/live-first-order-review.json" ||
		artifact.FirstOrderReview.SHA256 != strings.Repeat("c", 64) ||
		!artifact.FirstOrderReview.Ready ||
		artifact.FirstOrderReview.LatestOrderStatus != domainlive.ExchangeOrderStatusFilled {
		t.Fatalf("first-order metadata mismatch: %#v", artifact.FirstOrderReview)
	}
}

func TestBuildLiveOpsReportArtifactRejectsInconsistentReport(t *testing.T) {
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	report := applive.LiveOpsReport{
		Status: domainlive.LiveOpsStatusClear,
		Summary: domainlive.ReadinessCheckSummary{
			Total:  1,
			Passed: 1,
		},
		Checks: []domainlive.ReadinessCheck{
			domainlive.NewReadinessCheck("kill_switch", domainlive.ReadinessCheckStatusPass, "kill switch is inactive"),
		},
		Pending: applive.PendingLiveDecisionReport{
			Query: domainlive.PendingLiveDecisionQuery{Limit: 10},
		},
		Audit: applive.LiveLoopAuditReport{
			Query: domainlive.LiveLoopAuditQuery{Limit: 10},
			Summary: applive.LiveLoopAuditReportSummary{
				ReviewStatus:           domainlive.LiveLoopAuditReviewStatusClear,
				ReviewReason:           "no recent live-loop audit runs found",
				OperatorActionRequired: false,
			},
		},
	}

	tests := []struct {
		name       string
		mutate     func(*applive.LiveOpsReport)
		wantErrSub string
	}{
		{name: "summary mismatch", mutate: func(r *applive.LiveOpsReport) {
			r.Summary.Failed = 1
		}, wantErrSub: "summary"},
		{name: "status mismatch", mutate: func(r *applive.LiveOpsReport) {
			r.Status = domainlive.LiveOpsStatusBlocked
		}, wantErrSub: "status"},
		{name: "first-order metadata requires path", mutate: func(r *applive.LiveOpsReport) {
			r.HasFirstOrderReview = true
			r.FirstOrderReview = validAppLiveOpsFirstOrderReviewArtifact(t, now)
			r.Checks = append(r.Checks, domainlive.NewReadinessCheck("first_order_review", domainlive.ReadinessCheckStatusPass, "first-order review passed"))
			r.Summary = domainlive.SummarizeReadinessChecks(r.Checks)
		}, wantErrSub: "first_order_review.path"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			candidate := report
			candidate.Checks = append([]domainlive.ReadinessCheck(nil), report.Checks...)
			if tt.mutate != nil {
				tt.mutate(&candidate)
			}
			_, err := applive.BuildLiveOpsReportArtifact(applive.BuildLiveOpsReportArtifactRequest{
				Report:     candidate,
				CreatedAt:  now,
				ConfigPath: "configs/live.local.yaml",
			})
			if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
		})
	}
}

func validBuildLiveOrderPlanResult(t *testing.T, now time.Time) applive.BuildLiveOrderPlanResult {
	t.Helper()

	record := liveRiskDecisionAudit(now.Add(-time.Minute))
	identity, err := domainlive.NewDeterministicLiveLoopOrderIdentity(record.DecisionID, "live_loop_artifact_app_0001")
	if err != nil {
		t.Fatalf("identity: %v", err)
	}
	submission, err := domainlive.NewOrderSubmission(domainlive.OrderSubmissionInput{
		SubmissionID:     identity.SubmissionID,
		ClientOrderID:    identity.ClientOrderID,
		DecisionID:       record.DecisionID,
		DecisionApproved: true,
		IntentID:         record.Decision.IntentID,
		RiskMode:         domainlive.RiskModeLive,
		Exchange:         "bybit",
		Category:         "linear",
		Symbol:           record.Symbol,
		Side:             domainlive.OrderSideLong,
		Type:             domainlive.OrderTypeMarket,
		TimeInForce:      domainlive.TimeInForceIOC,
		Quantity:         record.Decision.FinalQuantity,
		ReferencePrice:   record.EntryPrice,
		StopLoss:         record.Decision.StopLoss,
		TakeProfit:       record.Decision.TakeProfit,
		Leverage:         record.Leverage,
		MaxLoss:          record.Decision.MaxLoss,
		Confidence:       record.Confidence,
		Reason:           record.Decision.Reason,
		CreatedAt:        now,
	})
	if err != nil {
		t.Fatalf("new submission: %v", err)
	}
	return applive.BuildLiveOrderPlanResult{
		Decision:   record,
		Submission: submission,
	}
}

func validLiveReadinessArtifactReport(now time.Time, plan domainlive.LiveOrderPlanArtifact) applive.LiveReadinessReport {
	oldestAt := now.Add(-2 * time.Minute)
	newestAt := now.Add(-time.Minute)
	checks := []domainlive.ReadinessCheck{
		domainlive.NewReadinessCheck("live_config", domainlive.ReadinessCheckStatusPass, "live config ok"),
		domainlive.NewReadinessCheck("live_order_plan_artifact", domainlive.ReadinessCheckStatusPass, "plan ok"),
	}
	return applive.LiveReadinessReport{
		Ready:   true,
		Summary: domainlive.SummarizeReadinessChecks(checks),
		Checks:  checks,
		Pending: applive.PendingLiveDecisionReport{
			Summary: applive.PendingLiveDecisionReportSummary{
				Total:      1,
				OldestAt:   oldestAt,
				NewestAt:   newestAt,
				NextID:     plan.DecisionID,
				NextSymbol: plan.Symbol,
			},
		},
		Audit: applive.LiveLoopAuditReport{
			Summary: applive.LiveLoopAuditReportSummary{
				Total:                  1,
				Completed:              1,
				ReviewStatus:           domainlive.LiveLoopAuditReviewStatusClear,
				ReviewReason:           "recent live-loop audit has no running or failed runs",
				OperatorActionRequired: false,
			},
		},
		KillSwitch:     domainrisk.KillSwitchState{},
		NextDecisionID: plan.DecisionID,
		NextSymbol:     plan.Symbol,
	}
}

func validLiveLoopAuditArtifactReport(
	t *testing.T,
	runs []domainlive.LiveLoopRunAudit,
	query domainlive.LiveLoopAuditQuery,
) applive.LiveLoopAuditReport {
	t.Helper()

	review, err := domainlive.SummarizeLiveLoopAuditReview(runs)
	if err != nil {
		t.Fatalf("review: %v", err)
	}
	report := applive.LiveLoopAuditReport{
		Query: query,
		Runs:  append([]domainlive.LiveLoopRunAudit(nil), runs...),
		Summary: applive.LiveLoopAuditReportSummary{
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
			report.Summary.Running++
		case domainlive.LiveLoopRunStatusCompleted:
			report.Summary.Completed++
		case domainlive.LiveLoopRunStatusFailed:
			report.Summary.Failed++
		}
	}
	return report
}
