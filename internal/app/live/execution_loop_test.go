package live_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	applive "github.com/VersoIt/Inquisitor/internal/app/live"
	"github.com/VersoIt/Inquisitor/internal/clock"
	domainlive "github.com/VersoIt/Inquisitor/internal/live"
	domainrisk "github.com/VersoIt/Inquisitor/internal/risk"
)

func TestServiceRunBoundedLiveLoopRunsPreflightThenBoundedIterations(t *testing.T) {
	now := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	killSwitch := &fakeLiveKillSwitchRepository{}
	runner := &fakeLiveLoopIterationRunner{}
	service := boundedLiveLoopService(now, killSwitch, runner)

	got, err := service.RunBoundedLiveLoop(context.Background(), validBoundedLiveLoopRequest())
	if err != nil {
		t.Fatalf("run bounded live loop: %v", err)
	}

	if !got.PreflightChecked || !got.Preflight.Ready {
		t.Fatalf("preflight result mismatch: %#v", got)
	}
	if got.StopReason != applive.LiveLoopStopMaxIterations || !got.CompletedWithinBounds {
		t.Fatalf("bounded loop stop mismatch: %#v", got)
	}
	if got.IterationsAttempted != 3 || got.IterationsSucceeded != 3 || len(got.Iterations) != 3 || runner.calls != 3 {
		t.Fatalf("iteration counters mismatch: result=%#v runner_calls=%d", got, runner.calls)
	}
	if killSwitch.currentCalls != 4 {
		t.Fatalf("kill switch should be checked once in preflight and once per iteration, got %d", killSwitch.currentCalls)
	}
	for index, req := range runner.requests {
		if req.RunID != "live_loop_app_0001" || req.Iteration != index+1 || req.StartedAt != now {
			t.Fatalf("iteration request[%d] mismatch: %#v", index, req)
		}
	}
}

func TestServiceRunBoundedLiveLoopJournalsRunAndIterations(t *testing.T) {
	now := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	runner := &fakeLiveLoopIterationRunner{}
	journal := &fakeLiveLoopJournal{}
	service := boundedLiveLoopServiceWithJournal(now, &fakeLiveKillSwitchRepository{}, runner, journal)

	got, err := service.RunBoundedLiveLoop(context.Background(), validBoundedLiveLoopRequest())
	if err != nil {
		t.Fatalf("run bounded live loop: %v", err)
	}

	if journal.startCalls != 1 || journal.finishCalls != 1 || journal.iterationCalls != 3 {
		t.Fatalf("journal calls mismatch: start=%d finish=%d iterations=%d", journal.startCalls, journal.finishCalls, journal.iterationCalls)
	}
	start := journal.started[0]
	if start.RunID != got.RunID || start.StartedAt != got.StartedAt ||
		start.MaxIterations != 3 || start.MaxRuntime != time.Minute || start.IterationTimeout != 5*time.Second {
		t.Fatalf("start audit mismatch: %#v result=%#v", start, got)
	}
	finish := journal.finished[0]
	if finish.RunID != got.RunID ||
		finish.StartedAt != got.StartedAt ||
		finish.Status != domainlive.LiveLoopRunStatusCompleted ||
		finish.StopReason != string(applive.LiveLoopStopMaxIterations) ||
		!finish.PreflightChecked ||
		!finish.PreflightReady ||
		finish.IterationsAttempted != 3 ||
		finish.IterationsSucceeded != 3 ||
		!finish.CompletedWithinBounds ||
		finish.Error != "" {
		t.Fatalf("finish audit mismatch: %#v result=%#v", finish, got)
	}
	for index, audit := range journal.iterations {
		if audit.RunID != got.RunID || audit.RunStartedAt != got.StartedAt ||
			audit.Iteration != index+1 ||
			audit.Action != domainlive.LiveLoopAuditIterationActionNone ||
			audit.StartedAt != now ||
			audit.FinishedAt != now {
			t.Fatalf("iteration audit[%d] mismatch: %#v result=%#v", index, audit, got)
		}
	}
}

