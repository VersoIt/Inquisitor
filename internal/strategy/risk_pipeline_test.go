package strategy_test

import (
	"context"
	"testing"
	"time"

	apprisk "github.com/VersoIt/Inquisitor/internal/app/risk"
	appstrategy "github.com/VersoIt/Inquisitor/internal/app/strategy"
	"github.com/VersoIt/Inquisitor/internal/clock"
	"github.com/VersoIt/Inquisitor/internal/risk"
	"github.com/VersoIt/Inquisitor/internal/strategy"
	"github.com/shopspring/decimal"
)

// Evaluate the same generated proposal against changing execution conditions.
// Strategy approval must never substitute for the final Risk Engine decision.
func TestGeneratedStrategySignalsStillRequireCurrentRiskApproval(t *testing.T) {
	trend, err := strategy.NewTrendStrategy(trendConfig())
	if err != nil {
		t.Fatal(err)
	}
	mean := mustMeanReversion(t, meanReversionConfig())
	breakout := mustBreakout(t, breakoutConfig())
	for _, source := range []struct {
		name           string
		market         func() strategy.MarketContext
		implementation strategy.Strategy
		quantity, loss string
	}{
		{"trend long", trendMarket, trend, "0.62", "2.48"},
		{"trend short", shortTrendMarket, trend, "0.62", "2.48"},
		{"range long", rangeMarket, mean, "0.62", "2.48"},
		{"range short", shortRangeMarket, mean, "0.62", "2.48"},
		{"breakout long", breakoutMarket, breakout, "0.27", "2.43"},
		{"breakout short", shortBreakoutMarket, breakout, "0.27", "2.43"},
	} {
		t.Run(source.name, func(t *testing.T) {
			market := source.market()
			selector, err := strategy.NewSelector(selectionPolicy(), source.implementation)
			if err != nil {
				t.Fatal(err)
			}
			signal, err := selector.GenerateSignal(context.Background(), market)
			if err != nil || signal.Trade == nil {
				t.Fatalf("generate signal: %+v, %v", signal, err)
			}
			proposal, err := appstrategy.NewService().BuildRiskIntent(context.Background(), appstrategy.BuildRiskIntentRequest{IntentID: "intent-trend-1", Signal: *signal.Trade})
			if err != nil {
				t.Fatal(err)
			}
			policy := risk.Policy{
				AllowedMode: risk.ModePaper, AllowShort: true, RiskPerTradePct: decimal.RequireFromString("0.25"),
				MaxDailyLossPct: decimal.NewFromInt(1), MaxWeeklyLossPct: decimal.NewFromInt(3), MaxTotalDrawdownPct: decimal.NewFromInt(8),
				MaxLosingStreak: 5, MaxOpenPositions: 2, MaxLeverage: decimal.NewFromInt(1),
				MaxSpreadBPS: decimal.NewFromInt(5), MaxSlippageBPS: decimal.NewFromInt(10), MinConfidence: 70,
				MinLiquidityQuote: decimal.NewFromInt(100000), MaxPortfolioExposurePct: decimal.NewFromInt(30),
				MaxCorrelatedExposurePct: decimal.NewFromInt(20), MaxDataAge: 3 * time.Second, AllowedSymbols: []string{"BTCUSDT"},
			}
			engine, err := risk.NewTradeRiskEngine(policy)
			if err != nil {
				t.Fatal(err)
			}
			service := apprisk.NewService(engine, apprisk.WithClock(clock.FixedClock{Time: market.Now}))
			tests := []struct {
				name        string
				mutate      func(*apprisk.EvaluateRequest)
				failedCheck string
			}{
				{"valid proposal sized by risk", func(*apprisk.EvaluateRequest) {}, ""},
				{"kill switch activated after signal", func(r *apprisk.EvaluateRequest) { r.Runtime.KillSwitchActive = true }, "kill_switch_inactive"},
				{"trading disabled after signal", func(r *apprisk.EvaluateRequest) { r.Runtime.TradingEnabled = false }, "trading_enabled"},
				{"data becomes stale", func(r *apprisk.EvaluateRequest) { r.Market.DataTime = market.Now.Add(-4 * time.Second) }, "data_fresh"},
				{"spread widens", func(r *apprisk.EvaluateRequest) { r.Market.SpreadBPS = decimal.NewFromInt(6) }, "spread_acceptable"},
				{"daily budget exhausted", func(r *apprisk.EvaluateRequest) { r.Account.DailyLoss = decimal.NewFromInt(10) }, "daily_loss_available"},
				{"instrument unavailable", func(r *apprisk.EvaluateRequest) { r.Market.Instrument.Available = false }, "instrument_available"},
			}
			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					req := apprisk.EvaluateRequest{
						Intent:  proposal.Intent,
						Account: risk.AccountState{Equity: decimal.NewFromInt(1000), DayStartEquity: decimal.NewFromInt(1000), WeekStartEquity: decimal.NewFromInt(1000), PeakEquity: decimal.NewFromInt(1000)},
						Market: risk.MarketContext{
							DataTime: market.ObservedAt, SymbolAllowed: true, SpreadBPS: decimal.NewFromInt(2), ExpectedSlippageBPS: decimal.NewFromInt(3),
							VolatilityAcceptable: true, OrderbookValid: true, LiquidityQuote: decimal.NewFromInt(200000),
							Instrument: risk.Instrument{Available: true, Symbol: "BTCUSDT", TickSize: decimal.RequireFromString("0.5"), QuantityStep: decimal.RequireFromString("0.01"), MinOrderQuantity: decimal.RequireFromString("0.01"), MaxOrderQuantity: decimal.NewFromInt(100), MinNotional: decimal.NewFromInt(5)},
						},
						Runtime: risk.RuntimeState{TradingEnabled: true, Mode: risk.ModePaper},
					}
					tt.mutate(&req)
					got, err := service.Evaluate(context.Background(), req)
					if err != nil {
						t.Fatal(err)
					}
					if tt.failedCheck == "" {
						if !got.Approved || !got.FinalQuantity.Equal(decimal.RequireFromString(source.quantity)) || !got.MaxLoss.Equal(decimal.RequireFromString(source.loss)) {
							t.Fatalf("expected rounded, risk-sized approval: %+v", got)
						}
						return
					}
					if got.Approved || !got.FinalQuantity.IsZero() || !got.MaxLoss.IsZero() {
						t.Fatalf("unsafe approval: %+v", got)
					}
					for _, check := range got.Checks {
						if check.Name == tt.failedCheck && !check.Passed && check.Reason != "" {
							return
						}
					}
					t.Fatalf("missing failed %q check: %+v", tt.failedCheck, got.Checks)
				})
			}
		})
	}
}
