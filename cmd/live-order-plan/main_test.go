package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/shopspring/decimal"

	applive "github.com/VersoIt/Inquisitor/internal/app/live"
	"github.com/VersoIt/Inquisitor/internal/clock"
	"github.com/VersoIt/Inquisitor/internal/config"
	domainlive "github.com/VersoIt/Inquisitor/internal/live"
	domainrisk "github.com/VersoIt/Inquisitor/internal/risk"
)

func TestRunLiveOrderPlanPreviewsExplicitDecisionWithoutExchangeOrWrites(t *testing.T) {
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	decision := liveOrderPlanRiskDecision("risk_decision_live_plan_cli_0001", "BTCUSDT", now.Add(-time.Minute))
	reader := &fakeLiveOrderPlanRiskDecisionReader{records: []domainrisk.DecisionAuditRecord{decision}}
	pendingReader := &fakeLiveOrderPlanPendingReader{}

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	identity, err := domainlive.NewDeterministicLiveLoopOrderIdentity(decision.DecisionID, "live_loop_plan_cli_0001")
	if err != nil {
		t.Fatalf("identity: %v", err)
	}

	artifactPath := filepath.Join(t.TempDir(), "artifacts", "live-order-plan.json")
	var output bytes.Buffer
	err = runLiveOrderPlan(context.Background(), []string{
		"-decision-id", " risk_decision_live_plan_cli_0001 ",
		"-run-id", "live_loop_plan_cli_0001",
		"-artifact-path", artifactPath,
	}, liveOrderPlanDependencies{
		loadConfig: func(string) (*config.Config, error) {
			return validLiveOrderPlanConfig(), nil
		},
		openDB: func(context.Context, config.DatabaseConfig) (*sql.DB, error) {
			return db, nil
		},
		newRiskDecisionReader: func(*sql.DB) applive.RiskDecisionReader {
			return reader
		},
		newPendingReader: func(*sql.DB) domainlive.PendingLiveDecisionReader {
			return pendingReader
		},
		clock:  clock.FixedClock{Time: now},
		output: &output,
	})
	if err != nil {
		t.Fatalf("run live order plan: %v\nlogs:\n%s", err, output.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sql expectations: %v", err)
	}
	if reader.calls != 1 || reader.query.DecisionID != "risk_decision_live_plan_cli_0001" || reader.query.Limit != 2 {
		t.Fatalf("risk decision reader mismatch: calls=%d query=%#v", reader.calls, reader.query)
	}
	if pendingReader.calls != 0 {
		t.Fatalf("explicit decision plan must not scan pending decisions, calls=%d", pendingReader.calls)
	}
	artifact := readLiveOrderPlanArtifact(t, artifactPath)
	if artifact.SchemaVersion != domainlive.LiveOrderPlanArtifactSchemaVersion ||
		artifact.RunID != "live_loop_plan_cli_0001" ||
		artifact.DecisionID != decision.DecisionID ||
		artifact.SubmissionID != identity.SubmissionID ||
		artifact.ClientOrderID != identity.ClientOrderID ||
		artifact.OrderType != domainlive.OrderTypeMarket ||
		artifact.TimeInForce != domainlive.TimeInForceIOC ||
		artifact.ExchangeContacted ||
		artifact.OrderSubmitted ||
		artifact.Reserved {
		t.Fatalf("artifact mismatch: %#v", artifact)
	}

	logs := output.String()
	for _, want := range []string{
		`"msg":"live order plan"`,
		`"source":"decision-id"`,
		`"run_id":"live_loop_plan_cli_0001"`,
		`"decision_id":"risk_decision_live_plan_cli_0001"`,
		`"submission_id":"` + identity.SubmissionID + `"`,
		`"client_order_id":"` + identity.ClientOrderID + `"`,
		`"symbol":"BTCUSDT"`,
		`"order_type":"MARKET"`,
		`"time_in_force":"IOC"`,
		`"quantity":"0.005"`,
		`"notional":"500"`,
		`"reserved":false`,
		`"exchange_contacted":false`,
		`"order_submitted":false`,
		`"msg":"live order plan artifact written"`,
	} {
		if !strings.Contains(logs, want) {
			t.Fatalf("expected logs to contain %s, got\n%s", want, logs)
		}
	}
}

func TestRunLiveOrderPlanSelectsPendingDecisionForUnreservedPreview(t *testing.T) {
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	decision := liveOrderPlanRiskDecision("risk_decision_live_plan_pending_0001", "BTCUSDT", now.Add(-time.Minute))
	reader := &fakeLiveOrderPlanRiskDecisionReader{records: []domainrisk.DecisionAuditRecord{decision}}
	pendingReader := &fakeLiveOrderPlanPendingReader{candidates: []domainlive.PendingLiveDecision{{Decision: decision}}}

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	var output bytes.Buffer
	err = runLiveOrderPlan(context.Background(), []string{
		"-select-pending",
		"-pending-symbol", "btcusdt",
		"-run-id", "live_loop_plan_cli_0001",
		"-order-type", "limit",
		"-time-in-force", "post-only",
		"-limit-price", "100100",
	}, liveOrderPlanDependencies{
		loadConfig: func(string) (*config.Config, error) {
			return validLiveOrderPlanConfig(), nil
		},
		openDB: func(context.Context, config.DatabaseConfig) (*sql.DB, error) {
			return db, nil
		},
		newRiskDecisionReader: func(*sql.DB) applive.RiskDecisionReader {
			return reader
		},
		newPendingReader: func(*sql.DB) domainlive.PendingLiveDecisionReader {
			return pendingReader
		},
		clock:  clock.FixedClock{Time: now},
		output: &output,
	})
	if err != nil {
		t.Fatalf("run live order plan with pending selection: %v\nlogs:\n%s", err, output.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sql expectations: %v", err)
	}
	if pendingReader.calls != 1 || pendingReader.query.Symbol != "BTCUSDT" || pendingReader.query.Limit != 1 {
		t.Fatalf("pending reader mismatch: calls=%d query=%#v", pendingReader.calls, pendingReader.query)
	}
	if reader.calls != 1 || reader.query.DecisionID != decision.DecisionID {
		t.Fatalf("risk decision reader mismatch: calls=%d query=%#v", reader.calls, reader.query)
	}

	logs := output.String()
	for _, want := range []string{
		`"msg":"pending live decision selected for order plan"`,
		`"msg":"pending live decision preview is not reserved"`,
		`"msg":"live order plan"`,
		`"source":"select-pending"`,
		`"pending_symbol":"BTCUSDT"`,
		`"decision_id":"risk_decision_live_plan_pending_0001"`,
		`"order_type":"LIMIT"`,
		`"time_in_force":"POST_ONLY"`,
		`"limit_price":"100100"`,
		`"reserved":false`,
		`"exchange_contacted":false`,
		`"order_submitted":false`,
	} {
		if !strings.Contains(logs, want) {
			t.Fatalf("expected logs to contain %s, got\n%s", want, logs)
		}
	}
}

func TestRunLiveOrderPlanRejectsUnsafeFlagsBeforeSideEffects(t *testing.T) {
	var loaded bool
	var opened bool

	err := runLiveOrderPlan(context.Background(), []string{
		"-pending-symbol", "BTCUSDT",
	}, liveOrderPlanDependencies{
		loadConfig: func(string) (*config.Config, error) {
			loaded = true
			return validLiveOrderPlanConfig(), nil
		},
		openDB: func(context.Context, config.DatabaseConfig) (*sql.DB, error) {
			opened = true
			return nil, nil
		},
		output: &bytes.Buffer{},
	})
	if err == nil || !strings.Contains(err.Error(), "select-pending") {
		t.Fatalf("expected select-pending validation error, got %v", err)
	}
	if loaded || opened {
		t.Fatalf("unsafe flags must stop before side effects: loaded=%t opened=%t", loaded, opened)
	}
}

func TestRunLiveOrderPlanSelectPendingRequiresCandidateBeforeBuildingPlan(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	reader := &fakeLiveOrderPlanRiskDecisionReader{}
	pendingReader := &fakeLiveOrderPlanPendingReader{}

	err = runLiveOrderPlan(context.Background(), []string{
		"-select-pending",
	}, liveOrderPlanDependencies{
		loadConfig: func(string) (*config.Config, error) {
			return validLiveOrderPlanConfig(), nil
		},
		openDB: func(context.Context, config.DatabaseConfig) (*sql.DB, error) {
			return db, nil
		},
		newRiskDecisionReader: func(*sql.DB) applive.RiskDecisionReader {
			return reader
		},
		newPendingReader: func(*sql.DB) domainlive.PendingLiveDecisionReader {
			return pendingReader
		},
		output: &bytes.Buffer{},
	})
	if err == nil || !strings.Contains(err.Error(), "no pending LIVE risk decisions") {
		t.Fatalf("expected missing pending decision error, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sql expectations: %v", err)
	}
	if pendingReader.calls != 1 || reader.calls != 0 {
		t.Fatalf("missing candidate must stop before plan load: pending_calls=%d reader_calls=%d", pendingReader.calls, reader.calls)
	}
}

func TestLiveOrderPlanDecisionSourceFlagsTableDriven(t *testing.T) {
	tests := []struct {
		name          string
		decisionID    string
		selectPending bool
		runID         string
		wantErrSub    string
	}{
		{name: "explicit decision", decisionID: "risk_decision_live_plan_cli_0001"},
		{name: "pending selector", selectPending: true},
		{name: "both sources rejected", decisionID: "risk_decision_live_plan_cli_0001", selectPending: true, wantErrSub: "decision-id"},
		{name: "missing source rejected", wantErrSub: "decision-id is required"},
		{name: "untrimmed run id rejected", decisionID: "risk_decision_live_plan_cli_0001", runID: " live_loop_plan_cli_0001 ", wantErrSub: "run-id"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateLiveOrderPlanDecisionSourceFlags(tt.decisionID, tt.selectPending, tt.runID)
			if tt.wantErrSub != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("validate decision source: %v", err)
			}
		})
	}
}

