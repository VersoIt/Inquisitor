package testutil

import (
	"testing"
	"time"

	"github.com/VersoIt/Inquisitor/internal/hypothesis"
	"github.com/VersoIt/Inquisitor/internal/paper"
	"github.com/VersoIt/Inquisitor/internal/research"
	"github.com/VersoIt/Inquisitor/internal/strategy"
	"github.com/shopspring/decimal"
	"gopkg.in/yaml.v3"
)

type ApprovalFixture struct {
	Grant    strategy.PaperApproval
	Evidence strategy.PaperApprovalEvidence
	Policy   research.ResultGatePolicy
	Now      time.Time
}

func PaperApproval(t testing.TB) ApprovalFixture {
	t.Helper()
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	spec := hypothesis.Hypothesis{Name: "approval_test_hypothesis", Version: "v1", Status: hypothesis.StatusDraft, Description: "Paper authorization fixture", Thesis: "Directional feature confirmation",
		Market: hypothesis.MarketScope{Exchange: "bybit", Category: "linear", Symbols: []string{"BTCUSDT"}, Intervals: []string{"1"}},
		Regime: hypothesis.RegimeScope{Allowed: []string{"TREND_UP", "TREND_DOWN"}, Blocked: []string{"NO_TRADE", "CHAOS"}}, Direction: hypothesis.DirectionBoth,
		Signals:    []hypothesis.SignalRule{{Name: "adx", Description: "Trend strength", Feature: "trend.adx", Operator: ">=", Value: "25"}},
		Risk:       hypothesis.RiskRules{MaxRiskPerTradePct: 0.25, MinConfidence: 70, RequireStopLoss: true},
		Validation: hypothesis.ValidationRules{MinTrades: 100, RequireOutOfSample: true, RequireWalkForward: true, RequireRegimeAnalysis: true}, Costs: hypothesis.CostRules{IncludeFees: true, IncludeSpread: true, IncludeSlippage: true}}
	raw, err := yaml.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	spec, err = hypothesis.ParseYAML(raw)
	if err != nil {
		t.Fatal(err)
	}
	h, err := hypothesis.NewRecord(spec, "approval-test.yaml", raw, now.Add(-3*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	run, err := research.NewPlannedRun(research.PlanInput{RunID: "approval_test_run_0001", HypothesisName: h.Name, HypothesisVersion: h.Version, HypothesisContentSHA256: h.ContentSHA256, Exchange: "bybit", Category: "linear", Symbols: []string{"BTCUSDT"}, Intervals: []string{"1"}, WindowStart: now.Add(-48 * time.Hour), WindowEnd: now.Add(-24 * time.Hour), PlannedAt: now.Add(-2 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	result, err := research.NewResult(research.ResultInput{RunID: run.RunID, FinalStatus: research.StatusCompleted, Outcome: research.OutcomeCandidate, Summary: "Complete conservative candidate", Reasons: []string{"research_gates_passed"}, RecordedAt: now.Add(-90 * time.Minute),
		Metrics: research.Metrics{Trades: 100, RegimeStates: 100, ExpectedRegimeStates: 100, RegimeCoveragePct: 100, InSampleTrades: 50, OutOfSampleTrades: 50, GrossProfit: "20", GrossLoss: "10", TotalFees: "1", NetPnL: "10", Expectancy: "0.1", ProfitFactor: "2", ProfitFactorDefined: true, MaxDrawdownPct: 5, OutOfSampleNetPnL: "5", OutOfSampleProfitFactor: "1.5", OutOfSampleProfitFactorDefined: true, OutOfSampleMaxDrawdownPct: 4, WalkForwardFolds: 3, WalkForwardPassedFolds: 3, WalkForwardTrades: 100, FeesIncluded: true, SpreadIncluded: true, SlippageIncluded: true, OutOfSample: true, WalkForward: true, RegimeAnalysisIncluded: true}})
	if err != nil {
		t.Fatal(err)
	}
	run, err = research.FinalizeRun(run, result)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := paper.NewValidationPlan(run, result, paper.SafetyPolicy{TradingEnabled: true, TradingMode: "paper", InitialBalance: decimal.NewFromInt(1000), MinimumDays: 30, SimulateFees: true, SimulateSlippage: true, SimulateSpread: true}, now.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	validation, err := paper.NewValidationRecord(paper.ValidationRecordInput{ValidationID: "approval_test_validation", Plan: plan})
	if err != nil {
		t.Fatal(err)
	}
	validation, err = paper.StartValidation(validation, now.Add(-30*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	grant := strategy.PaperApproval{ApprovalID: "approval_test_grant", ValidationID: validation.ValidationID, RunID: run.RunID, HypothesisName: h.Name, HypothesisVersion: h.Version, HypothesisSHA256: h.ContentSHA256, StrategyName: strategy.NameTrend, Exchange: "bybit", Category: "linear", Symbol: "BTCUSDT", Interval: "1", ReviewedBy: "test_operator", Reason: "Reviewed for paper validation", GrantedAt: now.Add(-10 * time.Minute)}
	policy := research.ResultGatePolicy{Enabled: true, MinTrades: 100, MinProfitFactor: decimal.RequireFromString("1.15"), MinExpectancy: decimal.RequireFromString("0.05"), MaxDrawdownPct: 15, RequireCosts: true, RequireOutOfSample: true, RequireWalkForward: true, RequireRegimeAnalysis: true}
	f := ApprovalFixture{Grant: grant, Evidence: strategy.PaperApprovalEvidence{Hypothesis: h, Run: run, Result: result, Validation: validation}, Policy: policy, Now: now}
	if err := strategy.CheckPaperApprovalEvidence(grant, f.Evidence, policy, now); err != nil {
		t.Fatal(err)
	}
	return f
}
