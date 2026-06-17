package paper_test

import (
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/VersoIt/Inquisitor/internal/paper"
	domainresearch "github.com/VersoIt/Inquisitor/internal/research"
)

func TestNewValidationPlanAllowsCandidateWithSafePaperPolicy(t *testing.T) {
	run, result := paperRunResult(t, domainresearch.OutcomeCandidate)
	requestedAt := time.Date(2026, 6, 17, 15, 0, 0, 0, time.UTC)

	got, err := paper.NewValidationPlan(run, result, validSafetyPolicy(), requestedAt)
	if err != nil {
		t.Fatalf("new validation plan: %v", err)
	}

	if !got.Allowed {
		t.Fatalf("expected plan to be allowed: %#v", got)
	}
	if got.RunID != run.RunID || got.Mode != "paper" || got.RequestedAt != requestedAt {
		t.Fatalf("plan metadata mismatch: %#v", got)
	}
	if len(got.Reasons) != 1 || got.Reasons[0] != "paper_validation_allowed" {
		t.Fatalf("allowed reason mismatch: %#v", got.Reasons)
	}
}

func TestNewValidationPlanBlocksUnsafeOrIncompleteInputsTableDriven(t *testing.T) {
	tests := []struct {
		name       string
		outcome    domainresearch.Outcome
		mutateRun  func(*domainresearch.Run)
		mutatePlan func(*domainresearch.Result, *paper.SafetyPolicy)
		wantReason string
	}{
		{
			name:       "non candidate result",
			outcome:    domainresearch.OutcomeRejected,
			wantReason: "research_result_not_candidate",
		},
		{
			name:    "non completed run",
			outcome: domainresearch.OutcomeRejected,
			mutateRun: func(run *domainresearch.Run) {
				run.Status = domainresearch.StatusFailed
			},
			mutatePlan: func(result *domainresearch.Result, _ *paper.SafetyPolicy) {
				result.FinalStatus = domainresearch.StatusFailed
				result.Outcome = domainresearch.OutcomeNotExecuted
				result.Metrics = domainresearch.Metrics{}
			},
			wantReason: "research_run_not_completed",
		},
		{
			name:    "paper trading disabled",
			outcome: domainresearch.OutcomeCandidate,
			mutatePlan: func(_ *domainresearch.Result, policy *paper.SafetyPolicy) {
				policy.TradingEnabled = false
			},
			wantReason: "paper_trading_disabled",
		},
		{
			name:    "live trading enabled",
			outcome: domainresearch.OutcomeCandidate,
			mutatePlan: func(_ *domainresearch.Result, policy *paper.SafetyPolicy) {
				policy.AllowLive = true
			},
			wantReason: "live_trading_enabled",
		},
		{
			name:    "missing conservative paper cost simulation",
			outcome: domainresearch.OutcomeCandidate,
			mutatePlan: func(_ *domainresearch.Result, policy *paper.SafetyPolicy) {
				policy.SimulateSlippage = false
			},
			wantReason: "paper_slippage_simulation_disabled",
		},
		{
			name:    "candidate metrics missing walk forward",
			outcome: domainresearch.OutcomeRejected,
			mutatePlan: func(result *domainresearch.Result, _ *paper.SafetyPolicy) {
				result.Metrics.WalkForward = false
				result.Metrics.WalkForwardFolds = 0
				result.Metrics.WalkForwardPassedFolds = 0
				result.Metrics.WalkForwardTrades = 0
			},
			wantReason: "candidate_missing_walk_forward",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			run, result := paperRunResult(t, tt.outcome)
			if tt.mutateRun != nil {
				tt.mutateRun(&run)
			}
			policy := validSafetyPolicy()
			if tt.mutatePlan != nil {
				tt.mutatePlan(&result, &policy)
			}

			got, err := paper.NewValidationPlan(run, result, policy, time.Date(2026, 6, 17, 15, 0, 0, 0, time.UTC))
			if err != nil {
				t.Fatalf("new validation plan: %v", err)
			}
			if got.Allowed {
				t.Fatalf("expected plan to be blocked: %#v", got)
			}
			if !containsString(got.Reasons, tt.wantReason) {
				t.Fatalf("missing reason %q in %#v", tt.wantReason, got.Reasons)
			}
		})
	}
}

