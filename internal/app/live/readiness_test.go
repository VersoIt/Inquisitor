package live_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	applive "github.com/VersoIt/Inquisitor/internal/app/live"
	"github.com/VersoIt/Inquisitor/internal/clock"
	domainlive "github.com/VersoIt/Inquisitor/internal/live"
	domainrisk "github.com/VersoIt/Inquisitor/internal/risk"
)

func TestServiceBuildLiveReadinessReportPasses(t *testing.T) {
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	pendingReader := &fakePendingLiveDecisionReader{candidates: []domainlive.PendingLiveDecision{
		pendingLiveDecision("risk_decision_live_ready_0001", "BTCUSDT", now),
	}}
	auditReader := &fakeLiveLoopAuditReader{runs: []domainlive.LiveLoopRunAudit{
		liveLoopAuditRun(now.Add(-time.Minute), domainlive.LiveLoopRunStatusCompleted),
	}}
	killSwitch := &fakeLiveKillSwitchRepository{}
	service := applive.NewService(
		applive.WithPendingLiveDecisionReader(pendingReader),
		applive.WithLiveLoopAuditReader(auditReader),
		applive.WithKillSwitchRepository(killSwitch),
	)

	got, err := service.BuildLiveReadinessReport(context.Background(), validLiveReadinessRequest())
	if err != nil {
		t.Fatalf("build live readiness report: %v", err)
	}
	if !got.Ready || got.Summary.Total != 7 || got.Summary.Passed != 7 || got.Summary.Failed != 0 {
		t.Fatalf("readiness summary mismatch: ready=%t summary=%#v checks=%#v", got.Ready, got.Summary, got.Checks)
	}
	if got.NextDecisionID != "risk_decision_live_ready_0001" || got.NextSymbol != "BTCUSDT" {
		t.Fatalf("next pending decision mismatch: %#v", got)
	}
	if killSwitch.currentCalls != 1 {
		t.Fatalf("kill switch calls mismatch: %d", killSwitch.currentCalls)
	}
	if pendingReader.query.Symbol != "BTCUSDT" || pendingReader.query.Limit != 1 {
		t.Fatalf("pending query mismatch: %#v", pendingReader.query)
	}
	if auditReader.query.Limit != 10 || !auditReader.query.IncludeIterations {
		t.Fatalf("audit query mismatch: %#v", auditReader.query)
	}
}

