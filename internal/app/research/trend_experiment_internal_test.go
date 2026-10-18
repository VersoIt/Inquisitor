package research

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/VersoIt/Inquisitor/internal/backtest"
	"github.com/VersoIt/Inquisitor/internal/marketdata"
	domain "github.com/VersoIt/Inquisitor/internal/research"
	"github.com/VersoIt/Inquisitor/internal/strategy"
	"github.com/shopspring/decimal"
)

func TestTrendReplayUsesOnlyPreviousSignalAndNextOpen(t *testing.T) {
	at := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	var candles []marketdata.Candle
	for i := 0; i < 5; i++ {
		start := at.Add(time.Duration(i) * time.Hour)
		candles = append(candles, marketdata.Candle{Exchange: "bybit", Category: "spot", Symbol: "BTCUSDT", Interval: "60", OpenTime: start, CloseTime: start.Add(time.Hour), Open: decimal.NewFromInt(100), High: decimal.NewFromInt(105), Low: decimal.NewFromInt(99), Close: decimal.NewFromInt(100), IsClosed: true})
	}
	candles[2].Open = decimal.NewFromInt(110)
	candles[2].High = decimal.NewFromInt(125)
	candles[2].Low = decimal.NewFromInt(109)
	costs, _ := backtest.NewCostModel(0, 0, 0, 0, 1)
	p := domain.TrendExperimentPlan{InitialEquity: decimal.NewFromInt(1000), RiskPerTradePct: decimal.RequireFromString("0.25"), QuantityStep: decimal.RequireFromString("0.001"), MinNotional: decimal.NewFromInt(1), DevelopmentStart: at, MaxHoldingCandles: 48}
	var indices []int
	signalAt := func(index int) (strategy.Signal, error) {
		indices = append(indices, index)
		if index != 1 {
			return strategy.Signal{Type: strategy.SignalNoTrade}, nil
		}
		return strategy.Signal{Type: strategy.SignalBuy, Trade: &strategy.TradeSignal{Side: strategy.SideLong, CreatedAt: candles[1].CloseTime, StopLoss: decimal.NewFromInt(90), TakeProfit: decimal.NewFromInt(120)}}, nil
	}
	r, err := replayTrendPeriod(context.Background(), p, candles, candles[2].OpenTime, candles[4].CloseTime, costs, signalAt)
	if err != nil || len(r.Trades) != 1 {
		t.Fatalf("replay %+v %v", r, err)
	}
	trade := r.Trades[0]
	if !trade.Trade.Entry.MidPrice.Equal(decimal.NewFromInt(110)) || !trade.Trade.Entry.Quantity.Equal(decimal.RequireFromString("0.125")) || trade.ExitReason != "target" || !trade.Trade.NetPnL.Equal(decimal.RequireFromString("1.25")) || !trade.SignalTime.Equal(candles[2].OpenTime) || indices[0] != 1 {
		t.Fatalf("next-open semantics broken %+v indices=%v", trade, indices)
	}
	// Changing future bars cannot retroactively alter the already closed first trade.
	candles[4].Close = decimal.NewFromInt(999)
	r2, err := replayTrendPeriod(context.Background(), p, candles, candles[2].OpenTime, candles[4].CloseTime, costs, signalAt)
	if err != nil || !r2.Trades[0].Trade.NetPnL.Equal(trade.Trade.NetPnL) {
		t.Fatal("future prices changed earlier trade")
	}
}

