package features

import (
	"math"
	"time"

	"github.com/shopspring/decimal"

	"github.com/VersoIt/Inquisitor/internal/marketdata"
)

type VolatilityFeatureConfig struct {
	ATRWindow               int
	RollingVolatilityWindow int
	VolatilityZScoreWindow  int
	BollingerWindow         int
	BollingerStdDev         float64
	CompressionWindow       int
}

// VolatilityFeatures contains deterministic volatility research inputs.
// Float64 fields are statistical features derived from standard deviation, not money or sizing values.
type VolatilityFeatures struct {
	Exchange string
	Category string
	Symbol   string
	Interval string

	OpenTime  time.Time
	CloseTime time.Time

	ATR                   decimal.Decimal
	ATRPercentage         decimal.Decimal
	RollingVolatility     float64
	VolatilityZScore      float64
	BollingerMiddle       float64
	BollingerUpper        float64
	BollingerLower        float64
	BollingerWidth        float64
	VolatilityCompression float64

	Complete       bool
	MissingReasons []string
}

func DefaultVolatilityFeatureConfig() VolatilityFeatureConfig {
	return VolatilityFeatureConfig{
		ATRWindow:               14,
		RollingVolatilityWindow: 20,
		VolatilityZScoreWindow:  20,
		BollingerWindow:         20,
		BollingerStdDev:         2,
		CompressionWindow:       20,
	}
}

// ComputeVolatilityFeatures calculates ATR, return volatility, and Bollinger-derived features.
func ComputeVolatilityFeatures(candles []marketdata.Candle, cfg VolatilityFeatureConfig) ([]VolatilityFeatures, error) {
	cfg, err := normalizeVolatilityConfig(cfg)
	if err != nil {
		return nil, err
	}
	if len(candles) == 0 {
		return nil, nil
	}
	if err := validateFeatureCandles(candles); err != nil {
		return nil, err
	}

	atr, atrOK := averageTrueRangeSeries(candles, cfg.ATRWindow)
	rollingVolatility, rollingVolatilityOK := rollingLogReturnVolatilitySeries(candles, cfg.RollingVolatilityWindow)
	volatilityZScore, volatilityZScoreOK := volatilityZScoreSeries(rollingVolatility, rollingVolatilityOK, cfg.VolatilityZScoreWindow)
	bollinger, bollingerOK := bollingerSeries(candles, cfg.BollingerWindow, cfg.BollingerStdDev)
	compression, compressionOK := compressionSeries(bollinger, bollingerOK, cfg.CompressionWindow)

	rows := make([]VolatilityFeatures, 0, len(candles))
	for index, candle := range candles {
		item := VolatilityFeatures{
			Exchange:  candle.Exchange,
			Category:  candle.Category,
			Symbol:    candle.Symbol,
			Interval:  candle.Interval,
			OpenTime:  candle.OpenTime.UTC(),
			CloseTime: candle.CloseTime.UTC(),
		}
		var missing []string

		if atrOK[index] {
			item.ATR = atr[index]
			item.ATRPercentage = atr[index].Div(candle.Close)
		} else {
			missing = append(missing, "atr_window")
		}
		if rollingVolatilityOK[index] {
			item.RollingVolatility = rollingVolatility[index]
		} else {
			missing = append(missing, "rolling_volatility_window")
		}
		if volatilityZScoreOK[index] {
			item.VolatilityZScore = volatilityZScore[index]
		} else {
			missing = append(missing, "volatility_z_score_window")
		}
		if bollingerOK[index] {
			item.BollingerMiddle = bollinger[index].middle
			item.BollingerUpper = bollinger[index].upper
			item.BollingerLower = bollinger[index].lower
			item.BollingerWidth = bollinger[index].width
		} else {
			missing = append(missing, "bollinger_window")
		}
		if compressionOK[index] {
			item.VolatilityCompression = compression[index]
		} else {
			missing = append(missing, "compression_window")
		}

		item.MissingReasons = missing
		item.Complete = len(missing) == 0
		rows = append(rows, item)
	}

	return rows, nil
}

func normalizeVolatilityConfig(cfg VolatilityFeatureConfig) (VolatilityFeatureConfig, error) {
	defaults := DefaultVolatilityFeatureConfig()
	if cfg.ATRWindow == 0 {
		cfg.ATRWindow = defaults.ATRWindow
	}
	if cfg.RollingVolatilityWindow == 0 {
		cfg.RollingVolatilityWindow = defaults.RollingVolatilityWindow
	}
	if cfg.VolatilityZScoreWindow == 0 {
		cfg.VolatilityZScoreWindow = defaults.VolatilityZScoreWindow
	}
	if cfg.BollingerWindow == 0 {
		cfg.BollingerWindow = defaults.BollingerWindow
	}
	if cfg.BollingerStdDev == 0 {
		cfg.BollingerStdDev = defaults.BollingerStdDev
	}
	if cfg.CompressionWindow == 0 {
		cfg.CompressionWindow = defaults.CompressionWindow
	}

	var problems []Problem
	addPositive := func(field string) {
		problems = append(problems, Problem{
			Field:   field,
			Code:    "must_be_positive",
			Message: field + " must be positive",
		})
	}
	addGreaterThanOne := func(field string) {
		problems = append(problems, Problem{
			Field:   field,
			Code:    "must_be_greater_than_one",
			Message: field + " must be greater than one",
		})
	}

	if cfg.ATRWindow <= 0 {
		addPositive("atr_window")
	}
	if cfg.RollingVolatilityWindow <= 1 {
		addGreaterThanOne("rolling_volatility_window")
	}
	if cfg.VolatilityZScoreWindow <= 1 {
		addGreaterThanOne("volatility_z_score_window")
	}
	if cfg.BollingerWindow <= 1 {
		addGreaterThanOne("bollinger_window")
	}
	if cfg.BollingerStdDev <= 0 {
		addPositive("bollinger_std_dev")
	}
	if cfg.CompressionWindow <= 1 {
		addGreaterThanOne("compression_window")
	}
	if len(problems) > 0 {
		return VolatilityFeatureConfig{}, ValidationError{Problems: problems}
	}
	return cfg, nil
}

