package features

import (
	"fmt"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/VersoIt/Inquisitor/internal/marketdata"
	"github.com/VersoIt/Inquisitor/internal/marketdata/validator"
)

type DataQualityFeatureConfig struct {
	MaxStaleness time.Duration
}

type FeatureSetCompleteness struct {
	Name           string
	Complete       bool
	MissingReasons []string
}

type DataQualityFeatureInput struct {
	Candles            []marketdata.Candle
	ObservedAt         time.Time
	WebSocketConnected bool
	OrderbookValid     bool
	FeatureSets        []FeatureSetCompleteness
}

// DataQualityFeatures contains data-health research inputs for later NO_TRADE and regime layers.
type DataQualityFeatures struct {
	Exchange string
	Category string
	Symbol   string
	Interval string

	ObservedAt     time.Time
	LatestDataTime time.Time

	DataFreshnessMS          int64
	MissingCandleCount       int
	WebSocketConnected       bool
	OrderbookValid           bool
	FeatureCompletenessScore decimal.Decimal

	Complete       bool
	MissingReasons []string
}

func DefaultDataQualityFeatureConfig() DataQualityFeatureConfig {
	return DataQualityFeatureConfig{MaxStaleness: time.Minute}
}

// ComputeDataQualityFeatures calculates freshness, gap count, runtime health flags, and feature completeness.
func ComputeDataQualityFeatures(input DataQualityFeatureInput, cfg DataQualityFeatureConfig) (DataQualityFeatures, error) {
	cfg, err := normalizeDataQualityConfig(cfg)
	if err != nil {
		return DataQualityFeatures{}, err
	}
	if input.ObservedAt.IsZero() {
		return DataQualityFeatures{}, ValidationError{Problems: []Problem{{
			Field:   "observed_at",
			Code:    "required",
			Message: "observed_at is required",
		}}}
	}
	if err := validateDataQualityFeatureSets(input.FeatureSets); err != nil {
		return DataQualityFeatures{}, err
	}
	if err := validateDataQualityCandles(input.Candles); err != nil {
		return DataQualityFeatures{}, err
	}

	item := DataQualityFeatures{
		ObservedAt:               input.ObservedAt.UTC(),
		WebSocketConnected:       input.WebSocketConnected,
		OrderbookValid:           input.OrderbookValid,
		MissingCandleCount:       countMissingCandles(input.Candles),
		FeatureCompletenessScore: featureCompletenessScore(input.FeatureSets),
	}
	var missing []string

	if len(input.Candles) == 0 {
		missing = append(missing, "candles")
	} else {
		first := input.Candles[0]
		latest := input.Candles[len(input.Candles)-1]
		item.Exchange = first.Exchange
		item.Category = first.Category
		item.Symbol = first.Symbol
		item.Interval = first.Interval
		item.LatestDataTime = latest.CloseTime.UTC()
		freshness := item.ObservedAt.Sub(item.LatestDataTime)
		if freshness < 0 {
			freshness = 0
		}
		item.DataFreshnessMS = freshness.Milliseconds()
		if freshness > cfg.MaxStaleness {
			missing = append(missing, "stale_data")
		}
	}
	if item.MissingCandleCount > 0 {
		missing = append(missing, "missing_candles")
	}
	if !input.WebSocketConnected {
		missing = append(missing, "websocket_disconnected")
	}
	if !input.OrderbookValid {
		missing = append(missing, "orderbook_invalid")
	}
	if len(input.FeatureSets) == 0 {
		missing = append(missing, "feature_sets")
	}
	for _, featureSet := range input.FeatureSets {
		if featureSet.Complete {
			continue
		}
		if len(featureSet.MissingReasons) == 0 {
			missing = append(missing, "feature_set:"+featureSet.Name+":incomplete")
			continue
		}
		for _, reason := range featureSet.MissingReasons {
			missing = append(missing, "feature_set:"+featureSet.Name+":"+reason)
		}
	}

	item.MissingReasons = missing
	item.Complete = len(missing) == 0
	return item, nil
}

func normalizeDataQualityConfig(cfg DataQualityFeatureConfig) (DataQualityFeatureConfig, error) {
	if cfg.MaxStaleness == 0 {
		cfg = DefaultDataQualityFeatureConfig()
	}
	if cfg.MaxStaleness <= 0 {
		return DataQualityFeatureConfig{}, ValidationError{Problems: []Problem{{
			Field:   "max_staleness",
			Code:    "must_be_positive",
			Message: "max_staleness must be positive",
		}}}
	}
	return cfg, nil
}

func validateDataQualityFeatureSets(featureSets []FeatureSetCompleteness) error {
	var problems []Problem
	for index, featureSet := range featureSets {
		if strings.TrimSpace(featureSet.Name) == "" {
			problems = append(problems, Problem{
				Field:   fmt.Sprintf("feature_sets[%d].name", index),
				Code:    "required",
				Message: "feature set name is required",
			})
		}
	}
	if len(problems) > 0 {
		return ValidationError{Problems: problems}
	}
	return nil
}

func validateDataQualityCandles(candles []marketdata.Candle) error {
	if len(candles) == 0 {
		return nil
	}
	if err := validator.ValidateCandles(candles); err != nil {
		return err
	}

	var problems []Problem
	first := candles[0]
	for index, candle := range candles {
		fieldPrefix := fmt.Sprintf("candles[%d]", index)
		if !sameCandleIdentity(first, candle) {
			problems = append(problems, Problem{
				Field:   fieldPrefix,
				Code:    "identity_mismatch",
				Message: "all candles must have the same exchange, category, symbol, and interval",
			})
		}
		if index == 0 {
			continue
		}
		previous := candles[index-1]
		if !candle.OpenTime.After(previous.OpenTime) {
			problems = append(problems, Problem{
				Field:   fieldPrefix + ".open_time",
				Code:    "not_sorted",
				Message: "candles must be sorted by ascending open_time",
			})
			continue
		}
		duration, err := marketdata.IntervalDuration(candle.Interval)
		if err != nil {
			return err
		}
		if candle.OpenTime.Sub(previous.OpenTime)%duration != 0 {
			problems = append(problems, Problem{
				Field:   fieldPrefix + ".open_time",
				Code:    "interval_alignment",
				Message: "candle open_time distance must align with interval",
			})
		}
	}
	if len(problems) > 0 {
		return ValidationError{Problems: problems}
	}
	return nil
}

func countMissingCandles(candles []marketdata.Candle) int {
	if len(candles) < 2 {
		return 0
	}
	duration, err := marketdata.IntervalDuration(candles[0].Interval)
	if err != nil {
		return 0
	}

	missing := 0
	for index := 1; index < len(candles); index++ {
		delta := candles[index].OpenTime.Sub(candles[index-1].OpenTime)
		if delta <= duration {
			continue
		}
		missing += int(delta/duration) - 1
	}
	return missing
}

func featureCompletenessScore(featureSets []FeatureSetCompleteness) decimal.Decimal {
	if len(featureSets) == 0 {
		return decimal.Zero
	}
	complete := 0
	for _, featureSet := range featureSets {
		if featureSet.Complete {
			complete++
		}
	}
	return decimal.NewFromInt(int64(complete)).Div(decimal.NewFromInt(int64(len(featureSets))))
}
