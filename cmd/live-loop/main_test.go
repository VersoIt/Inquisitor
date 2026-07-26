package main

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/shopspring/decimal"

	"github.com/VersoIt/Inquisitor/internal/config"
	domainlive "github.com/VersoIt/Inquisitor/internal/live"
)

func TestDeterministicLiveLoopIdentityIsStableAndBybitSafe(t *testing.T) {
	first, err := deterministicLiveLoopIdentity(" risk_decision_live_cli_0001 ", "")
	if err != nil {
		t.Fatalf("identity: %v", err)
	}
	second, err := deterministicLiveLoopIdentity("risk_decision_live_cli_0001", "")
	if err != nil {
		t.Fatalf("identity: %v", err)
	}

	if first != second {
		t.Fatalf("identity must be deterministic after trimming: first=%#v second=%#v", first, second)
	}
	if len(first.ClientOrderID) > 36 || len(first.SubmissionID) > 36 {
		t.Fatalf("identity must stay within Bybit orderLinkId length: %#v", first)
	}
	for _, value := range []string{first.RunID, first.SubmissionID, first.ClientOrderID} {
		for _, r := range value {
			if !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') && r != '_' {
				t.Fatalf("identity contains unsupported character %q in %q", r, value)
			}
		}
	}

	custom, err := deterministicLiveLoopIdentity("risk_decision_live_cli_0001", "live_loop_operator_0001")
	if err != nil {
		t.Fatalf("custom run identity: %v", err)
	}
	if custom.RunID != "live_loop_operator_0001" {
		t.Fatalf("custom run id mismatch: %#v", custom)
	}
	if custom.SubmissionID != first.SubmissionID || custom.ClientOrderID != first.ClientOrderID {
		t.Fatalf("custom run id must not change idempotency keys: got %#v want submission=%s client=%s", custom, first.SubmissionID, first.ClientOrderID)
	}

	_, err = deterministicLiveLoopIdentity("risk_decision_live_cli_0001", " live_loop_operator_0001 ")
	if err == nil || !strings.Contains(err.Error(), "run-id") || !strings.Contains(err.Error(), "trimmed") {
		t.Fatalf("expected trimmed run-id error, got %v", err)
	}
}

func TestRunLiveLoopRequiresExecuteBeforeSideEffects(t *testing.T) {
	var opened bool

	err := runLiveLoop(context.Background(), []string{
		"-decision-id", "risk_decision_live_cli_0001",
	}, liveLoopDependencies{
		openDB: func(context.Context, config.DatabaseConfig) (*sql.DB, error) {
			opened = true
			return nil, nil
		},
		output: &bytes.Buffer{},
	})
	if err == nil || !strings.Contains(err.Error(), "execute") {
		t.Fatalf("expected execute gate error, got %v", err)
	}
	if opened {
		t.Fatal("database must not be opened when execute gate is not armed")
	}
}

