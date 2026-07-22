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

func TestDeterministicLiveSubmissionIdentityIsStableAndBybitSafe(t *testing.T) {
	first, err := deterministicLiveSubmissionIdentity(" risk_decision_live_cli_0001 ")
	if err != nil {
		t.Fatalf("identity: %v", err)
	}
	second, err := deterministicLiveSubmissionIdentity("risk_decision_live_cli_0001")
	if err != nil {
		t.Fatalf("identity: %v", err)
	}

	if first != second {
		t.Fatalf("identity must be deterministic after trimming: first=%#v second=%#v", first, second)
	}
	if len(first.ClientOrderID) > 36 || len(first.SubmissionID) > 36 {
		t.Fatalf("identity must stay within Bybit orderLinkId length: %#v", first)
	}
	for _, value := range []string{first.SubmissionID, first.ClientOrderID} {
		for _, r := range value {
			if !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') && r != '_' {
				t.Fatalf("identity contains unsupported character %q in %q", r, value)
			}
		}
	}
}

func TestParseLiveOrderInstructionsTableDriven(t *testing.T) {
	tests := []struct {
		name        string
		orderType   string
		timeInForce string
		limitPrice  string
		wantType    domainlive.OrderType
		wantTIF     domainlive.TimeInForce
		wantLimit   string
		wantErrSub  string
	}{
		{name: "market defaults", orderType: "", timeInForce: "", wantType: domainlive.OrderTypeMarket, wantTIF: domainlive.TimeInForceIOC, wantLimit: "0"},
		{name: "market fok", orderType: "market", timeInForce: "fok", wantType: domainlive.OrderTypeMarket, wantTIF: domainlive.TimeInForceFOK, wantLimit: "0"},
		{name: "limit post only", orderType: "limit", timeInForce: "post-only", limitPrice: "100000.5", wantType: domainlive.OrderTypeLimit, wantTIF: domainlive.TimeInForcePostOnly, wantLimit: "100000.5"},
		{name: "unknown order type", orderType: "stop", wantErrSub: "order-type"},
		{name: "unknown tif", orderType: "market", timeInForce: "day", wantErrSub: "time-in-force"},
		{name: "market with limit price", orderType: "market", limitPrice: "100", wantErrSub: "MARKET"},
		{name: "limit without price", orderType: "limit", wantErrSub: "required"},
		{name: "limit with zero price", orderType: "limit", limitPrice: "0", wantErrSub: "positive"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotType, err := parseLiveOrderType(tt.orderType)
			if err == nil {
				var gotTIF domainlive.TimeInForce
				gotTIF, err = parseLiveTimeInForce(tt.timeInForce)
				if err == nil {
					var gotLimit decimal.Decimal
					gotLimit, err = parseLiveLimitPrice(gotType, tt.limitPrice)
					if err == nil && tt.wantErrSub == "" {
						if gotType != tt.wantType || gotTIF != tt.wantTIF || gotLimit.String() != tt.wantLimit {
							t.Fatalf("instruction mismatch: type=%s tif=%s limit=%s", gotType, gotTIF, gotLimit)
						}
					}
				}
			}
			if tt.wantErrSub != "" {
				if err == nil {
					t.Fatal("expected error")
				}
				if !strings.Contains(err.Error(), tt.wantErrSub) {
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

func TestRunLiveSubmitRequiresExecuteBeforeSideEffects(t *testing.T) {
	var opened bool

	err := runLiveSubmit(context.Background(), []string{
		"-decision-id", "risk_decision_live_cli_0001",
	}, liveSubmitDependencies{
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

func TestRunLiveSubmitSubmitsPersistedDecisionThroughJournalAndExecutor(t *testing.T) {
	t.Setenv("TRADING_LIVE_CONFIRM", "true")
	t.Setenv("BYBIT_API_KEY", "actual-live-api-key-value")
	t.Setenv("BYBIT_API_SECRET", "actual-live-api-secret-value")

	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	mock.ExpectQuery("SELECT active, reason, source, created_at").
		WillReturnRows(sqlmock.NewRows([]string{"active", "reason", "source", "created_at"}))
	mock.ExpectExec("INSERT INTO live_account_snapshots").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("INSERT INTO live_position_snapshots").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectQuery("SELECT decision_id, intent_id, mode").
		WillReturnRows(liveSubmitRiskDecisionRows(now))
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

	identity, err := deterministicLiveSubmissionIdentity("risk_decision_live_cli_0001")
	if err != nil {
		t.Fatalf("identity: %v", err)
	}
	fakeExecutor := &fakeLiveSubmitExecutor{receivedAt: now.Add(time.Second)}
	preflightAccountReader := &fakeLiveSubmitPreflightAccountReader{
		snapshot: validLiveSubmitPreflightAccountSnapshot(t),
	}
	preflightPositionReader := &fakeLiveSubmitPreflightPositionReader{
		snapshot: validLiveSubmitFlatPreflightPositionSnapshot(t),
	}
	var output bytes.Buffer
	err = runLiveSubmit(context.Background(), []string{
		"-config", writeLiveSubmitConfig(t),
		"-decision-id", "risk_decision_live_cli_0001",
		"-subaccount-confirmed",
		"-max-initial-live-capital-usdt", "100",
		"-execute",
	}, liveSubmitDependencies{
		openDB: func(context.Context, config.DatabaseConfig) (*sql.DB, error) {
			return db, nil
		},
		newExecutor: func(_ *config.Config, apiKey string, apiSecret string) (domainlive.OrderExecutor, error) {
			if apiKey != "actual-live-api-key-value" || apiSecret != "actual-live-api-secret-value" {
				t.Fatalf("executor credentials mismatch")
			}
			return fakeExecutor, nil
		},
		newAccountReader: func(*config.Config) (domainlive.AccountSnapshotReader, error) {
			return preflightAccountReader, nil
		},
		newPositionReader: func(*config.Config) (domainlive.PositionSnapshotReader, error) {
			return preflightPositionReader, nil
		},
		output: &output,
	})
	if err != nil {
		t.Fatalf("run live submit: %v\nlogs:\n%s", err, output.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sql expectations: %v", err)
	}
	if fakeExecutor.calls != 1 {
		t.Fatalf("executor calls mismatch: got %d", fakeExecutor.calls)
	}
	if preflightAccountReader.calls != 1 || preflightAccountReader.query.AccountType != domainlive.AccountTypeUnified {
		t.Fatalf("preflight account reader mismatch: calls=%d query=%#v", preflightAccountReader.calls, preflightAccountReader.query)
	}
	if preflightPositionReader.calls != 1 || preflightPositionReader.query.Symbol != "BTCUSDT" {
		t.Fatalf("preflight position reader mismatch: calls=%d query=%#v", preflightPositionReader.calls, preflightPositionReader.query)
	}
	if fakeExecutor.statusCalls != 1 {
		t.Fatalf("status reader calls mismatch: got %d", fakeExecutor.statusCalls)
	}
	if fakeExecutor.positionCalls != 1 {
		t.Fatalf("position reader calls mismatch: got %d", fakeExecutor.positionCalls)
	}
	if fakeExecutor.statusQuery.ClientOrderID != identity.ClientOrderID ||
		fakeExecutor.statusQuery.Symbol != "BTCUSDT" ||
		fakeExecutor.statusQuery.Exchange != "bybit" {
		t.Fatalf("status query mismatch: %#v", fakeExecutor.statusQuery)
	}
	if fakeExecutor.positionQuery.Symbol != "BTCUSDT" ||
		fakeExecutor.positionQuery.Exchange != "bybit" ||
		fakeExecutor.positionQuery.Category != "linear" {
		t.Fatalf("position query mismatch: %#v", fakeExecutor.positionQuery)
	}
	if fakeExecutor.submission.DecisionID != "risk_decision_live_cli_0001" ||
		fakeExecutor.submission.SubmissionID != identity.SubmissionID ||
		fakeExecutor.submission.ClientOrderID != identity.ClientOrderID ||
		fakeExecutor.submission.Type != domainlive.OrderTypeMarket ||
		fakeExecutor.submission.TimeInForce != domainlive.TimeInForceIOC {
		t.Fatalf("executor submission mismatch: %#v", fakeExecutor.submission)
	}
	logs := output.String()
	for _, want := range []string{
		`"msg":"live submit preflight checked"`,
		`"ready":true`,
		`"account_type":"UNIFIED"`,
		`"account_total_equity":"50"`,
		`"account_snapshot_inserted":1`,
		`"position_checks":1`,
		`"position_snapshots":1`,
		`"msg":"live order submit checked"`,
		`"exchange_submitted":true`,
		`"ack_status":"ACCEPTED"`,
		`"msg":"live order status reconciled"`,
		`"exchange_status":"FILLED"`,
		`"snapshot_inserted":1`,
		`"msg":"live position reconciled"`,
		`"open":true`,
	} {
		if !strings.Contains(logs, want) {
			t.Fatalf("expected logs to contain %s, got\n%s", want, logs)
		}
	}
	if strings.Contains(logs, "actual-live-api-key-value") || strings.Contains(logs, "actual-live-api-secret-value") {
		t.Fatalf("logs must not contain credential values, got\n%s", logs)
	}
}

func TestRunLiveSubmitRequiresStatusReaderBeforeOrderSideEffects(t *testing.T) {
	t.Setenv("TRADING_LIVE_CONFIRM", "true")
	t.Setenv("BYBIT_API_KEY", "actual-live-api-key-value")
	t.Setenv("BYBIT_API_SECRET", "actual-live-api-secret-value")

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	mock.ExpectQuery("SELECT active, reason, source, created_at").
		WillReturnRows(sqlmock.NewRows([]string{"active", "reason", "source", "created_at"}))
	mock.ExpectExec("INSERT INTO live_account_snapshots").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("INSERT INTO live_position_snapshots").
		WillReturnResult(sqlmock.NewResult(1, 1))

	executor := &fakeLiveSubmitSubmitOnlyExecutor{}
	preflightAccountReader := &fakeLiveSubmitPreflightAccountReader{
		snapshot: validLiveSubmitPreflightAccountSnapshot(t),
	}
	preflightPositionReader := &fakeLiveSubmitPreflightPositionReader{
		snapshot: validLiveSubmitFlatPreflightPositionSnapshot(t),
	}
	err = runLiveSubmit(context.Background(), []string{
		"-config", writeLiveSubmitConfig(t),
		"-decision-id", "risk_decision_live_cli_0001",
		"-subaccount-confirmed",
		"-max-initial-live-capital-usdt", "100",
		"-execute",
	}, liveSubmitDependencies{
		openDB: func(context.Context, config.DatabaseConfig) (*sql.DB, error) {
			return db, nil
		},
		newExecutor: func(_ *config.Config, _ string, _ string) (domainlive.OrderExecutor, error) {
			return executor, nil
		},
		newAccountReader: func(*config.Config) (domainlive.AccountSnapshotReader, error) {
			return preflightAccountReader, nil
		},
		newPositionReader: func(*config.Config) (domainlive.PositionSnapshotReader, error) {
			return preflightPositionReader, nil
		},
		output: &bytes.Buffer{},
	})
	if err == nil || !strings.Contains(err.Error(), "status reconciliation") {
		t.Fatalf("expected status reader requirement error, got %v", err)
	}
	if executor.calls != 0 {
		t.Fatalf("executor must not submit before status reader capability check, calls=%d", executor.calls)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sql expectations: %v", err)
	}
}

func TestRunLiveSubmitRequiresPositionReaderBeforeOrderSideEffects(t *testing.T) {
	t.Setenv("TRADING_LIVE_CONFIRM", "true")
	t.Setenv("BYBIT_API_KEY", "actual-live-api-key-value")
	t.Setenv("BYBIT_API_SECRET", "actual-live-api-secret-value")

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	mock.ExpectQuery("SELECT active, reason, source, created_at").
		WillReturnRows(sqlmock.NewRows([]string{"active", "reason", "source", "created_at"}))
	mock.ExpectExec("INSERT INTO live_account_snapshots").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("INSERT INTO live_position_snapshots").
		WillReturnResult(sqlmock.NewResult(1, 1))

	executor := &fakeLiveSubmitStatusOnlyExecutor{}
	preflightAccountReader := &fakeLiveSubmitPreflightAccountReader{
		snapshot: validLiveSubmitPreflightAccountSnapshot(t),
	}
	preflightPositionReader := &fakeLiveSubmitPreflightPositionReader{
		snapshot: validLiveSubmitFlatPreflightPositionSnapshot(t),
	}
	err = runLiveSubmit(context.Background(), []string{
		"-config", writeLiveSubmitConfig(t),
		"-decision-id", "risk_decision_live_cli_0001",
		"-subaccount-confirmed",
		"-max-initial-live-capital-usdt", "100",
		"-execute",
	}, liveSubmitDependencies{
		openDB: func(context.Context, config.DatabaseConfig) (*sql.DB, error) {
			return db, nil
		},
		newExecutor: func(_ *config.Config, _ string, _ string) (domainlive.OrderExecutor, error) {
			return executor, nil
		},
		newAccountReader: func(*config.Config) (domainlive.AccountSnapshotReader, error) {
			return preflightAccountReader, nil
		},
		newPositionReader: func(*config.Config) (domainlive.PositionSnapshotReader, error) {
			return preflightPositionReader, nil
		},
		output: &bytes.Buffer{},
	})
	if err == nil || !strings.Contains(err.Error(), "position reconciliation") {
		t.Fatalf("expected position reader requirement error, got %v", err)
	}
	if executor.calls != 0 {
		t.Fatalf("executor must not submit before position reader capability check, calls=%d", executor.calls)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sql expectations: %v", err)
	}
}

func TestRunLiveSubmitBlocksOpenStartupPositionBeforeOrderSideEffects(t *testing.T) {
	t.Setenv("TRADING_LIVE_CONFIRM", "true")
	t.Setenv("BYBIT_API_KEY", "actual-live-api-key-value")
	t.Setenv("BYBIT_API_SECRET", "actual-live-api-secret-value")

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	mock.ExpectQuery("SELECT active, reason, source, created_at").
		WillReturnRows(sqlmock.NewRows([]string{"active", "reason", "source", "created_at"}))
	mock.ExpectExec("INSERT INTO live_account_snapshots").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("INSERT INTO live_position_snapshots").
		WillReturnResult(sqlmock.NewResult(1, 1))

	var executorCreated bool
	preflightAccountReader := &fakeLiveSubmitPreflightAccountReader{
		snapshot: validLiveSubmitPreflightAccountSnapshot(t),
	}
	preflightPositionReader := &fakeLiveSubmitPreflightPositionReader{
		snapshot: validLiveSubmitOpenPreflightPositionSnapshot(t),
	}
	err = runLiveSubmit(context.Background(), []string{
		"-config", writeLiveSubmitConfig(t),
		"-decision-id", "risk_decision_live_cli_0001",
		"-subaccount-confirmed",
		"-max-initial-live-capital-usdt", "100",
		"-execute",
	}, liveSubmitDependencies{
		openDB: func(context.Context, config.DatabaseConfig) (*sql.DB, error) {
			return db, nil
		},
		newExecutor: func(_ *config.Config, _ string, _ string) (domainlive.OrderExecutor, error) {
			executorCreated = true
			return &fakeLiveSubmitExecutor{}, nil
		},
		newAccountReader: func(*config.Config) (domainlive.AccountSnapshotReader, error) {
			return preflightAccountReader, nil
		},
		newPositionReader: func(*config.Config) (domainlive.PositionSnapshotReader, error) {
			return preflightPositionReader, nil
		},
		output: &bytes.Buffer{},
	})
	if err == nil || !strings.Contains(err.Error(), "must be flat") {
		t.Fatalf("expected open-position preflight error, got %v", err)
	}
	if executorCreated {
		t.Fatal("order executor must not be created after failed startup position preflight")
	}
	if preflightPositionReader.calls != 1 {
		t.Fatalf("position reader calls mismatch: got %d", preflightPositionReader.calls)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sql expectations: %v", err)
	}
}

func TestRunLiveSubmitBlocksUnsafeStartupAccountBeforeOrderSideEffects(t *testing.T) {
	t.Setenv("TRADING_LIVE_CONFIRM", "true")
	t.Setenv("BYBIT_API_KEY", "actual-live-api-key-value")
	t.Setenv("BYBIT_API_SECRET", "actual-live-api-secret-value")

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	mock.ExpectQuery("SELECT active, reason, source, created_at").
		WillReturnRows(sqlmock.NewRows([]string{"active", "reason", "source", "created_at"}))
	mock.ExpectExec("INSERT INTO live_account_snapshots").
		WillReturnResult(sqlmock.NewResult(1, 1))

	var executorCreated bool
	preflightAccountReader := &fakeLiveSubmitPreflightAccountReader{
		snapshot: mutateLiveSubmitPreflightAccountSnapshot(validLiveSubmitPreflightAccountSnapshot(t), func(s *domainlive.AccountSnapshot) {
			s.TotalEquity = decimal.RequireFromString("101")
		}),
	}
	err = runLiveSubmit(context.Background(), []string{
		"-config", writeLiveSubmitConfig(t),
		"-decision-id", "risk_decision_live_cli_0001",
		"-subaccount-confirmed",
		"-max-initial-live-capital-usdt", "100",
		"-execute",
	}, liveSubmitDependencies{
		openDB: func(context.Context, config.DatabaseConfig) (*sql.DB, error) {
			return db, nil
		},
		newExecutor: func(_ *config.Config, _ string, _ string) (domainlive.OrderExecutor, error) {
			executorCreated = true
			return &fakeLiveSubmitExecutor{}, nil
		},
		newAccountReader: func(*config.Config) (domainlive.AccountSnapshotReader, error) {
			return preflightAccountReader, nil
		},
		newPositionReader: func(*config.Config) (domainlive.PositionSnapshotReader, error) {
			return &fakeLiveSubmitPreflightPositionReader{snapshot: validLiveSubmitFlatPreflightPositionSnapshot(t)}, nil
		},
		output: &bytes.Buffer{},
	})
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("expected account cap preflight error, got %v", err)
	}
	if executorCreated {
		t.Fatal("order executor must not be created after failed startup account preflight")
	}
	if preflightAccountReader.calls != 1 {
		t.Fatalf("account reader calls mismatch: got %d", preflightAccountReader.calls)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sql expectations: %v", err)
	}
}

type fakeLiveSubmitPreflightAccountReader struct {
	query    domainlive.AccountSnapshotQuery
	snapshot domainlive.AccountSnapshot
	calls    int
	err      error
}

func (r *fakeLiveSubmitPreflightAccountReader) GetAccountSnapshot(_ context.Context, query domainlive.AccountSnapshotQuery) (domainlive.AccountSnapshot, error) {
	r.calls++
	r.query = query
	if r.err != nil {
		return domainlive.AccountSnapshot{}, r.err
	}
	return r.snapshot, nil
}

type fakeLiveSubmitPreflightPositionReader struct {
	query    domainlive.PositionSnapshotQuery
	snapshot domainlive.PositionSnapshot
	calls    int
	err      error
}

func (r *fakeLiveSubmitPreflightPositionReader) GetPositionSnapshot(_ context.Context, query domainlive.PositionSnapshotQuery) (domainlive.PositionSnapshot, error) {
	r.calls++
	r.query = query
	if r.err != nil {
		return domainlive.PositionSnapshot{}, r.err
	}
	return r.snapshot, nil
}

func validLiveSubmitPreflightAccountSnapshot(t *testing.T) domainlive.AccountSnapshot {
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
		t.Fatalf("new live submit preflight account snapshot: %v", err)
	}
	return snapshot
}

func mutateLiveSubmitPreflightAccountSnapshot(snapshot domainlive.AccountSnapshot, mutate func(*domainlive.AccountSnapshot)) domainlive.AccountSnapshot {
	mutate(&snapshot)
	return snapshot
}

type fakeLiveSubmitExecutor struct {
	submission    domainlive.OrderSubmission
	statusQuery   domainlive.OrderStatusQuery
	positionQuery domainlive.PositionSnapshotQuery
	receivedAt    time.Time
	calls         int
	statusCalls   int
	positionCalls int
}

func (e *fakeLiveSubmitExecutor) SubmitOrder(_ context.Context, submission domainlive.OrderSubmission) (domainlive.OrderAcknowledgement, error) {
	e.calls++
	e.submission = submission
	return domainlive.NewOrderAcknowledgement(domainlive.OrderAcknowledgementInput{
		SubmissionID:    submission.SubmissionID,
		ClientOrderID:   submission.ClientOrderID,
		Exchange:        submission.Exchange,
		ExchangeOrderID: "bybit_order_cli_0001",
		Status:          domainlive.OrderStatusAccepted,
		ReceivedAt:      e.receivedAt,
	})
}

func (e *fakeLiveSubmitExecutor) GetOrderStatus(_ context.Context, query domainlive.OrderStatusQuery) (domainlive.OrderStatusSnapshot, error) {
	e.statusCalls++
	e.statusQuery = query
	return domainlive.NewOrderStatusSnapshot(domainlive.OrderStatusSnapshotInput{
		ClientOrderID:              e.submission.ClientOrderID,
		ExchangeOrderID:            "bybit_order_cli_0001",
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

func (e *fakeLiveSubmitExecutor) GetPositionSnapshot(_ context.Context, query domainlive.PositionSnapshotQuery) (domainlive.PositionSnapshot, error) {
	e.positionCalls++
	e.positionQuery = query
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

type fakeLiveSubmitSubmitOnlyExecutor struct {
	calls int
}

func (e *fakeLiveSubmitSubmitOnlyExecutor) SubmitOrder(context.Context, domainlive.OrderSubmission) (domainlive.OrderAcknowledgement, error) {
	e.calls++
	return domainlive.OrderAcknowledgement{}, nil
}

type fakeLiveSubmitStatusOnlyExecutor struct {
	calls int
}

func (e *fakeLiveSubmitStatusOnlyExecutor) SubmitOrder(context.Context, domainlive.OrderSubmission) (domainlive.OrderAcknowledgement, error) {
	e.calls++
	return domainlive.OrderAcknowledgement{}, nil
}

func (e *fakeLiveSubmitStatusOnlyExecutor) GetOrderStatus(context.Context, domainlive.OrderStatusQuery) (domainlive.OrderStatusSnapshot, error) {
	return domainlive.OrderStatusSnapshot{}, nil
}

func validLiveSubmitFlatPreflightPositionSnapshot(t *testing.T) domainlive.PositionSnapshot {
	t.Helper()

	snapshot, err := domainlive.NewPositionSnapshot(domainlive.PositionSnapshotInput{
		Exchange:       "bybit",
		Category:       "linear",
		Symbol:         "BTCUSDT",
		Size:           decimal.Zero,
		MarkPrice:      decimal.RequireFromString("100000"),
		ExchangeStatus: domainlive.ExchangePositionStatusNormal,
		PositionIndex:  0,
		Sequence:       -1,
		ObservedAt:     time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("new flat live submit preflight position snapshot: %v", err)
	}
	return snapshot
}

func validLiveSubmitOpenPreflightPositionSnapshot(t *testing.T) domainlive.PositionSnapshot {
	t.Helper()

	observedAt := time.Now().UTC()
	snapshot, err := domainlive.NewPositionSnapshot(domainlive.PositionSnapshotInput{
		Exchange:          "bybit",
		Category:          "linear",
		Symbol:            "BTCUSDT",
		Side:              domainlive.OrderSideLong,
		Size:              decimal.RequireFromString("0.001"),
		AveragePrice:      decimal.RequireFromString("100000"),
		PositionValue:     decimal.RequireFromString("100"),
		MarkPrice:         decimal.RequireFromString("100000"),
		LiquidationPrice:  decimal.RequireFromString("99000"),
		Leverage:          decimal.RequireFromString("1"),
		ExchangeStatus:    domainlive.ExchangePositionStatusNormal,
		PositionIndex:     0,
		Sequence:          123,
		ExchangeCreatedAt: observedAt.Add(-time.Minute),
		ExchangeUpdatedAt: observedAt,
		ObservedAt:        observedAt,
	})
	if err != nil {
		t.Fatalf("new open live submit preflight position snapshot: %v", err)
	}
	return snapshot
}

func liveSubmitRiskDecisionRows(now time.Time) *sqlmock.Rows {
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

func writeLiveSubmitConfig(t *testing.T) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "config.yaml")
	raw := []byte(`
app:
  name: crypto-quant-platform
  env: test
  mode: live-submit
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