func TestServiceRunBoundedLiveLoopBlocksWhenStartAuditFailsBeforePreflight(t *testing.T) {
	now := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	auditErr := errors.New("audit database unavailable")
	killSwitch := &fakeLiveKillSwitchRepository{}
	runner := &fakeLiveLoopIterationRunner{}
	journal := &fakeLiveLoopJournal{startErr: auditErr}
	service := boundedLiveLoopServiceWithJournal(now, killSwitch, runner, journal)

	got, err := service.RunBoundedLiveLoop(context.Background(), validBoundedLiveLoopRequest())
	if err == nil || !strings.Contains(err.Error(), "start audit") || !strings.Contains(err.Error(), auditErr.Error()) {
		t.Fatalf("expected start audit error, got %v", err)
	}
	if killSwitch.currentCalls != 0 || runner.calls != 0 || journal.finishCalls != 0 || got.PreflightChecked {
		t.Fatalf("start audit failure must block preflight and iteration: result=%#v kill=%d runner=%d finish=%d", got, killSwitch.currentCalls, runner.calls, journal.finishCalls)
	}
}

func TestServiceRunBoundedLiveLoopFailsClosedWhenIterationAuditFails(t *testing.T) {
	now := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	auditErr := errors.New("iteration audit insert failed")
	runner := &fakeLiveLoopIterationRunner{}
	journal := &fakeLiveLoopJournal{iterationErr: auditErr}
	service := boundedLiveLoopServiceWithJournal(now, &fakeLiveKillSwitchRepository{}, runner, journal)

	got, err := service.RunBoundedLiveLoop(context.Background(), validBoundedLiveLoopRequest())
	if err == nil || !strings.Contains(err.Error(), auditErr.Error()) {
		t.Fatalf("expected iteration audit error, got %v", err)
	}
	if got.StopReason != applive.LiveLoopStopAuditJournalError ||
		got.IterationsAttempted != 1 ||
		got.IterationsSucceeded != 1 ||
		journal.startCalls != 1 ||
		journal.iterationCalls != 1 ||
		journal.finishCalls != 1 {
		t.Fatalf("iteration audit failure mismatch: result=%#v journal=%#v", got, journal)
	}
	if journal.finished[0].Status != domainlive.LiveLoopRunStatusFailed ||
		!strings.Contains(journal.finished[0].Error, auditErr.Error()) {
		t.Fatalf("failed finish audit mismatch: %#v", journal.finished[0])
	}
}

func TestServiceRunBoundedLiveLoopStopsWhenIterationRequestsStop(t *testing.T) {
	now := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	runner := &fakeLiveLoopIterationRunner{results: []applive.LiveLoopIterationResult{
		{Action: applive.LiveLoopIterationActionNone},
		{Action: applive.LiveLoopIterationActionStop, Reason: "operator_requested_review"},
	}}
	service := boundedLiveLoopService(now, &fakeLiveKillSwitchRepository{}, runner)

	got, err := service.RunBoundedLiveLoop(context.Background(), mutateBoundedLiveLoopRequest(func(req *applive.RunBoundedLiveLoopRequest) {
		req.MaxIterations = 10
	}))
	if err != nil {
		t.Fatalf("run bounded live loop with stop result: %v", err)
	}

	if got.StopReason != applive.LiveLoopStopIterationRequested || got.StopDetails != "operator_requested_review" ||
		got.IterationsSucceeded != 2 || runner.calls != 2 {
		t.Fatalf("requested stop mismatch: result=%#v calls=%d", got, runner.calls)
	}
}

