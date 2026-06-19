package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/VersoIt/Inquisitor/internal/config"
)

func TestLoadExampleConfigExpandsRequiredEnvironment(t *testing.T) {
	t.Setenv("DATABASE_DSN", "postgres://user:pass@localhost:5432/inquisitor?sslmode=disable")

	cfg, err := config.Load("../../configs/config.example.yaml")
	if err != nil {
		t.Fatalf("load example config: %v", err)
	}

	if cfg.Database.DSN != "postgres://user:pass@localhost:5432/inquisitor?sslmode=disable" {
		t.Fatalf("unexpected database dsn: %s", cfg.Database.DSN)
	}
	if cfg.Exchange.RestBaseURL == "" {
		t.Fatal("expected exchange rest base url")
	}
	if cfg.Exchange.PublicWSURL == "" {
		t.Fatal("expected exchange public websocket url")
	}
	if cfg.Trading.Enabled || cfg.Trading.AllowLive {
		t.Fatalf("live trading must be disabled by default: %#v", cfg.Trading)
	}
}

func TestLoadExampleConfigFailsOnMissingEnvironment(t *testing.T) {
	missingEnv := "INQUISITOR_CONFIG_TEST_MISSING_DSN"
	originalValue, wasSet := os.LookupEnv(missingEnv)
	if err := os.Unsetenv(missingEnv); err != nil {
		t.Fatalf("unset env: %v", err)
	}
	t.Cleanup(func() {
		if wasSet {
			_ = os.Setenv(missingEnv, originalValue)
		}
	})

	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("database:\n  dsn: ${"+missingEnv+"}\n"), 0o600); err != nil {
		t.Fatalf("write temp config: %v", err)
	}

	_, err := config.Load(path)
	if err == nil {
		t.Fatal("expected missing environment error")
	}
	if !strings.Contains(err.Error(), missingEnv) {
		t.Fatalf("expected %s in error, got %v", missingEnv, err)
	}
}

func TestValidateRejectsUnsafeLiveDefaults(t *testing.T) {
	cfg := validConfig()
	cfg.Trading.AllowLive = true

	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected unsafe live config to fail")
	}
	if !strings.Contains(err.Error(), "live trading") {
		t.Fatalf("expected live trading safety error, got %v", err)
	}
}

