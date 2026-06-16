package research_test

import (
	"strings"
	"testing"
	"time"

	"github.com/VersoIt/Inquisitor/internal/research"
)

func TestNewResultBuildsValidatedResult(t *testing.T) {
	recordedAt := time.Date(2026, 6, 12, 13, 0, 0, 0, time.UTC)

	got, err := research.NewResult(research.ResultInput{
		RunID:       "research_result_0001",
		FinalStatus: "failed",
		Outcome:     "not_executed",
		Summary:     " Strategy executor is intentionally not implemented yet. ",
		Reasons:     []string{" scaffold_only "},
		RecordedAt:  recordedAt,
	})
	if err != nil {
		t.Fatalf("new result: %v", err)
	}

	if got.FinalStatus != research.StatusFailed || got.Outcome != research.OutcomeNotExecuted {
		t.Fatalf("result was not canonicalized: %#v", got)
	}
	if got.Summary != "Strategy executor is intentionally not implemented yet." {
		t.Fatalf("summary was not trimmed: %q", got.Summary)
	}
	if got.Reasons[0] != "scaffold_only" {
		t.Fatalf("reasons were not trimmed: %#v", got.Reasons)
	}
}

func TestValidateResultRejectsInvalidResultsTableDriven(t *testing.T) {
	recordedAt := time.Date(2026, 6, 12, 13, 0, 0, 0, time.UTC)
	valid := research.Result{
		RunID:       "research_result_0001",
		FinalStatus: research.StatusFailed,
		Outcome:     research.OutcomeNotExecuted,
		Summary:     "Strategy executor is intentionally not implemented yet.",
		RecordedAt:  recordedAt,
	}

	tests := []struct {
		name       string
		mutate     func(*research.Result)
		wantErrSub string
	}{
		{
			name: "missing run id",
			mutate: func(result *research.Result) {
				result.RunID = ""
			},
			wantErrSub: "run_id",
		},
		{
			name: "non final status",
			mutate: func(result *research.Result) {
				result.FinalStatus = research.StatusRunning
			},
			wantErrSub: "final_status",
		},
		{
			name: "unknown outcome",
			mutate: func(result *research.Result) {
				result.Outcome = "MOON"
			},
			wantErrSub: "outcome",
		},
		{
			name: "completed not executed",
			mutate: func(result *research.Result) {
				result.FinalStatus = research.StatusCompleted
			},
			wantErrSub: "NOT_EXECUTED",
		},
		{
			name: "negative regime states",
			mutate: func(result *research.Result) {
				result.Metrics.RegimeStates = -1
			},
			wantErrSub: "regime_states",
		},
		{
			name: "unbalanced regime state counts",
			mutate: func(result *research.Result) {
				result.Metrics.ExpectedRegimeStates = 10
				result.Metrics.RegimeStates = 8
				result.Metrics.MissingRegimeStates = 1
			},
			wantErrSub: "counts must balance",
		},
		{
			name: "coverage above percent range",
			mutate: func(result *research.Result) {
				result.Metrics.RegimeCoveragePct = 101
			},
			wantErrSub: "coverage",
		},
		{
			name: "negative rule observations",
			mutate: func(result *research.Result) {
				result.Metrics.RuleObservations = -1
			},
			wantErrSub: "rule_observations",
		},
		{
			name: "unbalanced rule observations",
			mutate: func(result *research.Result) {
				result.Metrics.RuleObservations = 3
				result.Metrics.RegimeAllowedObservations = 2
				result.Metrics.RegimeBlockedObservations = 0
			},
			wantErrSub: "rule observation counts must balance",
		},
		{
			name: "unbalanced signal evaluations",
			mutate: func(result *research.Result) {
				result.Metrics.RuleEvaluations = 3
				result.Metrics.SignalMatches = 1
				result.Metrics.SignalFailures = 1
				result.Metrics.SignalSkips = 0
			},
			wantErrSub: "signal evaluation counts must balance",
		},
		{
			name: "invalid net pnl decimal",
			mutate: func(result *research.Result) {
				result.Metrics.NetPnL = "moon"
			},
			wantErrSub: "net_pnl",
		},
		{
			name: "negative total fees",
			mutate: func(result *research.Result) {
				result.Metrics.TotalFees = "-1"
			},
			wantErrSub: "total_fees",
		},
		{
			name: "win rate above percent range",
			mutate: func(result *research.Result) {
				result.Metrics.WinRatePct = 101
			},
			wantErrSub: "win_rate_pct",
		},
		{
			name: "drawdown below percent range",
			mutate: func(result *research.Result) {
				result.Metrics.MaxDrawdownPct = -1
			},
			wantErrSub: "max_drawdown_pct",
		},
		{
			name: "not executed with trades",
			mutate: func(result *research.Result) {
				result.Metrics.Trades = 1
			},
			wantErrSub: "zero trades",
		},
		{
			name: "trades without fees",
			mutate: func(result *research.Result) {
				result.Outcome = research.OutcomeRejected
				result.Metrics = research.Metrics{
					Trades:                 10,
					SpreadIncluded:         true,
					SlippageIncluded:       true,
					RegimeAnalysisIncluded: true,
				}
			},
			wantErrSub: "fees_included",
		},
		{
			name: "candidate without out of sample",
			mutate: func(result *research.Result) {
				result.FinalStatus = research.StatusCompleted
				result.Outcome = research.OutcomeCandidate
				result.Metrics = research.Metrics{
					Trades:                 100,
					FeesIncluded:           true,
					SpreadIncluded:         true,
					SlippageIncluded:       true,
					WalkForward:            true,
					RegimeAnalysisIncluded: true,
				}
			},
			wantErrSub: "out_of_sample",
		},
		{
			name: "empty reason",
			mutate: func(result *research.Result) {
				result.Reasons = []string{" "}
			},
			wantErrSub: "reasons[0]",
		},
		{
			name: "missing recorded at",
			mutate: func(result *research.Result) {
				result.RecordedAt = time.Time{}
			},
			wantErrSub: "recorded_at",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := valid
			tt.mutate(&result)

			err := research.ValidateResult(result)
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
		})
	}
}

