package backtest

import (
	"errors"
	"strings"

	"github.com/shopspring/decimal"
)

type Summary struct {
	Trades    int
	Wins      int
	Losses    int
	Breakeven int

	GrossProfit decimal.Decimal
	GrossLoss   decimal.Decimal
	TotalFees   decimal.Decimal
	NetPnL      decimal.Decimal
	Expectancy  decimal.Decimal

	ProfitFactor        decimal.Decimal
	ProfitFactorDefined bool
	WinRate             decimal.Decimal
	MaxDrawdown         decimal.Decimal
	FinalEquity         decimal.Decimal
}

func SummarizeRoundTrips(initialEquity decimal.Decimal, trades []RoundTrip) (Summary, error) {
	if initialEquity.LessThanOrEqual(decimal.Zero) {
		return Summary{}, errors.New("backtest summary validation failed: initial_equity must be positive")
	}

	summary := Summary{
		Trades:      len(trades),
		FinalEquity: initialEquity,
	}
	peak := initialEquity
	for index, trade := range trades {
		if err := ValidateRoundTrip(trade); err != nil {
			return Summary{}, errors.New("backtest summary validation failed: trade[" + decimal.NewFromInt(int64(index)).String() + "] " + err.Error())
		}

		summary.TotalFees = summary.TotalFees.Add(trade.Fees)
		summary.NetPnL = summary.NetPnL.Add(trade.NetPnL)
		switch {
		case trade.NetPnL.GreaterThan(decimal.Zero):
			summary.Wins++
			summary.GrossProfit = summary.GrossProfit.Add(trade.NetPnL)
		case trade.NetPnL.LessThan(decimal.Zero):
			summary.Losses++
			summary.GrossLoss = summary.GrossLoss.Add(trade.NetPnL.Abs())
		default:
			summary.Breakeven++
		}

		summary.FinalEquity = summary.FinalEquity.Add(trade.NetPnL)
		if summary.FinalEquity.GreaterThan(peak) {
			peak = summary.FinalEquity
		}
		if peak.GreaterThan(decimal.Zero) {
			drawdown := peak.Sub(summary.FinalEquity).Div(peak)
			if drawdown.GreaterThan(summary.MaxDrawdown) {
				summary.MaxDrawdown = drawdown
			}
		}
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

func ValidateRoundTrip(trade RoundTrip) error {
	var problems []string
	if !KnownDirection(trade.Direction) {
		problems = append(problems, "direction must be LONG or SHORT")
	}
	if trade.Entry.ExecutedPrice.LessThanOrEqual(decimal.Zero) {
		problems = append(problems, "entry.executed_price must be positive")
	}
	if trade.Exit.ExecutedPrice.LessThanOrEqual(decimal.Zero) {
		problems = append(problems, "exit.executed_price must be positive")
	}
	if trade.Entry.Quantity.LessThanOrEqual(decimal.Zero) {
		problems = append(problems, "entry.quantity must be positive")
	}
	if trade.Exit.Quantity.LessThanOrEqual(decimal.Zero) {
		problems = append(problems, "exit.quantity must be positive")
	}
	if !trade.Entry.Quantity.Equal(trade.Exit.Quantity) {
		problems = append(problems, "entry and exit quantity must match")
	}
	if trade.Fees.IsNegative() {
		problems = append(problems, "fees must be greater than or equal to zero")
	}
	if !trade.NetPnL.Equal(trade.GrossPnL.Sub(trade.Fees)) {
		problems = append(problems, "net_pnl must equal gross_pnl minus fees")
	}
	if len(problems) > 0 {
		return errors.New("round trip validation failed: " + strings.Join(problems, "; "))
	}
	return nil
}
