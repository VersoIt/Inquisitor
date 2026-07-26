package main

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/shopspring/decimal"

	"github.com/VersoIt/Inquisitor/internal/config"
	domainlive "github.com/VersoIt/Inquisitor/internal/live"
	domainrisk "github.com/VersoIt/Inquisitor/internal/risk"
)

func TestLiveReadinessPendingQueryFromFlagsTableDriven(t *testing.T) {
	tests := []struct {
		name       string
		symbol     string
		limit      int
		want       domainlive.PendingLiveDecisionQuery
		wantErrSub string
	}{
		{name: "default", want: domainlive.PendingLiveDecisionQuery{Limit: 1}},
		{name: "normalizes lowercase symbol", symbol: "btcusdt", limit: 5, want: domainlive.PendingLiveDecisionQuery{Symbol: "BTCUSDT", Limit: 5}},
		{name: "zero limit defaults", symbol: "ETHUSDT", limit: 0, want: domainlive.PendingLiveDecisionQuery{Symbol: "ETHUSDT", Limit: 1}},
		{name: "untrimmed symbol", symbol: " BTCUSDT ", limit: 1, wantErrSub: "trimmed"},
		{name: "negative limit", limit: -1, wantErrSub: "limit"},
		{name: "limit above max", limit: 101, wantErrSub: "limit"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := liveReadinessPendingQueryFromFlags(tt.symbol, tt.limit)
			if tt.wantErrSub != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("query from flags: %v", err)
			}
			if got != tt.want {
				t.Fatalf("query mismatch: got %#v want %#v", got, tt.want)
			}
		})
	}
}

func TestLiveReadinessAuditLimitFromFlagsTableDriven(t *testing.T) {
	tests := []struct {
		name       string
		limit      int
		want       int
		wantErrSub string
	}{
		{name: "default", want: 10},
		{name: "explicit", limit: 5, want: 5},
		{name: "negative", limit: -1, wantErrSub: "limit"},
		{name: "above max", limit: 101, wantErrSub: "limit"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := liveReadinessAuditLimitFromFlags(tt.limit)
			if tt.wantErrSub != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("audit limit from flags: %v", err)
			}
			if got != tt.want {
				t.Fatalf("limit mismatch: got %d want %d", got, tt.want)
			}
		})
	}
}

func TestRunLiveReadinessRejectsUnsafeFlagsBeforeSideEffects(t *testing.T) {
	var loaded bool
	var opened bool

	err := runLiveReadiness(context.Background(), []string{
		"-symbol", " BTCUSDT ",
	}, liveReadinessDependencies{
		loadConfig: func(string) (*config.Config, error) {
			loaded = true
			return &config.Config{}, nil
		},
		openDB: func(context.Context, config.DatabaseConfig) (*sql.DB, error) {
			opened = true
			return nil, nil
		},
		output: &bytes.Buffer{},
	})
	if err == nil || !strings.Contains(err.Error(), "symbol") {
		t.Fatalf("expected symbol error, got %v", err)
	}
	if loaded || opened {
		t.Fatalf("unsafe flags must stop before side effects: loaded=%t opened=%t", loaded, opened)
	}
}

func TestLiveReadinessRequestFromConfigMapsEnvAndSafetyInputs(t *testing.T) {
	cfg := validLiveReadinessConfig()
	req, err := liveReadinessRequestFromConfig(
		cfg,
		mapLookupEnv(map[string]string{
			"TRADING_LIVE_CONFIRM": "true",
			"BYBIT_API_KEY":        "key",
			"BYBIT_API_SECRET":     "secret",
		}),
		true,
		decimal.RequireFromString("100"),
		domainlive.PendingLiveDecisionQuery{Symbol: "BTCUSDT", Limit: 1},
		7,
		true,
	)
	if err != nil {
		t.Fatalf("request from config: %v", err)
	}
	if !req.ConfirmationAccepted || !req.APIKeyPresent || !req.APISecretPresent || !req.SubaccountConfirmed {
		t.Fatalf("env/operator readiness mismatch: %#v", req)
	}
	if req.PendingSymbol != "BTCUSDT" || req.PendingLimit != 1 || req.AuditLimit != 7 || !req.RequirePendingDecision {
		t.Fatalf("query readiness mismatch: %#v", req)
	}
	if !req.InitialLiveCapitalUSDT.Equal(decimal.RequireFromString("50.25")) ||
		!req.MaxInitialLiveCapitalUSDT.Equal(decimal.RequireFromString("100")) ||
		req.DatabaseMaxOpenConns != 2 {
		t.Fatalf("capital/db readiness mismatch: %#v", req)
	}
}

