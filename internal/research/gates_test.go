package research_test

import (
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/VersoIt/Inquisitor/internal/research"
)

func TestEvaluateMetricsGatesTableDriven(t *testing.T) {
	policy := research.ResultGatePolicy{
		Enabled:               true,
		MinTrades:             3,
		MinProfitFactor:       decimal.RequireFromString("1.2"),
		MinExpectancy:         decimal.RequireFromString("0.05"),
		MaxDrawdownPct:        12,
		RequireOutOfSample:    true,
		RequireWalkForward:    true,
		RequireRegimeAnalysis: true,
		RequireCosts:          true,
	}
	valid := gateMetrics()

	tests := []struct {
		name        string
		mutate      func(*research.Metrics)
		wantPassed  bool
		wantReasons []string
	}{
		{
			name:       "passes all conservative gates",
			wantPassed: true,
			wantReasons: []string{
				"research_gates_passed",
			},
		},
		{
			name: "fails min trades",
			mutate: func(metrics *research.Metrics) {
				metrics.Trades = 2
			},
			wantReasons: []string{"gate_min_trades_failed:2<3"},
		},
		{
			name: "fails missing conservative costs",
			mutate: func(metrics *research.Metrics) {
				metrics.SlippageIncluded = false
			},
			wantReasons: []string{"gate_conservative_costs_missing:slippage"},
		},
		{
			name: "fails low profit factor",
			mutate: func(metrics *research.Metrics) {
				metrics.ProfitFactor = "1.19"
			},
			wantReasons: []string{"gate_profit_factor_failed:1.19<1.2"},
		},
		{
			name: "passes unbounded profit factor when there are wins and no losses",
			mutate: func(metrics *research.Metrics) {
				metrics.ProfitFactor = ""
				metrics.ProfitFactorDefined = false
				metrics.GrossProfit = "4"
				metrics.GrossLoss = "0"
			},
			wantPassed:  true,
			wantReasons: []string{"research_gates_passed"},
		},
		{
			name: "fails undefined profit factor without profit",
			mutate: func(metrics *research.Metrics) {
				metrics.ProfitFactor = ""
				metrics.ProfitFactorDefined = false
				metrics.GrossProfit = "0"
				metrics.GrossLoss = "0"
			},
			wantReasons: []string{"gate_profit_factor_undefined"},
		},
		{
			name: "fails low expectancy",
			mutate: func(metrics *research.Metrics) {
				metrics.Expectancy = "0.04"
			},
			wantReasons: []string{"gate_expectancy_failed:0.04<0.05"},
		},
		{
			name: "fails high drawdown",
			mutate: func(metrics *research.Metrics) {
				metrics.MaxDrawdownPct = 12.01
			},
			wantReasons: []string{"gate_max_drawdown_failed:12.01>12"},
		},
		{
			name: "fails missing out of sample",
			mutate: func(metrics *research.Metrics) {
				metrics.OutOfSample = false
				metrics.OutOfSampleTrades = 0
			},
			wantReasons: []string{"gate_out_of_sample_missing"},
		},
		{
			name: "fails empty out of sample trades",
			mutate: func(metrics *research.Metrics) {
				metrics.OutOfSampleTrades = 0
			},
			wantReasons: []string{"gate_out_of_sample_trades_missing"},
		},
		{
			name: "fails missing walk forward",
			mutate: func(metrics *research.Metrics) {
				metrics.WalkForward = false
			},
			wantReasons: []string{"gate_walk_forward_missing"},
		},
		{
			name: "fails missing regime analysis",
			mutate: func(metrics *research.Metrics) {
				metrics.RegimeAnalysisIncluded = false
			},
			wantReasons: []string{"gate_regime_analysis_missing"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			metrics := valid
			if tt.mutate != nil {
				tt.mutate(&metrics)
			}

			got, err := research.EvaluateMetricsGates(metrics, policy)
			if err != nil {
				t.Fatalf("evaluate metrics gates: %v", err)
			}
			if got.Passed != tt.wantPassed {
				t.Fatalf("passed mismatch: got %v want %v, reasons=%#v", got.Passed, tt.wantPassed, got.Reasons)
			}
			for _, reason := range tt.wantReasons {
				if !containsString(got.Reasons, reason) {
					t.Fatalf("missing reason %q in %#v", reason, got.Reasons)
				}
			}
		})
	}
}

func TestEvaluateMetricsGatesDisabledDoesNotBlock(t *testing.T) {
	got, err := research.EvaluateMetricsGates(research.Metrics{}, research.ResultGatePolicy{})
	if err != nil {
		t.Fatalf("evaluate disabled gates: %v", err)
	}
	if got.Enabled || !got.Passed || got.CandidateEligible || len(got.Checks) != 0 || len(got.Reasons) != 0 {
		t.Fatalf("disabled gate evaluation mismatch: %#v", got)
	}
}

func TestEvaluateResultGatesMarksCandidateEligibility(t *testing.T) {
	result := gateResult(t)

	got, err := research.EvaluateResultGates(result, research.ResultGatePolicy{
		Enabled:               true,
		MinTrades:             3,
		MinProfitFactor:       decimal.RequireFromString("1.2"),
		MinExpectancy:         decimal.RequireFromString("0.05"),
		MaxDrawdownPct:        12,
		RequireOutOfSample:    true,
		RequireWalkForward:    true,
		RequireRegimeAnalysis: true,
		RequireCosts:          true,
	})
	if err != nil {
		t.Fatalf("evaluate result gates: %v", err)
	}
	if !got.Passed || !got.CandidateEligible {
		t.Fatalf("expected candidate-eligible result, got %#v", got)
	}
}

