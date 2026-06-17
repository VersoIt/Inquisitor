package backtest

import (
	"errors"
	"strings"
	"time"

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
	InitialEquity       decimal.Decimal
	FinalEquity         decimal.Decimal
}

type SplitSummary struct {
	SplitTime   time.Time
	InSample    Summary
	OutOfSample Summary
}

func SummarizeRoundTrips(initialEquity decimal.Decimal, trades []RoundTrip) (Summary, error) {
	if initialEquity.LessThanOrEqual(decimal.Zero) {
		return Summary{}, errors.New("backtest summary validation failed: initial_equity must be positive")
	}

	summary := Summary{
		Trades:        len(trades),
		InitialEquity: initialEquity,
		FinalEquity:   initialEquity,
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

func SummarizeRoundTripsBySplit(initialEquity decimal.Decimal, trades []RoundTrip, splitTime time.Time) (SplitSummary, error) {
	if splitTime.IsZero() {
		return SplitSummary{}, errors.New("backtest split summary validation failed: split_time is required")
	}
	if initialEquity.LessThanOrEqual(decimal.Zero) {
		return SplitSummary{}, errors.New("backtest split summary validation failed: initial_equity must be positive")
	}

	var inSample []RoundTrip
	var outOfSample []RoundTrip
	for index, trade := range trades {
		if err := ValidateRoundTrip(trade); err != nil {
			return SplitSummary{}, errors.New("backtest split summary validation failed: trade[" + decimal.NewFromInt(int64(index)).String() + "] " + err.Error())
		}
		if trade.Entry.Time.Before(splitTime.UTC()) {
			inSample = append(inSample, trade)
			continue
		}
		outOfSample = append(outOfSample, trade)
	}

	inSampleSummary, err := SummarizeRoundTrips(initialEquity, inSample)
	if err != nil {
		return SplitSummary{}, err
	}
	outOfSampleSummary, err := SummarizeRoundTrips(initialEquity, outOfSample)
	if err != nil {
		return SplitSummary{}, err
	}
	return SplitSummary{
		SplitTime:   splitTime.UTC(),
		InSample:    inSampleSummary,
		OutOfSample: outOfSampleSummary,
	}, nil
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