func TestRunLiveReadinessLogsReadyReport(t *testing.T) {
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	pendingReader := &fakeLiveReadinessPendingReader{candidates: []domainlive.PendingLiveDecision{
		liveReadinessPendingDecision("risk_decision_live_ready_cli_0001", "BTCUSDT", now),
	}}
	auditReader := &fakeLiveReadinessAuditReader{runs: []domainlive.LiveLoopRunAudit{
		liveReadinessAuditRun(now.Add(-time.Minute), domainlive.LiveLoopRunStatusCompleted),
	}}
	killSwitch := &fakeLiveReadinessKillSwitchRepository{}

	var output bytes.Buffer
	err = runLiveReadiness(context.Background(), []string{
		"-symbol", "btcusdt",
		"-pending-limit", "1",
		"-audit-limit", "10",
		"-subaccount-confirmed",
		"-max-initial-live-capital-usdt", "100",
	}, liveReadinessDependencies{
		loadConfig: func(string) (*config.Config, error) {
			return validLiveReadinessConfig(), nil
		},
		openDB: func(context.Context, config.DatabaseConfig) (*sql.DB, error) {
			return db, nil
		},
		newPendingReader: func(*sql.DB) domainlive.PendingLiveDecisionReader {
			return pendingReader
		},
		newAuditReader: func(*sql.DB) domainlive.LiveLoopAuditReader {
			return auditReader
		},
		newKillSwitch: func(*sql.DB) domainrisk.KillSwitchRepository {
			return killSwitch
		},
		lookupEnv: mapLookupEnv(map[string]string{
			"TRADING_LIVE_CONFIRM": "true",
			"BYBIT_API_KEY":        "key",
			"BYBIT_API_SECRET":     "secret",
		}),
		output: &output,
	})
	if err != nil {
		t.Fatalf("run live readiness: %v\nlogs:\n%s", err, output.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sql expectations: %v", err)
	}
	if killSwitch.calls != 1 {
		t.Fatalf("kill switch calls mismatch: %d", killSwitch.calls)
	}
	if pendingReader.query.Symbol != "BTCUSDT" || pendingReader.query.Limit != 1 {
		t.Fatalf("pending query mismatch: %#v", pendingReader.query)
	}
	if auditReader.query.Limit != 10 || !auditReader.query.IncludeIterations {
		t.Fatalf("audit query mismatch: %#v", auditReader.query)
	}

	logs := output.String()
	for _, want := range []string{
		`"msg":"live readiness checked"`,
		`"ready":true`,
		`"failed":0`,
		`"pending_candidates":1`,
		`"next_decision_id":"risk_decision_live_ready_cli_0001"`,
		`"msg":"live readiness check"`,
		`"status":"PASS"`,
		`"msg":"live readiness passed"`,
	} {
		if !strings.Contains(logs, want) {
			t.Fatalf("expected logs to contain %s, got\n%s", want, logs)
		}
	}
}

func TestRunLiveReadinessFailsWhenReportHasBlockingChecks(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	var output bytes.Buffer
	err = runLiveReadiness(context.Background(), []string{
		"-symbol", "BTCUSDT",
		"-subaccount-confirmed",
	}, liveReadinessDependencies{
		loadConfig: func(string) (*config.Config, error) {
			return validLiveReadinessConfig(), nil
		},
		openDB: func(context.Context, config.DatabaseConfig) (*sql.DB, error) {
			return db, nil
		},
		newPendingReader: func(*sql.DB) domainlive.PendingLiveDecisionReader {
			return &fakeLiveReadinessPendingReader{}
		},
		newAuditReader: func(*sql.DB) domainlive.LiveLoopAuditReader {
			return &fakeLiveReadinessAuditReader{}
		},
		newKillSwitch: func(*sql.DB) domainrisk.KillSwitchRepository {
			return &fakeLiveReadinessKillSwitchRepository{}
		},
		lookupEnv: mapLookupEnv(map[string]string{
			"TRADING_LIVE_CONFIRM": "true",
			"BYBIT_API_KEY":        "key",
			"BYBIT_API_SECRET":     "secret",
		}),
		output: &output,
	})
	if err == nil || !strings.Contains(err.Error(), "pending_live_decision") {
		t.Fatalf("expected pending readiness failure, got %v\nlogs:\n%s", err, output.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sql expectations: %v", err)
	}
	if !strings.Contains(output.String(), `"ready":false`) || !strings.Contains(output.String(), `"status":"FAIL"`) {
		t.Fatalf("expected failing readiness logs, got\n%s", output.String())
	}
}

type fakeLiveReadinessPendingReader struct {
	query      domainlive.PendingLiveDecisionQuery
	candidates []domainlive.PendingLiveDecision
	calls      int
	err        error
}

func (r *fakeLiveReadinessPendingReader) ListPendingLiveDecisions(
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

type fakeLiveReadinessAuditReader struct {
	query domainlive.LiveLoopAuditQuery
	runs  []domainlive.LiveLoopRunAudit
	calls int
	err   error
}

func (r *fakeLiveReadinessAuditReader) ListLiveLoopRunAudits(
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

type fakeLiveReadinessKillSwitchRepository struct {
	state domainrisk.KillSwitchState
	calls int
	err   error
}

func (r *fakeLiveReadinessKillSwitchRepository) AppendKillSwitchEvent(context.Context, domainrisk.KillSwitchEvent) (domainrisk.KillSwitchStats, error) {
	return domainrisk.KillSwitchStats{}, fmt.Errorf("not implemented")
}

func (r *fakeLiveReadinessKillSwitchRepository) CurrentKillSwitchState(context.Context) (domainrisk.KillSwitchState, error) {
	r.calls++
	if r.err != nil {
		return domainrisk.KillSwitchState{}, r.err
	}
	return r.state, nil
}

func (r *fakeLiveReadinessKillSwitchRepository) ListKillSwitchEvents(context.Context, domainrisk.KillSwitchEventQuery) ([]domainrisk.KillSwitchEvent, error) {
	return nil, fmt.Errorf("not implemented")
}

func validLiveReadinessConfig() *config.Config {
	return &config.Config{
		App: config.AppConfig{LogLevel: "info"},
		Database: config.DatabaseConfig{
			MaxOpenConns: 2,
		},
		Trading: config.TradingConfig{
			Enabled:   true,
			Mode:      "live",
			AllowLive: true,
		},
		Live: config.LiveConfig{
			RequireEnvConfirmation:      true,
			ConfirmationEnv:             "TRADING_LIVE_CONFIRM",
			APIKeyEnv:                   "BYBIT_API_KEY",
			APISecretEnv:                "BYBIT_API_SECRET",
			RequireSubaccount:           true,
			WithdrawalPermissionAllowed: false,
			InitialLiveCapitalUSDT:      50.25,
		},
	}
}

func mapLookupEnv(values map[string]string) func(string) (string, bool) {
	return func(name string) (string, bool) {
		value, ok := values[strings.TrimSpace(name)]
		return value, ok
	}
}

func liveReadinessPendingDecision(decisionID string, symbol string, createdAt time.Time) domainlive.PendingLiveDecision {
	return domainlive.PendingLiveDecision{
		Decision: domainrisk.DecisionAuditRecord{
			DecisionID: decisionID,
			Decision: domainrisk.Decision{
				IntentID:      "risk_intent_" + strings.TrimPrefix(decisionID, "risk_decision_"),
				Approved:      true,
				FinalQuantity: decimal.RequireFromString("0.005"),
				MaxLoss:       decimal.RequireFromString("5"),
				StopLoss:      decimal.RequireFromString("99000"),
				TakeProfit:    decimal.RequireFromString("102000"),
				Reason:        "risk_checks_passed",
				Checks: []domainrisk.Check{{
					Name:   "trading_enabled",
					Passed: true,
				}},
				CreatedAt: createdAt,
			},
			Mode:            domainrisk.ModeLive,
			HypothesisID:    "hypothesis_live_ready_0001",
			StrategyName:    "trend-momentum",
			Symbol:          symbol,
			Side:            domainrisk.SideLong,
			EntryPrice:      decimal.RequireFromString("100000"),
			Leverage:        decimal.RequireFromString("1"),
			Confidence:      80,
			IntentReason:    "signal confirmed",
			IntentCreatedAt: createdAt.Add(-time.Minute),
			RecordedAt:      createdAt.Add(time.Second),
		},
	}
}

func liveReadinessAuditRun(startedAt time.Time, status domainlive.LiveLoopRunStatus) domainlive.LiveLoopRunAudit {
	run := domainlive.LiveLoopRunAudit{
		RunID:                 "live_loop_ready_cli_0001",
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
	return run
}
