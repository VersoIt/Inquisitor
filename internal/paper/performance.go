package paper

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/VersoIt/Inquisitor/internal/backtest"
)

type DailyPerformance struct {
	ValidationID string
	Day          time.Time
	Summary      backtest.Summary
	CalculatedAt time.Time
}

type DailyPerformanceStats struct {
	Inserted int
	Updated  int
}

type DailyPerformanceQuery struct {
	ValidationID string
	Start        time.Time
	End          time.Time
	Limit        int
}

type DailyPerformanceRepository interface {
	RecordDailyPerformance(ctx context.Context, performance []DailyPerformance) (DailyPerformanceStats, error)
	ListDailyPerformance(ctx context.Context, query DailyPerformanceQuery) ([]DailyPerformance, error)
}

func (s DailyPerformanceStats) Total() int {
	return s.Inserted + s.Updated
}

func BuildDailyPerformance(validationID string, initialBalance decimal.Decimal, trades []ValidationTrade, calculatedAt time.Time) ([]DailyPerformance, error) {
	validationID = strings.TrimSpace(validationID)
	if validationID == "" {
		return nil, errors.New("paper daily performance failed: validation_id is required")
	}
	if initialBalance.LessThanOrEqual(decimal.Zero) {
		return nil, errors.New("paper daily performance failed: initial_balance must be positive")
	}
	calculatedAt = calculatedAt.UTC()
	if calculatedAt.IsZero() {
		return nil, errors.New("paper daily performance failed: calculated_at is required")
	}
	if len(trades) == 0 {
		return nil, nil
	}

	ordered := append([]ValidationTrade(nil), trades...)
	slices.SortStableFunc(ordered, func(left, right ValidationTrade) int {
		if comparison := left.RoundTrip.Exit.Time.Compare(right.RoundTrip.Exit.Time); comparison != 0 {
			return comparison
		}
		return strings.Compare(left.TradeID, right.TradeID)
	})
	seen := make(map[string]struct{}, len(ordered))
	equity := initialBalance
	for index, trade := range ordered {
		if err := ValidateValidationTrade(trade); err != nil {
			return nil, fmt.Errorf("paper daily performance failed: trade[%d]: %w", index, err)
		}
		if trade.ValidationID != validationID {
			return nil, fmt.Errorf("paper daily performance failed: trade[%d] validation_id mismatch", index)
		}
		if trade.RoundTrip.Exit.Time.After(calculatedAt) {
			return nil, fmt.Errorf("paper daily performance failed: trade[%d] exits after calculated_at", index)
		}
		if _, exists := seen[trade.TradeID]; exists {
			return nil, fmt.Errorf("paper daily performance failed: duplicate trade_id %q", trade.TradeID)
		}
		seen[trade.TradeID] = struct{}{}
		if !trade.EquityBefore.Equal(equity) {
			return nil, fmt.Errorf("paper daily performance failed: trade[%d] breaks equity continuity", index)
		}
		equity = trade.EquityAfter
	}

	var out []DailyPerformance
	for start := 0; start < len(ordered); {
		day := utcDay(ordered[start].RoundTrip.Exit.Time)
		end := start + 1
		for end < len(ordered) && utcDay(ordered[end].RoundTrip.Exit.Time).Equal(day) {
			end++
		}
		roundTrips := make([]backtest.RoundTrip, 0, end-start)
		for _, trade := range ordered[start:end] {
			roundTrips = append(roundTrips, trade.RoundTrip)
		}
		summary, err := backtest.SummarizeRoundTrips(ordered[start].EquityBefore, roundTrips)
		if err != nil {
			return nil, fmt.Errorf("paper daily performance failed for %s: %w", day.Format(time.DateOnly), err)
		}
		if !summary.FinalEquity.Equal(ordered[end-1].EquityAfter) {
			return nil, fmt.Errorf("paper daily performance failed for %s: final equity mismatch", day.Format(time.DateOnly))
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

func ValidateDailyPerformance(performance DailyPerformance) error {
	var problems []string
	if strings.TrimSpace(performance.ValidationID) == "" {
		problems = append(problems, "validation_id is required")
	}
	if performance.Day.IsZero() || !performance.Day.Equal(utcDay(performance.Day)) {
		problems = append(problems, "day must be UTC midnight")
	}
	if performance.CalculatedAt.IsZero() {
		problems = append(problems, "calculated_at is required")
	}
	if performance.Summary.Trades <= 0 {
		problems = append(problems, "trades must be positive")
	}
	if performance.Summary.Wins < 0 || performance.Summary.Losses < 0 || performance.Summary.Breakeven < 0 ||
		performance.Summary.Wins+performance.Summary.Losses+performance.Summary.Breakeven != performance.Summary.Trades {
		problems = append(problems, "trade outcome counts must equal trades")
	}
	if performance.Summary.InitialEquity.LessThanOrEqual(decimal.Zero) || performance.Summary.FinalEquity.IsNegative() {
		problems = append(problems, "equity values are invalid")
	}
	if !performance.Summary.FinalEquity.Equal(performance.Summary.InitialEquity.Add(performance.Summary.NetPnL)) {
		problems = append(problems, "final_equity must equal initial_equity plus net_pnl")
	}
	if performance.Summary.TotalFees.IsNegative() || performance.Summary.GrossProfit.IsNegative() || performance.Summary.GrossLoss.IsNegative() {
		problems = append(problems, "fees and gross totals must be non-negative")
	}
	if !performance.Summary.NetPnL.Equal(performance.Summary.GrossProfit.Sub(performance.Summary.GrossLoss)) {
		problems = append(problems, "net_pnl must equal gross_profit minus gross_loss")
	}
	if performance.Summary.Trades > 0 {
		tradeCount := decimal.NewFromInt(int64(performance.Summary.Trades))
		if !performance.Summary.Expectancy.Equal(performance.Summary.NetPnL.Div(tradeCount)) {
			problems = append(problems, "expectancy must equal net_pnl divided by trades")
		}
		if !performance.Summary.WinRate.Equal(decimal.NewFromInt(int64(performance.Summary.Wins)).Div(tradeCount)) {
			problems = append(problems, "win_rate must equal wins divided by trades")
		}
	}
	if performance.Summary.GrossLoss.GreaterThan(decimal.Zero) {
		if !performance.Summary.ProfitFactorDefined || !performance.Summary.ProfitFactor.Equal(performance.Summary.GrossProfit.Div(performance.Summary.GrossLoss)) {
			problems = append(problems, "profit_factor must equal gross_profit divided by gross_loss")
		}
	} else if performance.Summary.ProfitFactorDefined || !performance.Summary.ProfitFactor.IsZero() {
		problems = append(problems, "profit_factor must be undefined and zero without gross loss")
	}
	if performance.Summary.MaxDrawdown.IsNegative() || performance.Summary.MaxDrawdown.GreaterThan(decimal.NewFromInt(1)) {
		problems = append(problems, "max_drawdown must be between zero and one")
	}
	if performance.Summary.WinRate.IsNegative() || performance.Summary.WinRate.GreaterThan(decimal.NewFromInt(1)) {
		problems = append(problems, "win_rate must be between zero and one")
	}
	if len(problems) > 0 {
		return errors.New("paper daily performance validation failed: " + strings.Join(problems, "; "))
	}
	return nil
}

func ValidateDailyPerformanceRecords(records []DailyPerformance) error {
	for index, record := range records {
		if err := ValidateDailyPerformance(record); err != nil {
			return fmt.Errorf("paper_daily_performance[%d]: %w", index, err)
		}
	}
	return nil
}

func ValidateDailyPerformanceQuery(query DailyPerformanceQuery) error {
	if !query.Start.IsZero() && !query.Start.Equal(utcDay(query.Start)) {
		return errors.New("start must be UTC midnight")
	}
	if !query.End.IsZero() && !query.End.Equal(utcDay(query.End)) {
		return errors.New("end must be UTC midnight")
	}
	if !query.Start.IsZero() && !query.End.IsZero() && !query.End.After(query.Start) {
		return errors.New("end must be after start")
	}
	if query.Limit < 0 {
		return errors.New("limit must be greater than or equal to zero")
	}
	return nil
}

func utcDay(value time.Time) time.Time {
	value = value.UTC()
	return time.Date(value.Year(), value.Month(), value.Day(), 0, 0, 0, 0, time.UTC)
}
