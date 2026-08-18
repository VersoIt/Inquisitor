package main

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	applive "github.com/VersoIt/Inquisitor/internal/app/live"
	"github.com/VersoIt/Inquisitor/internal/clock"
	"github.com/VersoIt/Inquisitor/internal/config"
	domainlive "github.com/VersoIt/Inquisitor/internal/live"
	domainrisk "github.com/VersoIt/Inquisitor/internal/risk"
	"github.com/VersoIt/Inquisitor/internal/storage/postgres"
)

func TestRunLiveLoopSmokeRequiresExecuteBeforeSideEffects(t *testing.T) {
	var opened bool
	var migrated bool

	err := runLiveLoopSmoke(context.Background(), []string{
		"-config", "missing-config.yaml",
		"-subaccount-confirmed",
	}, liveLoopSmokeDependencies{
		openDB: func(context.Context, config.DatabaseConfig) (*sql.DB, error) {
			opened = true
			return nil, nil
		},
		applyMigrations: func(context.Context, *sql.DB, string) (postgres.MigrationResult, error) {
			migrated = true
			return postgres.MigrationResult{}, nil
		},
		output: &bytes.Buffer{},
	})
	if err == nil || !strings.Contains(err.Error(), "execute") {
		t.Fatalf("expected execute gate error, got %v", err)
	}
	if opened || migrated {
		t.Fatalf("execute gate must run before side effects: opened=%t migrated=%t", opened, migrated)
	}
}

func TestDeterministicLiveLoopSmokeIdentityIsStableAndBybitSafe(t *testing.T) {
	first, err := deterministicLiveLoopSmokeIdentity(" risk_decision_live_smoke_001 ", "live_loop_smoke_001")
	if err != nil {
		t.Fatalf("identity: %v", err)
	}
	second, err := deterministicLiveLoopSmokeIdentity("risk_decision_live_smoke_001", "live_loop_smoke_001")
	if err != nil {
		t.Fatalf("identity: %v", err)
	}

	if first != second {
		t.Fatalf("identity must be stable after trimming: first=%#v second=%#v", first, second)
	}
	if len(first.ClientOrderID) > 36 || len(first.SubmissionID) > 36 {
		t.Fatalf("identity must fit Bybit orderLinkId limit: %#v", first)
	}
	for _, value := range []string{first.RunID, first.DecisionID, first.SubmissionID, first.ClientOrderID, first.ExchangeOrder} {
		if strings.TrimSpace(value) == "" {
			t.Fatalf("identity value must not be blank: %#v", first)
		}
	}

	_, err = deterministicLiveLoopSmokeIdentity("risk_decision_live_smoke_001", " live_loop_smoke_001 ")
	if err == nil || !strings.Contains(err.Error(), "run-id") || !strings.Contains(err.Error(), "trimmed") {
		t.Fatalf("expected trimmed run id error, got %v", err)
	}
}

func TestLiveLoopSmokePreflightConfigModesTableDriven(t *testing.T) {
	cfg := liveLoopSmokeTestConfig()
	maxCapital := decimal.RequireFromString("100")

	tests := []struct {
		name              string
		requireLiveConfig bool
		wantTrading       bool
		wantMode          string
		wantAllowLive     bool
		wantErrSub        string
	}{
		{
			name:              "local smoke overrides paper config into safe fake live preflight",
			requireLiveConfig: false,
			wantTrading:       true,
			wantMode:          "live",
			wantAllowLive:     true,
		},
		{
			name:              "strict deployment mode exposes non-live config",
			requireLiveConfig: true,
			wantTrading:       false,
			wantMode:          "paper",
			wantAllowLive:     false,
			wantErrSub:        "trading.enabled",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := liveLoopSmokePreflightRequestFromConfig(&cfg, true, maxCapital, tt.requireLiveConfig)
			if err != nil {
				t.Fatalf("preflight request: %v", err)
			}
			if got.TradingEnabled != tt.wantTrading || got.TradingMode != tt.wantMode || got.AllowLive != tt.wantAllowLive {
				t.Fatalf("preflight mode mismatch: got enabled=%t mode=%s allow_live=%t", got.TradingEnabled, got.TradingMode, got.AllowLive)
			}
			err = validateLiveLoopSmokeStaticPreflightGate(got)
			if tt.wantErrSub != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("static preflight gate: %v", err)
			}
		})
	}
}

