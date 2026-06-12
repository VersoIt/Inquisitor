package research_test

import (
	"context"
	"strings"
	"testing"
	"time"

	appresearch "github.com/VersoIt/Inquisitor/internal/app/research"
	"github.com/VersoIt/Inquisitor/internal/clock"
	domainregime "github.com/VersoIt/Inquisitor/internal/regime"
	domainresearch "github.com/VersoIt/Inquisitor/internal/research"
)

func TestServiceDryRunRecordsInconclusiveResultWhenRegimeCoverageIsComplete(t *testing.T) {
	plannedAt := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)
	run := shortResearchRun(t, plannedAt)
	regimes := &fakeRegimeRepository{statesByKey: map[string][]domainregime.State{
		"BTCUSDT|1": {
			testRegimeState(plannedAt.Add(-3 * time.Minute)),
			testRegimeState(plannedAt.Add(-2 * time.Minute)),
			testRegimeState(plannedAt.Add(-time.Minute)),
		},
	}}
	recorder := &fakeResultRecorder{stats: domainresearch.RecordResultStats{RunUpdated: 1, ResultInserted: 1}}
	service := testDryRunService(plannedAt, &fakeRunRepository{runs: []domainresearch.Run{run}}, recorder, regimes)

	got, err := service.DryRun(context.Background(), appresearch.DryRunRequest{RunID: run.RunID})
	if err != nil {
		t.Fatalf("dry-run research: %v", err)
	}

	if got.Run.Status != domainresearch.StatusCompleted {
		t.Fatalf("run status mismatch: got %s want COMPLETED", got.Run.Status)
	}
	if got.Result.Outcome != domainresearch.OutcomeInconclusive {
		t.Fatalf("outcome mismatch: got %s want INCONCLUSIVE", got.Result.Outcome)
	}
	if got.Coverage.Expected != 3 || got.Coverage.Observed != 3 || got.Coverage.Missing != 0 || got.Coverage.Percent != 100 {
		t.Fatalf("coverage mismatch: %#v", got.Coverage)
	}
	if got.Result.Metrics.Trades != 0 || !got.Result.Metrics.RegimeAnalysisIncluded {
		t.Fatalf("metrics mismatch: %#v", got.Result.Metrics)
	}
	if recorder.run.Status != domainresearch.StatusCompleted || recorder.result.Outcome != domainresearch.OutcomeInconclusive {
		t.Fatalf("recorded payload mismatch: run=%#v result=%#v", recorder.run, recorder.result)
	}
	if len(regimes.queries) != 1 || regimes.queries[0].Exchange != "bybit" || regimes.queries[0].Category != "linear" {
		t.Fatalf("regime query mismatch: %#v", regimes.queries)
	}
}

func TestServiceDryRunRecordsFailedResultWhenRegimeCoverageIsIncomplete(t *testing.T) {
	plannedAt := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)
	run := shortResearchRun(t, plannedAt)
	regimes := &fakeRegimeRepository{statesByKey: map[string][]domainregime.State{
		"BTCUSDT|1": {
			testRegimeState(plannedAt.Add(-3 * time.Minute)),
			testRegimeState(plannedAt.Add(-2 * time.Minute)),
		},
	}}
	service := testDryRunService(
		plannedAt,
		&fakeRunRepository{runs: []domainresearch.Run{run}},
		&fakeResultRecorder{stats: domainresearch.RecordResultStats{RunUpdated: 1, ResultInserted: 1}},
		regimes,
	)

	got, err := service.DryRun(context.Background(), appresearch.DryRunRequest{RunID: run.RunID})
	if err != nil {
		t.Fatalf("dry-run research: %v", err)
	}

	if got.Run.Status != domainresearch.StatusFailed {
		t.Fatalf("run status mismatch: got %s want FAILED", got.Run.Status)
	}
	if got.Result.Outcome != domainresearch.OutcomeNotExecuted {
		t.Fatalf("outcome mismatch: got %s want NOT_EXECUTED", got.Result.Outcome)
	}
	if got.Coverage.Expected != 3 || got.Coverage.Observed != 2 || got.Coverage.Missing != 1 {
		t.Fatalf("coverage mismatch: %#v", got.Coverage)
	}
	if !containsReason(got.Result.Reasons, "regime_coverage_below_threshold") {
		t.Fatalf("missing coverage failure reason: %#v", got.Result.Reasons)
	}
}