func TestRunLiveLoopProcessesPersistedDecisionThroughBoundedLoop(t *testing.T) {
	t.Setenv("TRADING_LIVE_CONFIRM", "true")
	t.Setenv("BYBIT_API_KEY", "actual-live-api-key-value")
	t.Setenv("BYBIT_API_SECRET", "actual-live-api-secret-value")

	now := time.Now().UTC()
	decisionTime := now.Add(-2 * time.Second)
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	mock.ExpectExec("INSERT INTO live_loop_runs").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectQuery("SELECT active, reason, source, created_at").
		WillReturnRows(sqlmock.NewRows([]string{"active", "reason", "source", "created_at"}))
	mock.ExpectExec("INSERT INTO live_account_snapshots").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("INSERT INTO live_position_snapshots").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectQuery("SELECT active, reason, source, created_at").
		WillReturnRows(sqlmock.NewRows([]string{"active", "reason", "source", "created_at"}))
	mock.ExpectQuery("SELECT decision_id, intent_id, mode").
		WillReturnRows(liveLoopRiskDecisionRows(decisionTime))
	mock.ExpectQuery("SELECT active, reason, source, created_at").
		WillReturnRows(sqlmock.NewRows([]string{"active", "reason", "source", "created_at"}))
	mock.ExpectExec("INSERT INTO live_order_submissions").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("INSERT INTO live_order_acknowledgements").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("INSERT INTO live_order_status_snapshots").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("INSERT INTO live_position_snapshots").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("INSERT INTO live_loop_iterations").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("UPDATE live_loop_runs").
		WillReturnResult(sqlmock.NewResult(0, 1))

	identity, err := deterministicLiveLoopIdentity("risk_decision_live_cli_0001", "live_loop_cli_0001")
	if err != nil {
		t.Fatalf("identity: %v", err)
	}
	executor := &fakeLiveLoopExecutor{receivedAt: now}
	accountReader := &fakeLiveLoopAccountReader{
		snapshot: validLiveLoopAccountSnapshot(t),
	}

	var output bytes.Buffer
	err = runLiveLoop(context.Background(), []string{
		"-config", writeLiveLoopConfig(t),
		"-decision-id", "risk_decision_live_cli_0001",
		"-subaccount-confirmed",
		"-max-initial-live-capital-usdt", "100",
		"-run-id", "live_loop_cli_0001",
		"-max-iterations", "1",
		"-max-runtime", "15s",
		"-iteration-timeout", "10s",
		"-execute",
	}, liveLoopDependencies{
		openDB: func(context.Context, config.DatabaseConfig) (*sql.DB, error) {
			return db, nil
		},
		newExecutor: func(_ *config.Config, apiKey string, apiSecret string) (domainlive.OrderExecutor, error) {
			if apiKey != "actual-live-api-key-value" || apiSecret != "actual-live-api-secret-value" {
				t.Fatalf("executor credentials mismatch")
			}
			return executor, nil
		},
		newAccountReader: func(*config.Config) (domainlive.AccountSnapshotReader, error) {
			return accountReader, nil
		},
		output: &output,
	})
	if err != nil {
		t.Fatalf("run live loop: %v\nlogs:\n%s", err, output.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sql expectations: %v", err)
	}
	if accountReader.calls != 1 || accountReader.query.AccountType != domainlive.AccountTypeUnified {
		t.Fatalf("account reader mismatch: calls=%d query=%#v", accountReader.calls, accountReader.query)
	}
	if executor.calls != 1 || executor.statusCalls != 1 || executor.positionCalls != 2 {
		t.Fatalf("executor calls mismatch: submit=%d status=%d position=%d", executor.calls, executor.statusCalls, executor.positionCalls)
	}
	if len(executor.positionQueries) != 2 {
		t.Fatalf("position queries mismatch: %#v", executor.positionQueries)
	}
	if executor.positionQueries[0].Symbol != "BTCUSDT" || executor.positionQueries[0].Exchange != "bybit" || executor.positionQueries[0].Category != "linear" {
		t.Fatalf("preflight position query mismatch: %#v", executor.positionQueries[0])
	}
	if executor.positionQueries[1].Symbol != "BTCUSDT" || executor.positionQueries[1].Exchange != "bybit" || executor.positionQueries[1].Category != "linear" {
		t.Fatalf("reconciliation position query mismatch: %#v", executor.positionQueries[1])
	}
	if executor.statusQuery.ClientOrderID != identity.ClientOrderID ||
		executor.statusQuery.Symbol != "BTCUSDT" ||
		executor.statusQuery.Exchange != "bybit" {
		t.Fatalf("status query mismatch: %#v", executor.statusQuery)
	}
	if executor.submission.DecisionID != "risk_decision_live_cli_0001" ||
		executor.submission.SubmissionID != identity.SubmissionID ||
		executor.submission.ClientOrderID != identity.ClientOrderID ||
		executor.submission.Type != domainlive.OrderTypeMarket ||
		executor.submission.TimeInForce != domainlive.TimeInForceIOC {
		t.Fatalf("executor submission mismatch: %#v", executor.submission)
	}

	logs := output.String()
	for _, want := range []string{
		`"msg":"live loop checked"`,
		`"completed":true`,
		`"run_id":"live_loop_cli_0001"`,
		`"preflight_checked":true`,
		`"preflight_ready":true`,
		`"account_snapshot_inserted":1`,
		`"position_snapshot_inserted":1`,
		`"iterations_attempted":1`,
		`"iterations_succeeded":1`,
		`"stop_reason":"ITERATION_REQUESTED"`,
		`"iteration_action":"SUBMITTED"`,
		`"iteration_reason":"live_order_submitted"`,
		`"decision_id":"risk_decision_live_cli_0001"`,
		`"exchange_submitted":true`,
		`"msg":"live loop completed"`,
	} {
		if !strings.Contains(logs, want) {
			t.Fatalf("expected logs to contain %s, got\n%s", want, logs)
		}
	}
	if strings.Contains(logs, "actual-live-api-key-value") || strings.Contains(logs, "actual-live-api-secret-value") {
		t.Fatalf("logs must not contain credential values, got\n%s", logs)
	}
}

func TestRunLiveLoopRequiresExecutorStatusReaderBeforeLoopSideEffects(t *testing.T) {
	t.Setenv("TRADING_LIVE_CONFIRM", "true")
	t.Setenv("BYBIT_API_KEY", "actual-live-api-key-value")
	t.Setenv("BYBIT_API_SECRET", "actual-live-api-secret-value")

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	executor := &fakeLiveLoopSubmitOnlyExecutor{}
	var accountReaderCreated bool

	err = runLiveLoop(context.Background(), []string{
		"-config", writeLiveLoopConfig(t),
		"-decision-id", "risk_decision_live_cli_0001",
		"-subaccount-confirmed",
		"-max-initial-live-capital-usdt", "100",
		"-run-id", "live_loop_cli_0001",
		"-max-iterations", "1",
		"-max-runtime", "15s",
		"-iteration-timeout", "10s",
		"-execute",
	}, liveLoopDependencies{
		openDB: func(context.Context, config.DatabaseConfig) (*sql.DB, error) {
			return db, nil
		},
		newExecutor: func(_ *config.Config, _ string, _ string) (domainlive.OrderExecutor, error) {
			return executor, nil
		},
		newAccountReader: func(*config.Config) (domainlive.AccountSnapshotReader, error) {
			accountReaderCreated = true
			return &fakeLiveLoopAccountReader{}, nil
		},
		output: &bytes.Buffer{},
	})
	if err == nil || !strings.Contains(err.Error(), "order status reconciliation") {
		t.Fatalf("expected status reader requirement error, got %v", err)
	}
	if executor.calls != 0 {
		t.Fatalf("executor must not submit before capability check, calls=%d", executor.calls)
	}
	if accountReaderCreated {
		t.Fatal("account reader must not be created before executor capability check")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sql expectations: %v", err)
	}
}

func TestRunLiveLoopRejectsUnsafePreflightBeforeOrderIteration(t *testing.T) {
	t.Setenv("TRADING_LIVE_CONFIRM", "true")
	t.Setenv("BYBIT_API_KEY", "actual-live-api-key-value")
	t.Setenv("BYBIT_API_SECRET", "actual-live-api-secret-value")

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	mock.ExpectExec("INSERT INTO live_loop_runs").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectQuery("SELECT active, reason, source, created_at").
		WillReturnRows(sqlmock.NewRows([]string{"active", "reason", "source", "created_at"}))
	mock.ExpectExec("UPDATE live_loop_runs").
		WillReturnResult(sqlmock.NewResult(0, 1))
	executor := &fakeLiveLoopExecutor{receivedAt: time.Now().UTC()}
	accountReader := &fakeLiveLoopAccountReader{snapshot: validLiveLoopAccountSnapshot(t)}

	var output bytes.Buffer
	err = runLiveLoop(context.Background(), []string{
		"-config", writeLiveLoopConfig(t),
		"-decision-id", "risk_decision_live_cli_0001",
		"-max-initial-live-capital-usdt", "100",
		"-run-id", "live_loop_cli_0001",
		"-max-iterations", "1",
		"-max-runtime", "15s",
		"-iteration-timeout", "10s",
		"-execute",
	}, liveLoopDependencies{
		openDB: func(context.Context, config.DatabaseConfig) (*sql.DB, error) {
			return db, nil
		},
		newExecutor: func(_ *config.Config, _ string, _ string) (domainlive.OrderExecutor, error) {
			return executor, nil
		},
		newAccountReader: func(*config.Config) (domainlive.AccountSnapshotReader, error) {
			return accountReader, nil
		},
		output: &output,
	})
	if err == nil || !strings.Contains(err.Error(), "dedicated live subaccount") {
		t.Fatalf("expected subaccount preflight error, got %v", err)
	}
	if executor.calls != 0 || executor.statusCalls != 0 || executor.positionCalls != 0 {
		t.Fatalf("unsafe preflight must block order iteration, executor=%#v", executor)
	}
	if accountReader.calls != 0 {
		t.Fatalf("unsafe policy preflight must not read account, calls=%d", accountReader.calls)
	}
	logs := output.String()
	for _, want := range []string{
		`"msg":"live loop checked"`,
		`"completed":false`,
		`"preflight_checked":true`,
		`"preflight_ready":false`,
		`"stop_reason":"PREFLIGHT_FAILED"`,
	} {
		if !strings.Contains(logs, want) {
			t.Fatalf("expected logs to contain %s, got\n%s", want, logs)
		}
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sql expectations: %v", err)
	}
}

func TestValidateLiveLoopFlagsTableDriven(t *testing.T) {
	tests := []struct {
		name       string
		runID      string
		iterations int
		runtime    time.Duration
		timeout    time.Duration
		wantErrSub string
	}{
		{name: "valid", runID: "live_loop_operator", iterations: 1, runtime: time.Second, timeout: time.Second},
		{name: "missing run id", runID: "", iterations: 1, runtime: time.Second, timeout: time.Second, wantErrSub: "run-id"},
		{name: "untrimmed run id", runID: " live_loop_operator ", iterations: 1, runtime: time.Second, timeout: time.Second, wantErrSub: "trimmed"},
		{name: "zero iterations", runID: "live_loop_operator", iterations: 0, runtime: time.Second, timeout: time.Second, wantErrSub: "max-iterations"},
		{name: "iterations above ceiling", runID: "live_loop_operator", iterations: 101, runtime: time.Second, timeout: time.Second, wantErrSub: "max-iterations"},
		{name: "zero runtime", runID: "live_loop_operator", iterations: 1, timeout: time.Second, wantErrSub: "max-runtime"},
		{name: "runtime above ceiling", runID: "live_loop_operator", iterations: 1, runtime: 25 * time.Hour, timeout: time.Second, wantErrSub: "max-runtime"},
		{name: "zero timeout", runID: "live_loop_operator", iterations: 1, runtime: time.Second, wantErrSub: "iteration-timeout"},
		{name: "timeout exceeds runtime", runID: "live_loop_operator", iterations: 1, runtime: time.Second, timeout: 2 * time.Second, wantErrSub: "must not exceed"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateLiveLoopFlags(tt.runID, tt.iterations, tt.runtime, tt.timeout)
			if tt.wantErrSub != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("validate live loop flags: %v", err)
			}
		})
	}
}

