package paper_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	apppaper "github.com/VersoIt/Inquisitor/internal/app/paper"
	domainbacktest "github.com/VersoIt/Inquisitor/internal/backtest"
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

func TestServiceValidateCandidateRecordsAllowedPlan(t *testing.T) {
	plannedAt := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	requestedAt := plannedAt.Add(3 * time.Hour)
	run, result := testRunResult(t, plannedAt, domainresearch.OutcomeCandidate)
	records := &fakeValidationRecordRepository{
		stats: domainpaper.ValidationRecordStats{Inserted: 1},
	}
	ids := &fakeIDGenerator{id: "paper_validation_app_0001"}
	service := apppaper.NewService(
		&fakeRunRepository{runs: []domainresearch.Run{run}},
		&fakeResultRepository{results: []domainresearch.Result{result}},
		apppaper.WithClock(clock.FixedClock{Time: requestedAt}),
		apppaper.WithValidationRecordRepository(records),
		apppaper.WithIDGenerator(ids),
	)

	got, err := service.ValidateCandidate(context.Background(), apppaper.ValidateCandidateRequest{
		RunID:  run.RunID,
		Policy: validPolicy(),
		Record: true,
	})
	if err != nil {
		t.Fatalf("validate and record candidate: %v", err)
	}

	if got.Record.ValidationID != "paper_validation_app_0001" || got.Record.RunID != run.RunID {
		t.Fatalf("record identity mismatch: %#v", got.Record)
	}
	if got.RecordStats.Inserted != 1 || got.RecordStats.Updated != 0 {
		t.Fatalf("record stats mismatch: %#v", got.RecordStats)
	}
	if records.calls != 1 || records.record.ValidationID != got.Record.ValidationID {
		t.Fatalf("record repository call mismatch: calls=%d record=%#v", records.calls, records.record)
	}
	if ids.calls != 1 {
		t.Fatalf("expected one id generation call, got %d", ids.calls)
	}
	if !got.Record.PlannedAt.Equal(requestedAt) {
		t.Fatalf("planned_at mismatch: got %s want %s", got.Record.PlannedAt, requestedAt)
	}
}

func TestServiceValidateCandidateSkipsRecordingBlockedPlan(t *testing.T) {
	plannedAt := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	run, result := testRunResult(t, plannedAt, domainresearch.OutcomeRejected)
	records := &fakeValidationRecordRepository{}
	service := apppaper.NewService(
		&fakeRunRepository{runs: []domainresearch.Run{run}},
		&fakeResultRepository{results: []domainresearch.Result{result}},
		apppaper.WithClock(clock.FixedClock{Time: plannedAt.Add(3 * time.Hour)}),
		apppaper.WithValidationRecordRepository(records),
		apppaper.WithIDGenerator(&fakeIDGenerator{id: "paper_validation_app_0001"}),
	)

	got, err := service.ValidateCandidate(context.Background(), apppaper.ValidateCandidateRequest{
		RunID:  run.RunID,
		Policy: validPolicy(),
		Record: true,
	})
	if err != nil {
		t.Fatalf("validate blocked candidate: %v", err)
	}
	if got.Plan.Allowed {
		t.Fatalf("expected blocked plan: %#v", got.Plan)
	}
	if records.calls != 0 || got.Record.ValidationID != "" {
		t.Fatalf("blocked plan must not be recorded: calls=%d record=%#v", records.calls, got.Record)
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

func TestServiceValidateCandidateRejectsRecordingFailuresTableDriven(t *testing.T) {
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
			name: "missing record repository",
			service: apppaper.NewService(
				&fakeRunRepository{runs: []domainresearch.Run{run}},
				&fakeResultRepository{results: []domainresearch.Result{result}},
				apppaper.WithClock(clock.FixedClock{Time: plannedAt}),
				apppaper.WithIDGenerator(&fakeIDGenerator{id: "paper_validation_app_0001"}),
			),
			req: apppaper.ValidateCandidateRequest{
				RunID:  run.RunID,
				Policy: validPolicy(),
				Record: true,
			},
			wantErrSub: "validation record repository",
		},
		{
			name: "missing id generator",
			service: apppaper.NewService(
				&fakeRunRepository{runs: []domainresearch.Run{run}},
				&fakeResultRepository{results: []domainresearch.Result{result}},
				apppaper.WithClock(clock.FixedClock{Time: plannedAt}),
				apppaper.WithValidationRecordRepository(&fakeValidationRecordRepository{}),
				apppaper.WithIDGenerator(nil),
			),
			req: apppaper.ValidateCandidateRequest{
				RunID:  run.RunID,
				Policy: validPolicy(),
				Record: true,
			},
			wantErrSub: "id generator",
		},
		{
			name: "record repository error",
			service: apppaper.NewService(
				&fakeRunRepository{runs: []domainresearch.Run{run}},
				&fakeResultRepository{results: []domainresearch.Result{result}},
				apppaper.WithClock(clock.FixedClock{Time: plannedAt}),
				apppaper.WithValidationRecordRepository(&fakeValidationRecordRepository{err: repositoryErr}),
				apppaper.WithIDGenerator(&fakeIDGenerator{id: "paper_validation_app_0001"}),
			),
			req: apppaper.ValidateCandidateRequest{
				RunID:  run.RunID,
				Policy: validPolicy(),
				Record: true,
			},
			wantErrSub: repositoryErr.Error(),
		},
		{
			name: "explicit validation id bypasses generator",
			service: apppaper.NewService(
				&fakeRunRepository{runs: []domainresearch.Run{run}},
				&fakeResultRepository{results: []domainresearch.Result{result}},
				apppaper.WithClock(clock.FixedClock{Time: plannedAt}),
				apppaper.WithValidationRecordRepository(&fakeValidationRecordRepository{}),
				apppaper.WithIDGenerator(nil),
			),
			req: apppaper.ValidateCandidateRequest{
				RunID:        run.RunID,
				Policy:       validPolicy(),
				Record:       true,
				ValidationID: "paper_validation_explicit_0001",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.service.ValidateCandidate(context.Background(), tt.req)
			if tt.wantErrSub == "" {
				if err != nil {
					t.Fatalf("validate candidate: %v", err)
				}
				if got.Record.ValidationID != tt.req.ValidationID {
					t.Fatalf("explicit validation id mismatch: %#v", got.Record)
				}
				return
			}
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
		})
	}
}