func TestServiceBuildLiveReadinessReportFailsOnBlockersTableDriven(t *testing.T) {
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name       string
		mutateReq  func(*applive.BuildLiveReadinessReportRequest)
		killSwitch domainrisk.KillSwitchState
		pending    []domainlive.PendingLiveDecision
		audit      []domainlive.LiveLoopRunAudit
		wantCheck  string
	}{
		{
			name:      "unsafe live config",
			mutateReq: func(req *applive.BuildLiveReadinessReportRequest) { req.TradingMode = "paper" },
			pending:   []domainlive.PendingLiveDecision{pendingLiveDecision("risk_decision_live_ready_0001", "BTCUSDT", now)},
			audit:     []domainlive.LiveLoopRunAudit{liveLoopAuditRun(now, domainlive.LiveLoopRunStatusCompleted)},
			wantCheck: "live_config",
		},
		{
			name:      "missing operator confirmation",
			mutateReq: func(req *applive.BuildLiveReadinessReportRequest) { req.ConfirmationAccepted = false },
			pending:   []domainlive.PendingLiveDecision{pendingLiveDecision("risk_decision_live_ready_0001", "BTCUSDT", now)},
			audit:     []domainlive.LiveLoopRunAudit{liveLoopAuditRun(now, domainlive.LiveLoopRunStatusCompleted)},
			wantCheck: "operator_confirmation",
		},
		{
			name: "capital above cap",
			mutateReq: func(req *applive.BuildLiveReadinessReportRequest) {
				req.InitialLiveCapitalUSDT = decimal.RequireFromString("101")
			},
			pending:   []domainlive.PendingLiveDecision{pendingLiveDecision("risk_decision_live_ready_0001", "BTCUSDT", now)},
			audit:     []domainlive.LiveLoopRunAudit{liveLoopAuditRun(now, domainlive.LiveLoopRunStatusCompleted)},
			wantCheck: "capital_cap",
		},
		{
			name:      "db pool cannot hold selector lock",
			mutateReq: func(req *applive.BuildLiveReadinessReportRequest) { req.DatabaseMaxOpenConns = 1 },
			pending:   []domainlive.PendingLiveDecision{pendingLiveDecision("risk_decision_live_ready_0001", "BTCUSDT", now)},
			audit:     []domainlive.LiveLoopRunAudit{liveLoopAuditRun(now, domainlive.LiveLoopRunStatusCompleted)},
			wantCheck: "database_pool",
		},
		{
			name: "active kill switch",
			killSwitch: domainrisk.KillSwitchState{
				Active:    true,
				Reason:    "operator stop",
				Source:    "operator",
				UpdatedAt: now,
			},
			pending:   []domainlive.PendingLiveDecision{pendingLiveDecision("risk_decision_live_ready_0001", "BTCUSDT", now)},
			audit:     []domainlive.LiveLoopRunAudit{liveLoopAuditRun(now, domainlive.LiveLoopRunStatusCompleted)},
			wantCheck: "kill_switch",
		},
		{
			name:      "missing pending decision",
			pending:   nil,
			audit:     []domainlive.LiveLoopRunAudit{liveLoopAuditRun(now, domainlive.LiveLoopRunStatusCompleted)},
			wantCheck: "pending_live_decision",
		},
		{
			name:      "running live loop",
			pending:   []domainlive.PendingLiveDecision{pendingLiveDecision("risk_decision_live_ready_0001", "BTCUSDT", now)},
			audit:     []domainlive.LiveLoopRunAudit{liveLoopAuditRun(now, domainlive.LiveLoopRunStatusRunning)},
			wantCheck: "recent_live_loop_audit",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := validLiveReadinessRequest()
			if tt.mutateReq != nil {
				tt.mutateReq(&req)
			}
			service := applive.NewService(
				applive.WithPendingLiveDecisionReader(&fakePendingLiveDecisionReader{candidates: tt.pending}),
				applive.WithLiveLoopAuditReader(&fakeLiveLoopAuditReader{runs: tt.audit}),
				applive.WithKillSwitchRepository(&fakeLiveKillSwitchRepository{state: tt.killSwitch}),
			)

			got, err := service.BuildLiveReadinessReport(context.Background(), req)
			if err != nil {
				t.Fatalf("build live readiness report: %v", err)
			}
			if got.Ready || got.Summary.Failed == 0 {
				t.Fatalf("expected readiness failure, got ready=%t summary=%#v checks=%#v", got.Ready, got.Summary, got.Checks)
			}
			check := readinessCheckByName(got.Checks, tt.wantCheck)
			if check.Status != domainlive.ReadinessCheckStatusFail {
				t.Fatalf("check %q mismatch: %#v", tt.wantCheck, check)
			}
		})
	}
}

func TestServiceBuildLiveReadinessReportWarnsOnRecentFailedRuns(t *testing.T) {
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	service := applive.NewService(
		applive.WithPendingLiveDecisionReader(&fakePendingLiveDecisionReader{candidates: []domainlive.PendingLiveDecision{
			pendingLiveDecision("risk_decision_live_ready_0001", "BTCUSDT", now),
		}}),
		applive.WithLiveLoopAuditReader(&fakeLiveLoopAuditReader{runs: []domainlive.LiveLoopRunAudit{
			liveLoopAuditRun(now, domainlive.LiveLoopRunStatusFailed),
		}}),
		applive.WithKillSwitchRepository(&fakeLiveKillSwitchRepository{}),
	)

	got, err := service.BuildLiveReadinessReport(context.Background(), validLiveReadinessRequest())
	if err != nil {
		t.Fatalf("build live readiness report: %v", err)
	}
	if !got.Ready || got.Summary.Warned != 1 || got.Summary.Failed != 0 {
		t.Fatalf("expected warning-only readiness, got ready=%t summary=%#v checks=%#v", got.Ready, got.Summary, got.Checks)
	}
	check := readinessCheckByName(got.Checks, "recent_live_loop_audit")
	if check.Status != domainlive.ReadinessCheckStatusWarn {
		t.Fatalf("audit check mismatch: %#v", check)
	}
}

