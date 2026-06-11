package regime_test

import (
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	domainfeatures "github.com/VersoIt/Inquisitor/internal/features"
	"github.com/VersoIt/Inquisitor/internal/regime"
)

func TestDetectorDetectTableDriven(t *testing.T) {
	now := time.Date(2026, 6, 11, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name          string
		mutate        func(*regime.Input)
		cfg           regime.Config
		wantRegime    regime.Regime
		wantCandidate regime.Regime
		wantNoTrade   bool
		wantReasons   []string
	}{
		{
			name:          "classifies aligned strong uptrend",
			wantRegime:    regime.RegimeTrendUp,
			wantCandidate: regime.RegimeTrendUp,
		},
		{
			name: "classifies aligned strong downtrend",
			mutate: func(input *regime.Input) {
				input.Trend.MA20 = decimal.RequireFromString("95")
				input.Trend.MA50 = decimal.RequireFromString("100")
				input.Trend.MA200 = decimal.RequireFromString("105")
				input.Trend.MA50Slope = decimal.RequireFromString("-0.03")
				input.Trend.MA200Slope = decimal.RequireFromString("-0.01")
				input.Microstructure.TradeAggressorImbalance = decimal.RequireFromString("-0.4")
			},
			wantRegime:    regime.RegimeTrendDown,
			wantCandidate: regime.RegimeTrendDown,
		},
		{
			name: "classifies low ADX as range",
			mutate: func(input *regime.Input) {
				input.Trend.ADX = decimal.RequireFromString("12")
				input.Volatility.VolatilityCompression = 0.8
			},
			wantRegime:    regime.RegimeRange,
			wantCandidate: regime.RegimeRange,
		},
		{
			name: "low confidence falls back to no trade",
			cfg:  regime.Config{MinConfidence: 90, ADXTrendThreshold: 25, ADXRangeThreshold: 18, ATRSpikeMultiplier: 2.5},
			mutate: func(input *regime.Input) {
				input.Trend.ADX = decimal.RequireFromString("25")
				input.Microstructure.TradeAggressorImbalance = decimal.Zero
				input.Microstructure.OrderbookImbalance = decimal.Zero
			},
			wantRegime:    regime.RegimeNoTrade,
			wantCandidate: regime.RegimeTrendUp,
			wantNoTrade:   true,
			wantReasons:   []string{"low_confidence"},
		},
		{
			name: "bad data quality falls back to no trade",
			mutate: func(input *regime.Input) {
				input.DataQuality.Complete = false
				input.DataQuality.MissingReasons = []string{"stale_data", "orderbook_invalid"}
			},
			wantRegime:    regime.RegimeNoTrade,
			wantCandidate: regime.RegimeTrendUp,
			wantNoTrade:   true,
			wantReasons:   []string{"data_quality:stale_data", "data_quality:orderbook_invalid"},
		},
		{
			name: "incomplete feature falls back to no trade",
			mutate: func(input *regime.Input) {
				input.Trend.Complete = false
				input.Trend.MissingReasons = []string{"ma200_window"}
			},
			wantRegime:    regime.RegimeNoTrade,
			wantCandidate: regime.RegimeTrendUp,
			wantNoTrade:   true,
			wantReasons:   []string{"feature_incomplete:trend:ma200_window"},
		},
		{
			name: "volatility spike falls back to no trade",
			mutate: func(input *regime.Input) {
				input.Volatility.VolatilityZScore = 3
			},
			wantRegime:    regime.RegimeNoTrade,
			wantCandidate: regime.RegimeHighVol,
			wantNoTrade:   true,
			wantReasons:   []string{"volatility_spike"},
		},
		{
			name: "missing required feature falls back to no trade",
			mutate: func(input *regime.Input) {
				input.Microstructure = nil
			},
			wantRegime:    regime.RegimeNoTrade,
			wantCandidate: regime.RegimeNoTrade,
			wantNoTrade:   true,
			wantReasons:   []string{"feature_missing:microstructure"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := tt.cfg
			if cfg == (regime.Config{}) {
				cfg = regime.DefaultConfig()
			}
			detector, err := regime.NewDetector(cfg)
			if err != nil {
				t.Fatalf("new detector: %v", err)
			}
			input := completeInput(now)
			if tt.mutate != nil {
				tt.mutate(&input)
			}

			got, err := detector.Detect(input)
			if err != nil {
				t.Fatalf("detect regime: %v", err)
			}

			if got.Regime != tt.wantRegime {
				t.Fatalf("regime mismatch: got %s want %s", got.Regime, tt.wantRegime)
			}
			if got.CandidateRegime != tt.wantCandidate {
				t.Fatalf("candidate mismatch: got %s want %s", got.CandidateRegime, tt.wantCandidate)
			}
			if got.NoTrade != tt.wantNoTrade {
				t.Fatalf("no-trade mismatch: got %v want %v", got.NoTrade, tt.wantNoTrade)
			}
			if got.Confidence < 0 || got.Confidence > 100 {
				t.Fatalf("confidence must be capped to [0,100], got %d", got.Confidence)
			}
			for _, reason := range tt.wantReasons {
				assertReason(t, got.Reasons, reason)
			}
		})
	}
}

