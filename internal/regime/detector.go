package regime

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	domainfeatures "github.com/VersoIt/Inquisitor/internal/features"
)

type Regime string

const (
	RegimeTrendUp       Regime = "TREND_UP"
	RegimeTrendDown     Regime = "TREND_DOWN"
	RegimeRange         Regime = "RANGE"
	RegimeBreakoutSetup Regime = "BREAKOUT_SETUP"
	RegimeHighVol       Regime = "HIGH_VOLATILITY"
	RegimeChaos         Regime = "CHAOS"
	RegimeNoTrade       Regime = "NO_TRADE"
)

type Config struct {
	MinConfidence      int
	ADXTrendThreshold  float64
	ADXRangeThreshold  float64
	ATRSpikeMultiplier float64
}

type Detector struct {
	cfg Config
}

type Input struct {
	Price          *domainfeatures.PriceFeatures
	Trend          *domainfeatures.TrendFeatures
	Volatility     *domainfeatures.VolatilityFeatures
	Volume         *domainfeatures.VolumeFeatures
	Microstructure *domainfeatures.MicrostructureFeatures
	DataQuality    *domainfeatures.DataQualityFeatures
	CalculatedAt   time.Time
}

type State struct {
	Exchange string
	Category string
	Symbol   string
	Interval string

	OpenTime     time.Time
	CloseTime    time.Time
	CalculatedAt time.Time

	Regime          Regime
	CandidateRegime Regime
	Confidence      int
	NoTrade         bool
	Reasons         []string
}

func DefaultConfig() Config {
	return Config{
		MinConfidence:      70,
		ADXTrendThreshold:  25,
		ADXRangeThreshold:  18,
		ATRSpikeMultiplier: 2.5,
	}
}

func NewDetector(cfg Config) (Detector, error) {
	cfg, err := normalizeConfig(cfg)
	if err != nil {
		return Detector{}, err
	}
	return Detector{cfg: cfg}, nil
}

func (d Detector) Detect(input Input) (State, error) {
	cfg, err := normalizeConfig(d.cfg)
	if err != nil {
		return State{}, err
	}
	d.cfg = cfg

	missing := missingRequiredFeatures(input)
	if len(missing) > 0 {
		return State{
			Regime:          RegimeNoTrade,
			CandidateRegime: RegimeNoTrade,
			NoTrade:         true,
			Reasons:         missing,
		}, nil
	}

	state := baseState(input)
	candidate, candidateReasons := d.classify(input)
	confidence := d.confidence(candidate, input)

	noTradeReasons := noTradeReasons(input)
	if volatilitySpike(*input.Volatility, d.cfg) {
		noTradeReasons = append(noTradeReasons, "volatility_spike")
	}
	if confidence < d.cfg.MinConfidence {
		noTradeReasons = append(noTradeReasons, "low_confidence")
	}

	state.CandidateRegime = candidate
	state.Confidence = confidence
	state.Reasons = append(candidateReasons, noTradeReasons...)
	if len(noTradeReasons) > 0 {
		state.Regime = RegimeNoTrade
		state.NoTrade = true
		return state, nil
	}

	state.Regime = candidate
	return state, nil
}

func normalizeConfig(cfg Config) (Config, error) {
	defaults := DefaultConfig()
	if cfg.MinConfidence == 0 {
		cfg.MinConfidence = defaults.MinConfidence
	}
	if cfg.ADXTrendThreshold == 0 {
		cfg.ADXTrendThreshold = defaults.ADXTrendThreshold
	}
	if cfg.ADXRangeThreshold == 0 {
		cfg.ADXRangeThreshold = defaults.ADXRangeThreshold
	}
	if cfg.ATRSpikeMultiplier == 0 {
		cfg.ATRSpikeMultiplier = defaults.ATRSpikeMultiplier
	}

	var problems []string
	if cfg.MinConfidence < 0 || cfg.MinConfidence > 100 {
		problems = append(problems, "min_confidence must be between 0 and 100")
	}
	if cfg.ADXTrendThreshold <= 0 {
		problems = append(problems, "adx_trend_threshold must be positive")
	}
	if cfg.ADXRangeThreshold <= 0 {
		problems = append(problems, "adx_range_threshold must be positive")
	}
	if cfg.ADXTrendThreshold <= cfg.ADXRangeThreshold {
		problems = append(problems, "adx_trend_threshold must be greater than adx_range_threshold")
	}
	if cfg.ATRSpikeMultiplier <= 0 {
		problems = append(problems, "atr_spike_multiplier must be positive")
	}
	if len(problems) > 0 {
		return Config{}, fmt.Errorf("regime detector config invalid: %s", strings.Join(problems, "; "))
	}
	return cfg, nil
}

func missingRequiredFeatures(input Input) []string {
	var missing []string
	if input.Price == nil {
		missing = append(missing, "feature_missing:price")
	}
	if input.Trend == nil {
		missing = append(missing, "feature_missing:trend")
	}
	if input.Volatility == nil {
		missing = append(missing, "feature_missing:volatility")
	}
	if input.Volume == nil {
		missing = append(missing, "feature_missing:volume")
	}
	if input.Microstructure == nil {
		missing = append(missing, "feature_missing:microstructure")
	}
	if input.DataQuality == nil {
		missing = append(missing, "feature_missing:data_quality")
	}
	return missing
}

func baseState(input Input) State {
	state := State{
		Exchange:     input.Price.Exchange,
		Category:     input.Price.Category,
		Symbol:       input.Price.Symbol,
		Interval:     input.Price.Interval,
		OpenTime:     input.Price.OpenTime.UTC(),
		CloseTime:    input.Price.CloseTime.UTC(),
		CalculatedAt: input.CalculatedAt.UTC(),
	}
	if state.CalculatedAt.IsZero() {
		state.CalculatedAt = input.DataQuality.ObservedAt.UTC()
	}
	return state
}