func TestLiveLoopSmokeReadinessRequestConfigModesTableDriven(t *testing.T) {
	cfg := liveLoopSmokeTestConfig()
	plan := domainlive.LiveOrderPlanArtifact{
		SchemaVersion:       domainlive.LiveOrderPlanArtifactSchemaVersion,
		Source:              domainlive.LiveOrderPlanArtifactSourceDecisionID,
		RunID:               "live_loop_smoke_001",
		DecisionID:          "risk_decision_live_smoke_001",
		SubmissionID:        "live_sub_smoke_001",
		ClientOrderID:       "inq_live_smoke_001",
		Exchange:            "bybit",
		Category:            "linear",
		Symbol:              "BTCUSDT",
		Side:                domainlive.OrderSideLong,
		OrderType:           domainlive.OrderTypeMarket,
		TimeInForce:         domainlive.TimeInForceIOC,
		LimitPrice:          "0",
		Quantity:            "0.005",
		EntryPrice:          "100000",
		Notional:            "500",
		MaxLoss:             "5",
		StopLoss:            "99000",
		TakeProfit:          "102000",
		Leverage:            "1",
		Confidence:          80,
		DecisionCreatedAt:   time.Date(2026, 8, 2, 11, 59, 0, 0, time.UTC),
		RecordedAt:          time.Date(2026, 8, 2, 11, 59, 30, 0, time.UTC),
		SubmissionCreatedAt: time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC),
	}

	tests := []struct {
		name              string
		mutate            func(*config.Config)
		requireLiveConfig bool
		wantTrading       bool
		wantMode          string
		wantAllowLive     bool
		wantAPIKeyPresent bool
	}{
		{
			name:              "local smoke overrides paper config into readiness live mode",
			wantTrading:       true,
			wantMode:          "live",
			wantAllowLive:     true,
			wantAPIKeyPresent: true,
		},
		{
			name:              "strict deployment mode preserves config flags",
			requireLiveConfig: true,
			wantTrading:       false,
			wantMode:          "paper",
			wantAllowLive:     false,
			wantAPIKeyPresent: true,
		},
		{
			name: "blank env names fail closed",
			mutate: func(cfg *config.Config) {
				cfg.Live.APIKeyEnv = ""
			},
			wantTrading:       true,
			wantMode:          "live",
			wantAllowLive:     true,
			wantAPIKeyPresent: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			candidate := cfg
			if tt.mutate != nil {
				tt.mutate(&candidate)
			}
			got, err := liveLoopSmokeReadinessRequestFromConfig(
				&candidate,
				true,
				decimal.RequireFromString("100"),
				tt.requireLiveConfig,
				plan,
			)
			if err != nil {
				t.Fatalf("readiness request: %v", err)
			}
			if got.TradingEnabled != tt.wantTrading ||
				got.TradingMode != tt.wantMode ||
				got.AllowLive != tt.wantAllowLive ||
				got.APIKeyPresent != tt.wantAPIKeyPresent ||
				got.PendingSymbol != "BTCUSDT" ||
				got.PendingLimit != 1 ||
				got.AuditLimit != defaultLiveLoopSmokeAuditLimit ||
				!got.HasPlanArtifact {
				t.Fatalf("readiness request mismatch: %#v", got)
			}
		})
	}
}

