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
	ADXWindow       int
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
	ADX        decimal.Decimal

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
		ADXWindow:       14,
		StructureWindow: 20,
	}
}

// ComputeTrendFeatures calculates moving-average, ADX, and market-structure features for closed contiguous candles.
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
	adx, adxOK := averageDirectionalIndexSeries(candles, cfg.ADXWindow)

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
		if adxOK[index] {
			item.ADX = adx[index]
		} else {
			missing = append(missing, "adx_window")
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
	if cfg.ADXWindow == 0 {
		cfg.ADXWindow = defaults.ADXWindow
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
	if cfg.ADXWindow <= 0 {
		add("adx_window")
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

func averageDirectionalIndexSeries(candles []marketdata.Candle, window int) ([]decimal.Decimal, []bool) {
	values := make([]decimal.Decimal, len(candles))
	ready := make([]bool, len(candles))
	if len(candles) <= window {
		return values, ready
	}

	smoothedTR := decimal.Zero
	smoothedPositiveDM := decimal.Zero
	smoothedNegativeDM := decimal.Zero
	var dxValues []decimal.Decimal
	var previousADX decimal.Decimal
	windowDecimal := decimal.NewFromInt(int64(window))

	for index := 1; index < len(candles); index++ {
		tr, positiveDM, negativeDM := directionalMovement(candles[index-1], candles[index])
		switch {
		case index <= window:
			smoothedTR = smoothedTR.Add(tr)
			smoothedPositiveDM = smoothedPositiveDM.Add(positiveDM)
			smoothedNegativeDM = smoothedNegativeDM.Add(negativeDM)
			if index < window {
				continue
			}
		default:
			smoothedTR = smoothedTR.Sub(smoothedTR.Div(windowDecimal)).Add(tr)
			smoothedPositiveDM = smoothedPositiveDM.Sub(smoothedPositiveDM.Div(windowDecimal)).Add(positiveDM)
			smoothedNegativeDM = smoothedNegativeDM.Sub(smoothedNegativeDM.Div(windowDecimal)).Add(negativeDM)
		}

		dx := directionalIndex(smoothedTR, smoothedPositiveDM, smoothedNegativeDM)
		if len(dxValues) < window {
			dxValues = append(dxValues, dx)
			if len(dxValues) == window {
				previousADX = averageDecimals(dxValues)
				values[index] = previousADX
				ready[index] = true
			}
			continue
		}

		previousADX = previousADX.Mul(decimal.NewFromInt(int64(window - 1))).Add(dx).Div(windowDecimal)
		values[index] = previousADX
		ready[index] = true
	}

	return values, ready
}

func directionalMovement(previous, current marketdata.Candle) (trueRange, positiveDM, negativeDM decimal.Decimal) {
	highLow := current.High.Sub(current.Low)
	highPreviousClose := absDecimal(current.High.Sub(previous.Close))
	lowPreviousClose := absDecimal(current.Low.Sub(previous.Close))
	trueRange = maxDecimal(highLow, maxDecimal(highPreviousClose, lowPreviousClose))

	upMove := current.High.Sub(previous.High)
	downMove := previous.Low.Sub(current.Low)
	if upMove.GreaterThan(downMove) && upMove.GreaterThan(decimal.Zero) {
		positiveDM = upMove
	}
	if downMove.GreaterThan(upMove) && downMove.GreaterThan(decimal.Zero) {
		negativeDM = downMove
	}
	return trueRange, positiveDM, negativeDM
}

func directionalIndex(smoothedTR, smoothedPositiveDM, smoothedNegativeDM decimal.Decimal) decimal.Decimal {
	if smoothedTR.Equal(decimal.Zero) {
		return decimal.Zero
	}

	hundred := decimal.NewFromInt(100)
	positiveDI := smoothedPositiveDM.Div(smoothedTR).Mul(hundred)
	negativeDI := smoothedNegativeDM.Div(smoothedTR).Mul(hundred)
	sum := positiveDI.Add(negativeDI)
	if sum.Equal(decimal.Zero) {
		return decimal.Zero
	}
	return absDecimal(positiveDI.Sub(negativeDI)).Div(sum).Mul(hundred)
}

func averageDecimals(values []decimal.Decimal) decimal.Decimal {
	if len(values) == 0 {
		return decimal.Zero
	}
	total := decimal.Zero
	for _, value := range values {
		total = total.Add(value)
	}
	return total.Div(decimal.NewFromInt(int64(len(values))))
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
