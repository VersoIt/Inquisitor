package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/shopspring/decimal"

	"github.com/VersoIt/Inquisitor/internal/config"
)

func TestPaperExecutionCostModelUsesDefaultsAndOverrides(t *testing.T) {
	cfg := safePaperExecutionConfig()
	cfg.Fees = config.FeesConfig{MakerBps: 2, TakerBps: 6}
	cfg.Risk.MaxSpreadBps = 4
	cfg.Slippage = config.SlippageConfig{DefaultBps: 3, ConservativeMultiplier: 1.5}

	tests := []struct {
		name             string
		spreadOverride   int
		slippageOverride int
		wantSpreadBPS    string
		wantSlippageBPS  string
	}{
		{
			name:             "config defaults",
			spreadOverride:   -1,
			slippageOverride: -1,
			wantSpreadBPS:    "4",
			wantSlippageBPS:  "3",
		},
		{
			name:             "explicit conservative overrides",
			spreadOverride:   8,
			slippageOverride: 5,
			wantSpreadBPS:    "8",
			wantSlippageBPS:  "5",
		},
		{
			name:             "zero overrides are accepted",
			spreadOverride:   0,
			slippageOverride: 0,
			wantSpreadBPS:    "0",
			wantSlippageBPS:  "0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := paperExecutionCostModel(&cfg, tt.spreadOverride, tt.slippageOverride)
			if err != nil {
				t.Fatalf("cost model: %v", err)
			}
			assertDecimalString(t, "maker fee", got.MakerFeeBPS, "2")
			assertDecimalString(t, "taker fee", got.TakerFeeBPS, "6")
			assertDecimalString(t, "spread", got.SpreadBPS, tt.wantSpreadBPS)
			assertDecimalString(t, "slippage", got.SlippageBPS, tt.wantSlippageBPS)
			assertDecimalString(t, "slippage multiplier", got.SlippageConservativeFactor, "1.5")
		})
	}
}

func TestPaperExecutionCostModelRejectsInvalidEconomics(t *testing.T) {
	tests := []struct {
		name       string
		mutate     func(*config.Config)
		wantErrSub string
	}{
		{
			name: "negative maker fee",
			mutate: func(cfg *config.Config) {
				cfg.Fees.MakerBps = -1
			},
			wantErrSub: "maker_fee_bps",
		},
		{
			name: "price impact at or above one hundred percent",
			mutate: func(cfg *config.Config) {
				cfg.Risk.MaxSpreadBps = 10000
				cfg.Slippage.DefaultBps = 5000
				cfg.Slippage.ConservativeMultiplier = 1
			},
			wantErrSub: "10000 bps",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := safePaperExecutionConfig()
			tt.mutate(&cfg)

			_, err := paperExecutionCostModel(&cfg, -1, -1)
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
		})
	}
}

func TestPaperExecutionSafetyPolicyRejectsUnsafeConfigTableDriven(t *testing.T) {
	cfg := safePaperExecutionConfig()
	if err := paperExecutionSafetyPolicy(&cfg); err != nil {
		t.Fatalf("safe config should pass: %v", err)
	}

	tests := []struct {
		name       string
		mutate     func(*config.Config)
		wantErrSub string
	}{
		{
			name: "trading disabled",
			mutate: func(cfg *config.Config) {
				cfg.Trading.Enabled = false
			},
			wantErrSub: "paper trading",
		},
		{
			name: "live mode",
			mutate: func(cfg *config.Config) {
				cfg.Trading.Mode = "live"
			},
			wantErrSub: "mode",
		},
		{
			name: "live trading allowed",
			mutate: func(cfg *config.Config) {
				cfg.Trading.AllowLive = true
			},
			wantErrSub: "live",
		},
		{
			name: "withdrawal permission allowed",
			mutate: func(cfg *config.Config) {
				cfg.Live.WithdrawalPermissionAllowed = true
			},
			wantErrSub: "withdrawal",
		},
		{
			name: "fees omitted",
			mutate: func(cfg *config.Config) {
				cfg.Paper.SimulateFees = false
			},
			wantErrSub: "fees",
		},
		{
			name: "slippage omitted",
			mutate: func(cfg *config.Config) {
				cfg.Paper.SimulateSlippage = false
			},
			wantErrSub: "slippage",
		},
		{
			name: "spread omitted",
			mutate: func(cfg *config.Config) {
				cfg.Paper.SimulateSpread = false
			},
			wantErrSub: "spread",
		},
		{
			name: "initial balance missing",
			mutate: func(cfg *config.Config) {
				cfg.Paper.InitialBalance = 0
			},
			wantErrSub: "initial_balance",
		},
		{
			name: "minimum days missing",
			mutate: func(cfg *config.Config) {
				cfg.Paper.MinimumDays = 0
			},
			wantErrSub: "minimum_days",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := safePaperExecutionConfig()
			tt.mutate(&cfg)

			err := paperExecutionSafetyPolicy(&cfg)
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
		})
	}
}