func TestServiceRunBoundedLiveLoopBlocksUnsafePreflightBeforeIteration(t *testing.T) {
	now := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	killSwitch := &fakeLiveKillSwitchRepository{}
	runner := &fakeLiveLoopIterationRunner{}
	service := boundedLiveLoopService(now, killSwitch, runner)

	got, err := service.RunBoundedLiveLoop(context.Background(), mutateBoundedLiveLoopRequest(func(req *applive.RunBoundedLiveLoopRequest) {
		req.Preflight.TradingEnabled = false
	}))
	if err == nil || !strings.Contains(err.Error(), "startup preflight") || !strings.Contains(err.Error(), "trading.enabled") {
		t.Fatalf("expected preflight error, got %v", err)
	}
	if got.StopReason != applive.LiveLoopStopPreflightFailed || runner.calls != 0 || killSwitch.currentCalls != 1 {
		t.Fatalf("preflight failure should block iterations: result=%#v runner_calls=%d kill_calls=%d", got, runner.calls, killSwitch.currentCalls)
	}
}

func TestServiceRunBoundedLiveLoopChecksKillSwitchBeforeEveryIteration(t *testing.T) {
	now := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	killSwitch := &fakeLiveKillSwitchRepository{states: []domainrisk.KillSwitchState{
		{},
		{},
		{Active: true, Reason: "operator emergency stop", Source: "operator", UpdatedAt: now.Add(time.Second)},
	}}
	runner := &fakeLiveLoopIterationRunner{}
	service := boundedLiveLoopService(now, killSwitch, runner)

	got, err := service.RunBoundedLiveLoop(context.Background(), validBoundedLiveLoopRequest())
	if err == nil || !strings.Contains(err.Error(), "kill switch") {
		t.Fatalf("expected kill switch error, got %v", err)
	}
	if got.StopReason != applive.LiveLoopStopKillSwitchActive || got.IterationsSucceeded != 1 || runner.calls != 1 ||
		killSwitch.currentCalls != 3 {
		t.Fatalf("kill switch stop mismatch: result=%#v runner_calls=%d kill_calls=%d", got, runner.calls, killSwitch.currentCalls)
	}
}

func TestServiceRunBoundedLiveLoopFailsClosedWhenKillSwitchCannotBeChecked(t *testing.T) {
	now := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	repositoryErr := errors.New("postgres unavailable")
	killSwitch := &fakeLiveKillSwitchRepository{states: []domainrisk.KillSwitchState{{}}, errAt: 2, err: repositoryErr}
	runner := &fakeLiveLoopIterationRunner{}
	service := boundedLiveLoopService(now, killSwitch, runner)

	got, err := service.RunBoundedLiveLoop(context.Background(), validBoundedLiveLoopRequest())
	if err == nil || !strings.Contains(err.Error(), repositoryErr.Error()) {
		t.Fatalf("expected kill switch lookup error, got %v", err)
	}
	if got.StopReason != applive.LiveLoopStopSafetyCheckError || got.IterationsSucceeded != 0 || runner.calls != 0 ||
		killSwitch.currentCalls != 2 {
		t.Fatalf("kill switch lookup failure mismatch: result=%#v runner_calls=%d kill_calls=%d", got, runner.calls, killSwitch.currentCalls)
	}
}

func TestServiceRunBoundedLiveLoopReturnsIterationErrorWithoutContinuing(t *testing.T) {
	now := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	runnerErr := errors.New("strategy source unavailable")
	runner := &fakeLiveLoopIterationRunner{errAt: 2, err: runnerErr}
	service := boundedLiveLoopService(now, &fakeLiveKillSwitchRepository{}, runner)

	got, err := service.RunBoundedLiveLoop(context.Background(), validBoundedLiveLoopRequest())
	if err == nil || !strings.Contains(err.Error(), runnerErr.Error()) {
		t.Fatalf("expected iteration error, got %v", err)
	}
	if got.StopReason != applive.LiveLoopStopIterationError || got.IterationsAttempted != 2 ||
		got.IterationsSucceeded != 1 || len(got.Iterations) != 1 || runner.calls != 2 {
		t.Fatalf("iteration error mismatch: result=%#v calls=%d", got, runner.calls)
	}
}