func TestTrendReplaySinglePositionTerminalExitAndFailures(t *testing.T) {
	at := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	var candles []marketdata.Candle
	for i := 0; i < 6; i++ {
		c := marketdata.Candle{OpenTime: at.Add(time.Duration(i) * time.Hour), CloseTime: at.Add(time.Duration(i+1) * time.Hour), Open: decimal.NewFromInt(100), High: decimal.NewFromInt(105), Low: decimal.NewFromInt(95), Close: decimal.NewFromInt(101)}
		candles = append(candles, c)
	}
	costs, _ := backtest.NewCostModel(0, 0, 0, 0, 1)
	p := domain.TrendExperimentPlan{InitialEquity: decimal.NewFromInt(1000), RiskPerTradePct: decimal.RequireFromString("0.25"), QuantityStep: decimal.RequireFromString("0.001"), MinNotional: decimal.NewFromInt(1), DevelopmentStart: at, MaxHoldingCandles: 2}
	calls := 0
	signalAt := func(index int) (strategy.Signal, error) {
		calls++
		return strategy.Signal{Trade: &strategy.TradeSignal{Side: strategy.SideLong, CreatedAt: candles[index].CloseTime, StopLoss: decimal.NewFromInt(90), TakeProfit: decimal.NewFromInt(120)}}, nil
	}
	r, err := replayTrendPeriod(context.Background(), p, candles, candles[2].OpenTime, candles[5].CloseTime, costs, signalAt)
	if err != nil || len(r.Trades) != 2 || calls != 2 || r.Trades[0].Trade.Exit.Time.After(r.Trades[1].Trade.Entry.Time) {
		t.Fatalf("overlapping entries %+v %v calls=%d", r, err, calls)
	}
	p.MaxHoldingCandles = 48
	r, err = replayTrendPeriod(context.Background(), p, candles, candles[2].OpenTime, candles[5].CloseTime, costs, signalAt)
	if err != nil || len(r.Trades) != 1 || r.Trades[0].ExitReason != "window_end" || !r.Trades[0].Trade.Exit.Time.Equal(candles[5].CloseTime) || !r.MarkedDrawdownPct.IsPositive() {
		t.Fatalf("terminal exit %+v %v", r, err)
	}
	failure := errors.New("generator failed")
	if _, err := replayTrendPeriod(context.Background(), p, candles, candles[2].OpenTime, candles[5].CloseTime, costs, func(int) (strategy.Signal, error) { return strategy.Signal{}, failure }); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := replayTrendPeriod(ctx, p, candles, candles[2].OpenTime, candles[5].CloseTime, costs, signalAt); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestExperimentVerdictUsesPreRegisteredGatesNotDevelopmentProfit(t *testing.T) {
	base := func() TrendExperimentReport {
		r := TrendExperimentReport{Plan: domain.TrendExperimentPlan{Criteria: domain.TrendExperimentCriteria{MinValidationTrades: 100, MinBlockTrades: 20, MinProfitableBlocks: 3, MinProfitFactor: decimal.RequireFromString("1.15"), MaxMarkedDrawdownPct: decimal.NewFromInt(15)}}}
		r.Validation.Summary = backtest.Summary{Trades: 100, NetPnL: decimal.NewFromInt(1), ProfitFactor: decimal.RequireFromString("1.15"), ProfitFactorDefined: true}
		r.Validation.MarkedDrawdownPct = decimal.NewFromInt(15)
		r.Stress.Summary.NetPnL = decimal.NewFromInt(1)
		for i := 0; i < 4; i++ {
			r.ValidationBlocks = append(r.ValidationBlocks, TrendExperimentPeriod{Summary: backtest.Summary{Trades: 20, NetPnL: decimal.NewFromInt(1)}})
		}
		return r
	}
	for _, tt := range []struct {
		name    string
		change  func(*TrendExperimentReport)
		verdict string
	}{
		{"inclusive thresholds", func(*TrendExperimentReport) {}, "PAPER_RESEARCH_CANDIDATE"},
		{"too few held-out trades", func(r *TrendExperimentReport) { r.Validation.Summary.Trades = 99 }, "INCONCLUSIVE"},
		{"empty chronological block", func(r *TrendExperimentReport) { r.ValidationBlocks[1].Summary.Trades = 0 }, "INCONCLUSIVE"},
		{"OOS loss despite development win", func(r *TrendExperimentReport) {
			r.Development.Summary.NetPnL = decimal.NewFromInt(1000)
			r.Validation.Summary.NetPnL = decimal.NewFromInt(-1)
		}, "REJECTED"},
		{"undefined profit factor", func(r *TrendExperimentReport) { r.Validation.Summary.ProfitFactorDefined = false }, "REJECTED"},
		{"low profit factor", func(r *TrendExperimentReport) {
			r.Validation.Summary.ProfitFactor = decimal.RequireFromString("1.14999")
		}, "REJECTED"},
		{"drawdown limit", func(r *TrendExperimentReport) { r.Validation.MarkedDrawdownPct = decimal.RequireFromString("15.00001") }, "REJECTED"},
		{"fragile costs", func(r *TrendExperimentReport) { r.Stress.Summary.NetPnL = decimal.Zero }, "REJECTED"},
		{"concentrated profit", func(r *TrendExperimentReport) {
			r.ValidationBlocks[0].Summary.NetPnL = decimal.NewFromInt(-1)
			r.ValidationBlocks[1].Summary.NetPnL = decimal.NewFromInt(-1)
		}, "REJECTED"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := base()
			tt.change(&r)
			got, reasons := assessTrendExperiment(r)
			if got != tt.verdict || len(reasons) == 0 {
				t.Fatalf("verdict=%s reasons=%v", got, reasons)
			}
		})
	}
}
