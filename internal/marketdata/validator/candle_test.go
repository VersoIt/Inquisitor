package validator_test

import (
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/VersoIt/Inquisitor/internal/marketdata"
	"github.com/VersoIt/Inquisitor/internal/marketdata/validator"
)

func TestValidateCandleAcceptsValidCandle(t *testing.T) {
	candle := validCandle(time.Date(2026, 6, 5, 0, 0, 0, 0, time.UTC))

	if err := validator.ValidateCandle(candle); err != nil {
		t.Fatalf("expected valid candle, got %v", err)
	}
}

func TestValidateCandleRejectsInvalidPrices(t *testing.T) {
	candle := validCandle(time.Date(2026, 6, 5, 0, 0, 0, 0, time.UTC))
	candle.Open = decimal.Zero
	candle.High = decimal.NewFromInt(99)

	err := validator.ValidateCandle(candle)
	if err == nil {
		t.Fatal("expected validation error")
	}

	var validationErr validator.CandleValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("expected CandleValidationError, got %T", err)
	}
	if len(validationErr.Problems) < 2 {
		t.Fatalf("expected multiple problems, got %#v", validationErr.Problems)
	}
}

func TestValidateCandleRejectsIntervalMismatch(t *testing.T) {
	candle := validCandle(time.Date(2026, 6, 5, 0, 0, 0, 0, time.UTC))
	candle.CloseTime = candle.OpenTime.Add(2 * time.Minute)

	err := validator.ValidateCandle(candle)
	if err == nil {
		t.Fatal("expected validation error")
	}

	var validationErr validator.CandleValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("expected CandleValidationError, got %T", err)
	}
	assertProblemCode(t, validationErr.Problems, "interval_mismatch")
}

func TestValidateCandleRejectsUnsupportedInterval(t *testing.T) {
	candle := validCandle(time.Date(2026, 6, 5, 0, 0, 0, 0, time.UTC))
	candle.Interval = "7"

	err := validator.ValidateCandle(candle)
	if err == nil {
		t.Fatal("expected validation error")
	}

	var validationErr validator.CandleValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("expected CandleValidationError, got %T", err)
	}
	assertProblemCode(t, validationErr.Problems, "unsupported")
}

func TestValidateCandlesRejectsDuplicateOpenTime(t *testing.T) {
	openTime := time.Date(2026, 6, 5, 0, 0, 0, 0, time.UTC)
	candles := []marketdata.Candle{
		validCandle(openTime),
		validCandle(openTime),
	}

	err := validator.ValidateCandles(candles)
	if err == nil {
		t.Fatal("expected validation error")
	}

	var validationErr validator.CandleValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("expected CandleValidationError, got %T", err)
	}
	assertProblemCode(t, validationErr.Problems, "duplicate")
}

func validCandle(openTime time.Time) marketdata.Candle {
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

func assertProblemCode(t *testing.T, problems []validator.Problem, code string) {
	t.Helper()
	for _, problem := range problems {
		if problem.Code == code {
			return
		}
	}
	t.Fatalf("expected problem code %q in %#v", code, problems)
}
