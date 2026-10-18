package research

import (
	"context"
	"fmt"
	"time"

	"github.com/VersoIt/Inquisitor/internal/backtest"
	"github.com/VersoIt/Inquisitor/internal/features"
	"github.com/VersoIt/Inquisitor/internal/hypothesis"
	"github.com/VersoIt/Inquisitor/internal/marketdata"
	"github.com/VersoIt/Inquisitor/internal/regime"
	domain "github.com/VersoIt/Inquisitor/internal/research"
	"github.com/VersoIt/Inquisitor/internal/risk"
	"github.com/VersoIt/Inquisitor/internal/strategy"
	"github.com/shopspring/decimal"
)

type TrendExperimentTrade struct {
	SignalTime   time.Time
	Stop, Target decimal.Decimal
	ExitReason   string
	Trade        backtest.RoundTrip
}

type TrendExperimentPeriod struct {
	Start, End            time.Time
	Summary               backtest.Summary
	MarkedDrawdownPct     decimal.Decimal
	NoTrade, SkippedEntry int
	Trades                []TrendExperimentTrade
}

type TrendExperimentReport struct {
	ModelVersion                            string
	Plan                                    domain.TrendExperimentPlan
	PlanSHA256, CandlesSHA256, SourceSHA256 string
	CandleCount                             int
	TrendFeatures                           features.TrendFeatureConfig
	VolatilityFeatures                      features.VolatilityFeatureConfig
	Development, Validation, Stress         TrendExperimentPeriod
	ValidationBlocks                        []TrendExperimentPeriod
	BuyAndHold                              backtest.RoundTrip
	CashNetPnL                              decimal.Decimal
	Verdict                                 string
	Reasons, Limitations                    []string
}