func TestNewValidationPlanRejectsInvalidInputsTableDriven(t *testing.T) {
	run, result := paperRunResult(t, domainresearch.OutcomeCandidate)

	tests := []struct {
		name       string
		run        domainresearch.Run
		result     domainresearch.Result
		policy     paper.SafetyPolicy
		requested  time.Time
		wantErrSub string
	}{
		{
			name:       "mismatched run id",
			run:        run,
			result:     func() domainresearch.Result { result := result; result.RunID = "research_paper_other"; return result }(),
			policy:     validSafetyPolicy(),
			requested:  time.Date(2026, 6, 17, 15, 0, 0, 0, time.UTC),
			wantErrSub: "must match",
		},
		{
			name:       "missing requested at",
			run:        run,
			result:     result,
			policy:     validSafetyPolicy(),
			requested:  time.Time{},
			wantErrSub: "requested_at",
		},
		{
			name:   "invalid initial balance",
			run:    run,
			result: result,
			policy: func() paper.SafetyPolicy {
				policy := validSafetyPolicy()
				policy.InitialBalance = decimal.Zero
				return policy
			}(),
			requested:  time.Date(2026, 6, 17, 15, 0, 0, 0, time.UTC),
			wantErrSub: "initial_balance",
		},
		{
			name:       "invalid minimum days",
			run:        run,
			result:     result,
			policy:     func() paper.SafetyPolicy { policy := validSafetyPolicy(); policy.MinimumDays = 0; return policy }(),
			requested:  time.Date(2026, 6, 17, 15, 0, 0, 0, time.UTC),
			wantErrSub: "minimum_days",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := paper.NewValidationPlan(tt.run, tt.result, tt.policy, tt.requested)
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
		})
	}
}

func paperRunResult(t *testing.T, outcome domainresearch.Outcome) (domainresearch.Run, domainresearch.Result) {
	t.Helper()

	plannedAt := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	run, err := domainresearch.NewPlannedRun(domainresearch.PlanInput{
		RunID:                   "research_paper_0001",
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
	metrics := domainresearch.Metrics{
		Trades:                 10,
		InSampleTrades:         5,
		FeesIncluded:           true,
		SpreadIncluded:         true,
		SlippageIncluded:       true,
		OutOfSample:            true,
		OutOfSampleTrades:      5,
		WalkForward:            true,
		WalkForwardFolds:       3,
		WalkForwardPassedFolds: 3,
		WalkForwardTrades:      10,
		RegimeAnalysisIncluded: true,
	}
	result, err := domainresearch.NewResult(domainresearch.ResultInput{
		RunID:       run.RunID,
		FinalStatus: domainresearch.StatusCompleted,
		Outcome:     outcome,
		Summary:     "Research validation completed.",
		Metrics:     metrics,
		Reasons:     []string{"test_result"},
		RecordedAt:  plannedAt.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("new result: %v", err)
	}
	finalRun, err := domainresearch.FinalizeRun(run, result)
	if err != nil {
		t.Fatalf("finalize run: %v", err)
	}
	return finalRun, result
}

func validSafetyPolicy() paper.SafetyPolicy {
	return paper.SafetyPolicy{
		TradingEnabled:              true,
		TradingMode:                 "paper",
		AllowLive:                   false,
		WithdrawalPermissionAllowed: false,
		InitialBalance:              decimal.RequireFromString("1000"),
		MinimumDays:                 30,
		SimulateFees:                true,
		SimulateSlippage:            true,
		SimulateSpread:              true,
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