type fakeLiveLoopAccountReader struct {
	query    domainlive.AccountSnapshotQuery
	snapshot domainlive.AccountSnapshot
	calls    int
	err      error
}

func (r *fakeLiveLoopAccountReader) GetAccountSnapshot(_ context.Context, query domainlive.AccountSnapshotQuery) (domainlive.AccountSnapshot, error) {
	r.calls++
	r.query = query
	if r.err != nil {
		return domainlive.AccountSnapshot{}, r.err
	}
	return r.snapshot, nil
}

type fakeLiveLoopExecutor struct {
	submission      domainlive.OrderSubmission
	statusQuery     domainlive.OrderStatusQuery
	positionQueries []domainlive.PositionSnapshotQuery
	receivedAt      time.Time
	calls           int
	statusCalls     int
	positionCalls   int
}

func (e *fakeLiveLoopExecutor) SubmitOrder(_ context.Context, submission domainlive.OrderSubmission) (domainlive.OrderAcknowledgement, error) {
	e.calls++
	e.submission = submission
	return domainlive.NewOrderAcknowledgement(domainlive.OrderAcknowledgementInput{
		SubmissionID:    submission.SubmissionID,
		ClientOrderID:   submission.ClientOrderID,
		Exchange:        submission.Exchange,
		ExchangeOrderID: "bybit_order_loop_0001",
		Status:          domainlive.OrderStatusAccepted,
		ReceivedAt:      e.receivedAt,
	})
}