func TestServiceRunBoundedLiveLoopRejectsUnsafeInputsTableDriven(t *testing.T) {
	now := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()

	tests := []struct {
		name       string
		ctx        context.Context
		service    *applive.Service
		req        applive.RunBoundedLiveLoopRequest
		wantErrSub string
	}{
		{
			name:       "cancelled context",
			ctx:        cancelled,
			service:    boundedLiveLoopService(now, &fakeLiveKillSwitchRepository{}, &fakeLiveLoopIterationRunner{}),
			req:        validBoundedLiveLoopRequest(),
			wantErrSub: "canceled",
		},
		{
			name: "missing runner",
			service: applive.NewService(
				applive.WithKillSwitchRepository(&fakeLiveKillSwitchRepository{}),
				applive.WithEnvironmentReader(validLiveStartupEnvironment()),
				applive.WithClock(clock.FixedClock{Time: now}),
			),
			req:        validBoundedLiveLoopRequest(),
			wantErrSub: "iteration runner",
		},
		{
			name: "missing kill switch",
			service: applive.NewService(
				applive.WithLiveLoopIterationRunner(&fakeLiveLoopIterationRunner{}),
				applive.WithEnvironmentReader(validLiveStartupEnvironment()),
				applive.WithClock(clock.FixedClock{Time: now}),
			),
			req:        validBoundedLiveLoopRequest(),
			wantErrSub: "kill switch",
		},
		{
			name: "missing environment reader",
			service: applive.NewService(
				applive.WithLiveLoopIterationRunner(&fakeLiveLoopIterationRunner{}),
				applive.WithKillSwitchRepository(&fakeLiveKillSwitchRepository{}),
				applive.WithEnvironmentReader(nil),
				applive.WithClock(clock.FixedClock{Time: now}),
			),
			req:        validBoundedLiveLoopRequest(),
			wantErrSub: "environment reader",
		},
		{
			name: "missing clock",
			service: applive.NewService(
				applive.WithLiveLoopIterationRunner(&fakeLiveLoopIterationRunner{}),
				applive.WithKillSwitchRepository(&fakeLiveKillSwitchRepository{}),
				applive.WithEnvironmentReader(validLiveStartupEnvironment()),
				applive.WithClock(nil),
			),
			req:        validBoundedLiveLoopRequest(),
			wantErrSub: "clock",
		},
		{
			name:       "missing run id",
			service:    boundedLiveLoopService(now, &fakeLiveKillSwitchRepository{}, &fakeLiveLoopIterationRunner{}),
			req:        mutateBoundedLiveLoopRequest(func(req *applive.RunBoundedLiveLoopRequest) { req.RunID = "" }),
			wantErrSub: "run_id",
		},
		{
			name:       "untrimmed run id",
			service:    boundedLiveLoopService(now, &fakeLiveKillSwitchRepository{}, &fakeLiveLoopIterationRunner{}),
			req:        mutateBoundedLiveLoopRequest(func(req *applive.RunBoundedLiveLoopRequest) { req.RunID = " live_loop_app_0001 " }),
			wantErrSub: "trimmed",
		},
		{
			name:       "zero max iterations",
			service:    boundedLiveLoopService(now, &fakeLiveKillSwitchRepository{}, &fakeLiveLoopIterationRunner{}),
			req:        mutateBoundedLiveLoopRequest(func(req *applive.RunBoundedLiveLoopRequest) { req.MaxIterations = 0 }),
			wantErrSub: "max_iterations",
		},
		{
			name:       "above max iterations ceiling",
			service:    boundedLiveLoopService(now, &fakeLiveKillSwitchRepository{}, &fakeLiveLoopIterationRunner{}),
			req:        mutateBoundedLiveLoopRequest(func(req *applive.RunBoundedLiveLoopRequest) { req.MaxIterations = 101 }),
			wantErrSub: "max_iterations",
		},
		{
			name:       "zero max runtime",
			service:    boundedLiveLoopService(now, &fakeLiveKillSwitchRepository{}, &fakeLiveLoopIterationRunner{}),
			req:        mutateBoundedLiveLoopRequest(func(req *applive.RunBoundedLiveLoopRequest) { req.MaxRuntime = 0 }),
			wantErrSub: "max_runtime",
		},
		{
			name:       "runtime above ceiling",
			service:    boundedLiveLoopService(now, &fakeLiveKillSwitchRepository{}, &fakeLiveLoopIterationRunner{}),
			req:        mutateBoundedLiveLoopRequest(func(req *applive.RunBoundedLiveLoopRequest) { req.MaxRuntime = 25 * time.Hour }),
			wantErrSub: "max_runtime",
		},
		{
			name:       "zero iteration timeout",
			service:    boundedLiveLoopService(now, &fakeLiveKillSwitchRepository{}, &fakeLiveLoopIterationRunner{}),
			req:        mutateBoundedLiveLoopRequest(func(req *applive.RunBoundedLiveLoopRequest) { req.IterationTimeout = 0 }),
			wantErrSub: "iteration_timeout",
		},
		{
			name:       "iteration timeout exceeds runtime",
			service:    boundedLiveLoopService(now, &fakeLiveKillSwitchRepository{}, &fakeLiveLoopIterationRunner{}),
			req:        mutateBoundedLiveLoopRequest(func(req *applive.RunBoundedLiveLoopRequest) { req.IterationTimeout = 2 * time.Minute }),
			wantErrSub: "iteration_timeout",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := tt.ctx
			if ctx == nil {
				ctx = context.Background()
			}

			_, err := tt.service.RunBoundedLiveLoop(ctx, tt.req)
			if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
		})
	}
}