func TestLiveLoopSmokeStaticPreflightGateTableDriven(t *testing.T) {
	valid := validLiveLoopSmokePreflightRequest()
	tests := []struct {
		name       string
		mutate     func(*configMutationTarget)
		wantErrSub string
	}{
		{
			name:       "valid",
			mutate:     func(*configMutationTarget) {},
			wantErrSub: "",
		},
		{
			name: "missing subaccount confirmation",
			mutate: func(target *configMutationTarget) {
				target.req.SubaccountConfirmed = false
			},
			wantErrSub: "dedicated live subaccount",
		},
		{
			name: "env confirmation not required",
			mutate: func(target *configMutationTarget) {
				target.req.RequireEnvConfirmation = false
			},
			wantErrSub: "require_env_confirmation",
		},
		{
			name: "subaccount not required",
			mutate: func(target *configMutationTarget) {
				target.req.RequireSubaccount = false
			},
			wantErrSub: "require_subaccount",
		},
		{
			name: "withdrawal permission allowed",
			mutate: func(target *configMutationTarget) {
				target.req.WithdrawalPermissionAllowed = true
			},
			wantErrSub: "withdrawal",
		},
		{
			name: "capital above operator cap",
			mutate: func(target *configMutationTarget) {
				target.req.InitialLiveCapitalUSDT = decimal.RequireFromString("101")
			},
			wantErrSub: "initial live capital",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target := configMutationTarget{req: valid}
			tt.mutate(&target)
			err := validateLiveLoopSmokeStaticPreflightGate(target.req)
			if tt.wantErrSub != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("validate static gate: %v", err)
			}
		})
	}
}

func TestLiveLoopSmokeDecisionIsValidAndMatchesOrderRiskMath(t *testing.T) {
	now := time.Now().UTC()
	record := liveLoopSmokeDecision("risk_decision_live_smoke_001", &config.Config{
		Exchange: config.ExchangeConfig{Symbols: []string{"btcusdt"}},
	}, now)

	if err := domainrisk.ValidateDecisionAuditRecord(record); err != nil {
		t.Fatalf("smoke decision must be a valid LIVE risk decision: %v", err)
	}
	if record.Mode != domainrisk.ModeLive || !record.Decision.Approved || record.Symbol != "BTCUSDT" {
		t.Fatalf("smoke decision identity mismatch: %#v", record)
	}
	if !record.Decision.FinalQuantity.Equal(decimal.RequireFromString("0.001")) ||
		!record.EntryPrice.Mul(record.Decision.FinalQuantity).Equal(decimal.RequireFromString("100")) {
		t.Fatalf("smoke decision must stay within first live micro notional: quantity=%s notional=%s", record.Decision.FinalQuantity, record.EntryPrice.Mul(record.Decision.FinalQuantity))
	}
	wantMaxLoss := record.Decision.FinalQuantity.Mul(record.EntryPrice.Sub(record.Decision.StopLoss).Abs())
	if !record.Decision.MaxLoss.Equal(wantMaxLoss) {
		t.Fatalf("max loss mismatch: got %s want %s", record.Decision.MaxLoss, wantMaxLoss)
	}
}