func TestPaperExecutionActionClassificationTableDriven(t *testing.T) {
	tests := []struct {
		action                string
		wantCosts             bool
		wantManualObservation bool
		wantCycleLock         bool
	}{
		{action: "quote"},
		{action: "pending"},
		{action: "auto-enter", wantCosts: true},
		{action: "auto-exit", wantCosts: true},
		{action: "cycle-preflight", wantCosts: true, wantCycleLock: true},
		{action: "auto-cycle", wantCosts: true, wantCycleLock: true},
		{action: "enter", wantCosts: true, wantManualObservation: true},
		{action: "fill", wantCosts: true, wantManualObservation: true},
		{action: "settle", wantCosts: true, wantManualObservation: true},
	}

	for _, tt := range tests {
		t.Run(tt.action, func(t *testing.T) {
			if got := actionRequiresCosts(tt.action); got != tt.wantCosts {
				t.Fatalf("actionRequiresCosts(%q) = %t, want %t", tt.action, got, tt.wantCosts)
			}
			if got := actionRequiresManualMarketObservation(tt.action); got != tt.wantManualObservation {
				t.Fatalf("actionRequiresManualMarketObservation(%q) = %t, want %t", tt.action, got, tt.wantManualObservation)
			}
			if got := actionRequiresPaperExecutionCycleLock(tt.action); got != tt.wantCycleLock {
				t.Fatalf("actionRequiresPaperExecutionCycleLock(%q) = %t, want %t", tt.action, got, tt.wantCycleLock)
			}
		})
	}
}

func TestRequirePaperExecutionCycleScopeTableDriven(t *testing.T) {
	tests := []struct {
		name             string
		validationID     string
		symbol           string
		interval         string
		wantValidationID string
		wantSymbol       string
		wantInterval     string
		wantErrSub       string
	}{
		{
			name:             "explicit scope",
			validationID:     "paper_validation_001",
			symbol:           "BTCUSDT",
			interval:         "1",
			wantValidationID: "paper_validation_001",
			wantSymbol:       "BTCUSDT",
			wantInterval:     "1",
		},
		{
			name:             "trims validation id symbol and interval",
			validationID:     " paper_validation_001 ",
			symbol:           " btcusdt ",
			interval:         " 5 ",
			wantValidationID: "paper_validation_001",
			wantSymbol:       "BTCUSDT",
			wantInterval:     "5",
		},
		{
			name:         "missing validation id",
			validationID: " ",
			symbol:       "BTCUSDT",
			interval:     "1",
			wantErrSub:   "validation_id",
		},
		{
			name:         "missing symbol",
			validationID: "paper_validation_001",
			symbol:       " ",
			interval:     "1",
			wantErrSub:   "symbol",
		},
		{
			name:         "missing interval",
			validationID: "paper_validation_001",
			symbol:       "BTCUSDT",
			interval:     " ",
			wantErrSub:   "interval",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := requirePaperExecutionCycleScope(tt.validationID, tt.symbol, tt.interval)
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
				t.Fatalf("require cycle scope: %v", err)
			}
			if got.ValidationID != tt.wantValidationID || got.Symbol != tt.wantSymbol || got.Interval != tt.wantInterval {
				t.Fatalf("scope mismatch: got %#v, want validation_id=%q symbol=%q interval=%q", got, tt.wantValidationID, tt.wantSymbol, tt.wantInterval)
			}
		})
	}
}

