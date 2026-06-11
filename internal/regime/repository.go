package regime

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

type WriteStats struct {
	Inserted int
	Updated  int
}

type StateQuery struct {
	Exchange string
	Category string
	Symbol   string
	Interval string
	Regime   Regime
	Start    time.Time
	End      time.Time
	Limit    int
}

type Repository interface {
	UpsertStates(ctx context.Context, states []State) (WriteStats, error)
	ListStates(ctx context.Context, query StateQuery) ([]State, error)
}

func (s WriteStats) Total() int {
	return s.Inserted + s.Updated
}

func ValidateStates(states []State) error {
	for index, state := range states {
		if err := ValidateState(state); err != nil {
			return fmt.Errorf("regime_state[%d]: %w", index, err)
		}
	}
	return nil
}

func ValidateState(state State) error {
	var problems []string
	addRequired := func(field, value string) {
		if strings.TrimSpace(value) == "" {
			problems = append(problems, field+" is required")
		}
	}

	addRequired("exchange", state.Exchange)
	addRequired("category", state.Category)
	addRequired("symbol", state.Symbol)
	addRequired("interval", state.Interval)
	if state.OpenTime.IsZero() {
		problems = append(problems, "open_time is required")
	}
	if state.CloseTime.IsZero() {
		problems = append(problems, "close_time is required")
	}
	if !state.OpenTime.IsZero() && !state.CloseTime.IsZero() && !state.CloseTime.After(state.OpenTime) {
		problems = append(problems, "close_time must be after open_time")
	}
	if state.CalculatedAt.IsZero() {
		problems = append(problems, "calculated_at is required")
	}
	if !KnownRegime(state.Regime) {
		problems = append(problems, "regime is unsupported")
	}
	if !KnownRegime(state.CandidateRegime) {
		problems = append(problems, "candidate_regime is unsupported")
	}
	if state.Confidence < 0 || state.Confidence > 100 {
		problems = append(problems, "confidence must be between 0 and 100")
	}
	if state.NoTrade && state.Regime != RegimeNoTrade {
		problems = append(problems, "no_trade=true requires regime=NO_TRADE")
	}
	if !state.NoTrade && state.Regime == RegimeNoTrade {
		problems = append(problems, "regime=NO_TRADE requires no_trade=true")
	}

	if len(problems) > 0 {
		return errors.New("regime state validation failed: " + strings.Join(problems, "; "))
	}
	return nil
}

func ValidateStateQuery(query StateQuery) error {
	var missing []string
	if strings.TrimSpace(query.Exchange) == "" {
		missing = append(missing, "exchange")
	}
	if strings.TrimSpace(query.Category) == "" {
		missing = append(missing, "category")
	}
	if strings.TrimSpace(query.Symbol) == "" {
		missing = append(missing, "symbol")
	}
	if strings.TrimSpace(query.Interval) == "" {
		missing = append(missing, "interval")
	}
	if len(missing) > 0 {
		return errors.New(strings.Join(missing, ", ") + " required")
	}
	if query.Regime != "" && !KnownRegime(query.Regime) {
		return errors.New("regime is unsupported")
	}
	if !query.Start.IsZero() && !query.End.IsZero() && !query.End.After(query.Start) {
		return errors.New("end must be after start")
	}
	return nil
}

func KnownRegime(value Regime) bool {
	switch value {
	case RegimeTrendUp,
		RegimeTrendDown,
		RegimeRange,
		RegimeBreakoutSetup,
		RegimeHighVol,
		RegimeChaos,
		RegimeNoTrade:
		return true
	default:
		return false
	}
}
