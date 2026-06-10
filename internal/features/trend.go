package features

import (
	"time"

	"github.com/shopspring/decimal"

	"github.com/VersoIt/Inquisitor/internal/marketdata"
)

type TrendFeatureConfig struct {
	MA20Window      int
	MA50Window      int
	MA200Window     int
	EMA20Window     int
	EMA50Window     int
	StructureWindow int
}

// TrendFeatures contains deterministic trend and market-structure research inputs.
type TrendFeatures struct {
	Exchange string
	Category string
	Symbol   string
	Interval string

	OpenTime  time.Time
	CloseTime time.Time

	MA20       decimal.Decimal
	MA50       decimal.Decimal
	MA200      decimal.Decimal
	EMA20      decimal.Decimal
	EMA50      decimal.Decimal
	MA50Slope  decimal.Decimal
	MA200Slope decimal.Decimal

	HigherHighCount int
	HigherLowCount  int
	LowerHighCount  int
	LowerLowCount   int

	Complete       bool
	MissingReasons []string
}

func DefaultTrendFeatureConfig() TrendFeatureConfig {
	return TrendFeatureConfig{
		MA20Window:      20,
		MA50Window:      50,
		MA200Window:     200,
		EMA20Window:     20,
		EMA50Window:     50,
		StructureWindow: 20,
	}
}

// ComputeTrendFeatures calculates moving-average and market-structure features for closed contiguous candles.
func ComputeTrendFeatures(candles []marketdata.Candle, cfg TrendFeatureConfig) ([]TrendFeatures, error) {
	cfg, err := normalizeTrendConfig(cfg)
	if err != nil {
		return nil, err
	}
	if len(candles) == 0 {
		return nil, nil
	}
	if err := validateFeatureCandles(candles); err != nil {
		return nil, err
	}

	ema20, ema20OK := exponentialMovingAverageSeries(candles, cfg.EMA20Window)
	ema50, ema50OK := exponentialMovingAverageSeries(candles, cfg.EMA50Window)

	rows := make([]TrendFeatures, 0, len(candles))
	for index, candle := range candles {
		item := TrendFeatures{
			Exchange:  candle.Exchange,
			Category:  candle.Category,
			Symbol:    candle.Symbol,
			Interval:  candle.Interval,
			OpenTime:  candle.OpenTime.UTC(),
			CloseTime: candle.CloseTime.UTC(),
		}
		var missing []string

		if ma, ok := simpleMovingAverage(candles, index, cfg.MA20Window); ok {
			item.MA20 = ma
		} else {
			missing = append(missing, "ma20_window")
		}
		if ma, ok := simpleMovingAverage(candles, index, cfg.MA50Window); ok {
			item.MA50 = ma
		} else {
			missing = append(missing, "ma50_window")
		}
		if ma, ok := simpleMovingAverage(candles, index, cfg.MA200Window); ok {
			item.MA200 = ma
		} else {
			missing = append(missing, "ma200_window")
		}
		if ema20OK[index] {
			item.EMA20 = ema20[index]
		} else {
			missing = append(missing, "ema20_window")
		}
		if ema50OK[index] {
			item.EMA50 = ema50[index]
		} else {
			missing = append(missing, "ema50_window")
		}
		if slope, ok := movingAverageSlope(candles, index, cfg.MA50Window); ok {
			item.MA50Slope = slope
		} else {
			missing = append(missing, "ma50_slope_window")
		}
		if slope, ok := movingAverageSlope(candles, index, cfg.MA200Window); ok {
			item.MA200Slope = slope
		} else {
			missing = append(missing, "ma200_slope_window")
		}
		if counts, ok := structureCounts(candles, index, cfg.StructureWindow); ok {
			item.HigherHighCount = counts.higherHigh
			item.HigherLowCount = counts.higherLow
			item.LowerHighCount = counts.lowerHigh
			item.LowerLowCount = counts.lowerLow
		} else {
			missing = append(missing, "structure_window")
		}

		item.MissingReasons = missing
		item.Complete = len(missing) == 0
		rows = append(rows, item)
	}

	return rows, nil
}