func TestServiceValidationLifecycleTransitionsTableDriven(t *testing.T) {
	plannedAt := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	run, result := testRunResult(t, plannedAt, domainresearch.OutcomeCandidate)

	tests := []struct {
		name          string
		record        domainpaper.ValidationRecord
		now           time.Time
		run           func(*apppaper.Service) (apppaper.LifecycleResult, error)
		wantStatus    domainpaper.ValidationStatus
		wantExpected  domainpaper.ValidationStatus
		wantErrSub    string
		wantRepoCalls int
	}{
		{
			name:   "starts planned candidate",
			record: testValidationRecord(t, run.RunID, plannedAt, domainpaper.ValidationStatusPlanned),
			now:    plannedAt.Add(time.Hour),
			run: func(service *apppaper.Service) (apppaper.LifecycleResult, error) {
				return service.StartValidation(context.Background(), " paper_validation_app_0001 ")
			},
			wantStatus: domainpaper.ValidationStatusRunning, wantExpected: domainpaper.ValidationStatusPlanned, wantRepoCalls: 1,
		},
		{
			name:   "completes at minimum boundary",
			record: testValidationRecord(t, run.RunID, plannedAt, domainpaper.ValidationStatusRunning),
			now:    plannedAt.Add(time.Hour).AddDate(0, 0, 30),
			run: func(service *apppaper.Service) (apppaper.LifecycleResult, error) {
				return service.CompleteValidation(context.Background(), "paper_validation_app_0001")
			},
			wantStatus: domainpaper.ValidationStatusCompleted, wantExpected: domainpaper.ValidationStatusRunning, wantRepoCalls: 1,
		},
		{
			name:   "rejects early completion",
			record: testValidationRecord(t, run.RunID, plannedAt, domainpaper.ValidationStatusRunning),
			now:    plannedAt.Add(time.Hour).AddDate(0, 0, 30).Add(-time.Nanosecond),
			run: func(service *apppaper.Service) (apppaper.LifecycleResult, error) {
				return service.CompleteValidation(context.Background(), "paper_validation_app_0001")
			},
			wantErrSub: "minimum validation period", wantRepoCalls: 0,
		},
		{
			name:   "cancels running validation",
			record: testValidationRecord(t, run.RunID, plannedAt, domainpaper.ValidationStatusRunning),
			now:    plannedAt.Add(2 * time.Hour),
			run: func(service *apppaper.Service) (apppaper.LifecycleResult, error) {
				return service.CancelValidation(context.Background(), "paper_validation_app_0001", " operator stop ")
			},
			wantStatus: domainpaper.ValidationStatusCancelled, wantExpected: domainpaper.ValidationStatusRunning, wantRepoCalls: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			records := &fakeValidationRecordRepository{
				records: []domainpaper.ValidationRecord{tt.record},
				stats:   domainpaper.ValidationRecordStats{Updated: 1},
			}
			service := apppaper.NewService(
				&fakeRunRepository{runs: []domainresearch.Run{run}},
				&fakeResultRepository{results: []domainresearch.Result{result}},
				apppaper.WithValidationRecordRepository(records),
				apppaper.WithValidationTradeRepository(&fakeValidationTradeRepository{}),
				apppaper.WithOpenPositionRepository(&fakeOpenPositionRepository{}),
				apppaper.WithPositionCloseRepository(&fakePositionCloseRepository{}),
				apppaper.WithEquityEventRepository(&fakeEquityEventRepository{}),
				apppaper.WithClock(clock.FixedClock{Time: tt.now}),
			)
			got, err := tt.run(service)
			if tt.wantErrSub != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
				}
			} else {
				if err != nil {
					t.Fatalf("transition validation: %v", err)
				}
				if got.Record.Status != tt.wantStatus || got.Stats.Updated != 1 {
					t.Fatalf("lifecycle result mismatch: %#v", got)
				}
			}
			if records.transitionCalls != tt.wantRepoCalls {
				t.Fatalf("transition call count mismatch: got %d want %d", records.transitionCalls, tt.wantRepoCalls)
			}
			if tt.wantRepoCalls > 0 && records.transitionExpected != tt.wantExpected {
				t.Fatalf("expected status mismatch: got %s want %s", records.transitionExpected, tt.wantExpected)
			}
		})
	}
}

