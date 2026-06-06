package gaps_test

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/VersoIt/Inquisitor/internal/marketdata"
	"github.com/VersoIt/Inquisitor/internal/marketdata/gaps"
)

func TestDetectReturnsNoGapsForContinuousCandles(t *testing.T) {
	start := time.Date(2026, 6, 5, 0, 0, 0, 0, time.UTC)
	candles := []marketdata.Candle{
		candleAt(start),
		candleAt(start.Add(time.Minute)),
		candleAt(start.Add(2 * time.Minute)),
	}

	found, err := gaps.Detect(candles, "1")
	if err != nil {
		t.Fatalf("detect gaps: %v", err)
	}
	if len(found) != 0 {
		t.Fatalf("expected no gaps, got %#v", found)
	}
}

func TestDetectFindsMissingCandles(t *testing.T) {
	start := time.Date(2026, 6, 5, 0, 0, 0, 0, time.UTC)
	candles := []marketdata.Candle{
		candleAt(start),
		candleAt(start.Add(3 * time.Minute)),
	}

	found, err := gaps.Detect(candles, "1")
	if err != nil {
		t.Fatalf("detect gaps: %v", err)
	}
	if len(found) != 1 {
		t.Fatalf("expected one gap, got %#v", found)
	}
	if found[0].MissingCandles != 2 {
		t.Fatalf("expected two missing candles, got %d", found[0].MissingCandles)
	}
	if !found[0].ExpectedOpenTime.Equal(start.Add(time.Minute)) {
		t.Fatalf("unexpected expected open time: %s", found[0].ExpectedOpenTime)
	}
}

func TestDetectSortsCandlesBeforeChecking(t *testing.T) {
	start := time.Date(2026, 6, 5, 0, 0, 0, 0, time.UTC)
	candles := []marketdata.Candle{
		candleAt(start.Add(2 * time.Minute)),
		candleAt(start),
	}

	found, err := gaps.Detect(candles, "1")
	if err != nil {
		t.Fatalf("detect gaps: %v", err)
	}
	if len(found) != 1 {
		t.Fatalf("expected one gap, got %#v", found)
	}
	if found[0].MissingCandles != 1 {
		t.Fatalf("expected one missing candle, got %d", found[0].MissingCandles)
	}
}

func TestDetectRejectsInvalidInterval(t *testing.T) {
	start := time.Date(2026, 6, 5, 0, 0, 0, 0, time.UTC)
	candles := []marketdata.Candle{
		candleAt(start),
		candleAt(start.Add(time.Minute)),
	}

	if _, err := gaps.Detect(candles, "7"); err == nil {
		t.Fatal("expected invalid interval error")
	}
}

func TestDetectRejectsMixedSeriesTableDriven(t *testing.T) {
	start := time.Date(2026, 6, 5, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name   string
		mutate func(*marketdata.Candle)
	}{
		{
			name: "mixed exchange",
			mutate: func(candle *marketdata.Candle) {
				candle.Exchange = "binance"
			},
		},
		{
			name: "mixed category",
			mutate: func(candle *marketdata.Candle) {
				candle.Category = "spot"
			},
		},
		{
			name: "mixed symbol",
			mutate: func(candle *marketdata.Candle) {
				candle.Symbol = "ETHUSDT"
			},
		},
		{
			name: "mixed interval",
			mutate: func(candle *marketdata.Candle) {
				candle.Interval = "5"
				candle.CloseTime = candle.OpenTime.Add(5 * time.Minute)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			first := candleAt(start)
			second := candleAt(start.Add(time.Minute))
			tt.mutate(&second)

			if _, err := gaps.Detect([]marketdata.Candle{first, second}, "1"); err == nil {
				t.Fatal("expected mixed series error")
			}
		})
	}
}

func candleAt(openTime time.Time) marketdata.Candle {
	return marketdata.Candle{
		Exchange:  "bybit",
		Category:  "linear",
		Symbol:    "BTCUSDT",
		Interval:  "1",
		OpenTime:  openTime,
		CloseTime: openTime.Add(time.Minute),
		Open:      decimal.NewFromInt(100),
		High:      decimal.NewFromInt(110),
		Low:       decimal.NewFromInt(90),
		Close:     decimal.NewFromInt(105),
		Volume:    decimal.NewFromInt(10),
		Turnover:  decimal.NewFromInt(1000),
		IsClosed:  true,
	}
}
