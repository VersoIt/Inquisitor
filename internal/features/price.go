package features

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/VersoIt/Inquisitor/internal/marketdata"
	"github.com/VersoIt/Inquisitor/internal/marketdata/validator"
)

type PriceFeatureConfig struct {
	RollingWindow int
}

// PriceFeatures contains deterministic candle-derived research inputs.
type PriceFeatures struct {
	Exchange string
	Category string
	Symbol   string
	Interval string

	OpenTime  time.Time
	CloseTime time.Time

	Return               decimal.Decimal
	LogReturn            float64
	RollingReturn        decimal.Decimal
	RollingHigh          decimal.Decimal
	RollingLow           decimal.Decimal
	CandleBodyPct        decimal.Decimal
	UpperWickPct         decimal.Decimal
	LowerWickPct         decimal.Decimal
	ClosePositionInRange decimal.Decimal

	Complete       bool
	MissingReasons []string
}

// Problem describes a feature validation issue.
type Problem struct {
	Field   string
	Code    string
	Message string
}

// ValidationError groups feature validation problems.
type ValidationError struct {
	Problems []Problem
}

func (e ValidationError) Error() string {
	if len(e.Problems) == 0 {
		return "feature validation failed"
	}

	parts := make([]string, 0, len(e.Problems))
	for _, problem := range e.Problems {
		parts = append(parts, fmt.Sprintf("%s: %s", problem.Field, problem.Message))
	}
	return "feature validation failed: " + strings.Join(parts, "; ")
}

// ComputePriceFeatures calculates price and candle-shape features for closed contiguous candles.
// LogReturn is intentionally float64 because it is a statistical feature, not a money or sizing value.
func ComputePriceFeatures(candles []marketdata.Candle, cfg PriceFeatureConfig) ([]PriceFeatures, error) {
	if cfg.RollingWindow <= 0 {
		return nil, ValidationError{Problems: []Problem{{
			Field:   "rolling_window",
			Code:    "must_be_positive",
			Message: "rolling_window must be positive",
		}}}
	}
	if len(candles) == 0 {
		return nil, nil
	}
	if err := validateFeatureCandles(candles); err != nil {
		return nil, err
	}

	features := make([]PriceFeatures, 0, len(candles))
	for index, candle := range candles {
		item := PriceFeatures{
			Exchange:  candle.Exchange,
			Category:  candle.Category,
			Symbol:    candle.Symbol,
			Interval:  candle.Interval,
			OpenTime:  candle.OpenTime.UTC(),
			CloseTime: candle.CloseTime.UTC(),
			Return:    candle.Close.Sub(candle.Open).Div(candle.Open),
		}

		ratio := candle.Close.Div(candle.Open).InexactFloat64()
		item.LogReturn = math.Log(ratio)
		item.CandleBodyPct, item.UpperWickPct, item.LowerWickPct, item.ClosePositionInRange = candleShape(candle)

		if index+1 < cfg.RollingWindow {
			item.MissingReasons = []string{"rolling_window"}
			features = append(features, item)
			continue
		}

		window := candles[index+1-cfg.RollingWindow : index+1]
		item.RollingReturn = candle.Close.Sub(window[0].Close).Div(window[0].Close)
		item.RollingHigh = rollingHigh(window)
		item.RollingLow = rollingLow(window)
		item.Complete = true
		features = append(features, item)
	}

	return features, nil
}

func validateFeatureCandles(candles []marketdata.Candle) error {
	if err := validator.ValidateCandles(candles); err != nil {
		return err
	}

	var problems []Problem
	add := func(field, code, message string) {
		problems = append(problems, Problem{Field: field, Code: code, Message: message})
	}

	first := candles[0]
	for index, candle := range candles {
		fieldPrefix := fmt.Sprintf("candles[%d]", index)
		if !sameCandleIdentity(first, candle) {
			add(fieldPrefix, "identity_mismatch", "all candles must have the same exchange, category, symbol, and interval")
		}
		if !candle.IsClosed {
			add(fieldPrefix+".is_closed", "required", "feature calculation requires closed candles")
		}
		if index == 0 {
			continue
		}

		previous := candles[index-1]
		if !candle.OpenTime.After(previous.OpenTime) {
			add(fieldPrefix+".open_time", "not_sorted", "candles must be sorted by ascending open_time")
		}
		if !candle.OpenTime.Equal(previous.CloseTime) {
			add(fieldPrefix+".open_time", "gap", "feature calculation requires contiguous candles")
		}
	}

	if len(problems) > 0 {
		return ValidationError{Problems: problems}
	}
	return nil
}

func candleShape(candle marketdata.Candle) (body, upperWick, lowerWick, closePosition decimal.Decimal) {
	rangeSize := candle.High.Sub(candle.Low)
	if rangeSize.Equal(decimal.Zero) {
		return decimal.Zero, decimal.Zero, decimal.Zero, decimal.RequireFromString("0.5")
	}

	body = absDecimal(candle.Close.Sub(candle.Open)).Div(rangeSize)
	upperWick = candle.High.Sub(maxDecimal(candle.Open, candle.Close)).Div(rangeSize)
	lowerWick = minDecimal(candle.Open, candle.Close).Sub(candle.Low).Div(rangeSize)
	closePosition = candle.Close.Sub(candle.Low).Div(rangeSize)
	return body, upperWick, lowerWick, closePosition
}

func rollingHigh(candles []marketdata.Candle) decimal.Decimal {
	high := candles[0].High
	for _, candle := range candles[1:] {
		high = maxDecimal(high, candle.High)
	}
	return high
}

func rollingLow(candles []marketdata.Candle) decimal.Decimal {
	low := candles[0].Low
	for _, candle := range candles[1:] {
		low = minDecimal(low, candle.Low)
	}
	return low
}

func sameCandleIdentity(left, right marketdata.Candle) bool {
	return strings.EqualFold(strings.TrimSpace(left.Exchange), strings.TrimSpace(right.Exchange)) &&
		strings.EqualFold(strings.TrimSpace(left.Category), strings.TrimSpace(right.Category)) &&
		strings.EqualFold(strings.TrimSpace(left.Symbol), strings.TrimSpace(right.Symbol)) &&
		strings.TrimSpace(left.Interval) == strings.TrimSpace(right.Interval)
}

func absDecimal(value decimal.Decimal) decimal.Decimal {
	if value.IsNegative() {
		return value.Neg()
	}
	return value
}

func maxDecimal(left, right decimal.Decimal) decimal.Decimal {
	if left.GreaterThan(right) {
		return left
	}
	return right
}

func minDecimal(left, right decimal.Decimal) decimal.Decimal {
	if left.LessThan(right) {
		return left
	}
	return right
}