func TestServiceCompleteValidationRequiresSettledPositionJournalTableDriven(t *testing.T) {
	plannedAt := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	record := testValidationRecord(t, "research_paper_app_0001", plannedAt, domainpaper.ValidationStatusRunning)
	now := record.StartedAt.AddDate(0, 0, record.MinimumDays)
	position := appOpenPosition(plannedAt)
	position.ValidationID = record.ValidationID
	close := appPositionClose(plannedAt)
	close.ValidationID = record.ValidationID
	close.PositionID = position.PositionID
	close.EntryFillID = position.FillID
	close.TicketID = position.TicketID
	close.DecisionID = position.DecisionID
	close.IntentID = position.IntentID
	close.Exchange = position.Exchange
	close.Category = position.Category
	close.Symbol = position.Symbol
	close.Interval = position.Interval
	close.Side = position.Side
	close.Quantity = position.Quantity
	close.EntryPrice = position.EntryPrice
	close.EntryNotional = position.EntryNotional
	close.EntryFee = position.EntryFee
	close.OpenedAt = position.OpenedAt
	close.ClosedAt = position.OpenedAt.Add(2 * time.Minute)
	close.RecordedAt = close.ClosedAt.Add(time.Minute)
	duplicateClose := close
	duplicateClose.CloseID = "paper_close_app_0002"
	repositoryErr := errors.New("postgres unavailable")

	tests := []struct {
		name                string
		positions           *fakeOpenPositionRepository
		closes              *fakePositionCloseRepository
		equity              *fakeEquityEventRepository
		wantErrSub          string
		wantTransitionCalls int
		wantPositionQueries int
		wantCloseQueries    int
		wantEquityQueries   int
	}{
		{
			name:                "completes when no positions exist",
			positions:           &fakeOpenPositionRepository{},
			closes:              &fakePositionCloseRepository{},
			equity:              &fakeEquityEventRepository{},
			wantTransitionCalls: 1,
			wantPositionQueries: 1,
		},
		{
			name:                "completes when every closed position has equity event",
			positions:           &fakeOpenPositionRepository{positions: []domainpaper.OpenPosition{position}},
			closes:              &fakePositionCloseRepository{closes: []domainpaper.PositionClose{close}},
			equity:              &fakeEquityEventRepository{events: []domainpaper.EquityEvent{appEquityEventForClose(plannedAt, close, "paper_equity_app_0001")}},
			wantTransitionCalls: 1,
			wantPositionQueries: 1,
			wantCloseQueries:    1,
			wantEquityQueries:   1,
		},
		{
			name:                "rejects active open position",
			positions:           &fakeOpenPositionRepository{positions: []domainpaper.OpenPosition{position}},
			closes:              &fakePositionCloseRepository{},
			equity:              &fakeEquityEventRepository{},
			wantErrSub:          "active open positions",
			wantPositionQueries: 1,
			wantCloseQueries:    1,
		},
		{
			name:                "rejects closed position without equity event",
			positions:           &fakeOpenPositionRepository{positions: []domainpaper.OpenPosition{position}},
			closes:              &fakePositionCloseRepository{closes: []domainpaper.PositionClose{close}},
			equity:              &fakeEquityEventRepository{},
			wantErrSub:          "equity ledger events",
			wantPositionQueries: 1,
			wantCloseQueries:    1,
			wantEquityQueries:   1,
		},
		{
			name:                "rejects duplicate close journal",
			positions:           &fakeOpenPositionRepository{positions: []domainpaper.OpenPosition{position}},
			closes:              &fakePositionCloseRepository{closes: []domainpaper.PositionClose{close, duplicateClose}},
			equity:              &fakeEquityEventRepository{},
			wantErrSub:          "inconsistent close journal",
			wantPositionQueries: 1,
			wantCloseQueries:    1,
		},
		{
			name:      "rejects duplicate equity events",
			positions: &fakeOpenPositionRepository{positions: []domainpaper.OpenPosition{position}},
			closes:    &fakePositionCloseRepository{closes: []domainpaper.PositionClose{close}},
			equity: &fakeEquityEventRepository{events: []domainpaper.EquityEvent{
				appEquityEventForClose(plannedAt, close, "paper_equity_app_0001"),
				appEquityEventForClose(plannedAt, close, "paper_equity_app_0002"),
			}},
			wantErrSub:          "inconsistent equity ledger",
			wantPositionQueries: 1,
			wantCloseQueries:    1,
			wantEquityQueries:   1,
		},
		{
			name:       "rejects missing open position repository",
			closes:     &fakePositionCloseRepository{},
			equity:     &fakeEquityEventRepository{},
			wantErrSub: "open position repository",
		},
		{
			name:       "rejects missing position close repository",
			positions:  &fakeOpenPositionRepository{},
			equity:     &fakeEquityEventRepository{},
			wantErrSub: "position close repository",
		},
		{
			name:       "rejects missing equity event repository",
			positions:  &fakeOpenPositionRepository{},
			closes:     &fakePositionCloseRepository{},
			wantErrSub: "equity event repository",
		},
		{
			name:                "reports position scan failure",
			positions:           &fakeOpenPositionRepository{err: repositoryErr},
			closes:              &fakePositionCloseRepository{},
			equity:              &fakeEquityEventRepository{},
			wantErrSub:          repositoryErr.Error(),
			wantPositionQueries: 1,
		},
		{
			name:                "reports close lookup failure",
			positions:           &fakeOpenPositionRepository{positions: []domainpaper.OpenPosition{position}},
			closes:              &fakePositionCloseRepository{listErr: repositoryErr},
			equity:              &fakeEquityEventRepository{},
			wantErrSub:          repositoryErr.Error(),
			wantPositionQueries: 1,
			wantCloseQueries:    1,
		},
		{
			name:                "reports equity lookup failure",
			positions:           &fakeOpenPositionRepository{positions: []domainpaper.OpenPosition{position}},
			closes:              &fakePositionCloseRepository{closes: []domainpaper.PositionClose{close}},
			equity:              &fakeEquityEventRepository{listErr: repositoryErr},
			wantErrSub:          repositoryErr.Error(),
			wantPositionQueries: 1,
			wantCloseQueries:    1,
			wantEquityQueries:   1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			records := &fakeValidationRecordRepository{
				records: []domainpaper.ValidationRecord{record},
				stats:   domainpaper.ValidationRecordStats{Updated: 1},
			}
			options := []apppaper.Option{
				apppaper.WithValidationRecordRepository(records),
				apppaper.WithClock(clock.FixedClock{Time: now}),
			}
			if tt.positions != nil {
				options = append(options, apppaper.WithOpenPositionRepository(tt.positions))
			}
			if tt.closes != nil {
				options = append(options, apppaper.WithPositionCloseRepository(tt.closes))
			}
			if tt.equity != nil {
				options = append(options, apppaper.WithEquityEventRepository(tt.equity))
			}
			service := apppaper.NewService(&fakeRunRepository{}, &fakeResultRepository{}, options...)

			got, err := service.CompleteValidation(context.Background(), record.ValidationID)
			if tt.wantErrSub != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
				}
			} else {
				if err != nil {
					t.Fatalf("complete validation: %v", err)
				}
				if got.Record.Status != domainpaper.ValidationStatusCompleted || got.Stats.Updated != 1 {
					t.Fatalf("completion result mismatch: %#v", got)
				}
			}
			if records.transitionCalls != tt.wantTransitionCalls {
				t.Fatalf("transition call count mismatch: got %d want %d", records.transitionCalls, tt.wantTransitionCalls)
			}
			if tt.wantTransitionCalls > 0 && records.transitionExpected != domainpaper.ValidationStatusRunning {
				t.Fatalf("transition expected status mismatch: got %s want %s", records.transitionExpected, domainpaper.ValidationStatusRunning)
			}
			if tt.positions != nil && len(tt.positions.queries) != tt.wantPositionQueries {
				t.Fatalf("position query count mismatch: got %d want %d queries=%#v", len(tt.positions.queries), tt.wantPositionQueries, tt.positions.queries)
			}
			if tt.wantPositionQueries > 0 && tt.positions.queries[0].ValidationID != record.ValidationID {
				t.Fatalf("position query scope mismatch: %#v", tt.positions.queries[0])
			}
			if tt.closes != nil && len(tt.closes.queries) != tt.wantCloseQueries {
				t.Fatalf("close query count mismatch: got %d want %d queries=%#v", len(tt.closes.queries), tt.wantCloseQueries, tt.closes.queries)
			}
			if tt.wantCloseQueries > 0 && (tt.closes.queries[0].ValidationID != record.ValidationID || tt.closes.queries[0].PositionID != position.PositionID || tt.closes.queries[0].Limit != 2) {
				t.Fatalf("close query scope mismatch: %#v", tt.closes.queries[0])
			}
			if tt.equity != nil && len(tt.equity.queries) != tt.wantEquityQueries {
				t.Fatalf("equity query count mismatch: got %d want %d queries=%#v", len(tt.equity.queries), tt.wantEquityQueries, tt.equity.queries)
			}
			if tt.wantEquityQueries > 0 && (tt.equity.queries[0].CloseID != close.CloseID || tt.equity.queries[0].Limit != 2) {
				t.Fatalf("equity query scope mismatch: %#v", tt.equity.queries[0])
			}
		})
	}
}