func (e *fakeLiveLoopExecutor) GetOrderStatus(_ context.Context, query domainlive.OrderStatusQuery) (domainlive.OrderStatusSnapshot, error) {
	e.statusCalls++
	e.statusQuery = query
	return domainlive.NewOrderStatusSnapshot(domainlive.OrderStatusSnapshotInput{
		ClientOrderID:              e.submission.ClientOrderID,
		ExchangeOrderID:            "bybit_order_loop_0001",
		Exchange:                   e.submission.Exchange,
		Category:                   e.submission.Category,
		Symbol:                     e.submission.Symbol,
		Side:                       e.submission.Side,
		Type:                       e.submission.Type,
		TimeInForce:                e.submission.TimeInForce,
		ExchangeStatus:             domainlive.ExchangeOrderStatusFilled,
		RejectReason:               "EC_NoError",
		Quantity:                   e.submission.Quantity,
		Price:                      decimal.Zero,
		AveragePrice:               e.submission.ReferencePrice,
		LeavesQuantity:             decimal.Zero,
		CumulativeExecutedQuantity: e.submission.Quantity,
		CumulativeExecutedValue:    e.submission.Notional,
		CumulativeFee:              decimal.RequireFromString("1"),
		ReduceOnly:                 e.submission.ReduceOnly,
		ExchangeCreatedAt:          e.receivedAt.Add(-time.Second),
		ExchangeUpdatedAt:          e.receivedAt,
		ObservedAt:                 e.receivedAt,
	})
}