func TestDecideResultFromGatesTableDriven(t *testing.T) {
	policy := research.ResultGatePolicy{
		Enabled:               true,
		MinTrades:             3,
		MinProfitFactor:       decimal.RequireFromString("1.2"),
		MinExpectancy:         decimal.RequireFromString("0.05"),
		MaxDrawdownPct:        12,
		RequireOutOfSample:    true,
		RequireWalkForward:    true,
		RequireRegimeAnalysis: true,
		RequireCosts:          true,
	}

	tests := []struct {
		name         string
		mutatePolicy func(*research.ResultGatePolicy)
		mutate       func(*research.Metrics)
		want         research.Outcome
		wantReason   string
	}{
		{
			name: "disabled gates stay inconclusive",
			mutatePolicy: func(policy *research.ResultGatePolicy) {
				policy.Enabled = false
			},
			want:       research.OutcomeInconclusive,
			wantReason: "research_gates_disabled",
		},
		{
			name: "missing out of sample stays inconclusive",
			mutate: func(metrics *research.Metrics) {
				metrics.OutOfSample = false
				metrics.InSampleTrades = 0
				metrics.OutOfSampleTrades = 0
			},
			want:       research.OutcomeInconclusive,
			wantReason: "validation_incomplete:out_of_sample",
		},
		{
			name: "missing walk forward stays inconclusive",
			mutate: func(metrics *research.Metrics) {
				metrics.WalkForward = false
				metrics.WalkForwardFolds = 0
				metrics.WalkForwardPassedFolds = 0
				metrics.WalkForwardTrades = 0
			},
			want:       research.OutcomeInconclusive,
			wantReason: "validation_incomplete:walk_forward",
		},
		{
			name:       "passing completed gates become candidate",
			want:       research.OutcomeCandidate,
			wantReason: "research_decision_candidate",
		},
		{
			name: "failing completed gates become rejected",
			mutate: func(metrics *research.Metrics) {
				metrics.Trades = 2
				metrics.InSampleTrades = 1
				metrics.OutOfSampleTrades = 1
			},
			want:       research.OutcomeRejected,
			wantReason: "research_decision_rejected_by_gates",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			metrics := gateMetrics()
			if tt.mutate != nil {
				tt.mutate(&metrics)
			}
			policy := policy
			if tt.mutatePolicy != nil {
				tt.mutatePolicy(&policy)
			}
			evaluation, err := research.EvaluateMetricsGates(metrics, policy)
			if err != nil {
				t.Fatalf("evaluate metrics gates: %v", err)
			}

			got, err := research.DecideResultFromGates(metrics, policy, evaluation)
			if err != nil {
				t.Fatalf("decide result from gates: %v", err)
			}
			if got.FinalStatus != research.StatusCompleted || got.Outcome != tt.want {
				t.Fatalf("decision mismatch: %#v", got)
			}
			if !containsString(got.Reasons, tt.wantReason) {
				t.Fatalf("missing reason %q in %#v", tt.wantReason, got.Reasons)
			}
		})
	}
}

func TestValidateResultGatePolicyRejectsInvalidPolicyTableDriven(t *testing.T) {
	tests := []struct {
		name       string
		policy     research.ResultGatePolicy
		wantErrSub string
	}{
		{
			name:       "negative min trades",
			policy:     research.ResultGatePolicy{MinTrades: -1},
			wantErrSub: "min_trades",
		},
		{
			name:       "negative profit factor",
			policy:     research.ResultGatePolicy{MinProfitFactor: decimal.RequireFromString("-1")},
			wantErrSub: "min_profit_factor",
		},
		{
			name:       "negative expectancy",
			policy:     research.ResultGatePolicy{MinExpectancy: decimal.RequireFromString("-0.01")},
			wantErrSub: "min_expectancy",
		},
		{
			name:       "drawdown above percent range",
			policy:     research.ResultGatePolicy{MaxDrawdownPct: 101},
			wantErrSub: "max_drawdown_pct",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := research.ValidateResultGatePolicy(tt.policy)
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
		})
	}
}

func gateResult(t *testing.T) research.Result {
	t.Helper()

	result, err := research.NewResult(research.ResultInput{
		RunID:       "research_gate_0001",
		FinalStatus: research.StatusCompleted,
		Outcome:     research.OutcomeCandidate,
		Summary:     "Candidate after complete conservative research gates.",
		Metrics:     gateMetrics(),
		Reasons:     []string{"research_gates_passed"},
		RecordedAt:  time.Date(2026, 6, 17, 13, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("new gate result: %v", err)
	}
	return result
}

func gateMetrics() research.Metrics {
	return research.Metrics{
		Trades:                         3,
		RegimeStates:                   10,
		ExpectedRegimeStates:           10,
		RegimeCoveragePct:              100,
		InSampleTrades:                 1,
		OutOfSampleTrades:              2,
		GrossProfit:                    "4",
		GrossLoss:                      "2",
		TotalFees:                      "0.5",
		NetPnL:                         "2",
		Expectancy:                     "0.1",
		ProfitFactor:                   "2",
		ProfitFactorDefined:            true,
		MaxDrawdownPct:                 5,
		OutOfSampleNetPnL:              "1",
		OutOfSampleProfitFactor:        "1.5",
		OutOfSampleProfitFactorDefined: true,
		FeesIncluded:                   true,
		SpreadIncluded:                 true,
		SlippageIncluded:               true,
		OutOfSample:                    true,
		WalkForward:                    true,
		WalkForwardFolds:               3,
		WalkForwardPassedFolds:         3,
		WalkForwardTrades:              3,
		RegimeAnalysisIncluded:         true,
	}
}