func TestPaperExecutionCycleLockKeyIsStableAndScoped(t *testing.T) {
	base := paperExecutionCycleScope{ValidationID: "paper_validation_001", Symbol: "BTCUSDT", Interval: "1"}

	if got, again := paperExecutionCycleLockKey(base), paperExecutionCycleLockKey(base); got != again {
		t.Fatalf("lock key must be stable: got %d then %d", got, again)
	}

	tests := []struct {
		name   string
		mutate func(paperExecutionCycleScope) paperExecutionCycleScope
	}{
		{name: "validation id", mutate: func(scope paperExecutionCycleScope) paperExecutionCycleScope {
			scope.ValidationID = "paper_validation_002"
			return scope
		}},
		{name: "symbol", mutate: func(scope paperExecutionCycleScope) paperExecutionCycleScope {
			scope.Symbol = "ETHUSDT"
			return scope
		}},
		{name: "interval", mutate: func(scope paperExecutionCycleScope) paperExecutionCycleScope {
			scope.Interval = "5"
			return scope
		}},
	}

	baseKey := paperExecutionCycleLockKey(base)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := paperExecutionCycleLockKey(tt.mutate(base)); got == baseKey {
				t.Fatalf("lock key should change when %s changes", tt.name)
			}
		})
	}
}

func TestAcquirePaperExecutionCycleLockTableDriven(t *testing.T) {
	ctx := context.Background()
	scope := paperExecutionCycleScope{ValidationID: "paper_validation_001", Symbol: "BTCUSDT", Interval: "1"}
	key := paperExecutionCycleLockKey(scope)

	tests := []struct {
		name       string
		mock       func(sqlmock.Sqlmock)
		wantErrSub string
		unlock     bool
	}{
		{
			name: "acquires and releases lock",
			mock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery("SELECT pg_try_advisory_lock").WithArgs(key).
					WillReturnRows(sqlmock.NewRows([]string{"pg_try_advisory_lock"}).AddRow(true))
				mock.ExpectQuery("SELECT pg_advisory_unlock").WithArgs(key).
					WillReturnRows(sqlmock.NewRows([]string{"pg_advisory_unlock"}).AddRow(true))
			},
			unlock: true,
		},
		{
			name: "rejects when lock is already held",
			mock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery("SELECT pg_try_advisory_lock").WithArgs(key).
					WillReturnRows(sqlmock.NewRows([]string{"pg_try_advisory_lock"}).AddRow(false))
			},
			wantErrSub: "already running",
		},
		{
			name: "reports unlock mismatch",
			mock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery("SELECT pg_try_advisory_lock").WithArgs(key).
					WillReturnRows(sqlmock.NewRows([]string{"pg_try_advisory_lock"}).AddRow(true))
				mock.ExpectQuery("SELECT pg_advisory_unlock").WithArgs(key).
					WillReturnRows(sqlmock.NewRows([]string{"pg_advisory_unlock"}).AddRow(false))
			},
			wantErrSub: "not held",
			unlock:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatalf("sqlmock: %v", err)
			}
			defer db.Close()
			tt.mock(mock)

			unlock, err := acquirePaperExecutionCycleLock(ctx, db, scope)
			if tt.wantErrSub != "" && !tt.unlock {
				if err == nil {
					t.Fatal("expected error")
				}
				if !strings.Contains(err.Error(), tt.wantErrSub) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
				}
				if unlock != nil {
					t.Fatal("unlock must be nil when lock acquisition fails")
				}
			} else {
				if err != nil {
					t.Fatalf("acquire lock: %v", err)
				}
				if unlock == nil {
					t.Fatal("unlock is required after successful acquisition")
				}
				err = unlock(ctx)
				if tt.wantErrSub != "" {
					if err == nil {
						t.Fatal("expected unlock error")
					}
					if !strings.Contains(err.Error(), tt.wantErrSub) {
						t.Fatalf("expected unlock error containing %q, got %v", tt.wantErrSub, err)
					}
				} else if err != nil {
					t.Fatalf("unlock: %v", err)
				}
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatalf("sql expectations: %v", err)
			}
		})
	}
}