func TestServiceBuildPerformanceReportPersistsDailySnapshots(t *testing.T) {
	plannedAt := time.Date(2026, 6, 18, 8, 0, 0, 0, time.UTC)
	record := testValidationRecord(t, "research_paper_app_0001", plannedAt, domainpaper.ValidationStatusPlanned)
	first := mustRoundTrip(t, plannedAt.Add(time.Hour), domainbacktest.DirectionLong, "100", "110")
	second := mustRoundTrip(t, plannedAt.Add(25*time.Hour), domainbacktest.DirectionLong, "100", "95")
	journal, err := domainpaper.NewValidationTradeSequence(domainpaper.ValidationTradeSequenceInput{
		ValidationID: record.ValidationID, TradeIDPrefix: "paper_trade", Exchange: "bybit", Category: "linear",
		Symbol: "BTCUSDT", Interval: "1", RoundTrips: []domainbacktest.RoundTrip{first, second},
		InitialEquity: record.InitialBalance, RecordedAt: second.Exit.Time.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("new validation trade sequence: %v", err)
	}
	performance := &fakeDailyPerformanceRepository{stats: domainpaper.DailyPerformanceStats{Inserted: 2}}
	service := apppaper.NewService(
		nil,
		nil,
		apppaper.WithValidationRecordRepository(&fakeValidationRecordRepository{records: []domainpaper.ValidationRecord{record}}),
		apppaper.WithValidationTradeRepository(&fakeValidationTradeRepository{records: journal}),
		apppaper.WithDailyPerformanceRepository(performance),
		apppaper.WithClock(clock.FixedClock{Time: plannedAt.Add(48 * time.Hour)}),
	)

	got, err := service.BuildPerformanceReport(context.Background(), apppaper.PerformanceReportRequest{
		ValidationID: " paper_validation_app_0001 ",
		RecordDaily:  true,
	})
	if err != nil {
		t.Fatalf("build performance report: %v", err)
	}
	if got.Summary.Trades != 2 || len(got.Daily) != 2 || got.DailyStats.Inserted != 2 {
		t.Fatalf("performance report mismatch: %#v", got)
	}
	if performance.calls != 1 || len(performance.records) != 2 {
		t.Fatalf("daily persistence mismatch: %#v", performance)
	}
}

func TestServiceStartValidationRejectsOfflineSimulationJournal(t *testing.T) {
	plannedAt := time.Date(2026, 6, 18, 8, 0, 0, 0, time.UTC)
	run, result := testRunResult(t, plannedAt, domainresearch.OutcomeCandidate)
	record := testValidationRecord(t, run.RunID, plannedAt.Add(time.Hour), domainpaper.ValidationStatusPlanned)
	journal, err := domainpaper.NewValidationTradeSequence(domainpaper.ValidationTradeSequenceInput{
		ValidationID: record.ValidationID, TradeIDPrefix: "offline", Exchange: "bybit", Category: "linear",
		Symbol: "BTCUSDT", Interval: "1",
		RoundTrips:    []domainbacktest.RoundTrip{mustRoundTrip(t, plannedAt.Add(2*time.Hour), domainbacktest.DirectionLong, "100", "110")},
		InitialEquity: record.InitialBalance, RecordedAt: plannedAt.Add(4 * time.Hour),
	})
	if err != nil {
		t.Fatalf("new offline journal: %v", err)
	}
	records := &fakeValidationRecordRepository{records: []domainpaper.ValidationRecord{record}}
	service := apppaper.NewService(
		&fakeRunRepository{runs: []domainresearch.Run{run}},
		&fakeResultRepository{results: []domainresearch.Result{result}},
		apppaper.WithValidationRecordRepository(records),
		apppaper.WithValidationTradeRepository(&fakeValidationTradeRepository{records: journal}),
		apppaper.WithClock(clock.FixedClock{Time: plannedAt.Add(5 * time.Hour)}),
	)

	_, err = service.StartValidation(context.Background(), record.ValidationID)
	if err == nil || !strings.Contains(err.Error(), "empty journal") {
		t.Fatalf("expected offline journal rejection, got %v", err)
	}
	if records.transitionCalls != 0 {
		t.Fatalf("rejected start must not transition record, got %d calls", records.transitionCalls)
	}
}

func TestServiceRecordSimulationPersistsCandidateValidationTrades(t *testing.T) {
	plannedAt := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	recordedAt := plannedAt.Add(4 * time.Hour)
	run, result := testRunResult(t, plannedAt, domainresearch.OutcomeCandidate)
	records := &fakeValidationRecordRepository{
		records: []domainpaper.ValidationRecord{testValidationRecord(t, run.RunID, plannedAt.Add(2*time.Hour), domainpaper.ValidationStatusPlanned)},
	}
	trades := &fakeValidationTradeRepository{
		stats: domainpaper.ValidationTradeStats{Inserted: 2},
	}
	service := apppaper.NewService(
		&fakeRunRepository{runs: []domainresearch.Run{run}},
		&fakeResultRepository{results: []domainresearch.Result{result}},
		apppaper.WithClock(clock.FixedClock{Time: recordedAt}),
		apppaper.WithValidationRecordRepository(records),
		apppaper.WithValidationTradeRepository(trades),
	)

	first := mustRoundTrip(t, plannedAt.Add(time.Hour), domainbacktest.DirectionLong, "100", "110")
	second := mustRoundTrip(t, plannedAt.Add(2*time.Hour), domainbacktest.DirectionShort, "100", "105")
	got, err := service.RecordSimulation(context.Background(), apppaper.RecordSimulationRequest{
		ValidationID:  " paper_validation_app_0001 ",
		TradeIDPrefix: "paper_trade_app",
		RoundTrips:    []domainbacktest.RoundTrip{first, second},
	})
	if err != nil {
		t.Fatalf("record paper simulation: %v", err)
	}

	if got.Record.ValidationID != "paper_validation_app_0001" || got.Run.RunID != run.RunID || got.Result.Outcome != domainresearch.OutcomeCandidate {
		t.Fatalf("payload mismatch: %#v", got)
	}
	if got.Summary.Trades != 2 || got.Stats.Inserted != 2 {
		t.Fatalf("summary/stats mismatch: summary=%#v stats=%#v", got.Summary, got.Stats)
	}
	if len(got.Trades) != 2 || len(trades.records) != 2 || trades.calls != 1 {
		t.Fatalf("trade repository mismatch: result=%d stored=%d calls=%d", len(got.Trades), len(trades.records), trades.calls)
	}
	if trades.expectedStatus != domainpaper.ValidationStatusPlanned {
		t.Fatalf("offline simulation must use PLANNED status guard, got %s", trades.expectedStatus)
	}
	if got.Trades[0].TradeID != "paper_trade_app_000001" || got.Trades[1].TradeID != "paper_trade_app_000002" {
		t.Fatalf("trade ids mismatch: %#v", got.Trades)
	}
	if got.Trades[0].Symbol != "BTCUSDT" || got.Trades[0].Interval != "1" {
		t.Fatalf("defaulted market scope mismatch: %#v", got.Trades[0])
	}
	if !got.Trades[1].EquityBefore.Equal(got.Trades[0].EquityAfter) {
		t.Fatalf("equity chain mismatch: first after=%s second before=%s", got.Trades[0].EquityAfter, got.Trades[1].EquityBefore)
	}
	if !got.Trades[0].RecordedAt.Equal(recordedAt) {
		t.Fatalf("recorded_at mismatch: got %s want %s", got.Trades[0].RecordedAt, recordedAt)
	}
	if len(records.queries) != 1 || records.queries[0].ValidationID != "paper_validation_app_0001" || records.queries[0].Limit != 2 {
		t.Fatalf("record query mismatch: %#v", records.queries)
	}
}

func TestServiceRecordSimulationAllowsNoTradeJournal(t *testing.T) {
	plannedAt := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	run, result := testRunResult(t, plannedAt, domainresearch.OutcomeCandidate)
	trades := &fakeValidationTradeRepository{}
	service := apppaper.NewService(
		&fakeRunRepository{runs: []domainresearch.Run{run}},
		&fakeResultRepository{results: []domainresearch.Result{result}},
		apppaper.WithClock(clock.FixedClock{Time: plannedAt.Add(4 * time.Hour)}),
		apppaper.WithValidationRecordRepository(&fakeValidationRecordRepository{
			records: []domainpaper.ValidationRecord{testValidationRecord(t, run.RunID, plannedAt.Add(2*time.Hour), domainpaper.ValidationStatusPlanned)},
		}),
		apppaper.WithValidationTradeRepository(trades),
	)

	got, err := service.RecordSimulation(context.Background(), apppaper.RecordSimulationRequest{
		ValidationID: "paper_validation_app_0001",
	})
	if err != nil {
		t.Fatalf("record empty paper simulation: %v", err)
	}
	if got.Summary.Trades != 0 || len(got.Trades) != 0 || trades.calls != 0 || got.Stats.Total() != 0 {
		t.Fatalf("empty simulation should not write trades: result=%#v calls=%d", got, trades.calls)
	}
}

func TestServiceGenerateSimulationRecordsGeneratedCandidateTrades(t *testing.T) {
	plannedAt := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	run, result := testRunResult(t, plannedAt, domainresearch.OutcomeCandidate)
	roundTrip := mustRoundTrip(t, plannedAt.Add(time.Hour), domainbacktest.DirectionLong, "100", "110")
	generator := &fakeSimulationTradeGenerator{
		result: apppaper.SimulationTradeGenerationResult{
			Symbol:             "BTCUSDT",
			Interval:           "1",
			RoundTrips:         []domainbacktest.RoundTrip{roundTrip},
			CoverageExpected:   10,
			CoverageObserved:   10,
			CoveragePct:        100,
			CoverageSufficient: true,
		},
	}
	trades := &fakeValidationTradeRepository{stats: domainpaper.ValidationTradeStats{Inserted: 1}}
	service := apppaper.NewService(
		&fakeRunRepository{runs: []domainresearch.Run{run}},
		&fakeResultRepository{results: []domainresearch.Result{result}},
		apppaper.WithClock(clock.FixedClock{Time: plannedAt.Add(4 * time.Hour)}),
		apppaper.WithValidationRecordRepository(&fakeValidationRecordRepository{
			records: []domainpaper.ValidationRecord{testValidationRecord(t, run.RunID, plannedAt.Add(2*time.Hour), domainpaper.ValidationStatusPlanned)},
		}),
		apppaper.WithValidationTradeRepository(trades),
		apppaper.WithSimulationTradeGenerator(generator),
	)
	costs, err := domainbacktest.NewCostModel(1, 6, 2, 3, 1.5)
	if err != nil {
		t.Fatalf("new cost model: %v", err)
	}

	got, err := service.GenerateSimulation(context.Background(), apppaper.GenerateSimulationRequest{
		ValidationID:         "paper_validation_app_0001",
		TradeIDPrefix:        "generated_trade",
		FeatureLookback:      168 * time.Hour,
		MinRegimeCoveragePct: 100,
		HoldingPeriodCandles: 2,
		Quantity:             decimal.RequireFromString("0.25"),
		Costs:                costs,
		CandleLimit:          500,
		TradeLimit:           300,
		SnapshotLimit:        20,
		UseRuntimeState:      true,
	})
	if err != nil {
		t.Fatalf("generate paper simulation: %v", err)
	}

	if generator.calls != 1 || generator.req.Run.RunID != run.RunID {
		t.Fatalf("generator call mismatch: calls=%d request=%#v", generator.calls, generator.req)
	}
	if generator.req.Symbol != "BTCUSDT" || generator.req.Interval != "1" || generator.req.HoldingPeriodCandles != 2 {
		t.Fatalf("generator scope/options mismatch: %#v", generator.req)
	}
	if !generator.req.Quantity.Equal(decimal.RequireFromString("0.25")) || !generator.req.Costs.SpreadBPS.Equal(decimal.RequireFromString("2")) {
		t.Fatalf("generator quantity/costs mismatch: %#v", generator.req)
	}
	if got.Generation.CoveragePct != 100 || got.Summary.Trades != 1 || got.Stats.Inserted != 1 {
		t.Fatalf("generated result mismatch: %#v", got)
	}
	if len(got.Trades) != 1 || got.Trades[0].TradeID != "generated_trade_000001" || trades.calls != 1 {
		t.Fatalf("generated journal mismatch: result=%#v repository_calls=%d", got.Trades, trades.calls)
	}
}

func TestServiceGenerateSimulationRejectsUnsafeGenerationTableDriven(t *testing.T) {
	plannedAt := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	run, result := testRunResult(t, plannedAt, domainresearch.OutcomeCandidate)
	record := testValidationRecord(t, run.RunID, plannedAt.Add(2*time.Hour), domainpaper.ValidationStatusPlanned)
	generatorErr := errors.New("feature store unavailable")

	newService := func(generator apppaper.SimulationTradeGenerator, trades *fakeValidationTradeRepository) *apppaper.Service {
		options := []apppaper.Option{
			apppaper.WithClock(clock.FixedClock{Time: plannedAt.Add(4 * time.Hour)}),
			apppaper.WithValidationRecordRepository(&fakeValidationRecordRepository{records: []domainpaper.ValidationRecord{record}}),
			apppaper.WithValidationTradeRepository(trades),
		}
		if generator != nil {
			options = append(options, apppaper.WithSimulationTradeGenerator(generator))
		}
		return apppaper.NewService(
			&fakeRunRepository{runs: []domainresearch.Run{run}},
			&fakeResultRepository{results: []domainresearch.Result{result}},
			options...,
		)
	}

	tests := []struct {
		name       string
		generator  apppaper.SimulationTradeGenerator
		wantErrSub string
	}{
		{
			name:       "missing generator",
			wantErrSub: "trade generator",
		},
		{
			name: "generator error",
			generator: &fakeSimulationTradeGenerator{
				err: generatorErr,
			},
			wantErrSub: generatorErr.Error(),
		},
		{
			name: "insufficient regime coverage",
			generator: &fakeSimulationTradeGenerator{
				result: apppaper.SimulationTradeGenerationResult{
					Symbol:           "BTCUSDT",
					Interval:         "1",
					CoverageExpected: 10,
					CoverageObserved: 8,
					CoverageMissing:  2,
					CoveragePct:      80,
				},
			},
			wantErrSub: "coverage",
		},
		{
			name: "mismatched generated scope",
			generator: &fakeSimulationTradeGenerator{
				result: apppaper.SimulationTradeGenerationResult{
					Symbol:             "ETHUSDT",
					Interval:           "1",
					CoverageSufficient: true,
				},
			},
			wantErrSub: "mismatched market scope",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			trades := &fakeValidationTradeRepository{}
			_, err := newService(tt.generator, trades).GenerateSimulation(context.Background(), apppaper.GenerateSimulationRequest{
				ValidationID: "paper_validation_app_0001",
			})
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
			if trades.calls != 0 || len(trades.records) != 0 {
				t.Fatalf("unsafe generation must not write trades: calls=%d records=%d", trades.calls, len(trades.records))
			}
		})
	}
}