func TestServiceBuildLiveReadinessReportPassesWithPlanArtifact(t *testing.T) {
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	record := liveRiskDecisionAudit(now.Add(-time.Minute))
	riskReader := &fakeLiveRiskDecisionReader{records: []domainrisk.DecisionAuditRecord{record}}
	service := applive.NewService(
		applive.WithPendingLiveDecisionReader(&fakePendingLiveDecisionReader{candidates: []domainlive.PendingLiveDecision{
			pendingLiveDecision("risk_decision_live_ready_0001", "BTCUSDT", now),
		}}),
		applive.WithLiveLoopAuditReader(&fakeLiveLoopAuditReader{runs: []domainlive.LiveLoopRunAudit{
			liveLoopAuditRun(now, domainlive.LiveLoopRunStatusCompleted),
		}}),
		applive.WithKillSwitchRepository(&fakeLiveKillSwitchRepository{}),
		applive.WithRiskDecisionReader(riskReader),
		applive.WithClock(clock.FixedClock{Time: now}),
	)
	req := validLiveReadinessRequest()
	req.HasPlanArtifact = true
	req.PlanArtifact = validLiveReadinessPlanArtifact(t, record, "decision-id")

	got, err := service.BuildLiveReadinessReport(context.Background(), req)
	if err != nil {
		t.Fatalf("build live readiness report: %v", err)
	}
	if !got.Ready || got.Summary.Total != 8 || got.Summary.Passed != 8 || got.Summary.Failed != 0 {
		t.Fatalf("readiness summary mismatch: ready=%t summary=%#v checks=%#v", got.Ready, got.Summary, got.Checks)
	}
	check := readinessCheckByName(got.Checks, "live_order_plan_artifact")
	if check.Status != domainlive.ReadinessCheckStatusPass || !strings.Contains(check.Details, record.DecisionID) {
		t.Fatalf("artifact check mismatch: %#v", check)
	}
	if riskReader.calls != 1 || riskReader.query.DecisionID != record.DecisionID || riskReader.query.Limit != 2 {
		t.Fatalf("risk reader query mismatch: calls=%d query=%#v", riskReader.calls, riskReader.query)
	}
}

func TestServiceBuildLiveReadinessReportPlanArtifactFailuresTableDriven(t *testing.T) {
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	record := liveRiskDecisionAudit(now.Add(-time.Minute))
	validArtifact := validLiveReadinessPlanArtifact(t, record, "decision-id")

	tests := []struct {
		name          string
		artifact      domainlive.LiveOrderPlanArtifact
		pending       []domainlive.PendingLiveDecision
		riskRecords   []domainrisk.DecisionAuditRecord
		wantDetailSub string
		wantRiskCalls int
	}{
		{
			name: "invalid read-only side effect marker",
			artifact: func() domainlive.LiveOrderPlanArtifact {
				artifact := validArtifact
				artifact.ExchangeContacted = true
				return artifact
			}(),
			pending:       []domainlive.PendingLiveDecision{pendingLiveDecision("risk_decision_live_ready_0001", "BTCUSDT", now)},
			riskRecords:   []domainrisk.DecisionAuditRecord{record},
			wantDetailSub: "exchange_contacted",
		},
		{
			name: "stale artifact age",
			artifact: func() domainlive.LiveOrderPlanArtifact {
				artifact := validArtifact
				artifact.SubmissionCreatedAt = now.Add(-time.Hour)
				return artifact
			}(),
			pending:       []domainlive.PendingLiveDecision{pendingLiveDecision("risk_decision_live_ready_0001", "BTCUSDT", now)},
			riskRecords:   []domainrisk.DecisionAuditRecord{record},
			wantDetailSub: "stale",
		},
		{
			name: "stale risk quantity",
			artifact: func() domainlive.LiveOrderPlanArtifact {
				artifact := validArtifact
				artifact.Quantity = "0.25"
				artifact.Notional = "25000"
				return artifact
			}(),
			pending:       []domainlive.PendingLiveDecision{pendingLiveDecision("risk_decision_live_ready_0001", "BTCUSDT", now)},
			riskRecords:   []domainrisk.DecisionAuditRecord{record},
			wantDetailSub: "quantity",
			wantRiskCalls: 1,
		},
		{
			name: "select pending artifact no longer next FIFO",
			artifact: func() domainlive.LiveOrderPlanArtifact {
				artifact := validLiveReadinessPlanArtifact(t, record, "select-pending")
				artifact.PendingSymbol = "BTCUSDT"
				return artifact
			}(),
			pending:       []domainlive.PendingLiveDecision{pendingLiveDecision("risk_decision_live_other_0001", "BTCUSDT", now)},
			riskRecords:   []domainrisk.DecisionAuditRecord{record},
			wantDetailSub: "no longer the next FIFO pending decision",
		},
		{
			name:          "risk decision missing",
			artifact:      validArtifact,
			pending:       []domainlive.PendingLiveDecision{pendingLiveDecision("risk_decision_live_ready_0001", "BTCUSDT", now)},
			wantDetailSub: "not found",
			wantRiskCalls: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			riskReader := &fakeLiveRiskDecisionReader{records: tt.riskRecords}
			service := applive.NewService(
				applive.WithPendingLiveDecisionReader(&fakePendingLiveDecisionReader{candidates: tt.pending}),
				applive.WithLiveLoopAuditReader(&fakeLiveLoopAuditReader{runs: []domainlive.LiveLoopRunAudit{
					liveLoopAuditRun(now, domainlive.LiveLoopRunStatusCompleted),
				}}),
				applive.WithKillSwitchRepository(&fakeLiveKillSwitchRepository{}),
				applive.WithRiskDecisionReader(riskReader),
				applive.WithClock(clock.FixedClock{Time: now}),
			)
			req := validLiveReadinessRequest()
			req.HasPlanArtifact = true
			req.PlanArtifact = tt.artifact

			got, err := service.BuildLiveReadinessReport(context.Background(), req)
			if err != nil {
				t.Fatalf("build live readiness report: %v", err)
			}
			check := readinessCheckByName(got.Checks, "live_order_plan_artifact")
			if got.Ready || check.Status != domainlive.ReadinessCheckStatusFail || !strings.Contains(check.Details, tt.wantDetailSub) {
				t.Fatalf("expected artifact readiness failure containing %q, ready=%t check=%#v summary=%#v", tt.wantDetailSub, got.Ready, check, got.Summary)
			}
			if riskReader.calls != tt.wantRiskCalls {
				t.Fatalf("risk reader calls mismatch: got %d want %d", riskReader.calls, tt.wantRiskCalls)
			}
		})
	}
}