func TestBuildAndValidateLiveLoopSmokeHandoffChecksArtifactChain(t *testing.T) {
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	cfg := liveLoopSmokeTestConfig()
	identity, err := deterministicLiveLoopSmokeIdentity("risk_decision_live_smoke_001", "live_loop_smoke_001")
	if err != nil {
		t.Fatalf("identity: %v", err)
	}
	decision := liveLoopSmokeDecision(identity.DecisionID, &cfg, now)
	riskReader := &fakeLiveLoopSmokeRiskDecisionReader{records: []domainrisk.DecisionAuditRecord{decision}}
	pendingReader := &fakeLiveLoopSmokePendingReader{candidates: []domainlive.PendingLiveDecision{{Decision: decision}}}
	auditReader := &fakeLiveLoopSmokeAuditReader{}
	killSwitch := &fakeLiveLoopSmokeKillSwitch{}
	service := applive.NewService(
		applive.WithRiskDecisionReader(riskReader),
		applive.WithPendingLiveDecisionReader(pendingReader),
		applive.WithLiveLoopAuditReader(auditReader),
		applive.WithKillSwitchRepository(killSwitch),
		applive.WithClock(clock.FixedClock{Time: now.Add(time.Second)}),
	)

	handoff, err := buildAndValidateLiveLoopSmokeHandoff(
		context.Background(),
		service,
		&cfg,
		"configs/config.example.yaml",
		identity,
		decimal.RequireFromString("100"),
		true,
		false,
	)
	if err != nil {
		t.Fatalf("build smoke handoff: %v", err)
	}
	if handoff.PlanArtifact.DecisionID != identity.DecisionID ||
		handoff.PlanArtifact.SubmissionID != identity.SubmissionID ||
		handoff.ReadinessArtifact.Pending.NextDecisionID != identity.DecisionID ||
		handoff.KillSwitchPath != defaultLiveLoopSmokeKillSwitchPath ||
		handoff.KillSwitchArtifact.State == nil ||
		handoff.KillSwitchArtifact.State.Active ||
		handoff.AuditArtifact.Summary.ReviewStatus != domainlive.LiveLoopAuditReviewStatusClear ||
		!handoff.DeploymentReport.Ready ||
		!handoff.DeploymentArtifact.Ready ||
		handoff.DeploymentArtifact.SchemaVersion != domainlive.LiveDeploymentCheckArtifactSchemaVersion ||
		handoff.OpsReportArtifact.Status != domainlive.LiveOpsStatusClear {
		t.Fatalf("handoff summary mismatch: %#v", handoff)
	}
	for _, tt := range []struct {
		name     string
		handoff  string
		artifact string
		sha256   string
		wantPath string
		wantSHA  string
	}{
		{
			name:     "plan",
			handoff:  handoff.PlanPath,
			artifact: handoff.DeploymentArtifact.PlanFile.Path,
			sha256:   handoff.PlanFileSHA256,
			wantPath: defaultLiveLoopSmokePlanArtifactPath,
			wantSHA:  handoff.DeploymentArtifact.PlanFile.SHA256,
		},
		{
			name:     "readiness",
			handoff:  handoff.ReadinessPath,
			artifact: handoff.DeploymentArtifact.ReadinessFile.Path,
			sha256:   handoff.ReadinessFileSHA256,
			wantPath: defaultLiveLoopSmokeReadinessPath,
			wantSHA:  handoff.DeploymentArtifact.ReadinessFile.SHA256,
		},
		{
			name:     "audit",
			handoff:  handoff.AuditPath,
			artifact: handoff.DeploymentArtifact.AuditFile.Path,
			sha256:   handoff.AuditFileSHA256,
			wantPath: defaultLiveLoopSmokeAuditArtifactPath,
			wantSHA:  handoff.DeploymentArtifact.AuditFile.SHA256,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if tt.handoff != tt.wantPath || tt.artifact != tt.wantPath {
				t.Fatalf("path mismatch: handoff=%q artifact=%q want=%q", tt.handoff, tt.artifact, tt.wantPath)
			}
			if len(tt.sha256) != 64 || tt.sha256 != tt.wantSHA {
				t.Fatalf("sha256 mismatch: got=%q want=%q", tt.sha256, tt.wantSHA)
			}
		})
	}
	if handoff.ReadinessArtifact.PlanFile == nil ||
		handoff.ReadinessArtifact.PlanFile.SHA256 != handoff.PlanFileSHA256 ||
		handoff.DeploymentPath != defaultLiveLoopSmokeDeployCheckPath ||
		handoff.DeploymentArtifact.Execution.SelectedDecisionID != identity.DecisionID ||
		handoff.DeploymentArtifact.Execution.MaxInitialLiveCapitalUSDT != "100" ||
		handoff.OpsReportPath != defaultLiveLoopSmokeOpsReportPath ||
		len(handoff.OpsReportFileSHA256) != 64 {
		t.Fatalf("artifact linkage mismatch: %#v", handoff)
	}
	if handoff.OpsReportArtifact.SchemaVersion != domainlive.LiveOpsReportArtifactSchemaVersion ||
		handoff.OpsReportArtifact.ConfigPath != "configs/config.example.yaml" ||
		handoff.OpsReportArtifact.Pending.Limit != 1 ||
		handoff.OpsReportArtifact.Pending.NextDecisionID != identity.DecisionID ||
		handoff.OpsReportArtifact.Audit.ReviewStatus != domainlive.LiveLoopAuditReviewStatusClear ||
		handoff.OpsReportArtifact.KillSwitch.Active {
		t.Fatalf("ops report artifact mismatch: %#v", handoff.OpsReportArtifact)
	}
	if err := domainlive.ValidateLiveOpsReportArtifactFreshness(
		handoff.OpsReportArtifact,
		handoff.OpsReportArtifact.CreatedAt,
		domainlive.DefaultLiveOpsReportArtifactMaxAge,
	); err != nil {
		t.Fatalf("ops report freshness: %v", err)
	}
	if err := domainlive.ValidateLiveDeploymentCheckArtifactHandoff(handoff.DeploymentArtifact, domainlive.LiveDeploymentCheckArtifactHandoffExecution{
		ConfigPath:                "configs/config.example.yaml",
		PlanPath:                  handoff.PlanPath,
		PlanFileSHA256:            handoff.PlanFileSHA256,
		PlanArtifact:              handoff.PlanArtifact,
		ReadinessPath:             handoff.ReadinessPath,
		ReadinessFileSHA256:       handoff.ReadinessFileSHA256,
		ReadinessArtifact:         handoff.ReadinessArtifact,
		AuditPath:                 handoff.AuditPath,
		AuditFileSHA256:           handoff.AuditFileSHA256,
		AuditArtifact:             handoff.AuditArtifact,
		Execute:                   true,
		SubaccountConfirmed:       true,
		DecisionID:                identity.DecisionID,
		SelectedDecisionID:        identity.DecisionID,
		MaxInitialLiveCapitalUSDT: decimal.RequireFromString("100"),
		MaxIterations:             1,
		MaxRuntime:                defaultLiveLoopSmokeMaxRuntime,
		IterationTimeout:          defaultLiveLoopSmokeIterationTimeout,
	}); err != nil {
		t.Fatalf("deployment artifact handoff: %v", err)
	}
	if err := domainrisk.ValidateKillSwitchArtifactHandoff(handoff.KillSwitchArtifact, domainrisk.KillSwitchArtifactHandoffExecution{
		ConfigPath: "configs/config.example.yaml",
	}); err != nil {
		t.Fatalf("kill switch artifact handoff: %v", err)
	}
	if err := domainlive.ValidateKillSwitchReadinessArtifactHandoff(handoff.KillSwitchArtifact, handoff.ReadinessArtifact); err != nil {
		t.Fatalf("kill switch readiness handoff: %v", err)
	}
	if riskReader.calls != 2 || pendingReader.calls != 2 || auditReader.calls != 2 || killSwitch.calls != 2 {
		t.Fatalf("reader calls mismatch: risk=%d pending=%d audit=%d kill=%d", riskReader.calls, pendingReader.calls, auditReader.calls, killSwitch.calls)
	}
}

