package live_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	applive "github.com/VersoIt/Inquisitor/internal/app/live"
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
