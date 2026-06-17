package paper_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	apppaper "github.com/VersoIt/Inquisitor/internal/app/paper"
	"github.com/VersoIt/Inquisitor/internal/clock"
	domainpaper "github.com/VersoIt/Inquisitor/internal/paper"
	domainresearch "github.com/VersoIt/Inquisitor/internal/research"
)

func TestServiceValidateCandidateBuildsAllowedPlan(t *testing.T) {
	plannedAt := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	requestedAt := plannedAt.Add(3 * time.Hour)
	run, result := testRunResult(t, plannedAt, domainresearch.OutcomeCandidate)
	runs := &fakeRunRepository{runs: []domainresearch.Run{run}}
	results := &fakeResultRepository{results: []domainresearch.Result{result}}
	service := apppaper.NewService(
		runs,
		results,
		apppaper.WithClock(clock.FixedClock{Time: requestedAt}),
	)

	got, err := service.ValidateCandidate(context.Background(), apppaper.ValidateCandidateRequest{
		RunID:  " " + run.RunID + " ",
		Policy: validPolicy(),
	})
	if err != nil {
		t.Fatalf("validate candidate: %v", err)
	}

	if !got.Plan.Allowed || got.Plan.RequestedAt != requestedAt {
		t.Fatalf("plan mismatch: %#v", got.Plan)
	}
	if got.Run.RunID != run.RunID || got.Result.RunID != run.RunID {
		t.Fatalf("payload mismatch: %#v", got)
	}
	if len(runs.queries) != 1 || runs.queries[0].RunID != run.RunID || runs.queries[0].Limit != 2 {
		t.Fatalf("run query mismatch: %#v", runs.queries)
	}
	if len(results.queries) != 1 || results.queries[0].RunID != run.RunID || results.queries[0].Limit != 2 {
		t.Fatalf("result query mismatch: %#v", results.queries)
	}
}

func TestServiceValidateCandidateReturnsBlockedPlanForNonCandidate(t *testing.T) {
	plannedAt := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	run, result := testRunResult(t, plannedAt, domainresearch.OutcomeRejected)
	service := apppaper.NewService(
		&fakeRunRepository{runs: []domainresearch.Run{run}},
		&fakeResultRepository{results: []domainresearch.Result{result}},
		apppaper.WithClock(clock.FixedClock{Time: plannedAt.Add(3 * time.Hour)}),
	)

	got, err := service.ValidateCandidate(context.Background(), apppaper.ValidateCandidateRequest{
		RunID:  run.RunID,
		Policy: validPolicy(),
	})
	if err != nil {
		t.Fatalf("validate rejected result: %v", err)
	}
	if got.Plan.Allowed {
		t.Fatalf("rejected result must not be allowed: %#v", got.Plan)
	}
	if !containsString(got.Plan.Reasons, "research_result_not_candidate") {
		t.Fatalf("missing non-candidate reason: %#v", got.Plan.Reasons)
	}
}

