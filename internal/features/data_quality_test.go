package features_test

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/VersoIt/Inquisitor/internal/features"
	"github.com/VersoIt/Inquisitor/internal/marketdata"
)

func TestComputeDataQualityFeaturesTableDriven(t *testing.T) {
	now := time.Date(2026, 6, 11, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name       string
		input      features.DataQualityFeatureInput
		cfg        features.DataQualityFeatureConfig
		assertions func(t *testing.T, got features.DataQualityFeatures)
	}{
		{
			name: "complete data quality row",
			input: features.DataQualityFeatureInput{
				Candles: []marketdata.Candle{
					testCandle(now, "100", "110", "90", "105"),
					testCandle(now.Add(time.Minute), "105", "112", "104", "110"),
					testCandle(now.Add(2*time.Minute), "110", "125", "108", "120"),
				},
				ObservedAt:         now.Add(3*time.Minute + 500*time.Millisecond),
				WebSocketConnected: true,
				OrderbookValid:     true,
				FeatureSets: []features.FeatureSetCompleteness{
					{Name: "price", Complete: true},
					{Name: "trend", Complete: true},
					{Name: "microstructure", Complete: true},
				},
			},
			cfg: features.DataQualityFeatureConfig{MaxStaleness: time.Second},
			assertions: func(t *testing.T, got features.DataQualityFeatures) {
				t.Helper()
				if !got.Complete {
					t.Fatalf("expected complete data-quality features: %#v", got)
				}
				if got.DataFreshnessMS != 500 {
					t.Fatalf("freshness mismatch: got %d want 500", got.DataFreshnessMS)
				}
				if got.MissingCandleCount != 0 {
					t.Fatalf("missing candle count mismatch: got %d want 0", got.MissingCandleCount)
				}
				assertDecimal(t, got.FeatureCompletenessScore, decimal.RequireFromString("1"))
				assertMissingReasons(t, got.MissingReasons, nil)
			},
		},
		{
			name: "degraded data quality row aggregates reasons",
			input: features.DataQualityFeatureInput{
				Candles: []marketdata.Candle{
					testCandle(now, "100", "110", "90", "105"),
					testCandle(now.Add(3*time.Minute), "110", "125", "108", "120"),
				},
				ObservedAt:         now.Add(4*time.Minute + 2*time.Second),
				WebSocketConnected: false,
				OrderbookValid:     false,
				FeatureSets: []features.FeatureSetCompleteness{
					{Name: "price", Complete: true},
					{Name: "trend", Complete: false, MissingReasons: []string{"ma200_window"}},
					{Name: "microstructure", Complete: false},
				},
			},
			cfg: features.DataQualityFeatureConfig{MaxStaleness: time.Second},
			assertions: func(t *testing.T, got features.DataQualityFeatures) {
				t.Helper()
				if got.Complete {
					t.Fatalf("expected degraded data-quality features: %#v", got)
				}
				if got.DataFreshnessMS != 2000 {
					t.Fatalf("freshness mismatch: got %d want 2000", got.DataFreshnessMS)
				}
				if got.MissingCandleCount != 2 {
					t.Fatalf("missing candle count mismatch: got %d want 2", got.MissingCandleCount)
				}
				assertDecimal(t, got.FeatureCompletenessScore, decimal.RequireFromString("1").Div(decimal.RequireFromString("3")))
				assertMissingReasons(t, got.MissingReasons, []string{
					"stale_data",
					"missing_candles",
					"websocket_disconnected",
					"orderbook_invalid",
					"feature_set:trend:ma200_window",
					"feature_set:microstructure:incomplete",
				})
			},
		},
		{
			name: "empty inputs are explicit missing reasons",
			input: features.DataQualityFeatureInput{
				ObservedAt: now,
			},
			assertions: func(t *testing.T, got features.DataQualityFeatures) {
				t.Helper()
				if got.Complete {
					t.Fatalf("expected incomplete empty data-quality row: %#v", got)
				}
				assertDecimal(t, got.FeatureCompletenessScore, decimal.Zero)
				assertMissingReasons(t, got.MissingReasons, []string{
					"candles",
					"websocket_disconnected",
					"orderbook_invalid",
					"feature_sets",
				})
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := features.ComputeDataQualityFeatures(tt.input, tt.cfg)
			if err != nil {
				t.Fatalf("compute data-quality features: %v", err)
			}
			tt.assertions(t, got)
		})
	}
}

func TestComputeDataQualityFeaturesRejectsInvalidInputTableDriven(t *testing.T) {
	now := time.Date(2026, 6, 11, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name  string
		input features.DataQualityFeatureInput
		cfg   features.DataQualityFeatureConfig
		code  string
	}{
		{
			name: "rejects missing observed time",
			input: features.DataQualityFeatureInput{
				Candles: []marketdata.Candle{testCandle(now, "100", "110", "90", "105")},
			},
			code: "required",
		},
		{
			name: "rejects non positive max staleness",
			input: features.DataQualityFeatureInput{
				Candles:    []marketdata.Candle{testCandle(now, "100", "110", "90", "105")},
				ObservedAt: now,
			},
			cfg:  features.DataQualityFeatureConfig{MaxStaleness: -time.Second},
			code: "must_be_positive",
		},
		{
			name: "rejects unnamed feature set",
			input: features.DataQualityFeatureInput{
				Candles:    []marketdata.Candle{testCandle(now, "100", "110", "90", "105")},
				ObservedAt: now,
				FeatureSets: []features.FeatureSetCompleteness{
					{Complete: true},
				},
			},
			code: "required",
		},
		{
			name: "rejects mixed candle identity",
			input: features.DataQualityFeatureInput{
				Candles: []marketdata.Candle{
					testCandle(now, "100", "110", "90", "105"),
					func() marketdata.Candle {
						candle := testCandle(now.Add(time.Minute), "105", "112", "104", "110")
						candle.Symbol = "ETHUSDT"
						return candle
					}(),
				},
				ObservedAt: now,
			},
			code: "identity_mismatch",
		},
		{
			name: "rejects unsorted candles",
			input: features.DataQualityFeatureInput{
				Candles: []marketdata.Candle{
					testCandle(now.Add(time.Minute), "105", "112", "104", "110"),
					testCandle(now, "100", "110", "90", "105"),
				},
				ObservedAt: now,
			},
			code: "not_sorted",
		},
		{
			name: "rejects interval misalignment",
			input: features.DataQualityFeatureInput{
				Candles: []marketdata.Candle{
					testCandle(now, "100", "110", "90", "105"),
					testCandle(now.Add(90*time.Second), "105", "112", "104", "110"),
				},
				ObservedAt: now,
			},
			code: "interval_alignment",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := features.ComputeDataQualityFeatures(tt.input, tt.cfg)
			assertValidationCode(t, err, tt.code)
		})
	}
}