func TestLiveOrderPlanPendingDecisionQueryFromFlagsTableDriven(t *testing.T) {
	tests := []struct {
		name       string
		symbol     string
		enabled    bool
		want       domainlive.PendingLiveDecisionQuery
		wantErrSub string
	}{
		{name: "disabled", want: domainlive.PendingLiveDecisionQuery{}},
		{name: "lowercase symbol normalizes", symbol: "btcusdt", enabled: true, want: domainlive.PendingLiveDecisionQuery{Symbol: "BTCUSDT", Limit: 1}},
		{name: "empty symbol selects across all pending decisions", enabled: true, want: domainlive.PendingLiveDecisionQuery{Limit: 1}},
		{name: "symbol without selector rejected", symbol: "BTCUSDT", wantErrSub: "select-pending"},
		{name: "untrimmed symbol rejected", symbol: " BTCUSDT ", enabled: true, wantErrSub: "trimmed"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := liveOrderPlanPendingDecisionQueryFromFlags(tt.symbol, tt.enabled)
			if tt.wantErrSub != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("pending query from flags: %v", err)
			}
			if got != tt.want {
				t.Fatalf("query mismatch: got %#v want %#v", got, tt.want)
			}
		})
	}
}

func TestLiveOrderPlanInstructionParsingTableDriven(t *testing.T) {
	tests := []struct {
		name        string
		orderType   string
		timeInForce string
		limitPrice  string
		wantType    domainlive.OrderType
		wantTIF     domainlive.TimeInForce
		wantLimit   decimal.Decimal
		wantErrSub  string
	}{
		{name: "market defaults", wantType: domainlive.OrderTypeMarket, wantTIF: domainlive.TimeInForceIOC},
		{name: "market fok", orderType: "market", timeInForce: "fok", wantType: domainlive.OrderTypeMarket, wantTIF: domainlive.TimeInForceFOK},
		{name: "limit post only", orderType: "limit", timeInForce: "post-only", limitPrice: "100100", wantType: domainlive.OrderTypeLimit, wantTIF: domainlive.TimeInForcePostOnly, wantLimit: decimal.RequireFromString("100100")},
		{name: "invalid type", orderType: "stop", wantErrSub: "order-type"},
		{name: "invalid time in force", timeInForce: "day", wantErrSub: "time-in-force"},
		{name: "market with limit price", limitPrice: "100100", wantErrSub: "limit-price"},
		{name: "limit missing price", orderType: "limit", wantErrSub: "limit-price"},
		{name: "limit non-positive price", orderType: "limit", limitPrice: "0", wantErrSub: "positive"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			orderType, err := parseLiveOrderPlanOrderType(tt.orderType)
			if err == nil {
				var tif domainlive.TimeInForce
				tif, err = parseLiveOrderPlanTimeInForce(tt.timeInForce)
				if err == nil {
					var limit decimal.Decimal
					limit, err = parseLiveOrderPlanLimitPrice(orderType, tt.limitPrice)
					if err == nil && (orderType != tt.wantType || tif != tt.wantTIF || !limit.Equal(tt.wantLimit)) {
						t.Fatalf("instructions mismatch: type=%s tif=%s limit=%s", orderType, tif, limit)
					}
				}
			}
			if tt.wantErrSub != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("parse instructions: %v", err)
			}
		})
	}
}