// RunTrendExperiment is an isolated candle experiment. It never reads an account,
// persists an approval, or submits a paper/live order.
func RunTrendExperiment(ctx context.Context, p domain.TrendExperimentPlan, candles []marketdata.Candle) (TrendExperimentReport, error) {
	if err := ctx.Err(); err != nil {
		return TrendExperimentReport{}, err
	}
	if err := domain.ValidateExperimentCandles(p, candles); err != nil {
		return TrendExperimentReport{}, err
	}
	tcfg, vcfg := features.DefaultTrendFeatureConfig(), features.DefaultVolatilityFeatureConfig()
	t, err := features.ComputeTrendFeatures(candles, tcfg)
	if err != nil {
		return TrendExperimentReport{}, err
	}
	v, err := features.ComputeVolatilityFeatures(candles, vcfg)
	if err != nil {
		return TrendExperimentReport{}, err
	}
	first := int(p.DevelopmentStart.Sub(p.DataStart)/time.Hour) - 1
	if first < 1 || !t[first].Complete || !v[first].Complete {
		return TrendExperimentReport{}, fmt.Errorf("experiment warmup is insufficient")
	}
	selection := strategy.SelectionPolicy{MinConfidence: 70, MaxDataAge: time.Second}
	generator, err := strategy.NewTrendStrategy(strategy.TrendConfig{SelectionPolicy: selection, StopATRMultiple: p.StopATRMultiple, TakeProfitR: p.TakeProfitR})
	if err != nil {
		return TrendExperimentReport{}, err
	}
	signalAt := func(index int) (strategy.Signal, error) {
		c := candles[index]
		if index < 1 || !t[index].Complete || !v[index].Complete || t[index].ADX.LessThan(p.MinADX) {
			return strategy.Signal{Type: strategy.SignalNoTrade, Reason: "ohlc_adx_or_warmup"}, nil
		}
		// Research-only eligibility adapter. This does not claim historical book
		// quality, production regime confidence, or persisted strategy approval.
		m := strategy.MarketContext{SignalID: fmt.Sprintf("%s_%d", p.ID, c.OpenTime.Unix()), Now: c.CloseTime, ObservedAt: c.CloseTime, EntryPrice: c.Close, Mode: risk.ModePaper, TradingEnabled: true,
			Approval: strategy.ApprovalSnapshot{HypothesisID: "experiment:" + p.ID, Version: "1", StrategyName: strategy.NameTrend, Mode: risk.ModePaper, Direction: hypothesis.DirectionLong, Exchange: p.Exchange, Category: p.Category, Symbol: p.Symbol, Interval: p.Interval, MaxRiskPct: p.RiskPerTradePct, MinConfidence: 70, Enabled: true},
			Regime:   regime.State{Exchange: p.Exchange, Category: p.Category, Symbol: p.Symbol, Interval: p.Interval, OpenTime: c.OpenTime, CloseTime: c.CloseTime, CalculatedAt: c.CloseTime, Regime: regime.RegimeTrendUp, CandidateRegime: regime.RegimeTrendUp, Confidence: 70},
			Current:  c, Previous: candles[index-1], Trend: t[index], Volatility: v[index]}
		return generator.GenerateSignal(ctx, m)
	}
	report := TrendExperimentReport{ModelVersion: "trend-spot-candle-v1", Plan: p, CandleCount: len(candles), TrendFeatures: tcfg, VolatilityFeatures: vcfg,
		Limitations: []string{"OHLC-only ADX gate, not the production microstructure RegimeDetector", "next-open modeled fills, not historical quotes or queue simulation", "fixed spot fee/lot assumptions; no borrowing, funding, taxes or outage reconstruction", "bar-low marked drawdown does not reconstruct an unknown intrabar price path", "a research verdict grants no paper/live permission; no parameter optimization performed"}}
	report.PlanSHA256, err = domain.ExperimentHash(p)
	if err != nil {
		return TrendExperimentReport{}, err
	}
	report.CandlesSHA256, err = domain.ExperimentHash(candles)
	if err != nil {
		return TrendExperimentReport{}, err
	}
	if report.Development, err = replayTrendPeriod(ctx, p, candles, p.DevelopmentStart, p.ValidationStart, p.Costs, signalAt); err != nil {
		return TrendExperimentReport{}, err
	}
	if report.Validation, err = replayTrendPeriod(ctx, p, candles, p.ValidationStart, p.ValidationEnd, p.Costs, signalAt); err != nil {
		return TrendExperimentReport{}, err
	}
	stress := p.Costs
	two := decimal.NewFromInt(2)
	stress.MakerFeeBPS = stress.MakerFeeBPS.Mul(two)
	stress.TakerFeeBPS = stress.TakerFeeBPS.Mul(two)
	stress.SpreadBPS = stress.SpreadBPS.Mul(two)
	stress.SlippageBPS = stress.SlippageBPS.Mul(two)
	if report.Stress, err = replayTrendPeriod(ctx, p, candles, p.ValidationStart, p.ValidationEnd, stress, signalAt); err != nil {
		return TrendExperimentReport{}, err
	}
	width := p.ValidationEnd.Sub(p.ValidationStart) / 4
	for block := 0; block < 4; block++ {
		start := p.ValidationStart.Add(time.Duration(block) * width)
		r, err := replayTrendPeriod(ctx, p, candles, start, start.Add(width), p.Costs, signalAt)
		if err != nil {
			return TrendExperimentReport{}, err
		}
		report.ValidationBlocks = append(report.ValidationBlocks, r)
	}
	start := int(p.ValidationStart.Sub(p.DataStart) / time.Hour)
	unit, err := backtest.EvaluateFill(backtest.FillInput{Direction: backtest.DirectionLong, Role: backtest.FillRoleEntry, Time: p.ValidationStart, MidPrice: candles[start].Open, Quantity: decimal.NewFromInt(1), Liquidity: backtest.LiquidityTaker, Costs: p.Costs})
	if err != nil {
		return TrendExperimentReport{}, err
	}
	quantity := p.InitialEquity.Div(unit.ExecutedPrice.Add(unit.Fee)).Div(p.QuantityStep).Floor().Mul(p.QuantityStep)
	if !quantity.IsPositive() {
		return TrendExperimentReport{}, fmt.Errorf("buy/hold benchmark is below minimum quantity")
	}
	report.BuyAndHold, err = backtest.EvaluateRoundTrip(backtest.RoundTripInput{Direction: backtest.DirectionLong, EntryTime: p.ValidationStart, ExitTime: p.ValidationEnd, EntryMidPrice: candles[start].Open, ExitMidPrice: candles[len(candles)-1].Close, Quantity: quantity, Costs: p.Costs})
	if err != nil {
		return TrendExperimentReport{}, err
	}
	report.Verdict, report.Reasons = assessTrendExperiment(report)
	return report, nil
}

type experimentPosition struct {
	signalTime, entryTime            time.Time
	entryMid, quantity, stop, target decimal.Decimal
	index                            int
}