func TestNewDetectorRejectsInvalidConfigTableDriven(t *testing.T) {
	tests := []struct {
		name       string
		cfg        regime.Config
		wantErrSub string
	}{
		{
			name:       "rejects confidence above percent range",
			cfg:        regime.Config{MinConfidence: 101},
			wantErrSub: "min_confidence",
		},
		{
			name: "rejects inverted ADX thresholds",
			cfg: regime.Config{
				MinConfidence:      70,
				ADXTrendThreshold:  18,
				ADXRangeThreshold:  25,
				ATRSpikeMultiplier: 2.5,
			},
			wantErrSub: "adx_trend_threshold",
		},
		{
			name: "rejects negative volatility spike multiplier",
			cfg: regime.Config{
				MinConfidence:      70,
				ADXTrendThreshold:  25,
				ADXRangeThreshold:  18,
				ATRSpikeMultiplier: -1,
			},
			wantErrSub: "atr_spike_multiplier",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := regime.NewDetector(tt.cfg)
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
		})
	}
}

func completeInput(now time.Time) regime.Input {
	return regime.Input{
		Price: &domainfeatures.PriceFeatures{
			Exchange:  "bybit",
			Category:  "linear",
			Symbol:    "BTCUSDT",
			Interval:  "1",
			OpenTime:  now.Add(-time.Minute),
			CloseTime: now,
			Complete:  true,
		},
		Trend: &domainfeatures.TrendFeatures{
			Exchange:   "bybit",
			Category:   "linear",
			Symbol:     "BTCUSDT",
			Interval:   "1",
			OpenTime:   now.Add(-time.Minute),
			CloseTime:  now,
			MA20:       decimal.RequireFromString("110"),
			MA50:       decimal.RequireFromString("105"),
			MA200:      decimal.RequireFromString("100"),
			MA50Slope:  decimal.RequireFromString("0.03"),
			MA200Slope: decimal.RequireFromString("0.01"),
			ADX:        decimal.RequireFromString("32"),
			Complete:   true,
		},
		Volatility: &domainfeatures.VolatilityFeatures{
			Exchange:              "bybit",
			Category:              "linear",
			Symbol:                "BTCUSDT",
			Interval:              "1",
			OpenTime:              now.Add(-time.Minute),
			CloseTime:             now,
			VolatilityZScore:      0.4,
			VolatilityCompression: 1,
			Complete:              true,
		},
		Volume: &domainfeatures.VolumeFeatures{
			Exchange:  "bybit",
			Category:  "linear",
			Symbol:    "BTCUSDT",
			Interval:  "1",
			OpenTime:  now.Add(-time.Minute),
			CloseTime: now,
			Complete:  true,
		},
		Microstructure: &domainfeatures.MicrostructureFeatures{
			Exchange:                "bybit",
			Category:                "linear",
			Symbol:                  "BTCUSDT",
			ExchangeTime:            now,
			OrderbookImbalance:      decimal.RequireFromString("0.1"),
			TradeAggressorImbalance: decimal.RequireFromString("0.2"),
			Complete:                true,
		},
		DataQuality: &domainfeatures.DataQualityFeatures{
			Exchange:           "bybit",
			Category:           "linear",
			Symbol:             "BTCUSDT",
			Interval:           "1",
			ObservedAt:         now,
			LatestDataTime:     now,
			WebSocketConnected: true,
			OrderbookValid:     true,
			Complete:           true,
		},
		CalculatedAt: now.Add(100 * time.Millisecond),
	}
}

func assertReason(t *testing.T, got []string, want string) {
	t.Helper()
	for _, reason := range got {
		if reason == want {
			return
		}
	}
	t.Fatalf("missing reason %q in %#v", want, got)
}