func (e *fakeLiveLoopExecutor) GetPositionSnapshot(_ context.Context, query domainlive.PositionSnapshotQuery) (domainlive.PositionSnapshot, error) {
	e.positionCalls++
	e.positionQueries = append(e.positionQueries, query)
	if e.positionCalls == 1 {
		return validLiveLoopFlatPositionSnapshot(query, e.receivedAt.Add(-500*time.Millisecond))
	}
	return domainlive.NewPositionSnapshot(domainlive.PositionSnapshotInput{
		Exchange:              e.submission.Exchange,
		Category:              e.submission.Category,
		Symbol:                e.submission.Symbol,
		Side:                  e.submission.Side,
		Size:                  e.submission.Quantity,
		AveragePrice:          e.submission.ReferencePrice,
		PositionValue:         e.submission.Notional,
		MarkPrice:             e.submission.ReferencePrice,
		LiquidationPrice:      e.submission.StopLoss,
		Leverage:              e.submission.Leverage,
		UnrealisedPnL:         decimal.Zero,
		CurrentRealisedPnL:    decimal.Zero,
		CumulativeRealisedPnL: decimal.Zero,
		ExchangeStatus:        domainlive.ExchangePositionStatusNormal,
		PositionIndex:         0,
		Sequence:              123,
		ExchangeCreatedAt:     e.receivedAt.Add(-time.Second),
		ExchangeUpdatedAt:     e.receivedAt,
		ObservedAt:            e.receivedAt,
	})
}

type fakeLiveLoopSubmitOnlyExecutor struct {
	calls int
}

func (e *fakeLiveLoopSubmitOnlyExecutor) SubmitOrder(context.Context, domainlive.OrderSubmission) (domainlive.OrderAcknowledgement, error) {
	e.calls++
	return domainlive.OrderAcknowledgement{}, nil
}