func TestServiceBuildLiveReadinessReportRejectsMissingDependenciesTableDriven(t *testing.T) {
	tests := []struct {
		name       string
		service    *applive.Service
		wantErrSub string
	}{
		{name: "missing kill switch", service: applive.NewService(applive.WithPendingLiveDecisionReader(&fakePendingLiveDecisionReader{}), applive.WithLiveLoopAuditReader(&fakeLiveLoopAuditReader{})), wantErrSub: "kill switch"},
		{name: "missing pending reader", service: applive.NewService(applive.WithKillSwitchRepository(&fakeLiveKillSwitchRepository{}), applive.WithLiveLoopAuditReader(&fakeLiveLoopAuditReader{})), wantErrSub: "pending decision"},
		{name: "missing audit reader", service: applive.NewService(applive.WithKillSwitchRepository(&fakeLiveKillSwitchRepository{}), applive.WithPendingLiveDecisionReader(&fakePendingLiveDecisionReader{})), wantErrSub: "audit"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.service.BuildLiveReadinessReport(context.Background(), validLiveReadinessRequest())
			if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
		})
	}
}

func TestServiceBuildLiveReadinessReportRequiresRiskReaderForPlanArtifact(t *testing.T) {
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	record := liveRiskDecisionAudit(now.Add(-time.Minute))
	service := applive.NewService(
		applive.WithPendingLiveDecisionReader(&fakePendingLiveDecisionReader{candidates: []domainlive.PendingLiveDecision{
			pendingLiveDecision("risk_decision_live_ready_0001", "BTCUSDT", now),
		}}),
		applive.WithLiveLoopAuditReader(&fakeLiveLoopAuditReader{runs: []domainlive.LiveLoopRunAudit{
			liveLoopAuditRun(now, domainlive.LiveLoopRunStatusCompleted),
		}}),
		applive.WithKillSwitchRepository(&fakeLiveKillSwitchRepository{}),
		applive.WithClock(clock.FixedClock{Time: now}),
	)
	req := validLiveReadinessRequest()
	req.HasPlanArtifact = true
	req.PlanArtifact = validLiveReadinessPlanArtifact(t, record, "decision-id")

	_, err := service.BuildLiveReadinessReport(context.Background(), req)
	if err == nil || !strings.Contains(err.Error(), "risk decision reader") {
		t.Fatalf("expected risk reader dependency error, got %v", err)
	}
}

