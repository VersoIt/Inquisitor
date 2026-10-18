package testutil

import (
	"testing"
	"time"

	"github.com/VersoIt/Inquisitor/internal/regime"
	"github.com/VersoIt/Inquisitor/internal/risk"
	"github.com/VersoIt/Inquisitor/internal/strategy"
	"github.com/shopspring/decimal"
)

type PaperRiskFixture struct {
	Approval ApprovalFixture
	Signal   strategy.SignalAuditRecord
	Regime   regime.State
	Input    risk.EvaluationInput
	Policy   risk.Policy
}

func PaperRisk(t testing.TB) PaperRiskFixture {
	t.Helper()
	f := PaperRiskFixture{Approval: PaperApproval(t)}
	now, grant := f.Approval.Now, f.Approval.Grant
	f.Regime = regime.State{Exchange: grant.Exchange, Category: grant.Category, Symbol: grant.Symbol, Interval: grant.Interval,
		Regime: regime.RegimeTrendUp, CandidateRegime: regime.RegimeTrendUp, Confidence: 80,
		OpenTime: now.Add(-time.Minute - time.Second), CloseTime: now.Add(-time.Second), CalculatedAt: now.Add(-time.Second)}
	authorization := strategy.PaperAuthorization{Grant: grant, Evidence: f.Approval.Evidence}
	approval, err := strategy.ResolvePaperAuthorization(authorization, f.Approval.Policy, risk.ModePaper, grant.Symbol, grant.Interval, f.Regime.Regime, now)
	if err != nil {
		t.Fatal(err)
	}
	trade, err := strategy.NewTradeSignal(strategy.TradeSignalInput{SignalID: "paper_risk_test_signal", HypothesisID: grant.HypothesisName, HypothesisVersion: grant.HypothesisVersion, StrategyName: grant.StrategyName,
		Symbol: grant.Symbol, Side: strategy.SideLong, Confidence: 80, EntryPrice: decimal.NewFromInt(100), StopLoss: decimal.NewFromInt(96), TakeProfit: decimal.NewFromInt(108), Leverage: decimal.NewFromInt(1),
		HypothesisApprovedForMode: true, HypothesisMaxRiskPct: approval.MaxRiskPct, HypothesisMinConfidence: approval.MinConfidence, Reason: "trend rules passed", ObservedAt: f.Regime.CloseTime, CreatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	market := strategy.MarketContext{SignalID: trade.SignalID, Now: now, ObservedAt: trade.ObservedAt, Mode: risk.ModePaper, TradingEnabled: true, AllowShort: true, EntryPrice: trade.EntryPrice, Regime: f.Regime, Approval: approval}
	f.Signal, err = strategy.NewSignalAuditRecord(strategy.SignalAuditRecord{Signal: strategy.Signal{SignalID: trade.SignalID, HypothesisID: trade.HypothesisID, HypothesisVersion: trade.HypothesisVersion,
		StrategyName: trade.StrategyName, Symbol: trade.Symbol, Interval: grant.Interval, Type: strategy.SignalBuy, Reason: trade.Reason, CreatedAt: trade.CreatedAt, Trade: &trade},
		Mode: risk.ModePaper, Exchange: grant.Exchange, Category: grant.Category, ObservedAt: trade.ObservedAt, Features: strategy.SignalEvidence(market), RecordedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	intentID, _ := strategy.PaperRiskIDs(trade.SignalID)
	intent, err := trade.RiskIntent(intentID)
	if err != nil {
		t.Fatal(err)
	}
	f.Input = risk.EvaluationInput{Intent: intent, EvaluatedAt: now, Runtime: risk.RuntimeState{TradingEnabled: true, Mode: risk.ModePaper},
		Account: risk.AccountState{Equity: decimal.NewFromInt(1000), DayStartEquity: decimal.NewFromInt(1000), WeekStartEquity: decimal.NewFromInt(1000), PeakEquity: decimal.NewFromInt(1000)},
		Market: risk.MarketContext{DataTime: trade.ObservedAt, SymbolAllowed: true, SpreadBPS: decimal.NewFromInt(2), ExpectedSlippageBPS: decimal.NewFromInt(3), VolatilityAcceptable: true, OrderbookValid: true, LiquidityQuote: decimal.NewFromInt(200000),
			Instrument: risk.Instrument{Available: true, Symbol: grant.Symbol, TickSize: decimal.RequireFromString("0.5"), QuantityStep: decimal.RequireFromString("0.01"), MinOrderQuantity: decimal.RequireFromString("0.01"), MaxOrderQuantity: decimal.NewFromInt(100), MinNotional: decimal.NewFromInt(5)}}}
	f.Policy = risk.Policy{AllowedMode: risk.ModePaper, AllowShort: true, RiskPerTradePct: decimal.RequireFromString("0.25"), MaxDailyLossPct: decimal.NewFromInt(1), MaxWeeklyLossPct: decimal.NewFromInt(3), MaxTotalDrawdownPct: decimal.NewFromInt(8),
		MaxLosingStreak: 5, MaxOpenPositions: 2, MaxLeverage: decimal.NewFromInt(1), MaxSpreadBPS: decimal.NewFromInt(5), MaxSlippageBPS: decimal.NewFromInt(10), MinConfidence: 70, MinLiquidityQuote: decimal.NewFromInt(100000),
		MaxPortfolioExposurePct: decimal.NewFromInt(30), MaxCorrelatedExposurePct: decimal.NewFromInt(20), MaxDataAge: 3 * time.Second, AllowedSymbols: []string{grant.Symbol}}
	return f
}

func (f PaperRiskFixture) Evaluation(t testing.TB) strategy.PaperRiskEvaluation {
	t.Helper()
	engine, err := risk.NewTradeRiskEngine(f.Policy)
	if err != nil {
		t.Fatal(err)
	}
	decision, err := engine.Evaluate(f.Input)
	if err != nil {
		t.Fatal(err)
	}
	_, id := strategy.PaperRiskIDs(f.Signal.Signal.SignalID)
	audit, err := risk.NewDecisionAuditRecord(risk.DecisionAuditInput{DecisionID: id, Decision: decision, Intent: f.Input.Intent, Runtime: f.Input.Runtime, RecordedAt: f.Input.EvaluatedAt})
	if err != nil {
		t.Fatal(err)
	}
	e := strategy.PaperRiskEvaluation{SignalID: f.Signal.Signal.SignalID, ApprovalID: f.Approval.Grant.ApprovalID, Context: strategy.PaperRiskContext{Regime: f.Regime, Input: f.Input, Policy: f.Policy}, Audit: audit}
	if err := strategy.ValidatePaperRiskEvaluation(e); err != nil {
		t.Fatal(err)
	}
	return e
}
