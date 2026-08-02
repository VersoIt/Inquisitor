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
				Total:     1,
				Completed: 1,
			},
		},
		KillSwitch:     domainrisk.KillSwitchState{},
		NextDecisionID: plan.DecisionID,
		NextSymbol:     plan.Symbol,
	}
}
