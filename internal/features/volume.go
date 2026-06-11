package features

import (
	"time"

	"github.com/shopspring/decimal"

	"github.com/VersoIt/Inquisitor/internal/marketdata"
)

type VolumeFeatureConfig struct {
	MovingAverageWindow int
	ZScoreWindow        int
}

// VolumeFeatures contains deterministic volume and turnover research inputs.
type VolumeFeatures struct {
	Exchange string
	Category string
	Symbol   string
	Interval string

	OpenTime  time.Time
	CloseTime time.Time

	VolumeMovingAverage decimal.Decimal
	VolumeZScore        float64
	VolumeChange        decimal.Decimal
	TurnoverChange      decimal.Decimal

	Complete       bool
	MissingReasons []string
}

func DefaultVolumeFeatureConfig() VolumeFeatureConfig {
	return VolumeFeatureConfig{
		MovingAverageWindow: 20,
		ZScoreWindow:        20,
	}
}

// ComputeVolumeFeatures calculates volume moving average, volume z-score, and one-period change features.
func ComputeVolumeFeatures(candles []marketdata.Candle, cfg VolumeFeatureConfig) ([]VolumeFeatures, error) {
	cfg, err := normalizeVolumeConfig(cfg)
	if err != nil {
		return nil, err
	}
	if len(candles) == 0 {
		return nil, nil
	}
	if err := validateFeatureCandles(candles); err != nil {
		return nil, err
	}

	rows := make([]VolumeFeatures, 0, len(candles))
	for index, candle := range candles {
		item := VolumeFeatures{
			Exchange:  candle.Exchange,
			Category:  candle.Category,
			Symbol:    candle.Symbol,
			Interval:  candle.Interval,
			OpenTime:  candle.OpenTime.UTC(),
			CloseTime: candle.CloseTime.UTC(),
		}
		var missing []string

		if ma, ok := volumeMovingAverage(candles, index, cfg.MovingAverageWindow); ok {
			item.VolumeMovingAverage = ma
		} else {
			missing = append(missing, "volume_ma_window")
		}
		if zScore, ok := volumeZScore(candles, index, cfg.ZScoreWindow); ok {
			item.VolumeZScore = zScore
		} else {
			missing = append(missing, "volume_z_score_window")
		}
		if change, ok := onePeriodChange(candles, index, func(candle marketdata.Candle) decimal.Decimal { return candle.Volume }); ok {
			item.VolumeChange = change
		} else {
			missing = append(missing, "previous_volume")
		}
		if change, ok := onePeriodChange(candles, index, func(candle marketdata.Candle) decimal.Decimal { return candle.Turnover }); ok {
			item.TurnoverChange = change
		} else {
			missing = append(missing, "previous_turnover")
		}

		item.MissingReasons = missing
		item.Complete = len(missing) == 0
		rows = append(rows, item)
	}

	return rows, nil
}

func normalizeVolumeConfig(cfg VolumeFeatureConfig) (VolumeFeatureConfig, error) {
	defaults := DefaultVolumeFeatureConfig()
	if cfg.MovingAverageWindow == 0 {
		cfg.MovingAverageWindow = defaults.MovingAverageWindow
	}
	if cfg.ZScoreWindow == 0 {
		cfg.ZScoreWindow = defaults.ZScoreWindow
	}

	var problems []Problem
	if cfg.MovingAverageWindow <= 0 {
		problems = append(problems, Problem{
			Field:   "moving_average_window",
			Code:    "must_be_positive",
			Message: "moving_average_window must be positive",
		})
	}
	if cfg.ZScoreWindow <= 1 {
		problems = append(problems, Problem{
			Field:   "z_score_window",
			Code:    "must_be_greater_than_one",
			Message: "z_score_window must be greater than one",
		})
	}
	if len(problems) > 0 {
		return VolumeFeatureConfig{}, ValidationError{Problems: problems}
	}
	return cfg, nil
}

func volumeMovingAverage(candles []marketdata.Candle, endIndex, window int) (decimal.Decimal, bool) {
	if endIndex+1 < window {
		return decimal.Zero, false
	}
	total := decimal.Zero
	for _, candle := range candles[endIndex+1-window : endIndex+1] {
		total = total.Add(candle.Volume)
	}
	return total.Div(decimal.NewFromInt(int64(window))), true
}

func volumeZScore(candles []marketdata.Candle, endIndex, window int) (float64, bool) {
	if endIndex+1 < window {
		return 0, false
	}

	volumes := make([]float64, 0, window)
	for _, candle := range candles[endIndex+1-window : endIndex+1] {
		volumes = append(volumes, candle.Volume.InexactFloat64())
	}
	stdDev := populationStdDev(volumes)
	if stdDev == 0 {
		return 0, true
	}
	return (candles[endIndex].Volume.InexactFloat64() - meanFloat64(volumes)) / stdDev, true
}

func onePeriodChange(candles []marketdata.Candle, endIndex int, value func(marketdata.Candle) decimal.Decimal) (decimal.Decimal, bool) {
	if endIndex == 0 {
		return decimal.Zero, false
	}
	previous := value(candles[endIndex-1])
	if previous.Equal(decimal.Zero) {
		return decimal.Zero, false
	}
	current := value(candles[endIndex])
	return current.Sub(previous).Div(previous), true
}