func TestServiceDryRunRejectsInvalidInputsTableDriven(t *testing.T) {
	plannedAt := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)
	run := shortResearchRun(t, plannedAt)
	finalRun := run
	finalRun.Status = domainresearch.StatusFailed

	tests := []struct {
		name       string
		service    *appresearch.Service
		req        appresearch.DryRunRequest
		wantErrSub string
	}{
		{
			name: "missing run id",
			service: testDryRunService(
				plannedAt,
				&fakeRunRepository{runs: []domainresearch.Run{run}},
				&fakeResultRecorder{},
				&fakeRegimeRepository{},
			),
			req:        appresearch.DryRunRequest{RunID: " "},
			wantErrSub: "run_id",
		},
		{
			name: "invalid threshold",
			service: testDryRunService(
				plannedAt,
				&fakeRunRepository{runs: []domainresearch.Run{run}},
				&fakeResultRecorder{},
				&fakeRegimeRepository{},
			),
			req:        appresearch.DryRunRequest{RunID: run.RunID, MinRegimeCoveragePct: 101},
			wantErrSub: "min_regime_coverage_pct",
		},
		{
			name: "run not found",
			service: testDryRunService(
				plannedAt,
				&fakeRunRepository{},
				&fakeResultRecorder{},
				&fakeRegimeRepository{},
			),
			req:        appresearch.DryRunRequest{RunID: run.RunID},
			wantErrSub: "not found",
		},
		{
			name: "final run",
			service: testDryRunService(
				plannedAt,
				&fakeRunRepository{runs: []domainresearch.Run{finalRun}},
				&fakeResultRecorder{},
				&fakeRegimeRepository{},
			),
			req:        appresearch.DryRunRequest{RunID: finalRun.RunID},
			wantErrSub: "final status",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.service.DryRun(context.Background(), tt.req)
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
		})
	}
}

func TestServiceDryRunRequiresDependenciesTableDriven(t *testing.T) {
	plannedAt := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)
	run := shortResearchRun(t, plannedAt)

	tests := []struct {
		name       string
		service    *appresearch.Service
		wantErrSub string
	}{
		{
			name: "missing run repository",
			service: appresearch.NewService(
				nil,
				nil,
				appresearch.WithResultRecorder(&fakeResultRecorder{}),
				appresearch.WithRegimeRepository(&fakeRegimeRepository{}),
			),
			wantErrSub: "research run repository",
		},
		{
			name: "missing result recorder",
			service: appresearch.NewService(
				nil,
				&fakeRunRepository{runs: []domainresearch.Run{run}},
				appresearch.WithRegimeRepository(&fakeRegimeRepository{}),
			),
			wantErrSub: "result recorder",
		},
		{
			name: "missing regime repository",
			service: appresearch.NewService(
				nil,
				&fakeRunRepository{runs: []domainresearch.Run{run}},
				appresearch.WithResultRecorder(&fakeResultRecorder{}),
			),
			wantErrSub: "regime repository",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.service.DryRun(context.Background(), appresearch.DryRunRequest{RunID: run.RunID})
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
		})
	}
}

func testDryRunService(plannedAt time.Time, runs *fakeRunRepository, recorder *fakeResultRecorder, regimes *fakeRegimeRepository) *appresearch.Service {
	return appresearch.NewService(
		nil,
		runs,
		appresearch.WithResultRecorder(recorder),
		appresearch.WithRegimeRepository(regimes),
		appresearch.WithClock(clock.FixedClock{Time: plannedAt}),
	)
}

func shortResearchRun(t *testing.T, plannedAt time.Time) domainresearch.Run {
	t.Helper()
	run, err := domainresearch.NewPlannedRun(domainresearch.PlanInput{
		RunID:                   "research_dryrun_0001",
		HypothesisName:          "trend_momentum_draft",
		HypothesisVersion:       "0.1.0",
		HypothesisContentSHA256: strings.Repeat("a", 64),
		Exchange:                "bybit",
		Category:                "linear",
		WindowStart:             plannedAt.Add(-3 * time.Minute),
		WindowEnd:               plannedAt,
		PlannedAt:               plannedAt,
		Symbols:                 []string{"BTCUSDT"},
		Intervals:               []string{"1"},
	})
	if err != nil {
		t.Fatalf("new short research run: %v", err)
	}
	return run
}

type fakeRegimeRepository struct {
	statesByKey map[string][]domainregime.State
	queries     []domainregime.StateQuery
	err         error
}

func (r *fakeRegimeRepository) UpsertStates(context.Context, []domainregime.State) (domainregime.WriteStats, error) {
	return domainregime.WriteStats{}, nil
}

func (r *fakeRegimeRepository) ListStates(_ context.Context, query domainregime.StateQuery) ([]domainregime.State, error) {
	r.queries = append(r.queries, query)
	if r.err != nil {
		return nil, r.err
	}
	var states []domainregime.State
	for _, state := range r.statesByKey[query.Symbol+"|"+query.Interval] {
		if !query.Start.IsZero() && state.CloseTime.Before(query.Start) {
			continue
		}
		if !query.End.IsZero() && !state.CloseTime.Before(query.End) {
			continue
		}
		states = append(states, state)
	}
	return states, nil
}

func testRegimeState(closeTime time.Time) domainregime.State {
	return domainregime.State{
		Exchange:        "bybit",
		Category:        "linear",
		Symbol:          "BTCUSDT",
		Interval:        "1",
		OpenTime:        closeTime.Add(-time.Minute),
		CloseTime:       closeTime,
		CalculatedAt:    closeTime.Add(100 * time.Millisecond),
		Regime:          domainregime.RegimeRange,
		CandidateRegime: domainregime.RegimeRange,
		Confidence:      80,
		NoTrade:         false,
		Reasons:         []string{"candidate:range"},
	}
}

func containsReason(reasons []string, want string) bool {
	for _, reason := range reasons {
		if reason == want {
			return true
		}
	}
	return false
}