func TestServiceRecordSimulationAllowsExactRerunAndRejectsConflictingTradeSet(t *testing.T) {
	plannedAt := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	run, result := testRunResult(t, plannedAt, domainresearch.OutcomeCandidate)
	trades := &fakeValidationTradeRepository{stats: domainpaper.ValidationTradeStats{Updated: 1}}
	service := apppaper.NewService(
		&fakeRunRepository{runs: []domainresearch.Run{run}},
		&fakeResultRepository{results: []domainresearch.Result{result}},
		apppaper.WithClock(clock.FixedClock{Time: plannedAt.Add(4 * time.Hour)}),
		apppaper.WithValidationRecordRepository(&fakeValidationRecordRepository{
			records: []domainpaper.ValidationRecord{testValidationRecord(t, run.RunID, plannedAt.Add(2*time.Hour), domainpaper.ValidationStatusPlanned)},
		}),
		apppaper.WithValidationTradeRepository(trades),
	)
	roundTrip := mustRoundTrip(t, plannedAt.Add(time.Hour), domainbacktest.DirectionLong, "100", "110")
	request := apppaper.RecordSimulationRequest{
		ValidationID:  "paper_validation_app_0001",
		TradeIDPrefix: "stable_trade",
		RoundTrips:    []domainbacktest.RoundTrip{roundTrip},
	}

	if _, err := service.RecordSimulation(context.Background(), request); err != nil {
		t.Fatalf("record initial simulation: %v", err)
	}
	trades.records = append([]domainpaper.ValidationTrade(nil), trades.records[:1]...)
	if _, err := service.RecordSimulation(context.Background(), request); err != nil {
		t.Fatalf("exact rerun should be accepted: %v", err)
	}
	callsAfterRerun := trades.calls

	request.TradeIDPrefix = "different_trade"
	_, err := service.RecordSimulation(context.Background(), request)
	if err == nil {
		t.Fatal("expected conflicting journal error")
	}
	if !strings.Contains(err.Error(), "different trade set") {
		t.Fatalf("expected conflicting trade-set error, got %v", err)
	}
	if trades.calls != callsAfterRerun {
		t.Fatalf("conflicting journal must not be written: before=%d after=%d", callsAfterRerun, trades.calls)
	}
}