func TestBuildAndValidateLiveLoopSmokeOpsReportTableDriven(t *testing.T) {
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	cfg := liveLoopSmokeTestConfig()
	decision := liveLoopSmokeDecision(defaultLiveLoopSmokeDecisionID, &cfg, now)

	tests := []struct {
		name       string
		pending    []domainlive.PendingLiveDecision
		killSwitch domainrisk.KillSwitchState
		wantStatus domainlive.LiveOpsStatus
		wantErrSub string
	}{
		{
			name:       "clear when pending decision audit and kill switch are healthy",
			pending:    []domainlive.PendingLiveDecision{{Decision: decision}},
			wantStatus: domainlive.LiveOpsStatusClear,
		},
		{
			name:       "attention is rejected when no pending decision is visible",
			wantStatus: domainlive.LiveOpsStatusAttention,
			wantErrSub: string(domainlive.LiveOpsStatusAttention),
		},
		{
			name:    "blocked is rejected when kill switch is active",
			pending: []domainlive.PendingLiveDecision{{Decision: decision}},
			killSwitch: domainrisk.KillSwitchState{
				Active:    true,
				Reason:    "operator pause",
				Source:    "operator",
				UpdatedAt: now.Add(-time.Minute),
			},
			wantStatus: domainlive.LiveOpsStatusBlocked,
			wantErrSub: string(domainlive.LiveOpsStatusBlocked),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := applive.NewService(
				applive.WithPendingLiveDecisionReader(&fakeLiveLoopSmokePendingReader{candidates: tt.pending}),
				applive.WithLiveLoopAuditReader(&fakeLiveLoopSmokeAuditReader{}),
				applive.WithKillSwitchRepository(&fakeLiveLoopSmokeKillSwitch{state: tt.killSwitch}),
			)

			artifact, err := buildAndValidateLiveLoopSmokeOpsReport(
				context.Background(),
				service,
				"configs/config.example.yaml",
				"btcusdt",
				now,
			)
			if tt.wantErrSub != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("build ops report: %v", err)
			}
			if artifact.Status != tt.wantStatus ||
				artifact.Pending.Symbol != "BTCUSDT" ||
				artifact.Pending.Limit != 1 ||
				artifact.Audit.Limit != defaultLiveLoopSmokeAuditLimit {
				t.Fatalf("ops artifact mismatch: %#v", artifact)
			}
		})
	}
}

