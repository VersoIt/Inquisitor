package features_test

import (
	"errors"
	"math"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/VersoIt/Inquisitor/internal/features"
	"github.com/VersoIt/Inquisitor/internal/marketdata"
)

func TestComputePriceFeaturesTableDriven(t *testing.T) {
	now := time.Date(2026, 6, 10, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name       string
		candles    []marketdata.Candle
		window     int
		assertions func(t *testing.T, got []features.PriceFeatures)
	}{
		{
			name: "computes price features and rolling window",
			candles: []marketdata.Candle{
				testCandle(now, "100", "110", "90", "105"),
				testCandle(now.Add(time.Minute), "105", "112", "104", "110"),
				testCandle(now.Add(2*time.Minute), "110", "125", "108", "120"),
			},
			window: 3,
			assertions: func(t *testing.T, got []features.PriceFeatures) {
				t.Helper()
				if len(got) != 3 {
					t.Fatalf("feature count mismatch: got %d want 3", len(got))
				}
				assertIncomplete(t, got[0], "rolling_window")
				assertIncomplete(t, got[1], "rolling_window")
				if !got[2].Complete {
					t.Fatalf("expected final feature row to be complete: %#v", got[2])
				}
				assertDecimal(t, got[2].Return, decimal.RequireFromString("10").Div(decimal.RequireFromString("110")))
				assertFloat(t, got[2].LogReturn, math.Log(120.0/110.0))
				assertDecimal(t, got[2].RollingReturn, decimal.RequireFromString("15").Div(decimal.RequireFromString("105")))
				assertDecimal(t, got[2].RollingHigh, decimal.RequireFromString("125"))
				assertDecimal(t, got[2].RollingLow, decimal.RequireFromString("90"))
				assertDecimal(t, got[2].CandleBodyPct, decimal.RequireFromString("10").Div(decimal.RequireFromString("17")))
				assertDecimal(t, got[2].UpperWickPct, decimal.RequireFromString("5").Div(decimal.RequireFromString("17")))
				assertDecimal(t, got[2].LowerWickPct, decimal.RequireFromString("2").Div(decimal.RequireFromString("17")))
				assertDecimal(t, got[2].ClosePositionInRange, decimal.RequireFromString("12").Div(decimal.RequireFromString("17")))
			},
		},
		{
			name: "flat candle uses neutral range position",
			candles: []marketdata.Candle{
				testCandle(now, "100", "100", "100", "100"),
			},
			window: 1,
			assertions: func(t *testing.T, got []features.PriceFeatures) {
				t.Helper()
				if len(got) != 1 {
					t.Fatalf("feature count mismatch: got %d want 1", len(got))
				}
				if !got[0].Complete {
					t.Fatalf("expected flat candle feature to be complete: %#v", got[0])
				}
				assertDecimal(t, got[0].Return, decimal.Zero)
				assertFloat(t, got[0].LogReturn, 0)
				assertDecimal(t, got[0].RollingReturn, decimal.Zero)
				assertDecimal(t, got[0].RollingHigh, decimal.RequireFromString("100"))
				assertDecimal(t, got[0].RollingLow, decimal.RequireFromString("100"))
				assertDecimal(t, got[0].CandleBodyPct, decimal.Zero)
				assertDecimal(t, got[0].UpperWickPct, decimal.Zero)
				assertDecimal(t, got[0].LowerWickPct, decimal.Zero)
				assertDecimal(t, got[0].ClosePositionInRange, decimal.RequireFromString("0.5"))
			},
		},
		{
			name:    "empty input is no-op",
			window:  3,
			candles: nil,
			assertions: func(t *testing.T, got []features.PriceFeatures) {
				t.Helper()
				if len(got) != 0 {
					t.Fatalf("expected empty result, got %#v", got)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := features.ComputePriceFeatures(tt.candles, features.PriceFeatureConfig{RollingWindow: tt.window})
			if err != nil {
				t.Fatalf("compute price features: %v", err)
			}
			tt.assertions(t, got)
		})
	}
}

func TestComputePriceFeaturesRejectsInvalidInputTableDriven(t *testing.T) {
	now := time.Date(2026, 6, 10, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name    string
		candles []marketdata.Candle
		window  int
		code    string
	}{
		{
			name:    "rejects non positive rolling window",
			candles: []marketdata.Candle{testCandle(now, "100", "110", "90", "105")},
			code:    "must_be_positive",
		},
		{
			name: "rejects open candle",
			candles: []marketdata.Candle{func() marketdata.Candle {
				candle := testCandle(now, "100", "110", "90", "105")
				candle.IsClosed = false
				return candle
			}()},
			window: 1,
			code:   "required",
		},
		{
			name: "rejects non contiguous candles",
			candles: []marketdata.Candle{
				testCandle(now, "100", "110", "90", "105"),
				testCandle(now.Add(2*time.Minute), "105", "112", "104", "110"),
			},
			window: 2,
			code:   "gap",
		},
		{
			name: "rejects mixed symbols",
			candles: []marketdata.Candle{
				testCandle(now, "100", "110", "90", "105"),
				func() marketdata.Candle {
					candle := testCandle(now.Add(time.Minute), "105", "112", "104", "110")
					candle.Symbol = "ETHUSDT"
					return candle
				}(),
			},
			window: 2,
			code:   "identity_mismatch",
		},
		{
			name: "rejects unsorted candles",
			candles: []marketdata.Candle{
				testCandle(now.Add(time.Minute), "105", "112", "104", "110"),
				testCandle(now, "100", "110", "90", "105"),
			},
			window: 2,
			code:   "not_sorted",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := features.ComputePriceFeatures(tt.candles, features.PriceFeatureConfig{RollingWindow: tt.window})
			assertValidationCode(t, err, tt.code)
		})
	}
}

func testCandle(openTime time.Time, openValue, highValue, lowValue, closeValue string) marketdata.Candle {
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

func assertIncomplete(t *testing.T, got features.PriceFeatures, reason string) {
	t.Helper()
	if got.Complete {
		t.Fatalf("expected incomplete feature row: %#v", got)
	}
	if len(got.MissingReasons) != 1 || got.MissingReasons[0] != reason {
		t.Fatalf("missing reasons mismatch: got %#v want [%s]", got.MissingReasons, reason)
	}
}

func assertDecimal(t *testing.T, got, want decimal.Decimal) {
	t.Helper()
	if !got.Equal(want) {
		t.Fatalf("decimal mismatch: got %s want %s", got, want)
	}
}

func assertFloat(t *testing.T, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-12 {
		t.Fatalf("float mismatch: got %.16f want %.16f", got, want)
	}
}

func assertValidationCode(t *testing.T, err error, code string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected validation error with code %q", code)
	}
	var validationErr features.ValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("expected features.ValidationError, got %T: %v", err, err)
	}
	for _, problem := range validationErr.Problems {
		if problem.Code == code {
			return
		}
	}
	t.Fatalf("expected validation code %q in %#v", code, validationErr.Problems)
}
