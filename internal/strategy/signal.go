package strategy

import (
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"

	domainrisk "github.com/VersoIt/Inquisitor/internal/risk"
	"github.com/shopspring/decimal"
)

type Side string

const (
	SideLong  Side = "LONG"
	SideShort Side = "SHORT"
)

var signalSymbolPattern = regexp.MustCompile(`^[A-Z0-9]{3,32}$`)

type Problem struct {
	Field   string
	Message string
}

type ValidationError struct {
	Problems []Problem
}

func (e ValidationError) Error() string {
	if len(e.Problems) == 0 {
		return "strategy trade signal validation failed"
	}
	parts := make([]string, 0, len(e.Problems))
	for _, problem := range e.Problems {
		parts = append(parts, problem.Field+" "+problem.Message)
	}
	return "strategy trade signal validation failed: " + strings.Join(parts, "; ")
}

type TradeSignalInput struct {
	SignalID                  string
	HypothesisID              string
	HypothesisVersion         string
	StrategyName              string
	Symbol                    string
	Side                      Side
	Confidence                int
	EntryPrice                decimal.Decimal
	StopLoss                  decimal.Decimal
	TakeProfit                decimal.Decimal
	Leverage                  decimal.Decimal
	HypothesisApprovedForMode bool
	HypothesisMaxRiskPct      decimal.Decimal
	HypothesisMinConfidence   int
	Reason                    string
	ObservedAt                time.Time
	CreatedAt                 time.Time
}

type TradeSignal struct {
	SignalID                  string
	HypothesisID              string
	HypothesisVersion         string
	StrategyName              string
	Symbol                    string
	Side                      Side
	Confidence                int
	EntryPrice                decimal.Decimal
	StopLoss                  decimal.Decimal
	TakeProfit                decimal.Decimal
	Leverage                  decimal.Decimal
	HypothesisApprovedForMode bool
	HypothesisMaxRiskPct      decimal.Decimal
	HypothesisMinConfidence   int
	Reason                    string
	ObservedAt                time.Time
	CreatedAt                 time.Time
}

func NewTradeSignal(input TradeSignalInput) (TradeSignal, error) {
	return CanonicalTradeSignal(TradeSignal{
		SignalID:                  input.SignalID,
		HypothesisID:              input.HypothesisID,
		HypothesisVersion:         input.HypothesisVersion,
		StrategyName:              input.StrategyName,
		Symbol:                    input.Symbol,
		Side:                      input.Side,
		Confidence:                input.Confidence,
		EntryPrice:                input.EntryPrice,
		StopLoss:                  input.StopLoss,
		TakeProfit:                input.TakeProfit,
		Leverage:                  input.Leverage,
		HypothesisApprovedForMode: input.HypothesisApprovedForMode,
		HypothesisMaxRiskPct:      input.HypothesisMaxRiskPct,
		HypothesisMinConfidence:   input.HypothesisMinConfidence,
		Reason:                    input.Reason,
		ObservedAt:                input.ObservedAt,
		CreatedAt:                 input.CreatedAt,
	})
}

func CanonicalTradeSignal(signal TradeSignal) (TradeSignal, error) {
	signal = normalizeTradeSignal(signal)
	if err := ValidateTradeSignal(signal); err != nil {
		return TradeSignal{}, err
	}
	return signal, nil
}

func (s TradeSignal) RiskIntent(intentID string) (domainrisk.TradeIntent, error) {
	signal, err := CanonicalTradeSignal(s)
	if err != nil {
		return domainrisk.TradeIntent{}, err
	}
	intentID = strings.TrimSpace(intentID)
	if intentID == "" {
		return domainrisk.TradeIntent{}, errors.New("strategy risk intent build failed: intent_id is required")
	}
	return domainrisk.TradeIntent{
		IntentID:                intentID,
		HypothesisID:            signal.HypothesisID,
		StrategyName:            signal.StrategyName,
		Symbol:                  signal.Symbol,
		Side:                    riskSide(signal.Side),
		Confidence:              signal.Confidence,
		EntryPrice:              signal.EntryPrice,
		StopLoss:                signal.StopLoss,
		TakeProfit:              signal.TakeProfit,
		Leverage:                signal.Leverage,
		HypothesisApproved:      signal.HypothesisApprovedForMode,
		HypothesisMaxRiskPct:    signal.HypothesisMaxRiskPct,
		HypothesisMinConfidence: signal.HypothesisMinConfidence,
		Reason:                  signal.Reason,
		CreatedAt:               signal.CreatedAt,
	}, nil
}