func TestValidateTableDriven(t *testing.T) {
	tests := []struct {
		name       string
		mutate     func(*config.Config)
		wantErrSub string
	}{
		{
			name: "accepts valid config",
		},
		{
			name: "rejects invalid rest url",
			mutate: func(cfg *config.Config) {
				cfg.Exchange.RestBaseURL = "ftp://api-testnet.bybit.com"
			},
			wantErrSub: "exchange.rest_base_url must use http or https",
		},
		{
			name: "rejects invalid websocket url",
			mutate: func(cfg *config.Config) {
				cfg.Exchange.PublicWSURL = "https://stream-testnet.bybit.com/v5/public/linear"
			},
			wantErrSub: "exchange.public_ws_url must use ws or wss",
		},
		{
			name: "rejects unsupported exchange category",
			mutate: func(cfg *config.Config) {
				cfg.Exchange.Category = "spot"
			},
			wantErrSub: "exchange.category must be linear",
		},
		{
			name: "rejects duplicate symbols",
			mutate: func(cfg *config.Config) {
				cfg.Exchange.Symbols = []string{"BTCUSDT", "btcusdt"}
			},
			wantErrSub: "exchange.symbols must not contain duplicates",
		},
		{
			name: "rejects unsupported candle interval",
			mutate: func(cfg *config.Config) {
				cfg.MarketData.CandleIntervals = []string{"1", "7"}
			},
			wantErrSub: "unsupported interval 7",
		},
		{
			name: "rejects live trading mode in phase one",
			mutate: func(cfg *config.Config) {
				cfg.Trading.Mode = "live"
			},
			wantErrSub: "trading.mode must be paper",
		},
		{
			name: "rejects confidence outside percent range",
			mutate: func(cfg *config.Config) {
				cfg.Risk.MinConfidence = 101
			},
			wantErrSub: "risk.min_confidence must be between 0 and 100",
		},
		{
			name: "rejects negative max spread",
			mutate: func(cfg *config.Config) {
				cfg.Risk.MaxSpreadBps = -1
			},
			wantErrSub: "risk.max_spread_bps must be greater than or equal to zero",
		},
		{
			name: "rejects risk per trade above one percent",
			mutate: func(cfg *config.Config) {
				cfg.Risk.RiskPerTradePct = 1.01
			},
			wantErrSub: "risk.risk_per_trade_pct",
		},
		{
			name: "rejects inverted risk loss limits",
			mutate: func(cfg *config.Config) {
				cfg.Risk.MaxDailyLossPct = 4
			},
			wantErrSub: "daily <= weekly",
		},
		{
			name: "rejects zero losing streak limit",
			mutate: func(cfg *config.Config) {
				cfg.Risk.MaxLosingStreak = 0
			},
			wantErrSub: "risk.max_losing_streak",
		},
		{
			name: "rejects negative max slippage",
			mutate: func(cfg *config.Config) {
				cfg.Risk.MaxSlippageBps = -1
			},
			wantErrSub: "risk.max_slippage_bps",
		},
		{
			name: "rejects correlated exposure above portfolio exposure",
			mutate: func(cfg *config.Config) {
				cfg.Risk.PortfolioMaxCorrelatedExposurePct = cfg.Risk.PortfolioMaxCryptoExposurePct + 1
			},
			wantErrSub: "risk.portfolio_max_correlated_exposure_pct",
		},
		{
			name: "rejects zero max open positions",
			mutate: func(cfg *config.Config) {
				cfg.Trading.MaxOpenPositions = 0
			},
			wantErrSub: "trading.max_open_positions",
		},
		{
			name: "rejects regime confidence outside percent range",
			mutate: func(cfg *config.Config) {
				cfg.Regime.MinConfidence = 101
			},
			wantErrSub: "regime.min_confidence must be between 0 and 100",
		},
		{
			name: "rejects inverted regime ADX thresholds",
			mutate: func(cfg *config.Config) {
				cfg.Regime.ADXTrendThreshold = 18
				cfg.Regime.ADXRangeThreshold = 25
			},
			wantErrSub: "regime.adx_trend_threshold must be greater",
		},
		{
			name: "rejects disabled research walk-forward gate",
			mutate: func(cfg *config.Config) {
				cfg.Research.RequireWalkForward = false
			},
			wantErrSub: "research.require_walk_forward must be true",
		},
		{
			name: "rejects invalid research drawdown threshold",
			mutate: func(cfg *config.Config) {
				cfg.Research.MaxDrawdownPct = 101
			},
			wantErrSub: "research.max_drawdown_pct",
		},
		{
			name: "rejects invalid paper initial balance",
			mutate: func(cfg *config.Config) {
				cfg.Paper.InitialBalance = 0
			},
			wantErrSub: "paper.initial_balance",
		},
		{
			name: "rejects disabled paper slippage simulation",
			mutate: func(cfg *config.Config) {
				cfg.Paper.SimulateSlippage = false
			},
			wantErrSub: "paper.simulate_slippage",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConfig()
			if tt.mutate != nil {
				tt.mutate(&cfg)
			}

			err := cfg.Validate()
			if tt.wantErrSub == "" {
				if err != nil {
					t.Fatalf("expected valid config, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("expected validation error")
			}
			if !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
		})
	}
}

func validConfig() config.Config {
	return config.Config{
		App: config.AppConfig{
			Name:     "crypto-quant-platform",
			LogLevel: "info",
		},
		Exchange: config.ExchangeConfig{
			Primary:     "bybit",
			RestBaseURL: "https://api-testnet.bybit.com",
			PublicWSURL: "wss://stream-testnet.bybit.com/v5/public/linear",
			Category:    "linear",
			Symbols:     []string{"BTCUSDT"},
		},
		Database: config.DatabaseConfig{
			DSN: "postgres://user:pass@localhost:5432/inquisitor?sslmode=disable",
		},
		MarketData: config.MarketDataConfig{
			CandleIntervals:    []string{"1"},
			BackfillDays:       1,
			OrderbookDepth:     50,
			MaxDataStalenessMs: 3000,
			ReconnectBackoffMs: 1000,
		},
		Slippage: config.SlippageConfig{
			ConservativeMultiplier: 1,
		},
		Trading: config.TradingConfig{
			Enabled:          false,
			Mode:             "paper",
			AllowLive:        false,
			MaxOpenPositions: 2,
			MaxLeverage:      1,
		},
		Live: config.LiveConfig{
			WithdrawalPermissionAllowed: false,
		},
		Risk: config.RiskConfig{
			RiskPerTradePct:                   0.25,
			MaxDailyLossPct:                   1,
			MaxWeeklyLossPct:                  3,
			MaxTotalDrawdownPct:               8,
			MaxLosingStreak:                   5,
			MinConfidence:                     70,
			MinLiquidityUSDT:                  100000,
			PortfolioMaxCryptoExposurePct:     30,
			PortfolioMaxCorrelatedExposurePct: 20,
		},
		Regime: config.RegimeConfig{
			MinConfidence:      70,
			ADXTrendThreshold:  25,
			ADXRangeThreshold:  18,
			ATRSpikeMultiplier: 2.5,
		},
		Research: config.ResearchConfig{
			MinTrades:             200,
			MinProfitFactor:       1.15,
			MinExpectancyR:        0.05,
			MaxDrawdownPct:        15,
			RequireOutOfSample:    true,
			RequireWalkForward:    true,
			RequireRegimeAnalysis: true,
		},
		Paper: config.PaperConfig{
			InitialBalance:   1000,
			MinimumDays:      30,
			SimulateFees:     true,
			SimulateSlippage: true,
			SimulateSpread:   true,
		},
	}
}
