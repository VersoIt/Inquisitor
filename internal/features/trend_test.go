package features_test

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/VersoIt/Inquisitor/internal/features"
	"github.com/VersoIt/Inquisitor/internal/marketdata"
)

func TestComputeTrendFeaturesTableDriven(t *testing.T) {
	now := time.Date(2026, 6, 11, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name       string
		candles    []marketdata.Candle
		cfg        features.TrendFeatureConfig
		assertions func(t *testing.T, got []features.TrendFeatures)
	}{
		{
			name: "computes moving averages slopes and structure counts",
			candles: []marketdata.Candle{
				testCandle(now, "10", "12", "8", "10"),
				testCandle(now.Add(time.Minute), "12", "14", "10", "12"),
				testCandle(now.Add(2*time.Minute), "14", "16", "12", "14"),
				testCandle(now.Add(3*time.Minute), "16", "18", "14", "16"),
				testCandle(now.Add(4*time.Minute), "18", "20", "16", "18"),
			},
			cfg: features.TrendFeatureConfig{
				MA20Window:      2,
				MA50Window:      3,
				MA200Window:     4,
				EMA20Window:     2,
				EMA50Window:     3,
				StructureWindow: 3,
			},
			assertions: func(t *testing.T, got []features.TrendFeatures) {
				t.Helper()
				if len(got) != 5 {
					t.Fatalf("feature count mismatch: got %d want 5", len(got))
				}
				if got[0].Complete {
					t.Fatalf("expected first row to be incomplete: %#v", got[0])
				}
				final := got[4]
				if !final.Complete {
					t.Fatalf("expected final row to be complete: %#v", final)
				}
				assertDecimal(t, final.MA20, decimal.RequireFromString("17"))
				assertDecimal(t, final.MA50, decimal.RequireFromString("16"))
				assertDecimal(t, final.MA200, decimal.RequireFromString("15"))
				assertDecimalApprox(t, final.EMA20, decimal.RequireFromString("17"), decimal.RequireFromString("0.000000000001"))
				assertDecimalApprox(t, final.EMA50, decimal.RequireFromString("16"), decimal.RequireFromString("0.000000000001"))
				assertDecimal(t, final.MA50Slope, decimal.RequireFromString("2").Div(decimal.RequireFromString("14")))
				assertDecimal(t, final.MA200Slope, decimal.RequireFromString("2").Div(decimal.RequireFromString("13")))
				if final.HigherHighCount != 2 || final.HigherLowCount != 2 || final.LowerHighCount != 0 || final.LowerLowCount != 0 {
					t.Fatalf("unexpected structure counts: %#v", final)
				}
			},
		},
		{
			name: "default config is accepted and marks insufficient history",
			candles: []marketdata.Candle{
				testCandle(now, "100", "110", "90", "105"),
			},
			assertions: func(t *testing.T, got []features.TrendFeatures) {
				t.Helper()
				if len(got) != 1 {
					t.Fatalf("feature count mismatch: got %d want 1", len(got))
				}
				if got[0].Complete {
					t.Fatalf("expected default-config row to be incomplete with one candle: %#v", got[0])
				}
				assertMissingReasons(t, got[0].MissingReasons, []string{
					"ma20_window",
					"ma50_window",
					"ma200_window",
					"ema20_window",
					"ema50_window",
					"ma50_slope_window",
					"ma200_slope_window",
					"structure_window",
				})
			},
		},
		{
			name:    "empty input is no-op",
			candles: nil,
			assertions: func(t *testing.T, got []features.TrendFeatures) {
				t.Helper()
				if len(got) != 0 {
					t.Fatalf("expected empty result, got %#v", got)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := features.ComputeTrendFeatures(tt.candles, tt.cfg)
			if err != nil {
				t.Fatalf("compute trend features: %v", err)
			}
			tt.assertions(t, got)
		})
	}
}

func TestComputeTrendFeaturesRejectsInvalidConfigTableDriven(t *testing.T) {
	now := time.Date(2026, 6, 11, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name string
		cfg  features.TrendFeatureConfig
		code string
	}{
		{
			name: "rejects negative moving average window",
			cfg: features.TrendFeatureConfig{
				MA20Window:      -1,
				MA50Window:      3,
				MA200Window:     4,
				EMA20Window:     2,
				EMA50Window:     3,
				StructureWindow: 3,
			},
			code: "must_be_positive",
		},
		{
			name: "rejects structure window without comparisons",
			cfg: features.TrendFeatureConfig{
				MA20Window:      2,
				MA50Window:      3,
				MA200Window:     4,
				EMA20Window:     2,
				EMA50Window:     3,
				StructureWindow: 1,
			},
			code: "must_be_greater_than_one",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := features.ComputeTrendFeatures([]marketdata.Candle{
				testCandle(now, "100", "110", "90", "105"),
			}, tt.cfg)
			assertValidationCode(t, err, tt.code)
		})
	}
}

func assertMissingReasons(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("missing reasons count mismatch: got %#v want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("missing reason[%d] mismatch: got %q want %q", i, got[i], want[i])
		}
	}
}

func assertDecimalApprox(t *testing.T, got, want, tolerance decimal.Decimal) {
	t.Helper()
	diff := got.Sub(want)
	if diff.IsNegative() {
		diff = diff.Neg()
	}
	if diff.GreaterThan(tolerance) {
		t.Fatalf("decimal mismatch: got %s want %s tolerance %s", got, want, tolerance)
	}
}