func TestValidateResultAcceptsCandidateOnlyWithConservativeGates(t *testing.T) {
	err := research.ValidateResult(research.Result{
		RunID:       "research_result_0001",
		FinalStatus: research.StatusCompleted,
		Outcome:     research.OutcomeCandidate,
		Summary:     "Candidate after complete conservative research.",
		Metrics: research.Metrics{
			Trades:                 150,
			FeesIncluded:           true,
			SpreadIncluded:         true,
			SlippageIncluded:       true,
			OutOfSample:            true,
			WalkForward:            true,
			RegimeAnalysisIncluded: true,
		},
		RecordedAt: time.Date(2026, 6, 12, 13, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("candidate result should pass conservative gates: %v", err)
	}
}

func TestFinalizeRunTransitionsToFinalStatus(t *testing.T) {
	plannedAt := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)
	run, err := research.NewPlannedRun(research.PlanInput{
		RunID:                   "research_result_0001",
		HypothesisName:          "trend_momentum_draft",
		HypothesisVersion:       "0.1.0",
		HypothesisContentSHA256: strings.Repeat("a", 64),
		Exchange:                "bybit",
		Category:                "linear",
		WindowStart:             plannedAt.Add(-24 * time.Hour),
		WindowEnd:               plannedAt.Add(-time.Hour),
		PlannedAt:               plannedAt,
		Symbols:                 []string{"BTCUSDT"},
		Intervals:               []string{"1"},
	})
	if err != nil {
		t.Fatalf("new run: %v", err)
	}
	result, err := research.NewResult(research.ResultInput{
		RunID:       run.RunID,
		FinalStatus: research.StatusFailed,
		Outcome:     research.OutcomeNotExecuted,
		Summary:     "Strategy executor is intentionally not implemented yet.",
		RecordedAt:  plannedAt.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("new result: %v", err)
	}

	got, err := research.FinalizeRun(run, result)
	if err != nil {
		t.Fatalf("finalize run: %v", err)
	}
	if got.Status != research.StatusFailed {
		t.Fatalf("status mismatch: got %s want FAILED", got.Status)
	}
}