func TestServiceBuildLiveReadinessReportPropagatesReadFailuresTableDriven(t *testing.T) {
	tests := []struct {
		name       string
		service    *applive.Service
		wantErrSub string
	}{
		{
			name: "kill switch failure",
			service: applive.NewService(
				applive.WithKillSwitchRepository(&fakeLiveKillSwitchRepository{err: errors.New("kill repo unavailable")}),
				applive.WithPendingLiveDecisionReader(&fakePendingLiveDecisionReader{}),
				applive.WithLiveLoopAuditReader(&fakeLiveLoopAuditReader{}),
			),
			wantErrSub: "kill repo unavailable",
		},
		{
			name: "pending failure",
			service: applive.NewService(
				applive.WithKillSwitchRepository(&fakeLiveKillSwitchRepository{}),
				applive.WithPendingLiveDecisionReader(&fakePendingLiveDecisionReader{err: errors.New("pending repo unavailable")}),
				applive.WithLiveLoopAuditReader(&fakeLiveLoopAuditReader{}),
			),
			wantErrSub: "pending repo unavailable",
		},
		{
			name: "audit failure",
			service: applive.NewService(
				applive.WithKillSwitchRepository(&fakeLiveKillSwitchRepository{}),
				applive.WithPendingLiveDecisionReader(&fakePendingLiveDecisionReader{}),
				applive.WithLiveLoopAuditReader(&fakeLiveLoopAuditReader{err: errors.New("audit repo unavailable")}),
			),
			wantErrSub: "audit repo unavailable",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.service.BuildLiveReadinessReport(context.Background(), validLiveReadinessRequest())
			if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
		})
	}
}

func validLiveReadinessRequest() applive.BuildLiveReadinessReportRequest {
	return applive.BuildLiveReadinessReportRequest{
		TradingEnabled:              true,
		TradingMode:                 "live",
		AllowLive:                   true,
		RequireEnvConfirmation:      true,
		ConfirmationEnv:             "TRADING_LIVE_CONFIRM",
		ConfirmationAccepted:        true,
		APIKeyEnv:                   "BYBIT_API_KEY",
		APIKeyPresent:               true,
		APISecretEnv:                "BYBIT_API_SECRET",
		APISecretPresent:            true,
		RequireSubaccount:           true,
		SubaccountConfirmed:         true,
		WithdrawalPermissionAllowed: false,
		InitialLiveCapitalUSDT:      decimal.RequireFromString("50"),
		MaxInitialLiveCapitalUSDT:   decimal.RequireFromString("100"),
		DatabaseMaxOpenConns:        2,
		PendingSymbol:               "BTCUSDT",
		RequirePendingDecision:      true,
	}
}

func readinessCheckByName(checks []domainlive.ReadinessCheck, name string) domainlive.ReadinessCheck {
	for _, check := range checks {
		if check.Name == name {
			return check
		}
	}
	return domainlive.ReadinessCheck{}
}

func validLiveReadinessPlanArtifact(
	t *testing.T,
	record domainrisk.DecisionAuditRecord,
	source string,
) domainlive.LiveOrderPlanArtifact {
	t.Helper()

	identity, err := domainlive.NewDeterministicLiveLoopOrderIdentity(record.DecisionID, "live_loop_readiness_plan_0001")
	if err != nil {
		t.Fatalf("identity: %v", err)
	}
	return domainlive.LiveOrderPlanArtifact{
		SchemaVersion:       domainlive.LiveOrderPlanArtifactSchemaVersion,
		Source:              source,
		RunID:               identity.RunID,
		DecisionID:          record.DecisionID,
		SubmissionID:        identity.SubmissionID,
		ClientOrderID:       identity.ClientOrderID,
		Exchange:            "bybit",
		Category:            "linear",
		Symbol:              record.Symbol,
		Side:                domainlive.OrderSideLong,
		OrderType:           domainlive.OrderTypeMarket,
		TimeInForce:         domainlive.TimeInForceIOC,
		LimitPrice:          "0",
		Quantity:            record.Decision.FinalQuantity.String(),
		EntryPrice:          record.EntryPrice.String(),
		Notional:            record.EntryPrice.Mul(record.Decision.FinalQuantity).String(),
		MaxLoss:             record.Decision.MaxLoss.String(),
		StopLoss:            record.Decision.StopLoss.String(),
		TakeProfit:          record.Decision.TakeProfit.String(),
		Leverage:            record.Leverage.String(),
		Confidence:          record.Confidence,
		DecisionCreatedAt:   record.Decision.CreatedAt,
		RecordedAt:          record.RecordedAt,
		SubmissionCreatedAt: record.RecordedAt.Add(time.Second),
	}
}