func ValidateTradeSignal(signal TradeSignal) error {
	var problems []Problem
	problems = appendRequiredTrimmed(problems, "signal_id", signal.SignalID)
	problems = appendRequiredTrimmed(problems, "hypothesis_id", signal.HypothesisID)
	problems = appendRequiredTrimmed(problems, "hypothesis_version", signal.HypothesisVersion)
	problems = appendRequiredTrimmed(problems, "strategy_name", signal.StrategyName)
	problems = appendRequiredTrimmed(problems, "symbol", signal.Symbol)
	problems = appendRequiredTrimmed(problems, "reason", signal.Reason)

	if signal.Symbol != "" && signal.Symbol != strings.ToUpper(strings.TrimSpace(signal.Symbol)) {
		problems = append(problems, Problem{Field: "symbol", Message: "must be uppercase and trimmed"})
	}
	if signal.Symbol != "" && strings.TrimSpace(signal.Symbol) == signal.Symbol && !signalSymbolPattern.MatchString(signal.Symbol) {
		problems = append(problems, Problem{Field: "symbol", Message: "must be an uppercase alphanumeric exchange symbol"})
	}
	if !KnownSide(signal.Side) {
		problems = append(problems, Problem{Field: "side", Message: "must be LONG or SHORT"})
	}
	if signal.Confidence < 0 || signal.Confidence > 100 {
		problems = append(problems, Problem{Field: "confidence", Message: "must be between 0 and 100"})
	}
	if signal.HypothesisMinConfidence < 0 || signal.HypothesisMinConfidence > 100 {
		problems = append(problems, Problem{Field: "hypothesis_min_confidence", Message: "must be between 0 and 100"})
	}
	if signal.EntryPrice.LessThanOrEqual(decimal.Zero) {
		problems = append(problems, Problem{Field: "entry_price", Message: "must be positive"})
	}
	if signal.StopLoss.LessThanOrEqual(decimal.Zero) {
		problems = append(problems, Problem{Field: "stop_loss", Message: "must be positive"})
	}
	if signal.TakeProfit.IsNegative() {
		problems = append(problems, Problem{Field: "take_profit", Message: "must not be negative"})
	}
	if signal.Leverage.LessThanOrEqual(decimal.Zero) {
		problems = append(problems, Problem{Field: "leverage", Message: "must be positive"})
	}
	if signal.HypothesisMaxRiskPct.LessThanOrEqual(decimal.Zero) || signal.HypothesisMaxRiskPct.GreaterThan(decimal.NewFromInt(1)) {
		problems = append(problems, Problem{Field: "hypothesis_max_risk_pct", Message: "must be greater than 0 and no more than 1"})
	}
	problems = append(problems, validateSignalPriceGeometry(signal)...)
	if signal.ObservedAt.IsZero() {
		problems = append(problems, Problem{Field: "observed_at", Message: "is required"})
	}
	if signal.CreatedAt.IsZero() {
		problems = append(problems, Problem{Field: "created_at", Message: "is required"})
	}
	if !signal.ObservedAt.IsZero() && !signal.CreatedAt.IsZero() && signal.ObservedAt.After(signal.CreatedAt) {
		problems = append(problems, Problem{Field: "observed_at", Message: "must not be after created_at"})
	}
	if len(problems) > 0 {
		return ValidationError{Problems: problems}
	}
	return nil
}

func KnownSide(side Side) bool {
	return side == SideLong || side == SideShort
}

func normalizeTradeSignal(signal TradeSignal) TradeSignal {
	signal.SignalID = strings.TrimSpace(signal.SignalID)
	signal.HypothesisID = strings.TrimSpace(signal.HypothesisID)
	signal.HypothesisVersion = strings.TrimSpace(signal.HypothesisVersion)
	signal.StrategyName = strings.TrimSpace(signal.StrategyName)
	signal.Symbol = strings.ToUpper(strings.TrimSpace(signal.Symbol))
	signal.Side = Side(strings.ToUpper(strings.TrimSpace(string(signal.Side))))
	signal.Reason = strings.TrimSpace(signal.Reason)
	signal.ObservedAt = signal.ObservedAt.UTC()
	signal.CreatedAt = signal.CreatedAt.UTC()
	return signal
}

func appendRequiredTrimmed(problems []Problem, field string, value string) []Problem {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return append(problems, Problem{Field: field, Message: "is required"})
	}
	if value != trimmed {
		return append(problems, Problem{Field: field, Message: "must be trimmed"})
	}
	return problems
}

func validateSignalPriceGeometry(signal TradeSignal) []Problem {
	if !KnownSide(signal.Side) || signal.EntryPrice.LessThanOrEqual(decimal.Zero) || signal.StopLoss.LessThanOrEqual(decimal.Zero) {
		return nil
	}
	var problems []Problem
	switch signal.Side {
	case SideLong:
		if !signal.StopLoss.LessThan(signal.EntryPrice) {
			problems = append(problems, Problem{Field: "stop_loss", Message: "must be below entry_price for LONG"})
		}
		if signal.TakeProfit.IsPositive() && !signal.TakeProfit.GreaterThan(signal.EntryPrice) {
			problems = append(problems, Problem{Field: "take_profit", Message: "must be above entry_price for LONG"})
		}
	case SideShort:
		if !signal.StopLoss.GreaterThan(signal.EntryPrice) {
			problems = append(problems, Problem{Field: "stop_loss", Message: "must be above entry_price for SHORT"})
		}
		if signal.TakeProfit.IsPositive() && !signal.TakeProfit.LessThan(signal.EntryPrice) {
			problems = append(problems, Problem{Field: "take_profit", Message: "must be below entry_price for SHORT"})
		}
	}
	return problems
}

func riskSide(side Side) domainrisk.Side {
	switch side {
	case SideLong:
		return domainrisk.SideLong
	case SideShort:
		return domainrisk.SideShort
	default:
		return domainrisk.Side("")
	}
}

func ValidateTradeSignals(signals []TradeSignal) error {
	for index, signal := range signals {
		if err := ValidateTradeSignal(signal); err != nil {
			return errors.New("strategy trade_signal[" + strconv.Itoa(index) + "]: " + err.Error())
		}
	}
	return nil
}
