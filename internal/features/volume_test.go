package features_test

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/VersoIt/Inquisitor/internal/features"
	"github.com/VersoIt/Inquisitor/internal/marketdata"
)

func TestComputeVolumeFeaturesTableDriven(t *testing.T) {
	now := time.Date(2026, 6, 11, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name       string
		candles    []marketdata.Candle
		cfg        features.VolumeFeatureConfig
		assertions func(t *testing.T, got []features.VolumeFeatures)
	}{
		{
			name: "computes volume moving average zscore and one period changes",
			candles: []marketdata.Candle{
				testVolumeCandle(now, "10", "100"),
				testVolumeCandle(now.Add(time.Minute), "20", "120"),
				testVolumeCandle(now.Add(2*time.Minute), "30", "180"),
				testVolumeCandle(now.Add(3*time.Minute), "50", "270"),
			},
			cfg: features.VolumeFeatureConfig{
				MovingAverageWindow: 3,
				ZScoreWindow:        3,
			},
			assertions: func(t *testing.T, got []features.VolumeFeatures) {
				t.Helper()
				if len(got) != 4 {
					t.Fatalf("feature count mismatch: got %d want 4", len(got))
				}
				assertVolumeIncomplete(t, got[0], []string{"volume_ma_window", "volume_z_score_window", "previous_volume", "previous_turnover"})
				assertVolumeIncomplete(t, got[1], []string{"volume_ma_window", "volume_z_score_window"})

				final := got[3]
				if !final.Complete {
					t.Fatalf("expected final volume row to be complete: %#v", final)
				}
				assertDecimal(t, final.VolumeMovingAverage, decimal.RequireFromString("100").Div(decimal.RequireFromString("3")))
				assertFloat(t, final.VolumeZScore, (50-testMean([]float64{20, 30, 50}))/testPopulationStdDev([]float64{20, 30, 50}))
				assertDecimal(t, final.VolumeChange, decimal.RequireFromString("20").Div(decimal.RequireFromString("30")))
				assertDecimal(t, final.TurnoverChange, decimal.RequireFromString("90").Div(decimal.RequireFromString("180")))
			},
		},
		{
			name: "flat volume has zero zscore but complete row",
			candles: []marketdata.Candle{
				testVolumeCandle(now, "10", "100"),
				testVolumeCandle(now.Add(time.Minute), "10", "100"),
			},
			cfg: features.VolumeFeatureConfig{
				MovingAverageWindow: 2,
				ZScoreWindow:        2,
			},
			assertions: func(t *testing.T, got []features.VolumeFeatures) {
				t.Helper()
				final := got[1]
				if !final.Complete {
					t.Fatalf("expected flat volume row to be complete: %#v", final)
				}
				assertDecimal(t, final.VolumeMovingAverage, decimal.RequireFromString("10"))
				assertFloat(t, final.VolumeZScore, 0)
				assertDecimal(t, final.VolumeChange, decimal.Zero)
				assertDecimal(t, final.TurnoverChange, decimal.Zero)
			},
		},
		{
			name: "zero previous volume and turnover are explicit missing reasons",
			candles: []marketdata.Candle{
				testVolumeCandle(now, "0", "0"),
				testVolumeCandle(now.Add(time.Minute), "10", "100"),
			},
			cfg: features.VolumeFeatureConfig{
				MovingAverageWindow: 1,
				ZScoreWindow:        2,
			},
			assertions: func(t *testing.T, got []features.VolumeFeatures) {
				t.Helper()
				if got[1].Complete {
					t.Fatalf("expected row after zero previous values to be incomplete: %#v", got[1])
				}
				assertMissingReasons(t, got[1].MissingReasons, []string{"previous_volume", "previous_turnover"})
				assertDecimal(t, got[1].VolumeMovingAverage, decimal.RequireFromString("10"))
				assertFloat(t, got[1].VolumeZScore, 1)
			},
		},
		{
			name:    "empty input is no-op",
			candles: nil,
			assertions: func(t *testing.T, got []features.VolumeFeatures) {
				t.Helper()
				if len(got) != 0 {
					t.Fatalf("expected empty result, got %#v", got)
				}
			},
		},
		{
			name: "default config is accepted and marks insufficient history",
			candles: []marketdata.Candle{
				testVolumeCandle(now, "10", "100"),
			},
			assertions: func(t *testing.T, got []features.VolumeFeatures) {
				t.Helper()
				if len(got) != 1 {
					t.Fatalf("feature count mismatch: got %d want 1", len(got))
				}
				assertVolumeIncomplete(t, got[0], []string{"volume_ma_window", "volume_z_score_window", "previous_volume", "previous_turnover"})
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := features.ComputeVolumeFeatures(tt.candles, tt.cfg)
			if err != nil {
				t.Fatalf("compute volume features: %v", err)
			}
			tt.assertions(t, got)
		})
	}
}

func TestComputeVolumeFeaturesRejectsInvalidConfigTableDriven(t *testing.T) {
	now := time.Date(2026, 6, 11, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name string
		cfg  features.VolumeFeatureConfig
		code string
	}{
		{
			name: "rejects non positive moving average window",
			cfg: features.VolumeFeatureConfig{
				MovingAverageWindow: -1,
				ZScoreWindow:        2,
			},
			code: "must_be_positive",
		},
		{
			name: "rejects zscore window without variance",
			cfg: features.VolumeFeatureConfig{
				MovingAverageWindow: 2,
				ZScoreWindow:        1,
			},
			code: "must_be_greater_than_one",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := features.ComputeVolumeFeatures([]marketdata.Candle{
				testVolumeCandle(now, "10", "100"),
			}, tt.cfg)
			assertValidationCode(t, err, tt.code)
		})
	}
}

func testVolumeCandle(openTime time.Time, volumeValue, turnoverValue string) marketdata.Candle {
	return marketdata.Candle{
		Exchange:  "bybit",
		Category:  "linear",
		Symbol:    "BTCUSDT",
		Interval:  "1",
		OpenTime:  openTime,
		CloseTime: openTime.Add(time.Minute),
		Open:      decimal.RequireFromString("100"),
		High:      decimal.RequireFromString("110"),
		Low:       decimal.RequireFromString("90"),
		Close:     decimal.RequireFromString("105"),
		Volume:    decimal.RequireFromString(volumeValue),
		Turnover:  decimal.RequireFromString(turnoverValue),
		IsClosed:  true,
	}
}

func assertVolumeIncomplete(t *testing.T, got features.VolumeFeatures, wantReasons []string) {
	t.Helper()
	if got.Complete {
		t.Fatalf("expected incomplete volume row: %#v", got)
	}
	assertMissingReasons(t, got.MissingReasons, wantReasons)
}
