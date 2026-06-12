package research_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	appresearch "github.com/VersoIt/Inquisitor/internal/app/research"
	"github.com/VersoIt/Inquisitor/internal/clock"
	domainresearch "github.com/VersoIt/Inquisitor/internal/research"
)

func TestServiceRecordResultFinalizesRunAndStoresResult(t *testing.T) {
	plannedAt := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)
	recordedAt := plannedAt.Add(time.Hour)
	run := testRun(t, plannedAt)
	runs := &fakeRunRepository{runs: []domainresearch.Run{run}}
	recorder := &fakeResultRecorder{stats: domainresearch.RecordResultStats{RunUpdated: 1, ResultInserted: 1}}
	service := appresearch.NewService(
		nil,
		runs,
		appresearch.WithResultRecorder(recorder),
		appresearch.WithClock(clock.FixedClock{Time: recordedAt}),
	)

	got, err := service.RecordResult(context.Background(), appresearch.RecordResultRequest{
		RunID:       " " + run.RunID + " ",
		FinalStatus: domainresearch.StatusFailed,
		Outcome:     domainresearch.OutcomeNotExecuted,
		Summary:     "Strategy executor is intentionally not implemented yet.",
		Reasons:     []string{"scaffold_only"},
	})
	if err != nil {
		t.Fatalf("record result: %v", err)
	}

	if got.Run.Status != domainresearch.StatusFailed {
		t.Fatalf("run status mismatch: got %s", got.Run.Status)
	}
	if got.Result.RecordedAt != recordedAt {
		t.Fatalf("recorded_at mismatch: got %s want %s", got.Result.RecordedAt, recordedAt)
	}
	if got.Stats.ResultInserted != 1 || got.Stats.RunUpdated != 1 {
		t.Fatalf("stats mismatch: %#v", got.Stats)
	}
	if recorder.run.Status != domainresearch.StatusFailed || recorder.result.Outcome != domainresearch.OutcomeNotExecuted {
		t.Fatalf("recorder received wrong payload: run=%#v result=%#v", recorder.run, recorder.result)
	}
	if len(runs.queries) != 1 || runs.queries[0].RunID != run.RunID {
		t.Fatalf("run query mismatch: %#v", runs.queries)
	}
}

func TestServiceRecordResultRejectsInvalidInputsTableDriven(t *testing.T) {
	plannedAt := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)
	validRun := testRun(t, plannedAt)
	repositoryErr := errors.New("postgres result recorder unavailable")

	tests := []struct {
		name       string
		service    *appresearch.Service
		req        appresearch.RecordResultRequest
		wantErrSub string
	}{
		{
			name: "missing run id",
			service: appresearch.NewService(
				nil,
				&fakeRunRepository{runs: []domainresearch.Run{validRun}},
				appresearch.WithResultRecorder(&fakeResultRecorder{}),
				appresearch.WithClock(clock.FixedClock{Time: plannedAt.Add(time.Hour)}),
			),
			req: appresearch.RecordResultRequest{
				RunID:       "",
				FinalStatus: domainresearch.StatusFailed,
				Outcome:     domainresearch.OutcomeNotExecuted,
				Summary:     "Strategy executor is intentionally not implemented yet.",
			},
			wantErrSub: "run_id",
		},
		{
			name: "run not found",
			service: appresearch.NewService(
				nil,
				&fakeRunRepository{},
				appresearch.WithResultRecorder(&fakeResultRecorder{}),
				appresearch.WithClock(clock.FixedClock{Time: plannedAt.Add(time.Hour)}),
			),
			req:        validResultRequest(validRun.RunID),
			wantErrSub: "not found",
		},
		{
			name: "invalid result",
			service: appresearch.NewService(
				nil,
				&fakeRunRepository{runs: []domainresearch.Run{validRun}},
				appresearch.WithResultRecorder(&fakeResultRecorder{}),
				appresearch.WithClock(clock.FixedClock{Time: plannedAt.Add(time.Hour)}),
			),
			req: func() appresearch.RecordResultRequest {
				req := validResultRequest(validRun.RunID)
				req.FinalStatus = domainresearch.StatusCompleted
				return req
			}(),
			wantErrSub: "NOT_EXECUTED",
		},
		{
			name: "recorder error",
			service: appresearch.NewService(
				nil,
				&fakeRunRepository{runs: []domainresearch.Run{validRun}},
				appresearch.WithResultRecorder(&fakeResultRecorder{err: repositoryErr}),
				appresearch.WithClock(clock.FixedClock{Time: plannedAt.Add(time.Hour)}),
			),
			req:        validResultRequest(validRun.RunID),
			wantErrSub: repositoryErr.Error(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.service.RecordResult(context.Background(), tt.req)
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
		})
	}
}

func TestServiceRecordResultRequiresDependenciesTableDriven(t *testing.T) {
	plannedAt := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)
	run := testRun(t, plannedAt)
	req := validResultRequest(run.RunID)

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
			),
			wantErrSub: "research run repository",
		},
		{
			name: "missing result recorder",
			service: appresearch.NewService(
				nil,
				&fakeRunRepository{runs: []domainresearch.Run{run}},
			),
			wantErrSub: "result recorder",
		},
		{
			name: "missing clock",
			service: appresearch.NewService(
				nil,
				&fakeRunRepository{runs: []domainresearch.Run{run}},
				appresearch.WithResultRecorder(&fakeResultRecorder{}),
				appresearch.WithClock(nil),
			),
			wantErrSub: "clock",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.service.RecordResult(context.Background(), req)
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
		})
	}
}

func validResultRequest(runID string) appresearch.RecordResultRequest {
	return appresearch.RecordResultRequest{
		RunID:       runID,
		FinalStatus: domainresearch.StatusFailed,
		Outcome:     domainresearch.OutcomeNotExecuted,
		Summary:     "Strategy executor is intentionally not implemented yet.",
		Reasons:     []string{"scaffold_only"},
	}
}

func testRun(t *testing.T, plannedAt time.Time) domainresearch.Run {
	t.Helper()

	run, err := domainresearch.NewPlannedRun(domainresearch.PlanInput{
		RunID:                   "research_result_0001",
		HypothesisName:          "trend_momentum_draft",
		HypothesisVersion:       "0.1.0",
		HypothesisContentSHA256: strings.Repeat("a", 64),
		WindowStart:             plannedAt.Add(-24 * time.Hour),
		WindowEnd:               plannedAt.Add(-time.Hour),
		PlannedAt:               plannedAt,
		Symbols:                 []string{"BTCUSDT"},
		Intervals:               []string{"1"},
	})
	if err != nil {
		t.Fatalf("new test run: %v", err)
	}
	return run
}