func TestBuildAndValidateLiveLoopSmokeHandoffRunsDeployGate(t *testing.T) {
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	cfg := liveLoopSmokeTestConfig()
	identity, err := deterministicLiveLoopSmokeIdentity("risk_decision_live_smoke_001", "live_loop_smoke_001")
	if err != nil {
		t.Fatalf("identity: %v", err)
	}
	decision := liveLoopSmokeDecision(identity.DecisionID, &cfg, now)
	decision.Decision.FinalQuantity = decimal.RequireFromString("0.002")
	decision.Decision.MaxLoss = decimal.RequireFromString("2")
	service := applive.NewService(
		applive.WithRiskDecisionReader(&fakeLiveLoopSmokeRiskDecisionReader{records: []domainrisk.DecisionAuditRecord{decision}}),
		applive.WithPendingLiveDecisionReader(&fakeLiveLoopSmokePendingReader{candidates: []domainlive.PendingLiveDecision{{Decision: decision}}}),
		applive.WithLiveLoopAuditReader(&fakeLiveLoopSmokeAuditReader{}),
		applive.WithKillSwitchRepository(&fakeLiveLoopSmokeKillSwitch{}),
		applive.WithClock(clock.FixedClock{Time: now.Add(time.Second)}),
	)

	_, err = buildAndValidateLiveLoopSmokeHandoff(
		context.Background(),
		service,
		&cfg,
		"configs/config.example.yaml",
		identity,
		decimal.RequireFromString("100"),
		true,
		false,
	)
	if err == nil || !strings.Contains(err.Error(), "live_micro_capital") {
		t.Fatalf("expected deploy gate live_micro_capital error, got %v", err)
	}
}

func TestFakeLiveLoopSmokeExchangeLifecycle(t *testing.T) {
	exchange := newFakeLiveLoopSmokeExchange("smoke_order_0001")
	query := domainlive.PositionSnapshotQuery{Exchange: "bybit", Category: "linear", Symbol: "BTCUSDT"}
	preflightReferenceAt := time.Now().UTC()

	flat, err := exchange.GetPositionSnapshot(context.Background(), query)
	if err != nil {
		t.Fatalf("flat position: %v", err)
	}
	if flat.Open {
		t.Fatalf("pre-submit position must be flat: %#v", flat)
	}
	if flat.ObservedAt.After(preflightReferenceAt) {
		t.Fatalf("flat position observed_at must be safe for preflight reference time: observed=%s reference=%s", flat.ObservedAt, preflightReferenceAt)
	}

	submission := validLiveLoopSmokeSubmission(t)
	ack, err := exchange.SubmitOrder(context.Background(), submission)
	if err != nil {
		t.Fatalf("submit smoke order: %v", err)
	}
	if ack.Status != domainlive.OrderStatusAccepted || ack.ExchangeOrderID != "smoke_order_0001" {
		t.Fatalf("ack mismatch: %#v", ack)
	}

	status, err := exchange.GetOrderStatus(context.Background(), domainlive.OrderStatusQuery{
		Exchange:      "bybit",
		Category:      "linear",
		Symbol:        "BTCUSDT",
		ClientOrderID: submission.ClientOrderID,
	})
	if err != nil {
		t.Fatalf("order status: %v", err)
	}
	if status.ExchangeStatus != domainlive.ExchangeOrderStatusFilled || !status.CumulativeExecutedQuantity.Equal(submission.Quantity) {
		t.Fatalf("status must report a filled smoke order: %#v", status)
	}

	open, err := exchange.GetPositionSnapshot(context.Background(), query)
	if err != nil {
		t.Fatalf("open position: %v", err)
	}
	if !open.Open || open.Side != submission.Side || !open.Size.Equal(submission.Quantity) {
		t.Fatalf("post-submit position mismatch: %#v", open)
	}
	if !status.ObservedAt.After(flat.ObservedAt) || !open.ObservedAt.After(status.ObservedAt) {
		t.Fatalf("smoke observed_at timestamps must advance: flat=%s status=%s open=%s", flat.ObservedAt, status.ObservedAt, open.ObservedAt)
	}
	if exchange.submitCalls != 1 || exchange.statusCalls != 1 || exchange.positionCalls != 2 {
		t.Fatalf("exchange call counts mismatch: submit=%d status=%d position=%d", exchange.submitCalls, exchange.statusCalls, exchange.positionCalls)
	}
}