func noTradeReasons(input Input) []string {
	var reasons []string
	appendIncomplete := func(name string, complete bool, missing []string) {
		if complete {
			return
		}
		if len(missing) == 0 {
			reasons = append(reasons, "feature_incomplete:"+name)
			return
		}
		for _, reason := range missing {
			reasons = append(reasons, "feature_incomplete:"+name+":"+reason)
		}
	}

	appendIncomplete("price", input.Price.Complete, input.Price.MissingReasons)
	appendIncomplete("trend", input.Trend.Complete, input.Trend.MissingReasons)
	appendIncomplete("volatility", input.Volatility.Complete, input.Volatility.MissingReasons)
	appendIncomplete("volume", input.Volume.Complete, input.Volume.MissingReasons)
	appendIncomplete("microstructure", input.Microstructure.Complete, input.Microstructure.MissingReasons)
	if !input.DataQuality.Complete {
		if len(input.DataQuality.MissingReasons) == 0 {
			reasons = append(reasons, "data_quality")
		} else {
			for _, reason := range input.DataQuality.MissingReasons {
				reasons = append(reasons, "data_quality:"+reason)
			}
		}
	}
	return reasons
}

func (d Detector) classify(input Input) (Regime, []string) {
	trend := input.Trend
	volatility := input.Volatility
	adx := trend.ADX.InexactFloat64()

	if volatilitySpike(*volatility, d.cfg) {
		return RegimeHighVol, []string{"candidate:high_volatility"}
	}
	if adx >= d.cfg.ADXTrendThreshold {
		switch {
		case trendUpAligned(*trend):
			return RegimeTrendUp, []string{"candidate:trend_up", "adx_trend", "ma_alignment_up"}
		case trendDownAligned(*trend):
			return RegimeTrendDown, []string{"candidate:trend_down", "adx_trend", "ma_alignment_down"}
		default:
			return RegimeChaos, []string{"candidate:chaos", "adx_without_directional_alignment"}
		}
	}
	if adx <= d.cfg.ADXRangeThreshold {
		return RegimeRange, []string{"candidate:range", "adx_range"}
	}
	if volatility.VolatilityCompression < 0.75 {
		return RegimeBreakoutSetup, []string{"candidate:breakout_setup", "volatility_compression"}
	}
	return RegimeChaos, []string{"candidate:chaos", "adx_transition_zone"}
}

func (d Detector) confidence(candidate Regime, input Input) int {
	trend := input.Trend
	volatility := input.Volatility
	adx := trend.ADX.InexactFloat64()

	switch candidate {
	case RegimeTrendUp, RegimeTrendDown:
		score := 70 + boundedInt(math.Round(adx-d.cfg.ADXTrendThreshold), 0, 20)
		if microstructureConfirms(candidate, *input.Microstructure) {
			score += 5
		}
		return capConfidence(score)
	case RegimeRange:
		score := 70 + boundedInt(math.Round(d.cfg.ADXRangeThreshold-adx), 0, 20)
		if volatility.VolatilityCompression <= 1 {
			score += 5
		}
		return capConfidence(score)
	case RegimeBreakoutSetup:
		score := 65 + boundedInt(math.Round((0.75-volatility.VolatilityCompression)*40), 0, 20)
		return capConfidence(score)
	case RegimeHighVol:
		spike := math.Max(math.Abs(volatility.VolatilityZScore), volatility.VolatilityCompression)
		score := 80 + boundedInt(math.Round((spike-d.cfg.ATRSpikeMultiplier)*10), 0, 20)
		return capConfidence(score)
	default:
		return 40
	}
}

func trendUpAligned(trend domainfeatures.TrendFeatures) bool {
	return trend.MA20.GreaterThan(trend.MA50) &&
		trend.MA50.GreaterThan(trend.MA200) &&
		trend.MA50Slope.GreaterThanOrEqual(decimal.Zero) &&
		trend.MA200Slope.GreaterThanOrEqual(decimal.Zero)
}

func trendDownAligned(trend domainfeatures.TrendFeatures) bool {
	return trend.MA20.LessThan(trend.MA50) &&
		trend.MA50.LessThan(trend.MA200) &&
		trend.MA50Slope.LessThanOrEqual(decimal.Zero) &&
		trend.MA200Slope.LessThanOrEqual(decimal.Zero)
}

func microstructureConfirms(candidate Regime, microstructure domainfeatures.MicrostructureFeatures) bool {
	switch candidate {
	case RegimeTrendUp:
		return microstructure.TradeAggressorImbalance.GreaterThan(decimal.Zero) ||
			microstructure.OrderbookImbalance.GreaterThan(decimal.Zero)
	case RegimeTrendDown:
		return microstructure.TradeAggressorImbalance.LessThan(decimal.Zero) ||
			microstructure.OrderbookImbalance.LessThan(decimal.Zero)
	default:
		return false
	}
}

func volatilitySpike(volatility domainfeatures.VolatilityFeatures, cfg Config) bool {
	return math.Abs(volatility.VolatilityZScore) >= cfg.ATRSpikeMultiplier ||
		volatility.VolatilityCompression >= cfg.ATRSpikeMultiplier
}

func boundedInt(value float64, minValue, maxValue int) int {
	valueInt := int(value)
	if valueInt < minValue {
		return minValue
	}
	if valueInt > maxValue {
		return maxValue
	}
	return valueInt
}

func capConfidence(score int) int {
	if score < 0 {
		return 0
	}
	if score > 100 {
		return 100
	}
	return score
}
