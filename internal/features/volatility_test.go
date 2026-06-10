package features_test

import (
	"math"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/VersoIt/Inquisitor/internal/features"
	"github.com/VersoIt/Inquisitor/internal/marketdata"
)

func TestComputeVolatilityFeaturesTableDriven(t *testing.T) {
	now := time.Date(2026, 6, 11, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name       string
		candles    []marketdata.Candle
		cfg        features.VolatilityFeatureConfig
		assertions func(t *testing.T, got []features.VolatilityFeatures)
	}{
		{
			name: "computes atr rolling volatility bollinger and compression",
			candles: []marketdata.Candle{
				testVolatilityCandle(now, "100", "101", "99", "100"),
				testVolatilityCandle(now.Add(time.Minute), "110", "111", "109", "110"),
				testVolatilityCandle(now.Add(2*time.Minute), "120", "121", "119", "120"),
				testVolatilityCandle(now.Add(3*time.Minute), "130", "131", "129", "130"),
				testVolatilityCandle(now.Add(4*time.Minute), "120", "121", "119", "120"),
			},
			cfg: features.VolatilityFeatureConfig{
				ATRWindow:               2,
				RollingVolatilityWindow: 2,
				VolatilityZScoreWindow:  2,
				BollingerWindow:         2,
				BollingerStdDev:         2,
				CompressionWindow:       2,
			},
			assertions: func(t *testing.T, got []features.VolatilityFeatures) {
				t.Helper()
				if len(got) != 5 {
					t.Fatalf("feature count mismatch: got %d want 5", len(got))
				}
				if got[0].Complete {
					t.Fatalf("expected first volatility row to be incomplete: %#v", got[0])
				}
				final := got[4]
				if !final.Complete {
					t.Fatalf("expected final volatility row to be complete: %#v", final)
				}

				assertDecimal(t, final.ATR, decimal.RequireFromString("11"))
				assertDecimal(t, final.ATRPercentage, decimal.RequireFromString("11").Div(decimal.RequireFromString("120")))

				return2 := math.Log(120.0 / 130.0)
				return1 := math.Log(130.0 / 120.0)
				wantRollingVolatility := testPopulationStdDev([]float64{return1, return2})
				assertFloat(t, final.RollingVolatility, wantRollingVolatility)

				previousVolatility := testPopulationStdDev([]float64{math.Log(120.0 / 110.0), math.Log(130.0 / 120.0)})
				wantZScore := (wantRollingVolatility - testMean([]float64{previousVolatility, wantRollingVolatility})) /
					testPopulationStdDev([]float64{previousVolatility, wantRollingVolatility})
				assertFloat(t, final.VolatilityZScore, wantZScore)

				assertFloat(t, final.BollingerMiddle, 125)
				assertFloat(t, final.BollingerUpper, 135)
				assertFloat(t, final.BollingerLower, 115)
				assertFloat(t, final.BollingerWidth, 0.16)
				assertFloat(t, final.VolatilityCompression, 1)
			},
		},
		{
			name: "flat market produces zero volatility features after warmup",
			candles: []marketdata.Candle{
				testVolatilityCandle(now, "100", "100", "100", "100"),
				testVolatilityCandle(now.Add(time.Minute), "100", "100", "100", "100"),
				testVolatilityCandle(now.Add(2*time.Minute), "100", "100", "100", "100"),
				testVolatilityCandle(now.Add(3*time.Minute), "100", "100", "100", "100"),
			},
			cfg: features.VolatilityFeatureConfig{
				ATRWindow:               2,
				RollingVolatilityWindow: 2,
				VolatilityZScoreWindow:  2,
				BollingerWindow:         2,
				BollingerStdDev:         2,
				CompressionWindow:       2,
			},
			assertions: func(t *testing.T, got []features.VolatilityFeatures) {
				t.Helper()
				final := got[len(got)-1]
				if !final.Complete {
					t.Fatalf("expected final flat-market row to be complete: %#v", final)
				}
				assertDecimal(t, final.ATR, decimal.Zero)
				assertDecimal(t, final.ATRPercentage, decimal.Zero)
				assertFloat(t, final.RollingVolatility, 0)
				assertFloat(t, final.VolatilityZScore, 0)
				assertFloat(t, final.BollingerMiddle, 100)
				assertFloat(t, final.BollingerUpper, 100)
				assertFloat(t, final.BollingerLower, 100)
				assertFloat(t, final.BollingerWidth, 0)
				assertFloat(t, final.VolatilityCompression, 0)
			},
		},
		{
			name:    "empty input is no-op",
			candles: nil,
			assertions: func(t *testing.T, got []features.VolatilityFeatures) {
				t.Helper()
				if len(got) != 0 {
					t.Fatalf("expected empty result, got %#v", got)
				}
			},
		},
		{
			name: "default config is accepted and marks insufficient history",
			candles: []marketdata.Candle{
				testVolatilityCandle(now, "100", "101", "99", "100"),
			},
			assertions: func(t *testing.T, got []features.VolatilityFeatures) {
				t.Helper()
				if len(got) != 1 {
					t.Fatalf("feature count mismatch: got %d want 1", len(got))
				}
				if got[0].Complete {
					t.Fatalf("expected default-config row to be incomplete: %#v", got[0])
				}
				assertMissingReasons(t, got[0].MissingReasons, []string{
					"atr_window",
					"rolling_volatility_window",
					"volatility_z_score_window",
					"bollinger_window",
					"compression_window",
				})
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := features.ComputeVolatilityFeatures(tt.candles, tt.cfg)
			if err != nil {
				t.Fatalf("compute volatility features: %v", err)
			}
			tt.assertions(t, got)
		})
	}
}