type fakeLiveLoopSmokeRiskDecisionReader struct {
	records []domainrisk.DecisionAuditRecord
	calls   int
	query   domainrisk.DecisionAuditQuery
	err     error
}

func (r *fakeLiveLoopSmokeRiskDecisionReader) ListDecisions(
	_ context.Context,
	query domainrisk.DecisionAuditQuery,
) ([]domainrisk.DecisionAuditRecord, error) {
	r.calls++
	r.query = query
	if r.err != nil {
		return nil, r.err
	}
	return append([]domainrisk.DecisionAuditRecord(nil), r.records...), nil
}

type fakeLiveLoopSmokePendingReader struct {
	candidates []domainlive.PendingLiveDecision
	calls      int
	query      domainlive.PendingLiveDecisionQuery
	err        error
}

func (r *fakeLiveLoopSmokePendingReader) ListPendingLiveDecisions(
	_ context.Context,
	query domainlive.PendingLiveDecisionQuery,
) ([]domainlive.PendingLiveDecision, error) {
	r.calls++
	r.query = query
	if r.err != nil {
		return nil, r.err
	}
	return append([]domainlive.PendingLiveDecision(nil), r.candidates...), nil
}

type fakeLiveLoopSmokeAuditReader struct {
	runs  []domainlive.LiveLoopRunAudit
	calls int
	query domainlive.LiveLoopAuditQuery
	err   error
}

func (r *fakeLiveLoopSmokeAuditReader) ListLiveLoopRunAudits(
	_ context.Context,
	query domainlive.LiveLoopAuditQuery,
) ([]domainlive.LiveLoopRunAudit, error) {
	r.calls++
	r.query = query
	if r.err != nil {
		return nil, r.err
	}
	return append([]domainlive.LiveLoopRunAudit(nil), r.runs...), nil
}

type fakeLiveLoopSmokeKillSwitch struct {
	state domainrisk.KillSwitchState
	calls int
	err   error
}

func (r *fakeLiveLoopSmokeKillSwitch) AppendKillSwitchEvent(context.Context, domainrisk.KillSwitchEvent) (domainrisk.KillSwitchStats, error) {
	return domainrisk.KillSwitchStats{}, errors.New("not implemented")
}

func (r *fakeLiveLoopSmokeKillSwitch) CurrentKillSwitchState(context.Context) (domainrisk.KillSwitchState, error) {
	r.calls++
	if r.err != nil {
		return domainrisk.KillSwitchState{}, r.err
	}
	return r.state, nil
}

func (r *fakeLiveLoopSmokeKillSwitch) ListKillSwitchEvents(context.Context, domainrisk.KillSwitchEventQuery) ([]domainrisk.KillSwitchEvent, error) {
	return nil, errors.New("not implemented")
}

