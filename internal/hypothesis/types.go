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
	Name        string          `yaml:"name"`
	Version     string          `yaml:"version"`
	Status      Status          `yaml:"status"`
	Description string          `yaml:"description"`
	Thesis      string          `yaml:"thesis"`
	Market      MarketScope     `yaml:"market"`
	Regime      RegimeScope     `yaml:"regime"`
	Direction   Direction       `yaml:"direction"`
	Signals     []SignalRule    `yaml:"signals"`
	Risk        RiskRules       `yaml:"risk"`
	Validation  ValidationRules `yaml:"validation"`
	Costs       CostRules       `yaml:"costs"`
	Tags        []string        `yaml:"tags"`
}

type MarketScope struct {
	Exchange  string   `yaml:"exchange"`
	Category  string   `yaml:"category"`
	Symbols   []string `yaml:"symbols"`
	Intervals []string `yaml:"intervals"`
}

type RegimeScope struct {
	Allowed []string `yaml:"allowed"`
	Blocked []string `yaml:"blocked"`
}

type SignalRule struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Feature     string `yaml:"feature"`
	Operator    string `yaml:"operator"`
	Value       Scalar `yaml:"value"`
}

type RiskRules struct {
	MaxRiskPerTradePct float64 `yaml:"max_risk_per_trade_pct"`
	MinConfidence      int     `yaml:"min_confidence"`
	RequireStopLoss    bool    `yaml:"require_stop_loss"`
}

type ValidationRules struct {
	MinTrades             int  `yaml:"min_trades"`
	RequireOutOfSample    bool `yaml:"require_out_of_sample"`
	RequireWalkForward    bool `yaml:"require_walk_forward"`
	RequireRegimeAnalysis bool `yaml:"require_regime_analysis"`
}

type CostRules struct {
	IncludeFees     bool `yaml:"include_fees"`
	IncludeSpread   bool `yaml:"include_spread"`
	IncludeSlippage bool `yaml:"include_slippage"`
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
