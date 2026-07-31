package main

import (
	"bytes"
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	applive "github.com/VersoIt/Inquisitor/internal/app/live"
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
	wantMaxLoss := record.Decision.FinalQuantity.Mul(record.EntryPrice.Sub(record.Decision.StopLoss).Abs())
	if !record.Decision.MaxLoss.Equal(wantMaxLoss) {
		t.Fatalf("max loss mismatch: got %s want %s", record.Decision.MaxLoss, wantMaxLoss)
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
