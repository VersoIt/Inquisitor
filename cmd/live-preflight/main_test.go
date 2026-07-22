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

	applive "github.com/VersoIt/Inquisitor/internal/app/live"
	"github.com/VersoIt/Inquisitor/internal/config"
	domainlive "github.com/VersoIt/Inquisitor/internal/live"
)

func TestLiveStartupRequestFromConfigMapsSafetyFields(t *testing.T) {
	cfg := safeLivePreflightConfig()
	maxCapital := decimal.RequireFromString("100")

	got, err := liveStartupRequestFromConfig(&cfg, true, maxCapital)
	if err != nil {
		t.Fatalf("request from config: %v", err)
	}

	assertLivePreflightRequest(t, got, applive.PreflightLiveStartupRequest{
		TradingEnabled:              true,
		TradingMode:                 "live",
		AllowLive:                   true,
		RequireEnvConfirmation:      true,
		ConfirmationEnv:             "TRADING_LIVE_CONFIRM",
		APIKeyEnv:                   "BYBIT_API_KEY",
		APISecretEnv:                "BYBIT_API_SECRET",
		RequireSubaccount:           true,
		SubaccountConfirmed:         true,
		WithdrawalPermissionAllowed: false,
		InitialLiveCapitalUSDT:      decimal.RequireFromString("50.25"),
		MaxInitialLiveCapitalUSDT:   maxCapital,
		ExpectedAccount: domainlive.AccountSnapshotQuery{
			Exchange:    "bybit",
			AccountType: domainlive.AccountTypeUnified,
		},
		AccountBaseCurrency:   "USDT",
		MaxAccountSnapshotAge: defaultMaxAccountSnapshotAge,
		ExpectedFlatPositions: []domainlive.PositionSnapshotQuery{{
			Exchange: "bybit",
			Category: "linear",
			Symbol:   "BTCUSDT",
		}},
		MaxPositionSnapshotAge: defaultMaxPositionSnapshotAge,
	})
}

func TestParsePositiveDecimalFlagTableDriven(t *testing.T) {
	tests := []struct {
		name       string
		value      string
		want       string
		wantErrSub string
	}{
		{name: "trimmed positive integer", value: " 100 ", want: "100"},
		{name: "fractional dollars", value: "50.25", want: "50.25"},
		{name: "missing", value: " ", wantErrSub: "max cap is required"},
		{name: "zero", value: "0", wantErrSub: "positive"},
		{name: "negative", value: "-1", wantErrSub: "positive"},
		{name: "invalid", value: "ten", wantErrSub: "decimal"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parsePositiveDecimalFlag("max cap", tt.value)
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
				t.Fatalf("parse decimal flag: %v", err)
			}
			if got.String() != tt.want {
				t.Fatalf("decimal mismatch: got %s, want %s", got, tt.want)
			}
		})
	}
}