func TestComputeVolatilityFeaturesRejectsInvalidConfigTableDriven(t *testing.T) {
	now := time.Date(2026, 6, 11, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name string
		cfg  features.VolatilityFeatureConfig
		code string
	}{
		{
			name: "rejects non positive atr window",
			cfg: features.VolatilityFeatureConfig{
				ATRWindow:               -1,
				RollingVolatilityWindow: 2,
				VolatilityZScoreWindow:  2,
				BollingerWindow:         2,
				BollingerStdDev:         2,
				CompressionWindow:       2,
			},
			code: "must_be_positive",
		},
		{
			name: "rejects rolling volatility window without variance",
			cfg: features.VolatilityFeatureConfig{
				ATRWindow:               2,
				RollingVolatilityWindow: 1,
				VolatilityZScoreWindow:  2,
				BollingerWindow:         2,
				BollingerStdDev:         2,
				CompressionWindow:       2,
			},
			code: "must_be_greater_than_one",
		},
		{
			name: "rejects non positive bollinger multiplier",
			cfg: features.VolatilityFeatureConfig{
				ATRWindow:               2,
				RollingVolatilityWindow: 2,
				VolatilityZScoreWindow:  2,
				BollingerWindow:         2,
				BollingerStdDev:         -1,
				CompressionWindow:       2,
			},
			code: "must_be_positive",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := features.ComputeVolatilityFeatures([]marketdata.Candle{
				testVolatilityCandle(now, "100", "101", "99", "100"),
			}, tt.cfg)
			assertValidationCode(t, err, tt.code)
		})
	}
}

func testVolatilityCandle(openTime time.Time, openValue, highValue, lowValue, closeValue string) marketdata.Candle {
	return marketdata.Candle{
		Exchange:  "bybit",
		Category:  "linear",
		Symbol:    "BTCUSDT",
		Interval:  "1",
		OpenTime:  openTime,
		CloseTime: openTime.Add(time.Minute),
		Open:      decimal.RequireFromString(openValue),
		High:      decimal.RequireFromString(highValue),
		Low:       decimal.RequireFromString(lowValue),
		Close:     decimal.RequireFromString(closeValue),
		Volume:    decimal.RequireFromString("10"),
		Turnover:  decimal.RequireFromString("1000"),
		IsClosed:  true,
	}
}

func testMean(values []float64) float64 {
	total := 0.0
	for _, value := range values {
		total += value
	}
	return total / float64(len(values))
}

func testPopulationStdDev(values []float64) float64 {
	mean := testMean(values)
	total := 0.0
	for _, value := range values {
		diff := value - mean
		total += diff * diff
	}
	return math.Sqrt(total / float64(len(values)))
}