func averageTrueRangeSeries(candles []marketdata.Candle, window int) ([]decimal.Decimal, []bool) {
	values := make([]decimal.Decimal, len(candles))
	ready := make([]bool, len(candles))
	if len(candles) <= window {
		return values, ready
	}

	smoothedTR := decimal.Zero
	windowDecimal := decimal.NewFromInt(int64(window))
	for index := 1; index < len(candles); index++ {
		tr, _, _ := directionalMovement(candles[index-1], candles[index])
		switch {
		case index <= window:
			smoothedTR = smoothedTR.Add(tr)
			if index == window {
				values[index] = smoothedTR.Div(windowDecimal)
				ready[index] = true
			}
		default:
			values[index] = values[index-1].Mul(decimal.NewFromInt(int64(window - 1))).Add(tr).Div(windowDecimal)
			ready[index] = true
		}
	}
	return values, ready
}

func rollingLogReturnVolatilitySeries(candles []marketdata.Candle, window int) ([]float64, []bool) {
	values := make([]float64, len(candles))
	ready := make([]bool, len(candles))
	for index := window; index < len(candles); index++ {
		returns := make([]float64, 0, window)
		for returnIndex := index + 1 - window; returnIndex <= index; returnIndex++ {
			current := candles[returnIndex].Close.InexactFloat64()
			previous := candles[returnIndex-1].Close.InexactFloat64()
			returns = append(returns, math.Log(current/previous))
		}
		values[index] = populationStdDev(returns)
		ready[index] = true
	}
	return values, ready
}

func volatilityZScoreSeries(volatility []float64, volatilityOK []bool, window int) ([]float64, []bool) {
	values := make([]float64, len(volatility))
	ready := make([]bool, len(volatility))
	for index := range volatility {
		if index+1 < window {
			continue
		}

		windowValues := make([]float64, 0, window)
		for valueIndex := index + 1 - window; valueIndex <= index; valueIndex++ {
			if !volatilityOK[valueIndex] {
				windowValues = nil
				break
			}
			windowValues = append(windowValues, volatility[valueIndex])
		}
		if len(windowValues) != window {
			continue
		}

		mean := meanFloat64(windowValues)
		stdDev := populationStdDev(windowValues)
		if stdDev != 0 {
			values[index] = (volatility[index] - mean) / stdDev
		}
		ready[index] = true
	}
	return values, ready
}

type bollingerValues struct {
	middle float64
	upper  float64
	lower  float64
	width  float64
}

func bollingerSeries(candles []marketdata.Candle, window int, multiplier float64) ([]bollingerValues, []bool) {
	values := make([]bollingerValues, len(candles))
	ready := make([]bool, len(candles))
	for index := window - 1; index < len(candles); index++ {
		closes := make([]float64, 0, window)
		for _, candle := range candles[index+1-window : index+1] {
			closes = append(closes, candle.Close.InexactFloat64())
		}
		middle := meanFloat64(closes)
		stdDev := populationStdDev(closes)
		upper := middle + multiplier*stdDev
		lower := middle - multiplier*stdDev
		width := 0.0
		if middle != 0 {
			width = (upper - lower) / middle
		}
		values[index] = bollingerValues{
			middle: middle,
			upper:  upper,
			lower:  lower,
			width:  width,
		}
		ready[index] = true
	}
	return values, ready
}

func compressionSeries(bollinger []bollingerValues, bollingerOK []bool, window int) ([]float64, []bool) {
	values := make([]float64, len(bollinger))
	ready := make([]bool, len(bollinger))
	for index := range bollinger {
		if index+1 < window {
			continue
		}

		widths := make([]float64, 0, window)
		for valueIndex := index + 1 - window; valueIndex <= index; valueIndex++ {
			if !bollingerOK[valueIndex] {
				widths = nil
				break
			}
			widths = append(widths, bollinger[valueIndex].width)
		}
		if len(widths) != window {
			continue
		}

		averageWidth := meanFloat64(widths)
		if averageWidth != 0 {
			values[index] = bollinger[index].width / averageWidth
		}
		ready[index] = true
	}
	return values, ready
}

func meanFloat64(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	total := 0.0
	for _, value := range values {
		total += value
	}
	return total / float64(len(values))
}

func populationStdDev(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	mean := meanFloat64(values)
	total := 0.0
	for _, value := range values {
		diff := value - mean
		total += diff * diff
	}
	return math.Sqrt(total / float64(len(values)))
}