func TestRunLivePreflightUsesConfigEnvAndKillSwitch(t *testing.T) {
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
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO live_position_snapshots").
		WillReturnResult(sqlmock.NewResult(0, 1))
	accountReader := &fakeLivePreflightAccountReader{
		snapshot: validLivePreflightAccountSnapshot(t),
	}
	positionReader := &fakeLivePreflightPositionReader{
		snapshot: validLivePreflightFlatPositionSnapshot(t, domainlive.PositionSnapshotQuery{
			Exchange: "bybit",
			Category: "linear",
			Symbol:   "BTCUSDT",
		}),
	}

	var output bytes.Buffer
	err = runLivePreflight(context.Background(), []string{
		"-config", writeLivePreflightConfig(t),
		"-subaccount-confirmed",
		"-max-initial-live-capital-usdt", "100",
	}, livePreflightDependencies{
		openDB: func(context.Context, config.DatabaseConfig) (*sql.DB, error) {
			return db, nil
		},
		newAccountReader: func(*config.Config) (domainlive.AccountSnapshotReader, error) {
			return accountReader, nil
		},
		newPositionReader: func(*config.Config) (domainlive.PositionSnapshotReader, error) {
			return positionReader, nil
		},
		output: &output,
	})
	if err != nil {
		t.Fatalf("run live preflight: %v\nlogs:\n%s", err, output.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sql expectations: %v", err)
	}

	logs := output.String()
	for _, want := range []string{
		`"msg":"live startup preflight checked"`,
		`"ready":true`,
		`"api_key_present":true`,
		`"api_secret_present":true`,
		`"account_type":"UNIFIED"`,
		`"account_total_equity":"50"`,
		`"account_snapshot_inserted":1`,
		`"position_checks":1`,
		`"position_snapshots":1`,
		`"position_snapshot_inserted":1`,
		`"msg":"live startup preflight passed"`,
	} {
		if !strings.Contains(logs, want) {
			t.Fatalf("expected logs to contain %s, got\n%s", want, logs)
		}
	}
	if accountReader.calls != 1 || accountReader.query.AccountType != domainlive.AccountTypeUnified {
		t.Fatalf("account reader mismatch: calls=%d query=%#v", accountReader.calls, accountReader.query)
	}
	if positionReader.calls != 1 || positionReader.query.Symbol != "BTCUSDT" {
		t.Fatalf("position reader mismatch: calls=%d query=%#v", positionReader.calls, positionReader.query)
	}
	if strings.Contains(logs, "actual-live-api-key-value") || strings.Contains(logs, "actual-live-api-secret-value") {
		t.Fatalf("logs must not contain credential values, got\n%s", logs)
	}
}

func TestRunLivePreflightRejectsMissingSubaccountConfirmation(t *testing.T) {
	t.Setenv("TRADING_LIVE_CONFIRM", "true")
	t.Setenv("BYBIT_API_KEY", "actual-live-api-key-value")
	t.Setenv("BYBIT_API_SECRET", "actual-live-api-secret-value")

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	mock.ExpectQuery("SELECT active, reason, source, created_at").
		WillReturnRows(sqlmock.NewRows([]string{"active", "reason", "source", "created_at"}))

	var output bytes.Buffer
	err = runLivePreflight(context.Background(), []string{
		"-config", writeLivePreflightConfig(t),
		"-max-initial-live-capital-usdt", "100",
	}, livePreflightDependencies{
		openDB: func(context.Context, config.DatabaseConfig) (*sql.DB, error) {
			return db, nil
		},
		output: &output,
	})
	if err == nil {
		t.Fatal("expected missing subaccount confirmation to fail")
	}
	if !strings.Contains(err.Error(), "dedicated live subaccount") {
		t.Fatalf("expected subaccount error, got %v", err)
	}
	if !strings.Contains(output.String(), `"ready":false`) {
		t.Fatalf("expected failed preflight log, got\n%s", output.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sql expectations: %v", err)
	}
}

func safeLivePreflightConfig() config.Config {
	return config.Config{
		App: config.AppConfig{
			LogLevel: "info",
		},
		Database: config.DatabaseConfig{
			DSN:          "postgres://user:pass@localhost:5432/inquisitor?sslmode=disable",
			MaxOpenConns: 1,
			MaxIdleConns: 1,
		},
		Exchange: config.ExchangeConfig{
			Primary:     "bybit",
			RestBaseURL: "https://api-testnet.bybit.com",
			PublicWSURL: "wss://stream-testnet.bybit.com/v5/public/linear",
			Category:    "linear",
			Symbols:     []string{"BTCUSDT"},
		},
		Trading: config.TradingConfig{
			Enabled:      true,
			Mode:         "live",
			AllowLive:    true,
			BaseCurrency: "USDT",
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

func writeLivePreflightConfig(t *testing.T) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "config.yaml")
	raw := []byte(`
app:
  name: crypto-quant-platform
  env: test
  mode: live-preflight
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

func assertLivePreflightRequest(t *testing.T, got applive.PreflightLiveStartupRequest, want applive.PreflightLiveStartupRequest) {
	t.Helper()

	if got.TradingEnabled != want.TradingEnabled ||
		got.TradingMode != want.TradingMode ||
		got.AllowLive != want.AllowLive ||
		got.RequireEnvConfirmation != want.RequireEnvConfirmation ||
		got.ConfirmationEnv != want.ConfirmationEnv ||
		got.APIKeyEnv != want.APIKeyEnv ||
		got.APISecretEnv != want.APISecretEnv ||
		got.RequireSubaccount != want.RequireSubaccount ||
		got.SubaccountConfirmed != want.SubaccountConfirmed ||
		got.WithdrawalPermissionAllowed != want.WithdrawalPermissionAllowed {
		t.Fatalf("request scalar fields mismatch:\ngot  %#v\nwant %#v", got, want)
	}
	if !got.InitialLiveCapitalUSDT.Equal(want.InitialLiveCapitalUSDT) {
		t.Fatalf("initial capital mismatch: got %s, want %s", got.InitialLiveCapitalUSDT, want.InitialLiveCapitalUSDT)
	}
	if !got.MaxInitialLiveCapitalUSDT.Equal(want.MaxInitialLiveCapitalUSDT) {
		t.Fatalf("max capital mismatch: got %s, want %s", got.MaxInitialLiveCapitalUSDT, want.MaxInitialLiveCapitalUSDT)
	}
	if got.ExpectedAccount != want.ExpectedAccount {
		t.Fatalf("account query mismatch: got %#v, want %#v", got.ExpectedAccount, want.ExpectedAccount)
	}
	if got.AccountBaseCurrency != want.AccountBaseCurrency {
		t.Fatalf("account base currency mismatch: got %q, want %q", got.AccountBaseCurrency, want.AccountBaseCurrency)
	}
	if got.MaxAccountSnapshotAge != want.MaxAccountSnapshotAge {
		t.Fatalf("max account snapshot age mismatch: got %s, want %s", got.MaxAccountSnapshotAge, want.MaxAccountSnapshotAge)
	}
	if got.MaxPositionSnapshotAge != want.MaxPositionSnapshotAge {
		t.Fatalf("max position snapshot age mismatch: got %s, want %s", got.MaxPositionSnapshotAge, want.MaxPositionSnapshotAge)
	}
	if len(got.ExpectedFlatPositions) != len(want.ExpectedFlatPositions) {
		t.Fatalf("position query length mismatch: got %#v, want %#v", got.ExpectedFlatPositions, want.ExpectedFlatPositions)
	}
	for index := range got.ExpectedFlatPositions {
		if got.ExpectedFlatPositions[index] != want.ExpectedFlatPositions[index] {
			t.Fatalf("position query[%d] mismatch: got %#v, want %#v", index, got.ExpectedFlatPositions[index], want.ExpectedFlatPositions[index])
		}
	}
}

type fakeLivePreflightAccountReader struct {
	query    domainlive.AccountSnapshotQuery
	snapshot domainlive.AccountSnapshot
	calls    int
	err      error
}

func (r *fakeLivePreflightAccountReader) GetAccountSnapshot(_ context.Context, query domainlive.AccountSnapshotQuery) (domainlive.AccountSnapshot, error) {
	r.calls++
	r.query = query
	if r.err != nil {
		return domainlive.AccountSnapshot{}, r.err
	}
	return r.snapshot, nil
}

type fakeLivePreflightPositionReader struct {
	query    domainlive.PositionSnapshotQuery
	snapshot domainlive.PositionSnapshot
	calls    int
	err      error
}

func (r *fakeLivePreflightPositionReader) GetPositionSnapshot(_ context.Context, query domainlive.PositionSnapshotQuery) (domainlive.PositionSnapshot, error) {
	r.calls++
	r.query = query
	if r.err != nil {
		return domainlive.PositionSnapshot{}, r.err
	}
	return r.snapshot, nil
}

func validLivePreflightAccountSnapshot(t *testing.T) domainlive.AccountSnapshot {
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
		t.Fatalf("new live preflight account snapshot: %v", err)
	}
	return snapshot
}

func validLivePreflightFlatPositionSnapshot(t *testing.T, query domainlive.PositionSnapshotQuery) domainlive.PositionSnapshot {
	t.Helper()

	snapshot, err := domainlive.NewPositionSnapshot(domainlive.PositionSnapshotInput{
		Exchange:       query.Exchange,
		Category:       query.Category,
		Symbol:         query.Symbol,
		Size:           decimal.Zero,
		MarkPrice:      decimal.RequireFromString("100000"),
		ExchangeStatus: domainlive.ExchangePositionStatusNormal,
		PositionIndex:  0,
		Sequence:       -1,
		ObservedAt:     time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("new live preflight flat position snapshot: %v", err)
	}
	return snapshot
}