func TestServiceRecordSimulationRejectsUnsafeInputsTableDriven(t *testing.T) {
	plannedAt := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	run, candidateResult := testRunResult(t, plannedAt, domainresearch.OutcomeCandidate)
	_, rejectedResult := testRunResult(t, plannedAt, domainresearch.OutcomeRejected)
	validRecord := testValidationRecord(t, run.RunID, plannedAt.Add(2*time.Hour), domainpaper.ValidationStatusPlanned)
	validTrade := mustRoundTrip(t, plannedAt.Add(time.Hour), domainbacktest.DirectionLong, "100", "110")
	repositoryErr := errors.New("postgres unavailable")

	tests := []struct {
		name       string
		service    *apppaper.Service
		req        apppaper.RecordSimulationRequest
		wantErrSub string
	}{
		{
			name: "missing validation id",
			service: apppaper.NewService(
				&fakeRunRepository{runs: []domainresearch.Run{run}},
				&fakeResultRepository{results: []domainresearch.Result{candidateResult}},
				apppaper.WithValidationRecordRepository(&fakeValidationRecordRepository{records: []domainpaper.ValidationRecord{validRecord}}),
				apppaper.WithValidationTradeRepository(&fakeValidationTradeRepository{}),
			),
			req:        apppaper.RecordSimulationRequest{ValidationID: " "},
			wantErrSub: "validation_id",
		},
		{
			name: "validation record not found",
			service: apppaper.NewService(
				&fakeRunRepository{runs: []domainresearch.Run{run}},
				&fakeResultRepository{results: []domainresearch.Result{candidateResult}},
				apppaper.WithValidationRecordRepository(&fakeValidationRecordRepository{}),
				apppaper.WithValidationTradeRepository(&fakeValidationTradeRepository{}),
			),
			req:        apppaper.RecordSimulationRequest{ValidationID: validRecord.ValidationID},
			wantErrSub: "not found",
		},
		{
			name: "non planned validation",
			service: apppaper.NewService(
				&fakeRunRepository{runs: []domainresearch.Run{run}},
				&fakeResultRepository{results: []domainresearch.Result{candidateResult}},
				apppaper.WithValidationRecordRepository(&fakeValidationRecordRepository{
					records: []domainpaper.ValidationRecord{testValidationRecord(t, run.RunID, plannedAt.Add(2*time.Hour), domainpaper.ValidationStatusCompleted)},
				}),
				apppaper.WithValidationTradeRepository(&fakeValidationTradeRepository{}),
			),
			req:        apppaper.RecordSimulationRequest{ValidationID: validRecord.ValidationID},
			wantErrSub: "PLANNED",
		},
		{
			name: "current result is not candidate",
			service: apppaper.NewService(
				&fakeRunRepository{runs: []domainresearch.Run{run}},
				&fakeResultRepository{results: []domainresearch.Result{rejectedResult}},
				apppaper.WithValidationRecordRepository(&fakeValidationRecordRepository{records: []domainpaper.ValidationRecord{validRecord}}),
				apppaper.WithValidationTradeRepository(&fakeValidationTradeRepository{}),
			),
			req:        apppaper.RecordSimulationRequest{ValidationID: validRecord.ValidationID},
			wantErrSub: "CANDIDATE",
		},
		{
			name: "symbol outside run scope",
			service: apppaper.NewService(
				&fakeRunRepository{runs: []domainresearch.Run{run}},
				&fakeResultRepository{results: []domainresearch.Result{candidateResult}},
				apppaper.WithValidationRecordRepository(&fakeValidationRecordRepository{records: []domainpaper.ValidationRecord{validRecord}}),
				apppaper.WithValidationTradeRepository(&fakeValidationTradeRepository{}),
			),
			req: apppaper.RecordSimulationRequest{
				ValidationID: validRecord.ValidationID,
				Symbol:       "ETHUSDT",
				Interval:     "1",
			},
			wantErrSub: "market scope",
		},
		{
			name: "missing symbol for multi-symbol run",
			service: apppaper.NewService(
				&fakeRunRepository{runs: []domainresearch.Run{func() domainresearch.Run {
					run := run
					run.Symbols = []string{"BTCUSDT", "ETHUSDT"}
					return run
				}()}},
				&fakeResultRepository{results: []domainresearch.Result{candidateResult}},
				apppaper.WithValidationRecordRepository(&fakeValidationRecordRepository{records: []domainpaper.ValidationRecord{validRecord}}),
				apppaper.WithValidationTradeRepository(&fakeValidationTradeRepository{}),
			),
			req:        apppaper.RecordSimulationRequest{ValidationID: validRecord.ValidationID},
			wantErrSub: "symbol",
		},
		{
			name: "missing trade repository",
			service: apppaper.NewService(
				&fakeRunRepository{runs: []domainresearch.Run{run}},
				&fakeResultRepository{results: []domainresearch.Result{candidateResult}},
				apppaper.WithValidationRecordRepository(&fakeValidationRecordRepository{records: []domainpaper.ValidationRecord{validRecord}}),
			),
			req:        apppaper.RecordSimulationRequest{ValidationID: validRecord.ValidationID},
			wantErrSub: "validation trade repository",
		},
		{
			name: "trade repository error",
			service: apppaper.NewService(
				&fakeRunRepository{runs: []domainresearch.Run{run}},
				&fakeResultRepository{results: []domainresearch.Result{candidateResult}},
				apppaper.WithValidationRecordRepository(&fakeValidationRecordRepository{records: []domainpaper.ValidationRecord{validRecord}}),
				apppaper.WithValidationTradeRepository(&fakeValidationTradeRepository{err: repositoryErr}),
			),
			req: apppaper.RecordSimulationRequest{
				ValidationID:  validRecord.ValidationID,
				TradeIDPrefix: "paper_trade_app",
				RoundTrips:    []domainbacktest.RoundTrip{validTrade},
			},
			wantErrSub: repositoryErr.Error(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.service.RecordSimulation(context.Background(), tt.req)
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

type fakeValidationRecordRepository struct {
	record             domainpaper.ValidationRecord
	records            []domainpaper.ValidationRecord
	queries            []domainpaper.ValidationRecordQuery
	stats              domainpaper.ValidationRecordStats
	calls              int
	transitionCalls    int
	transitionExpected domainpaper.ValidationStatus
	err                error
}

func (r *fakeValidationRecordRepository) TransitionValidation(_ context.Context, record domainpaper.ValidationRecord, expected domainpaper.ValidationStatus) (domainpaper.ValidationRecordStats, error) {
	r.transitionCalls++
	r.transitionExpected = expected
	r.record = record
	if r.err != nil {
		return domainpaper.ValidationRecordStats{}, r.err
	}
	return r.stats, nil
}

func (r *fakeValidationRecordRepository) RecordValidation(_ context.Context, record domainpaper.ValidationRecord) (domainpaper.ValidationRecordStats, error) {
	r.calls++
	r.record = record
	if r.err != nil {
		return domainpaper.ValidationRecordStats{}, r.err
	}
	return r.stats, nil
}

func (r *fakeValidationRecordRepository) ListValidationRecords(_ context.Context, query domainpaper.ValidationRecordQuery) ([]domainpaper.ValidationRecord, error) {
	r.queries = append(r.queries, query)
	if r.err != nil {
		return nil, r.err
	}
	return append([]domainpaper.ValidationRecord(nil), r.records...), nil
}

type fakeValidationTradeRepository struct {
	records        []domainpaper.ValidationTrade
	queries        []domainpaper.ValidationTradeQuery
	stats          domainpaper.ValidationTradeStats
	calls          int
	expectedStatus domainpaper.ValidationStatus
	err            error
}

type fakeDailyPerformanceRepository struct {
	records []domainpaper.DailyPerformance
	queries []domainpaper.DailyPerformanceQuery
	stats   domainpaper.DailyPerformanceStats
	calls   int
	err     error
}

func (r *fakeDailyPerformanceRepository) RecordDailyPerformance(_ context.Context, records []domainpaper.DailyPerformance) (domainpaper.DailyPerformanceStats, error) {
	r.calls++
	if r.err != nil {
		return domainpaper.DailyPerformanceStats{}, r.err
	}
	r.records = append(r.records, records...)
	return r.stats, nil
}

func (r *fakeDailyPerformanceRepository) ListDailyPerformance(_ context.Context, query domainpaper.DailyPerformanceQuery) ([]domainpaper.DailyPerformance, error) {
	r.queries = append(r.queries, query)
	if r.err != nil {
		return nil, r.err
	}
	return append([]domainpaper.DailyPerformance(nil), r.records...), nil
}

func (r *fakeValidationTradeRepository) RecordValidationTrades(_ context.Context, records []domainpaper.ValidationTrade, expectedStatus domainpaper.ValidationStatus) (domainpaper.ValidationTradeStats, error) {
	r.calls++
	r.expectedStatus = expectedStatus
	if r.err != nil {
		return domainpaper.ValidationTradeStats{}, r.err
	}
	r.records = append(r.records, records...)
	return r.stats, nil
}

func (r *fakeValidationTradeRepository) ListValidationTrades(_ context.Context, query domainpaper.ValidationTradeQuery) ([]domainpaper.ValidationTrade, error) {
	r.queries = append(r.queries, query)
	if r.err != nil {
		return nil, r.err
	}
	return append([]domainpaper.ValidationTrade(nil), r.records...), nil
}

type fakeSimulationTradeGenerator struct {
	req    apppaper.SimulationTradeGenerationRequest
	result apppaper.SimulationTradeGenerationResult
	calls  int
	err    error
}

func (g *fakeSimulationTradeGenerator) GenerateSimulationTrades(
	_ context.Context,
	req apppaper.SimulationTradeGenerationRequest,
) (apppaper.SimulationTradeGenerationResult, error) {
	g.calls++
	g.req = req
	if g.err != nil {
		return apppaper.SimulationTradeGenerationResult{}, g.err
	}
	return g.result, nil
}

type fakeIDGenerator struct {
	id    string
	err   error
	calls int
}

func (g *fakeIDGenerator) NewID() (string, error) {
	g.calls++
	if g.err != nil {
		return "", g.err
	}
	return g.id, nil
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

func testValidationRecord(t *testing.T, runID string, plannedAt time.Time, status domainpaper.ValidationStatus) domainpaper.ValidationRecord {
	t.Helper()

	record := domainpaper.ValidationRecord{
		ValidationID:   "paper_validation_app_0001",
		RunID:          runID,
		Status:         status,
		Mode:           "paper",
		InitialBalance: decimal.RequireFromString("1000"),
		MinimumDays:    30,
		Reasons:        []string{"paper_validation_allowed"},
		PlannedAt:      plannedAt,
	}
	switch status {
	case domainpaper.ValidationStatusRunning:
		record.StartedAt = plannedAt.Add(time.Hour)
	case domainpaper.ValidationStatusCompleted:
		record.StartedAt = plannedAt.Add(time.Hour)
		record.CompletedAt = record.StartedAt.AddDate(0, 0, record.MinimumDays)
		record.StatusReason = "minimum_validation_period_completed"
	case domainpaper.ValidationStatusCancelled:
		record.CancelledAt = plannedAt.Add(time.Hour)
		record.StatusReason = "test cancellation"
	}
	if err := domainpaper.ValidateValidationRecord(record); err != nil {
		t.Fatalf("validate paper record fixture: %v", err)
	}
	return record
}

func mustRoundTrip(t *testing.T, entryTime time.Time, direction domainbacktest.Direction, entryPrice string, exitPrice string) domainbacktest.RoundTrip {
	t.Helper()

	costs, err := domainbacktest.NewCostModel(0, 0, 0, 0, 1)
	if err != nil {
		t.Fatalf("new cost model: %v", err)
	}
	trade, err := domainbacktest.EvaluateRoundTrip(domainbacktest.RoundTripInput{
		Direction:      direction,
		EntryTime:      entryTime,
		ExitTime:       entryTime.Add(time.Hour),
		EntryMidPrice:  decimal.RequireFromString(entryPrice),
		ExitMidPrice:   decimal.RequireFromString(exitPrice),
		Quantity:       decimal.RequireFromString("1"),
		EntryLiquidity: domainbacktest.LiquidityTaker,
		ExitLiquidity:  domainbacktest.LiquidityTaker,
		Costs:          costs,
	})
	if err != nil {
		t.Fatalf("evaluate round trip: %v", err)
	}
	return trade
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
