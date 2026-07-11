package paper

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/VersoIt/Inquisitor/internal/backtest"
)

func SummarizeEquityEvents(validationID string, initialEquity decimal.Decimal, events []EquityEvent) (backtest.Summary, error) {
	validationID = strings.TrimSpace(validationID)
	if validationID == "" {
		return backtest.Summary{}, errors.New("paper equity summary failed: validation_id is required")
	}
	if err := ValidateEquityEventSequence(validationID, initialEquity, events); err != nil {
		return backtest.Summary{}, err
	}
	return summarizeEquityEventWindow(validationID, initialEquity, events)
}

func summarizeEquityEventWindow(validationID string, initialEquity decimal.Decimal, events []EquityEvent) (backtest.Summary, error) {
	if initialEquity.LessThanOrEqual(decimal.Zero) {
		return backtest.Summary{}, errors.New("paper equity summary failed: initial_equity must be positive")
	}

	summary := backtest.Summary{
		Trades:        len(events),
		InitialEquity: initialEquity,
		FinalEquity:   initialEquity,
	}
	seenEvents := make(map[string]struct{}, len(events))
	seenCloses := make(map[string]struct{}, len(events))
	seenPositions := make(map[string]struct{}, len(events))
	equity := initialEquity
	peak := initialEquity
	var previous EquityEvent
	for index, event := range orderEquityEvents(events) {
		if err := ValidateEquityEvent(event); err != nil {
			return backtest.Summary{}, fmt.Errorf("paper equity summary failed: event[%d]: %w", index, err)
		}
		if event.ValidationID != validationID {
			return backtest.Summary{}, fmt.Errorf("paper equity summary failed: event[%d] validation_id mismatch", index)
		}
		if _, exists := seenEvents[event.EventID]; exists {
			return backtest.Summary{}, fmt.Errorf("paper equity summary failed: duplicate event_id %q", event.EventID)
		}
		seenEvents[event.EventID] = struct{}{}
		if _, exists := seenCloses[event.CloseID]; exists {
			return backtest.Summary{}, fmt.Errorf("paper equity summary failed: duplicate close_id %q", event.CloseID)
		}
		seenCloses[event.CloseID] = struct{}{}
		if _, exists := seenPositions[event.PositionID]; exists {
			return backtest.Summary{}, fmt.Errorf("paper equity summary failed: duplicate position_id %q", event.PositionID)
		}
		seenPositions[event.PositionID] = struct{}{}
		if index > 0 {
			if event.Sequence != previous.Sequence+1 {
				return backtest.Summary{}, fmt.Errorf("paper equity summary failed: event[%d] sequence must follow previous event", index)
			}
			if event.OccurredAt.Before(previous.OccurredAt) {
				return backtest.Summary{}, fmt.Errorf("paper equity summary failed: event[%d] occurred_at is not monotonic", index)
			}
		}
		if !event.EquityBefore.Equal(equity) {
			return backtest.Summary{}, fmt.Errorf("paper equity summary failed: event[%d] breaks equity continuity", index)
		}
		summary.TotalFees = summary.TotalFees.Add(event.Fees)
		summary.NetPnL = summary.NetPnL.Add(event.NetPnL)
		switch {
		case event.NetPnL.GreaterThan(decimal.Zero):
			summary.Wins++
			summary.GrossProfit = summary.GrossProfit.Add(event.NetPnL)
		case event.NetPnL.LessThan(decimal.Zero):
			summary.Losses++
			summary.GrossLoss = summary.GrossLoss.Add(event.NetPnL.Abs())
		default:
			summary.Breakeven++
		}

		summary.FinalEquity = event.EquityAfter
		equity = event.EquityAfter
		if summary.FinalEquity.GreaterThan(peak) {
			peak = summary.FinalEquity
		}
		if peak.GreaterThan(decimal.Zero) {
			drawdown := peak.Sub(summary.FinalEquity).Div(peak)
			if drawdown.GreaterThan(summary.MaxDrawdown) {
				summary.MaxDrawdown = drawdown
			}
		}
		previous = event
	}
	if summary.Trades > 0 {
		tradeCount := decimal.NewFromInt(int64(summary.Trades))
		summary.Expectancy = summary.NetPnL.Div(tradeCount)
		summary.WinRate = decimal.NewFromInt(int64(summary.Wins)).Div(tradeCount)
	}
	if summary.GrossLoss.GreaterThan(decimal.Zero) {
		summary.ProfitFactor = summary.GrossProfit.Div(summary.GrossLoss)
		summary.ProfitFactorDefined = true
	}
	return summary, nil
}

func BuildDailyEquityPerformance(validationID string, initialEquity decimal.Decimal, events []EquityEvent, calculatedAt time.Time) ([]DailyPerformance, error) {
	validationID = strings.TrimSpace(validationID)
	if validationID == "" {
		return nil, errors.New("paper daily equity performance failed: validation_id is required")
	}
	if calculatedAt.IsZero() {
		return nil, errors.New("paper daily equity performance failed: calculated_at is required")
	}
	calculatedAt = calculatedAt.UTC()
	if err := ValidateEquityEventSequence(validationID, initialEquity, events); err != nil {
		return nil, err
	}
	if len(events) == 0 {
		return nil, nil
	}

	ordered := orderEquityEvents(events)
	for index, event := range ordered {
		if event.OccurredAt.After(calculatedAt) {
			return nil, fmt.Errorf("paper daily equity performance failed: event[%d] occurs after calculated_at", index)
		}
	}

	var out []DailyPerformance
	for start := 0; start < len(ordered); {
		day := utcDay(ordered[start].OccurredAt)
		end := start + 1
		for end < len(ordered) && utcDay(ordered[end].OccurredAt).Equal(day) {
			end++
		}
		summary, err := summarizeEquityEventWindow(validationID, ordered[start].EquityBefore, ordered[start:end])
		if err != nil {
			return nil, fmt.Errorf("paper daily equity performance failed for %s: %w", day.Format(time.DateOnly), err)
		}
		if !summary.FinalEquity.Equal(ordered[end-1].EquityAfter) {
			return nil, fmt.Errorf("paper daily equity performance failed for %s: final equity mismatch", day.Format(time.DateOnly))
		}
		out = append(out, DailyPerformance{
			ValidationID: validationID,
			Day:          day,
			Summary:      summary,
			CalculatedAt: calculatedAt,
		})
		start = end
	}
	return out, nil
}

func orderEquityEvents(events []EquityEvent) []EquityEvent {
	ordered := append([]EquityEvent(nil), events...)
	slices.SortStableFunc(ordered, func(left, right EquityEvent) int {
		if left.Sequence != right.Sequence {
			return left.Sequence - right.Sequence
		}
		return strings.Compare(left.EventID, right.EventID)
	})
	return ordered
}