func TestServiceValidateCandidateRejectsInvalidInputsTableDriven(t *testing.T) {
	plannedAt := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	run, result := testRunResult(t, plannedAt, domainresearch.OutcomeCandidate)
	repositoryErr := errors.New("postgres unavailable")

	tests := []struct {
		name       string
		service    *apppaper.Service
		req        apppaper.ValidateCandidateRequest
		wantErrSub string
	}{
		{
			name:       "missing run id",
			service:    apppaper.NewService(&fakeRunRepository{}, &fakeResultRepository{}, apppaper.WithClock(clock.FixedClock{Time: plannedAt})),
			req:        apppaper.ValidateCandidateRequest{RunID: "", Policy: validPolicy()},
			wantErrSub: "run_id",
		},
		{
			name:       "run not found",
			service:    apppaper.NewService(&fakeRunRepository{}, &fakeResultRepository{}, apppaper.WithClock(clock.FixedClock{Time: plannedAt})),
			req:        apppaper.ValidateCandidateRequest{RunID: run.RunID, Policy: validPolicy()},
			wantErrSub: "run",
		},
		{
			name:       "ambiguous run",
			service:    apppaper.NewService(&fakeRunRepository{runs: []domainresearch.Run{run, run}}, &fakeResultRepository{}, apppaper.WithClock(clock.FixedClock{Time: plannedAt})),
			req:        apppaper.ValidateCandidateRequest{RunID: run.RunID, Policy: validPolicy()},
			wantErrSub: "ambiguous",
		},
		{
			name:       "run repository error",
			service:    apppaper.NewService(&fakeRunRepository{err: repositoryErr}, &fakeResultRepository{}, apppaper.WithClock(clock.FixedClock{Time: plannedAt})),
			req:        apppaper.ValidateCandidateRequest{RunID: run.RunID, Policy: validPolicy()},
			wantErrSub: repositoryErr.Error(),
		},
		{
			name:       "result not found",
			service:    apppaper.NewService(&fakeRunRepository{runs: []domainresearch.Run{run}}, &fakeResultRepository{}, apppaper.WithClock(clock.FixedClock{Time: plannedAt})),
			req:        apppaper.ValidateCandidateRequest{RunID: run.RunID, Policy: validPolicy()},
			wantErrSub: "result",
		},
		{
			name:       "ambiguous result",
			service:    apppaper.NewService(&fakeRunRepository{runs: []domainresearch.Run{run}}, &fakeResultRepository{results: []domainresearch.Result{result, result}}, apppaper.WithClock(clock.FixedClock{Time: plannedAt})),
			req:        apppaper.ValidateCandidateRequest{RunID: run.RunID, Policy: validPolicy()},
			wantErrSub: "ambiguous",
		},
		{
			name:       "result repository error",
			service:    apppaper.NewService(&fakeRunRepository{runs: []domainresearch.Run{run}}, &fakeResultRepository{err: repositoryErr}, apppaper.WithClock(clock.FixedClock{Time: plannedAt})),
			req:        apppaper.ValidateCandidateRequest{RunID: run.RunID, Policy: validPolicy()},
			wantErrSub: repositoryErr.Error(),
		},
		{
			name:       "missing run repository",
			service:    apppaper.NewService(nil, &fakeResultRepository{}, apppaper.WithClock(clock.FixedClock{Time: plannedAt})),
			req:        apppaper.ValidateCandidateRequest{RunID: run.RunID, Policy: validPolicy()},
			wantErrSub: "research run repository",
		},
		{
			name:       "missing result repository",
			service:    apppaper.NewService(&fakeRunRepository{runs: []domainresearch.Run{run}}, nil, apppaper.WithClock(clock.FixedClock{Time: plannedAt})),
			req:        apppaper.ValidateCandidateRequest{RunID: run.RunID, Policy: validPolicy()},
			wantErrSub: "research result repository",
		},
		{
			name:       "missing clock",
			service:    apppaper.NewService(&fakeRunRepository{runs: []domainresearch.Run{run}}, &fakeResultRepository{results: []domainresearch.Result{result}}, apppaper.WithClock(nil)),
			req:        apppaper.ValidateCandidateRequest{RunID: run.RunID, Policy: validPolicy()},
			wantErrSub: "clock",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.service.ValidateCandidate(context.Background(), tt.req)
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
		})
	}
}

type fakeRunRepository struct {
	runs    []domainresearch.Run
	queries []domainresearch.Query
	err     error
}

func (r *fakeRunRepository) ListRuns(_ context.Context, query domainresearch.Query) ([]domainresearch.Run, error) {
	r.queries = append(r.queries, query)
	if r.err != nil {
		return nil, r.err
	}
	return append([]domainresearch.Run(nil), r.runs...), nil
}

type fakeResultRepository struct {
	results []domainresearch.Result
	queries []domainresearch.ResultQuery
	err     error
}

func (r *fakeResultRepository) ListResults(_ context.Context, query domainresearch.ResultQuery) ([]domainresearch.Result, error) {
	r.queries = append(r.queries, query)
	if r.err != nil {
		return nil, r.err
	}
	return append([]domainresearch.Result(nil), r.results...), nil
}

func testRunResult(t *testing.T, plannedAt time.Time, outcome domainresearch.Outcome) (domainresearch.Run, domainresearch.Result) {
	t.Helper()

	run, err := domainresearch.NewPlannedRun(domainresearch.PlanInput{
		RunID:                   "research_paper_app_0001",
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
	result, err := domainresearch.NewResult(domainresearch.ResultInput{
		RunID:       run.RunID,
		FinalStatus: domainresearch.StatusCompleted,
		Outcome:     outcome,
		Summary:     "Research validation completed.",
		Metrics: domainresearch.Metrics{
			Trades:                 10,
			InSampleTrades:         5,
			OutOfSampleTrades:      5,
			FeesIncluded:           true,
			SpreadIncluded:         true,
			SlippageIncluded:       true,
			OutOfSample:            true,
			WalkForward:            true,
			WalkForwardFolds:       3,
			WalkForwardPassedFolds: 3,
			WalkForwardTrades:      10,
			RegimeAnalysisIncluded: true,
		},
		Reasons:    []string{"test_result"},
		RecordedAt: plannedAt.Add(time.Hour),
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

func validPolicy() domainpaper.SafetyPolicy {
	return domainpaper.SafetyPolicy{
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