func TestRunLiveLoopSmokeRejectsMissingSubaccountBeforeConfigAndDB(t *testing.T) {
	var opened bool
	var migrated bool

	err := runLiveLoopSmoke(context.Background(), []string{
		"-config", "missing-config.yaml",
		"-execute",
	}, liveLoopSmokeDependencies{
		openDB: func(context.Context, config.DatabaseConfig) (*sql.DB, error) {
			opened = true
			return nil, nil
		},
		applyMigrations: func(context.Context, *sql.DB, string) (postgres.MigrationResult, error) {
			migrated = true
			return postgres.MigrationResult{}, nil
		},
		output: &bytes.Buffer{},
	})
	if err == nil || !strings.Contains(err.Error(), "subaccount-confirmed") {
		t.Fatalf("expected subaccount gate error, got %v", err)
	}
	if opened || migrated {
		t.Fatalf("subaccount gate must run before side effects: opened=%t migrated=%t", opened, migrated)
	}
}

func liveLoopSmokeTestConfig() config.Config {
	return config.Config{
		App: config.AppConfig{LogLevel: "info"},
		Exchange: config.ExchangeConfig{
			Primary:  "bybit",
			Category: "linear",
			Symbols:  []string{"BTCUSDT", "ETHUSDT"},
		},
		Trading: config.TradingConfig{
			Enabled:      false,
			Mode:         "paper",
			AllowLive:    false,
			BaseCurrency: "USDT",
		},
		Live: config.LiveConfig{
			RequireEnvConfirmation:      true,
			ConfirmationEnv:             "TRADING_LIVE_CONFIRM",
			APIKeyEnv:                   "BYBIT_API_KEY",
			APISecretEnv:                "BYBIT_API_SECRET",
			RequireSubaccount:           true,
			WithdrawalPermissionAllowed: false,
			InitialLiveCapitalUSDT:      50,
		},
	}
}

type configMutationTarget struct {
	req applive.PreflightLiveStartupRequest
}

func validLiveLoopSmokePreflightRequest() applive.PreflightLiveStartupRequest {
	return applive.PreflightLiveStartupRequest{
		TradingEnabled:            true,
		TradingMode:               "live",
		AllowLive:                 true,
		RequireEnvConfirmation:    true,
		ConfirmationEnv:           "TRADING_LIVE_CONFIRM",
		APIKeyEnv:                 "BYBIT_API_KEY",
		APISecretEnv:              "BYBIT_API_SECRET",
		RequireSubaccount:         true,
		SubaccountConfirmed:       true,
		InitialLiveCapitalUSDT:    decimal.RequireFromString("50"),
		MaxInitialLiveCapitalUSDT: decimal.RequireFromString("100"),
		ExpectedAccount: domainlive.AccountSnapshotQuery{
			Exchange:    "bybit",
			AccountType: domainlive.AccountTypeUnified,
		},
		AccountBaseCurrency:    "USDT",
		MaxAccountSnapshotAge:  time.Second,
		ExpectedFlatPositions:  []domainlive.PositionSnapshotQuery{{Exchange: "bybit", Category: "linear", Symbol: "BTCUSDT"}},
		MaxPositionSnapshotAge: time.Second,
	}
}

func validLiveLoopSmokeSubmission(t *testing.T) domainlive.OrderSubmission {
	t.Helper()

	submission, err := domainlive.NewOrderSubmission(domainlive.OrderSubmissionInput{
		SubmissionID:     "live_sub_smoke_0001",
		ClientOrderID:    "inq_live_smoke_0001",
		DecisionID:       "risk_decision_live_smoke_001",
		DecisionApproved: true,
		IntentID:         "risk_intent_live_smoke_001",
		RiskMode:         domainlive.RiskModeLive,
		Exchange:         "bybit",
		Category:         "linear",
		Symbol:           "BTCUSDT",
		Side:             domainlive.OrderSideLong,
		Type:             domainlive.OrderTypeMarket,
		TimeInForce:      domainlive.TimeInForceIOC,
		Quantity:         decimal.RequireFromString("0.005"),
		ReferencePrice:   decimal.RequireFromString("100000"),
		StopLoss:         decimal.RequireFromString("99000"),
		TakeProfit:       decimal.RequireFromString("102000"),
		Leverage:         decimal.RequireFromString("1"),
		MaxLoss:          decimal.RequireFromString("5"),
		Confidence:       80,
		Reason:           "risk_checks_passed",
		CreatedAt:        time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("new smoke submission: %v", err)
	}
	return submission
}
