package hypothesis

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

type Status string

const StatusDraft Status = "DRAFT"

type Direction string

const (
	DirectionLong  Direction = "LONG"
	DirectionShort Direction = "SHORT"
	DirectionBoth  Direction = "BOTH"
)

type Hypothesis struct {
	Name        string          `json:"name" yaml:"name"`
	Version     string          `json:"version" yaml:"version"`
	Status      Status          `json:"status" yaml:"status"`
	Description string          `json:"description" yaml:"description"`
	Thesis      string          `json:"thesis" yaml:"thesis"`
	Market      MarketScope     `json:"market" yaml:"market"`
	Regime      RegimeScope     `json:"regime" yaml:"regime"`
	Direction   Direction       `json:"direction" yaml:"direction"`
	Signals     []SignalRule    `json:"signals" yaml:"signals"`
	Risk        RiskRules       `json:"risk" yaml:"risk"`
	Validation  ValidationRules `json:"validation" yaml:"validation"`
	Costs       CostRules       `json:"costs" yaml:"costs"`
	Tags        []string        `json:"tags" yaml:"tags"`
}

type MarketScope struct {
	Exchange  string   `json:"exchange" yaml:"exchange"`
	Category  string   `json:"category" yaml:"category"`
	Symbols   []string `json:"symbols" yaml:"symbols"`
	Intervals []string `json:"intervals" yaml:"intervals"`
}

type RegimeScope struct {
	Allowed []string `json:"allowed" yaml:"allowed"`
	Blocked []string `json:"blocked" yaml:"blocked"`
}

type SignalRule struct {
	Name        string `json:"name" yaml:"name"`
	Description string `json:"description" yaml:"description"`
	Feature     string `json:"feature" yaml:"feature"`
	Operator    string `json:"operator" yaml:"operator"`
	Value       Scalar `json:"value" yaml:"value"`
}

type RiskRules struct {
	MaxRiskPerTradePct float64 `json:"max_risk_per_trade_pct" yaml:"max_risk_per_trade_pct"`
	MinConfidence      int     `json:"min_confidence" yaml:"min_confidence"`
	RequireStopLoss    bool    `json:"require_stop_loss" yaml:"require_stop_loss"`
}

type ValidationRules struct {
	MinTrades             int  `json:"min_trades" yaml:"min_trades"`
	RequireOutOfSample    bool `json:"require_out_of_sample" yaml:"require_out_of_sample"`
	RequireWalkForward    bool `json:"require_walk_forward" yaml:"require_walk_forward"`
	RequireRegimeAnalysis bool `json:"require_regime_analysis" yaml:"require_regime_analysis"`
}

type CostRules struct {
	IncludeFees     bool `json:"include_fees" yaml:"include_fees"`
	IncludeSpread   bool `json:"include_spread" yaml:"include_spread"`
	IncludeSlippage bool `json:"include_slippage" yaml:"include_slippage"`
}

type Scalar string

func (s *Scalar) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind != yaml.ScalarNode {
		return fmt.Errorf("must be a scalar")
	}
	*s = Scalar(strings.TrimSpace(value.Value))
	return nil
}

func (s Scalar) String() string {
	return string(s)
}