type fakeLiveOrderPlanRiskDecisionReader struct {
	query   domainrisk.DecisionAuditQuery
	records []domainrisk.DecisionAuditRecord
	calls   int
	err     error
}

func (r *fakeLiveOrderPlanRiskDecisionReader) ListDecisions(_ context.Context, query domainrisk.DecisionAuditQuery) ([]domainrisk.DecisionAuditRecord, error) {
	r.calls++
	r.query = query
	if r.err != nil {
		return nil, r.err
	}
	return append([]domainrisk.DecisionAuditRecord(nil), r.records...), nil
}

type fakeLiveOrderPlanPendingReader struct {
	query      domainlive.PendingLiveDecisionQuery
	candidates []domainlive.PendingLiveDecision
	calls      int
	err        error
}

func (r *fakeLiveOrderPlanPendingReader) ListPendingLiveDecisions(
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

func validLiveOrderPlanConfig() *config.Config {
	return &config.Config{
		App: config.AppConfig{LogLevel: "info"},
		Database: config.DatabaseConfig{
			MaxOpenConns: 1,
		},
		Exchange: config.ExchangeConfig{
			Primary:  "bybit",
			Category: "linear",
		},
	}
}

func liveOrderPlanRiskDecision(decisionID string, symbol string, recordedAt time.Time) domainrisk.DecisionAuditRecord {
	createdAt := recordedAt.Add(-time.Second)
	return domainrisk.DecisionAuditRecord{
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
		HypothesisID:    "hypothesis_live_plan_0001",
		StrategyName:    "trend-momentum",
		Symbol:          symbol,
		Side:            domainrisk.SideLong,
		EntryPrice:      decimal.RequireFromString("100000"),
		Leverage:        decimal.RequireFromString("1"),
		Confidence:      82,
		IntentReason:    "signal confirmed",
		IntentCreatedAt: createdAt.Add(-time.Minute),
		RecordedAt:      recordedAt,
	}
}

func (r *fakeLiveOrderPlanRiskDecisionReader) String() string {
	return fmt.Sprintf("calls=%d query=%#v records=%d", r.calls, r.query, len(r.records))
}

func readLiveOrderPlanArtifact(t *testing.T, path string) domainlive.LiveOrderPlanArtifact {
	t.Helper()
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read artifact: %v", err)
	}
	var artifact domainlive.LiveOrderPlanArtifact
	if err := json.Unmarshal(payload, &artifact); err != nil {
		t.Fatalf("decode artifact: %v", err)
	}
	if err := domainlive.ValidateLiveOrderPlanArtifact(artifact); err != nil {
		t.Fatalf("validate artifact: %v", err)
	}
	return artifact
}