func TestServiceRunBoundedLiveLoopRejectsInvalidIterationResultsTableDriven(t *testing.T) {
	now := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name       string
		result     applive.LiveLoopIterationResult
		wantErrSub string
	}{
		{
			name:       "wrong run id",
			result:     applive.LiveLoopIterationResult{RunID: "other"},
			wantErrSub: "run_id",
		},
		{
			name:       "wrong iteration",
			result:     applive.LiveLoopIterationResult{Iteration: 2},
			wantErrSub: "iteration",
		},
		{
			name:       "unknown action",
			result:     applive.LiveLoopIterationResult{Action: "TRADE"},
			wantErrSub: "action",
		},
		{
			name: "finished before started",
			result: applive.LiveLoopIterationResult{
				StartedAt:  now.Add(time.Second),
				FinishedAt: now,
			},
			wantErrSub: "finished_at",
		},
		{
			name:       "stop without reason",
			result:     applive.LiveLoopIterationResult{Action: applive.LiveLoopIterationActionStop},
			wantErrSub: "requires reason",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := &fakeLiveLoopIterationRunner{results: []applive.LiveLoopIterationResult{tt.result}}
			service := boundedLiveLoopService(now, &fakeLiveKillSwitchRepository{}, runner)

			got, err := service.RunBoundedLiveLoop(context.Background(), validBoundedLiveLoopRequest())
			if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
			if got.StopReason != applive.LiveLoopStopIterationError || runner.calls != 1 {
				t.Fatalf("bad iteration result should fail current iteration: result=%#v calls=%d", got, runner.calls)
			}
		})
	}
}

type fakeLiveLoopIterationRunner struct {
	requests []applive.LiveLoopIterationRequest
	results  []applive.LiveLoopIterationResult
	calls    int
	errAt    int
	err      error
}

func (r *fakeLiveLoopIterationRunner) RunLiveLoopIteration(_ context.Context, req applive.LiveLoopIterationRequest) (applive.LiveLoopIterationResult, error) {
	r.calls++
	r.requests = append(r.requests, req)
	if r.err != nil && r.errAt == r.calls {
		return applive.LiveLoopIterationResult{}, r.err
	}
	index := r.calls - 1
	if index < len(r.results) {
		return r.results[index], nil
	}
	return applive.LiveLoopIterationResult{}, nil
}