func normalizeTrendConfig(cfg TrendFeatureConfig) (TrendFeatureConfig, error) {
	defaults := DefaultTrendFeatureConfig()
	if cfg.MA20Window == 0 {
		cfg.MA20Window = defaults.MA20Window
	}
	if cfg.MA50Window == 0 {
		cfg.MA50Window = defaults.MA50Window
	}
	if cfg.MA200Window == 0 {
		cfg.MA200Window = defaults.MA200Window
	}
	if cfg.EMA20Window == 0 {
		cfg.EMA20Window = defaults.EMA20Window
	}
	if cfg.EMA50Window == 0 {
		cfg.EMA50Window = defaults.EMA50Window
	}
	if cfg.StructureWindow == 0 {
		cfg.StructureWindow = defaults.StructureWindow
	}

	var problems []Problem
	add := func(field string) {
		problems = append(problems, Problem{
			Field:   field,
			Code:    "must_be_positive",
			Message: field + " must be positive",
		})
	}

	if cfg.MA20Window <= 0 {
		add("ma20_window")
	}
	if cfg.MA50Window <= 0 {
		add("ma50_window")
	}
	if cfg.MA200Window <= 0 {
		add("ma200_window")
	}
	if cfg.EMA20Window <= 0 {
		add("ema20_window")
	}
	if cfg.EMA50Window <= 0 {
		add("ema50_window")
	}
	if cfg.StructureWindow <= 1 {
		problems = append(problems, Problem{
			Field:   "structure_window",
			Code:    "must_be_greater_than_one",
			Message: "structure_window must be greater than one",
		})
	}
	if len(problems) > 0 {
		return TrendFeatureConfig{}, ValidationError{Problems: problems}
	}
	return cfg, nil
}

func simpleMovingAverage(candles []marketdata.Candle, endIndex, window int) (decimal.Decimal, bool) {
	if endIndex+1 < window {
		return decimal.Zero, false
	}
	total := decimal.Zero
	for _, candle := range candles[endIndex+1-window : endIndex+1] {
		total = total.Add(candle.Close)
	}
	return total.Div(decimal.NewFromInt(int64(window))), true
}

func exponentialMovingAverageSeries(candles []marketdata.Candle, window int) ([]decimal.Decimal, []bool) {
	values := make([]decimal.Decimal, len(candles))
	ready := make([]bool, len(candles))
	alpha := decimal.NewFromInt(2).Div(decimal.NewFromInt(int64(window + 1)))
	oneMinusAlpha := decimal.NewFromInt(1).Sub(alpha)

	for index, candle := range candles {
		switch {
		case index+1 < window:
			continue
		case index+1 == window:
			values[index], ready[index] = simpleMovingAverage(candles, index, window)
		default:
			values[index] = candle.Close.Mul(alpha).Add(values[index-1].Mul(oneMinusAlpha))
			ready[index] = true
		}
	}
	return values, ready
}

func movingAverageSlope(candles []marketdata.Candle, endIndex, window int) (decimal.Decimal, bool) {
	current, currentOK := simpleMovingAverage(candles, endIndex, window)
	previous, previousOK := simpleMovingAverage(candles, endIndex-1, window)
	if !currentOK || !previousOK || previous.Equal(decimal.Zero) {
		return decimal.Zero, false
	}
	return current.Sub(previous).Div(previous), true
}

type marketStructureCounts struct {
	higherHigh int
	higherLow  int
	lowerHigh  int
	lowerLow   int
}

func structureCounts(candles []marketdata.Candle, endIndex, window int) (marketStructureCounts, bool) {
	if endIndex+1 < window {
		return marketStructureCounts{}, false
	}

	var counts marketStructureCounts
	start := endIndex + 1 - window
	for index := start + 1; index <= endIndex; index++ {
		previous := candles[index-1]
		current := candles[index]
		switch {
		case current.High.GreaterThan(previous.High):
			counts.higherHigh++
		case current.High.LessThan(previous.High):
			counts.lowerHigh++
		}
		switch {
		case current.Low.GreaterThan(previous.Low):
			counts.higherLow++
		case current.Low.LessThan(previous.Low):
			counts.lowerLow++
		}
	}
	return counts, true
}