func replayTrendPeriod(ctx context.Context, p domain.TrendExperimentPlan, candles []marketdata.Candle, start, end time.Time, costs backtest.CostModel, signalAt func(int) (strategy.Signal, error)) (TrendExperimentPeriod, error) {
	r := TrendExperimentPeriod{Start: start, End: end, Trades: []TrendExperimentTrade{}}
	equity, peak := p.InitialEquity, p.InitialEquity
	var position *experimentPosition
	mark := func(value decimal.Decimal) {
		if value.GreaterThan(peak) {
			peak = value
		}
		dd := peak.Sub(value).Div(peak).Mul(decimal.NewFromInt(100))
		if dd.GreaterThan(r.MarkedDrawdownPct) {
			r.MarkedDrawdownPct = dd
		}
	}
	for index, c := range candles {
		if err := ctx.Err(); err != nil {
			return TrendExperimentPeriod{}, err
		}
		if c.OpenTime.Before(start) || !c.OpenTime.Before(end) {
			continue
		}
		if position == nil && index > 1 && equity.IsPositive() {
			signal, err := signalAt(index - 1)
			if err != nil {
				return TrendExperimentPeriod{}, err
			}
			if signal.Trade == nil {
				r.NoTrade++
			} else {
				t := signal.Trade
				if t.Side != strategy.SideLong || !t.CreatedAt.Equal(c.OpenTime) || !c.Open.GreaterThan(t.StopLoss) || !c.Open.LessThan(t.TakeProfit) {
					r.SkippedEntry++
				} else {
					q, err := domain.SpotExperimentQuantity(p, equity, c.Open, t.StopLoss, costs)
					if err != nil {
						return TrendExperimentPeriod{}, err
					}
					if !q.IsPositive() {
						r.SkippedEntry++
					} else {
						position = &experimentPosition{signalTime: t.CreatedAt, entryTime: c.OpenTime, entryMid: c.Open, quantity: q, stop: t.StopLoss, target: t.TakeProfit, index: index}
					}
				}
			}
		}
		if position == nil {
			continue
		}
		pos := position
		exit := domain.LongExperimentExit(c, pos.stop, pos.target, index-pos.index+1, p.MaxHoldingCandles, end)
		// Sample an adverse intrabar liquidation before the close. For a stopped
		// position, do not count candle prices reached after its modeled exit.
		worst := c.Low
		if exit.Reason == "stop" || exit.Reason == "stop_gap" || exit.Reason == "target_gap_capped" {
			worst = exit.MidPrice
		}
		liquidation, err := backtest.EvaluateRoundTrip(backtest.RoundTripInput{Direction: backtest.DirectionLong, EntryTime: pos.entryTime, ExitTime: c.CloseTime, EntryMidPrice: pos.entryMid, ExitMidPrice: worst, Quantity: pos.quantity, Costs: costs})
		if err != nil {
			return TrendExperimentPeriod{}, err
		}
		mark(equity.Add(liquidation.NetPnL))
		if exit.Triggered {
			trade, err := backtest.EvaluateRoundTrip(backtest.RoundTripInput{Direction: backtest.DirectionLong, EntryTime: pos.entryTime, ExitTime: exit.At, EntryMidPrice: pos.entryMid, ExitMidPrice: exit.MidPrice, Quantity: pos.quantity, Costs: costs})
			if err != nil {
				return TrendExperimentPeriod{}, err
			}
			r.Trades = append(r.Trades, TrendExperimentTrade{SignalTime: pos.signalTime, Stop: pos.stop, Target: pos.target, ExitReason: exit.Reason, Trade: trade})
			equity = equity.Add(trade.NetPnL)
			mark(equity)
			position = nil
		} else {
			closeMark, err := backtest.EvaluateRoundTrip(backtest.RoundTripInput{Direction: backtest.DirectionLong, EntryTime: pos.entryTime, ExitTime: c.CloseTime, EntryMidPrice: pos.entryMid, ExitMidPrice: c.Close, Quantity: pos.quantity, Costs: costs})
			if err != nil {
				return TrendExperimentPeriod{}, err
			}
			mark(equity.Add(closeMark.NetPnL))
		}
	}
	if position != nil {
		return TrendExperimentPeriod{}, fmt.Errorf("experiment did not flatten at window end")
	}
	trades := make([]backtest.RoundTrip, len(r.Trades))
	for i, t := range r.Trades {
		trades[i] = t.Trade
	}
	var err error
	r.Summary, err = backtest.SummarizeRoundTrips(p.InitialEquity, trades)
	return r, err
}

func assessTrendExperiment(r TrendExperimentReport) (string, []string) {
	c := r.Plan.Criteria
	var reasons []string
	sampleOK := true
	if r.Validation.Summary.Trades < c.MinValidationTrades {
		sampleOK = false
		reasons = append(reasons, "insufficient_validation_trades")
	}
	profitable := 0
	for i, b := range r.ValidationBlocks {
		if b.Summary.Trades < c.MinBlockTrades {
			sampleOK = false
			reasons = append(reasons, fmt.Sprintf("block_%d_insufficient_trades", i+1))
		}
		if b.Summary.NetPnL.IsPositive() {
			profitable++
		}
	}
	if !r.Validation.Summary.NetPnL.IsPositive() {
		reasons = append(reasons, "validation_net_pnl_not_positive")
	}
	if !r.Validation.Summary.ProfitFactorDefined || r.Validation.Summary.ProfitFactor.LessThan(c.MinProfitFactor) {
		reasons = append(reasons, "validation_profit_factor_failed")
	}
	if r.Validation.MarkedDrawdownPct.GreaterThan(c.MaxMarkedDrawdownPct) {
		reasons = append(reasons, "validation_drawdown_failed")
	}
	if profitable < c.MinProfitableBlocks {
		reasons = append(reasons, "insufficient_profitable_blocks")
	}
	if !r.Stress.Summary.NetPnL.IsPositive() {
		reasons = append(reasons, "doubled_cost_stress_not_profitable")
	}
	if !sampleOK {
		return "INCONCLUSIVE", reasons
	}
	if len(reasons) > 0 {
		return "REJECTED", reasons
	}
	return "PAPER_RESEARCH_CANDIDATE", []string{"pre_registered_candle_gates_passed_not_live_permission"}
}