func boundedLiveLoopService(
	now time.Time,
	killSwitch *fakeLiveKillSwitchRepository,
	runner applive.LiveLoopIterationRunner,
) *applive.Service {
	return applive.NewService(
		applive.WithLiveLoopIterationRunner(runner),
		applive.WithKillSwitchRepository(killSwitch),
		applive.WithEnvironmentReader(validLiveStartupEnvironment()),
		applive.WithClock(clock.FixedClock{Time: now}),
	)
}

func boundedLiveLoopServiceWithJournal(
	now time.Time,
	killSwitch *fakeLiveKillSwitchRepository,
	runner applive.LiveLoopIterationRunner,
	journal *fakeLiveLoopJournal,
) *applive.Service {
	return applive.NewService(
		applive.WithLiveLoopIterationRunner(runner),
		applive.WithLiveLoopJournal(journal),
		applive.WithKillSwitchRepository(killSwitch),
		applive.WithEnvironmentReader(validLiveStartupEnvironment()),
		applive.WithClock(clock.FixedClock{Time: now}),
	)
}

func validBoundedLiveLoopRequest() applive.RunBoundedLiveLoopRequest {
	return applive.RunBoundedLiveLoopRequest{
		RunID:            "live_loop_app_0001",
		Preflight:        validLiveStartupRequest(),
		MaxIterations:    3,
		MaxRuntime:       time.Minute,
		IterationTimeout: 5 * time.Second,
	}
}

type fakeLiveLoopJournal struct {
	started        []domainlive.LiveLoopRunStarted
	finished       []domainlive.LiveLoopRunFinished
	iterations     []domainlive.LiveLoopIterationAudit
	startStats     domainlive.LiveLoopAuditStats
	finishStats    domainlive.LiveLoopAuditStats
	iterationStats domainlive.LiveLoopAuditStats
	startCalls     int
	finishCalls    int
	iterationCalls int
	startErr       error
	finishErr      error
	iterationErr   error
}

func (j *fakeLiveLoopJournal) RecordLiveLoopRunStarted(_ context.Context, run domainlive.LiveLoopRunStarted) (domainlive.LiveLoopAuditStats, error) {
	j.startCalls++
	j.started = append(j.started, run)
	if j.startErr != nil {
		return domainlive.LiveLoopAuditStats{}, j.startErr
	}
	if j.startStats.Total() > 0 {
		return j.startStats, nil
	}
	return domainlive.LiveLoopAuditStats{Inserted: 1}, nil
}

func (j *fakeLiveLoopJournal) RecordLiveLoopRunFinished(_ context.Context, run domainlive.LiveLoopRunFinished) (domainlive.LiveLoopAuditStats, error) {
	j.finishCalls++
	j.finished = append(j.finished, run)
	if j.finishErr != nil {
		return domainlive.LiveLoopAuditStats{}, j.finishErr
	}
	if j.finishStats.Total() > 0 {
		return j.finishStats, nil
	}
	return domainlive.LiveLoopAuditStats{Updated: 1}, nil
}

func (j *fakeLiveLoopJournal) RecordLiveLoopIteration(_ context.Context, iteration domainlive.LiveLoopIterationAudit) (domainlive.LiveLoopAuditStats, error) {
	j.iterationCalls++
	j.iterations = append(j.iterations, iteration)
	if j.iterationErr != nil {
		return domainlive.LiveLoopAuditStats{}, j.iterationErr
	}
	if j.iterationStats.Total() > 0 {
		return j.iterationStats, nil
	}
	return domainlive.LiveLoopAuditStats{Inserted: 1}, nil
}

func mutateBoundedLiveLoopRequest(mutate func(*applive.RunBoundedLiveLoopRequest)) applive.RunBoundedLiveLoopRequest {
	req := validBoundedLiveLoopRequest()
	mutate(&req)
	return req
}