func TestAcquireRequiredPaperExecutionCycleLockTableDriven(t *testing.T) {
	ctx := context.Background()
	scope := paperExecutionCycleScope{ValidationID: "paper_validation_001", Symbol: "BTCUSDT", Interval: "1"}
	key := paperExecutionCycleLockKey(scope)

	tests := []struct {
		name       string
		action     string
		wantErrSub string
	}{
		{name: "cycle preflight uses cycle lock", action: "cycle-preflight"},
		{name: "auto cycle uses cycle lock", action: "auto-cycle"},
		{name: "non cycle action rejected", action: "quote", wantErrSub: "not required"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sqlDB, mock, err := sqlmock.New()
			if err != nil {
				t.Fatalf("sqlmock: %v", err)
			}
			defer sqlDB.Close()

			if tt.wantErrSub == "" {
				mock.ExpectQuery("SELECT pg_try_advisory_lock").WithArgs(key).
					WillReturnRows(sqlmock.NewRows([]string{"pg_try_advisory_lock"}).AddRow(true))
				mock.ExpectQuery("SELECT pg_advisory_unlock").WithArgs(key).
					WillReturnRows(sqlmock.NewRows([]string{"pg_advisory_unlock"}).AddRow(true))
			}

			unlock, lockErr := acquireRequiredPaperExecutionCycleLock(ctx, sqlDB, tt.action, scope)
			if tt.wantErrSub != "" {
				if lockErr == nil {
					t.Fatal("expected error")
				}
				if !strings.Contains(lockErr.Error(), tt.wantErrSub) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, lockErr)
				}
				if unlock != nil {
					t.Fatal("unlock must be nil for non-cycle action")
				}
				return
			}
			if lockErr != nil {
				t.Fatalf("acquire required lock: %v", lockErr)
			}
			if err := unlock(ctx); err != nil {
				t.Fatalf("unlock: %v", err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatalf("sql expectations: %v", err)
			}
		})
	}
}

func TestParsePaperExecutionDecimalTableDriven(t *testing.T) {
	tests := []struct {
		name       string
		value      string
		want       string
		wantErrSub string
	}{
		{name: "trimmed decimal", value: " 100.125 ", want: "100.125"},
		{name: "missing", value: " ", wantErrSub: "mid-price is required"},
		{name: "invalid", value: "nope", wantErrSub: "decimal"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseRequiredDecimal("mid-price", tt.value)
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
				t.Fatalf("parse decimal: %v", err)
			}
			assertDecimalString(t, "decimal", got, tt.want)
		})
	}
}

func TestParsePaperExecutionTimeTableDriven(t *testing.T) {
	tests := []struct {
		name       string
		value      string
		want       time.Time
		wantErrSub string
	}{
		{
			name:  "utc timestamp",
			value: "2026-07-16T12:00:00Z",
			want:  time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC),
		},
		{
			name:  "offset timestamp normalized to utc",
			value: "2026-07-16T15:30:00+03:00",
			want:  time.Date(2026, 7, 16, 12, 30, 0, 0, time.UTC),
		},
		{name: "missing", value: " ", wantErrSub: "at is required"},
		{name: "invalid", value: "later", wantErrSub: "RFC3339"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseRequiredTime("at", tt.value)
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
				t.Fatalf("parse time: %v", err)
			}
			if !got.Equal(tt.want) || got.Location() != time.UTC {
				t.Fatalf("time mismatch: got %s (%s), want %s UTC", got, got.Location(), tt.want)
			}
		})
	}
}

func safePaperExecutionConfig() config.Config {
	return config.Config{
		Fees: config.FeesConfig{
			MakerBps: 1,
			TakerBps: 6,
		},
		Slippage: config.SlippageConfig{
			DefaultBps:             3,
			ConservativeMultiplier: 1.5,
		},
		Trading: config.TradingConfig{
			Enabled:   true,
			Mode:      "paper",
			AllowLive: false,
		},
		Risk: config.RiskConfig{
			MaxSpreadBps: 2,
		},
		Paper: config.PaperConfig{
			InitialBalance:   1000,
			MinimumDays:      30,
			SimulateFees:     true,
			SimulateSlippage: true,
			SimulateSpread:   true,
		},
		Live: config.LiveConfig{
			WithdrawalPermissionAllowed: false,
		},
	}
}

func assertDecimalString(t *testing.T, label string, got decimal.Decimal, want string) {
	t.Helper()

	if got.String() != want {
		t.Fatalf("%s mismatch: got %s, want %s", label, got.String(), want)
	}
}