func validLiveLoopAccountSnapshot(t *testing.T) domainlive.AccountSnapshot {
	t.Helper()

	snapshot, err := domainlive.NewAccountSnapshot(domainlive.AccountSnapshotInput{
		Exchange:               "bybit",
		AccountType:            domainlive.AccountTypeUnified,
		TotalEquity:            decimal.RequireFromString("50"),
		TotalWalletBalance:     decimal.RequireFromString("50"),
		TotalMarginBalance:     decimal.RequireFromString("50"),
		TotalAvailableBalance:  decimal.RequireFromString("50"),
		TotalPerpUPL:           decimal.Zero,
		TotalInitialMargin:     decimal.Zero,
		TotalMaintenanceMargin: decimal.Zero,
		Coins: []domainlive.AccountCoinSnapshot{{
			Coin:             "USDT",
			Equity:           decimal.RequireFromString("50"),
			USDValue:         decimal.RequireFromString("50"),
			WalletBalance:    decimal.RequireFromString("50"),
			MarginCollateral: true,
			CollateralSwitch: true,
		}},
		ObservedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("new live loop account snapshot: %v", err)
	}
	return snapshot
}

func validLiveLoopFlatPositionSnapshot(query domainlive.PositionSnapshotQuery, observedAt time.Time) (domainlive.PositionSnapshot, error) {
	return domainlive.NewPositionSnapshot(domainlive.PositionSnapshotInput{
		Exchange:       query.Exchange,
		Category:       query.Category,
		Symbol:         query.Symbol,
		Size:           decimal.Zero,
		MarkPrice:      decimal.RequireFromString("100000"),
		ExchangeStatus: domainlive.ExchangePositionStatusNormal,
		PositionIndex:  0,
		Sequence:       -1,
		ObservedAt:     observedAt,
	})
}

func liveLoopRiskDecisionRows(now time.Time) *sqlmock.Rows {
	createdAt := now.Add(-2 * time.Second)
	recordedAt := now.Add(-time.Second)
	intentCreatedAt := now.Add(-time.Minute)
	return sqlmock.NewRows([]string{
		"decision_id", "intent_id", "mode", "hypothesis_id", "strategy_name", "symbol", "side",
		"entry_price", "leverage", "confidence", "intent_reason", "intent_created_at",
		"approved", "final_quantity", "max_loss", "stop_loss", "take_profit",
		"reason", "checks_json", "created_at", "recorded_at",
	}).AddRow(
		"risk_decision_live_cli_0001",
		"risk_intent_live_cli_0001",
		"LIVE",
		"hypothesis_live_cli_0001",
		"trend-momentum",
		"BTCUSDT",
		"LONG",
		"100000",
		"1",
		82,
		"signal confirmed",
		intentCreatedAt,
		true,
		"0.005",
		"5",
		"99000",
		"102000",
		"risk_checks_passed",
		`[{"name":"trading_enabled","passed":true}]`,
		createdAt,
		recordedAt,
	)
}

func writeLiveLoopConfig(t *testing.T) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "config.yaml")
	raw := []byte(`
app:
  name: crypto-quant-platform
  env: test
  mode: live-loop
  log_level: info
database:
  dsn: postgres://user:pass@localhost:5432/inquisitor?sslmode=disable
  max_open_conns: 1
  max_idle_conns: 1
exchange:
  primary: bybit
  rest_base_url: https://api-testnet.bybit.com
  public_ws_url: wss://stream-testnet.bybit.com/v5/public/linear
  category: linear
  symbols: [BTCUSDT]
market_data:
  candle_intervals: ["1"]
  backfill_days: 1
  orderbook_depth: 50
  max_data_staleness_ms: 1000
  reconnect_backoff_ms: 1000
fees:
  maker_bps: 1
  taker_bps: 6
slippage:
  default_bps: 3
  conservative_multiplier: 1.5
trading:
  enabled: true
  mode: live
  allow_live: true
  max_open_positions: 1
  max_leverage: 1
  base_currency: USDT
risk:
  risk_per_trade_pct: 0.25
  max_daily_loss_pct: 1
  max_weekly_loss_pct: 3
  max_total_drawdown_pct: 8
  max_losing_streak: 5
  max_spread_bps: 5
  max_slippage_bps: 10
  min_confidence: 70
  min_liquidity_usdt: 100000
  portfolio_max_crypto_exposure_pct: 30
  portfolio_max_correlated_exposure_pct: 20
regime:
  min_confidence: 70
  adx_trend_threshold: 25
  adx_range_threshold: 18
  atr_spike_multiplier: 2.5
research:
  min_trades: 200
  min_profit_factor: 1.15
  min_expectancy_r: 0.05
  max_drawdown_pct: 15
  require_out_of_sample: true
  require_walk_forward: true
  require_regime_analysis: true
paper:
  initial_balance: 1000
  minimum_days: 30
  simulate_fees: true
  simulate_slippage: true
  simulate_spread: true
live:
  require_env_confirmation: true
  confirmation_env: TRADING_LIVE_CONFIRM
  api_key_env: BYBIT_API_KEY
  api_secret_env: BYBIT_API_SECRET
  require_subaccount: true
  withdrawal_permission_allowed: false
  initial_live_capital_usdt: 50.25
edge_decay:
  enabled: true
  rolling_window_days: 30
  min_recent_profit_factor: 1
  max_recent_drawdown_pct: 8
monitoring:
  health_port: 8080
`)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}
