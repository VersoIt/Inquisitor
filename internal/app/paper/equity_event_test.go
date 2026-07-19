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
)

func TestServiceAccountPositionCloseRecordsFirstEquityEvent(t *testing.T) {
	now := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	close := appPositionClose(now)
	record := testValidationRecord(t, "research_app_0001", now.Add(-2*time.Hour), domainpaper.ValidationStatusRunning)
	record.ValidationID = close.ValidationID
	equity := &fakeEquityEventRepository{stats: domainpaper.EquityEventStats{Inserted: 1}}
	service := accountPositionCloseService(now, record, []domainpaper.PositionClose{close}, equity)

	got, err := service.AccountPositionClose(context.Background(), apppaper.AccountPositionCloseRequest{
		EventID: " paper_equity_app_0001 ",
		CloseID: " paper_close_app_0001 ",
	})
	if err != nil {
		t.Fatalf("account position close: %v", err)
	}

	if got.Record.ValidationID != record.ValidationID || got.Close.CloseID != close.CloseID {
		t.Fatalf("context mismatch: %#v", got)
	}
	if got.Event.EventID != "paper_equity_app_0001" || got.Event.CloseID != close.CloseID ||
		got.Event.PositionID != close.PositionID || got.Event.Sequence != 1 {
		t.Fatalf("event identity mismatch: %#v", got.Event)
	}
	if !got.Event.EquityBefore.Equal(record.InitialBalance) ||
		!got.Event.EquityAfter.Equal(record.InitialBalance.Add(close.NetPnL)) {
		t.Fatalf("equity math mismatch: %#v", got.Event)
	}
	if equity.calls != 1 || equity.event.EventID != got.Event.EventID || got.Stats.Inserted != 1 {
		t.Fatalf("equity repository mismatch: calls=%d event=%#v stats=%#v", equity.calls, equity.event, got.Stats)
	}
	if len(equity.queries) != 2 || equity.queries[0].ValidationID != close.ValidationID ||
		equity.queries[0].CloseID != close.CloseID || equity.queries[0].Limit != 2 ||
		equity.queries[1].ValidationID != close.ValidationID {
		t.Fatalf("expected close lookup then validation ledger lookup, got %#v", equity.queries)
	}
}

func TestServiceAccountPositionCloseScopesExistingEventLookupToValidation(t *testing.T) {
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	close := appPositionClose(now)
	record := testValidationRecord(t, "research_app_0001", now.Add(-2*time.Hour), domainpaper.ValidationStatusRunning)
	record.ValidationID = close.ValidationID
	foreignEvent := appEquityEvent(now)
	foreignEvent.EventID = "paper_equity_foreign_0001"
	foreignEvent.ValidationID = "paper_validation_foreign_0001"
	foreignEvent.CloseID = close.CloseID
	foreignEvent.PositionID = close.PositionID
	equity := &fakeEquityEventRepository{
		events: []domainpaper.EquityEvent{foreignEvent},
		stats:  domainpaper.EquityEventStats{Inserted: 1},
	}
	service := accountPositionCloseService(now, record, []domainpaper.PositionClose{close}, equity)

	got, err := service.AccountPositionClose(context.Background(), apppaper.AccountPositionCloseRequest{
		EventID: "paper_equity_app_0001",
		CloseID: close.CloseID,
	})
	if err != nil {
		t.Fatalf("account position close with foreign event present: %v", err)
	}

	if got.Event.ValidationID != record.ValidationID || got.Event.EventID != "paper_equity_app_0001" || got.Stats.Inserted != 1 {
		t.Fatalf("scoped equity event result mismatch: %#v", got)
	}
	if equity.calls != 1 || len(equity.queries) != 2 {
		t.Fatalf("equity repository call mismatch: calls=%d queries=%#v", equity.calls, equity.queries)
	}
	if equity.queries[0].ValidationID != record.ValidationID || equity.queries[0].CloseID != close.CloseID || equity.queries[0].Limit != 2 {
		t.Fatalf("existing event lookup must be scoped to validation: %#v", equity.queries[0])
	}
	if equity.queries[1].ValidationID != record.ValidationID || equity.queries[1].CloseID != "" {
		t.Fatalf("ledger lookup must stay scoped to validation only: %#v", equity.queries[1])
	}
}

func TestServiceAccountPositionCloseScopesCloseLookupToValidation(t *testing.T) {
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	close := appPositionClose(now)
	record := testValidationRecord(t, "research_app_0001", now.Add(-2*time.Hour), domainpaper.ValidationStatusRunning)
	record.ValidationID = close.ValidationID
	foreignClose := close
	foreignClose.ValidationID = "paper_validation_foreign_0001"
	foreignClose.PositionID = "paper_position_foreign_0001"
	foreignClose.TicketID = "paper_ticket_foreign_0001"
	foreignClose.EntryFillID = "paper_fill_foreign_0001"
	closes := &fakePositionCloseRepository{closes: []domainpaper.PositionClose{foreignClose, close}}
	equity := &fakeEquityEventRepository{stats: domainpaper.EquityEventStats{Inserted: 1}}
	service := apppaper.NewService(
		&fakeRunRepository{},
		&fakeResultRepository{},
		apppaper.WithValidationRecordRepository(&fakeValidationRecordRepository{records: []domainpaper.ValidationRecord{record}}),
		apppaper.WithPositionCloseRepository(closes),
		apppaper.WithEquityEventRepository(equity),
		apppaper.WithClock(clock.FixedClock{Time: now.Add(5 * time.Minute)}),
	)

	got, err := service.AccountPositionClose(context.Background(), apppaper.AccountPositionCloseRequest{
		ValidationID: record.ValidationID,
		EventID:      "paper_equity_app_0001",
		CloseID:      close.CloseID,
	})
	if err != nil {
		t.Fatalf("account scoped position close: %v", err)
	}

	if got.Close.ValidationID != record.ValidationID || got.Close.PositionID != close.PositionID ||
		got.Event.ValidationID != record.ValidationID {
		t.Fatalf("scoped close lookup result mismatch: %#v", got)
	}
	if len(closes.queries) != 1 || closes.queries[0].ValidationID != record.ValidationID ||
		closes.queries[0].CloseID != close.CloseID || closes.queries[0].Limit != 2 {
		t.Fatalf("close lookup must be scoped to validation: %#v", closes.queries)
	}
}

func TestServiceAccountPositionCloseContinuesExistingEquityLedger(t *testing.T) {
	now := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	firstClose := appPositionClose(now)
	firstEvent := appEquityEvent(now)
	secondClose := appPositionClose(now)
	secondClose.CloseID = "paper_close_app_0002"
	secondClose.PositionID = "paper_position_app_0002"
	secondClose.ClosedAt = firstClose.ClosedAt.Add(time.Minute)
	secondClose.RecordedAt = secondClose.ClosedAt.Add(time.Minute)
	record := testValidationRecord(t, "research_app_0001", now.Add(-2*time.Hour), domainpaper.ValidationStatusRunning)
	record.ValidationID = secondClose.ValidationID
	equity := &fakeEquityEventRepository{
		events: []domainpaper.EquityEvent{firstEvent},
		stats:  domainpaper.EquityEventStats{Inserted: 1},
	}
	service := accountPositionCloseService(now, record, []domainpaper.PositionClose{secondClose}, equity)

	got, err := service.AccountPositionClose(context.Background(), apppaper.AccountPositionCloseRequest{
		EventID: "paper_equity_app_0002",
		CloseID: secondClose.CloseID,
	})
	if err != nil {
		t.Fatalf("account second position close: %v", err)
	}

	if got.Event.Sequence != 2 || !got.Event.EquityBefore.Equal(firstEvent.EquityAfter) ||
		!got.Event.EquityAfter.Equal(firstEvent.EquityAfter.Add(secondClose.NetPnL)) {
		t.Fatalf("ledger continuation mismatch: %#v", got.Event)
	}
}

func TestServiceAccountPositionCloseAllowsExactEventRerun(t *testing.T) {
	now := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	close := appPositionClose(now)
	event := appEquityEvent(now)
	record := testValidationRecord(t, "research_app_0001", now.Add(-2*time.Hour), domainpaper.ValidationStatusRunning)
	record.ValidationID = close.ValidationID
	equity := &fakeEquityEventRepository{
		events: []domainpaper.EquityEvent{event},
		stats:  domainpaper.EquityEventStats{Skipped: 1},
	}
	service := accountPositionCloseService(now, record, []domainpaper.PositionClose{close}, equity)

	got, err := service.AccountPositionClose(context.Background(), apppaper.AccountPositionCloseRequest{
		EventID: event.EventID,
		CloseID: close.CloseID,
	})
	if err != nil {
		t.Fatalf("account position close rerun: %v", err)
	}
	if got.Event.EventID != event.EventID || got.Stats.Skipped != 1 || equity.calls != 1 {
		t.Fatalf("idempotent event mismatch: result=%#v calls=%d", got, equity.calls)
	}
}

func TestServiceAccountPositionCloseRejectsUnsafeInputsTableDriven(t *testing.T) {
	now := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	close := appPositionClose(now)
	runningRecord := testValidationRecord(t, "research_app_0001", now.Add(-2*time.Hour), domainpaper.ValidationStatusRunning)
	runningRecord.ValidationID = close.ValidationID
	plannedRecord := runningRecord
	plannedRecord.Status = domainpaper.ValidationStatusPlanned
	plannedRecord.StartedAt = time.Time{}
	repositoryErr := errors.New("postgres unavailable")
	validReq := apppaper.AccountPositionCloseRequest{EventID: "paper_equity_app_0001", CloseID: close.CloseID}

	tests := []struct {
		name       string
		service    *apppaper.Service
		req        apppaper.AccountPositionCloseRequest
		wantErrSub string
	}{
		{
			name:       "missing close id",
			service:    accountPositionCloseService(now, runningRecord, []domainpaper.PositionClose{close}, &fakeEquityEventRepository{}),
			req:        func() apppaper.AccountPositionCloseRequest { req := validReq; req.CloseID = ""; return req }(),
			wantErrSub: "close_id",
		},
		{
			name:       "close not found",
			service:    accountPositionCloseService(now, runningRecord, nil, &fakeEquityEventRepository{}),
			req:        validReq,
			wantErrSub: "not found",
		},
		{
			name:       "close lookup ambiguous",
			service:    accountPositionCloseService(now, runningRecord, []domainpaper.PositionClose{close, close}, &fakeEquityEventRepository{}),
			req:        validReq,
			wantErrSub: "ambiguous",
		},
		{
			name:       "validation is not running",
			service:    accountPositionCloseService(now, plannedRecord, []domainpaper.PositionClose{close}, &fakeEquityEventRepository{}),
			req:        validReq,
			wantErrSub: "RUNNING",
		},
		{
			name:       "missing event id",
			service:    accountPositionCloseService(now, runningRecord, []domainpaper.PositionClose{close}, &fakeEquityEventRepository{}),
			req:        func() apppaper.AccountPositionCloseRequest { req := validReq; req.EventID = ""; return req }(),
			wantErrSub: "event_id",
		},
		{
			name: "close already accounted by another event",
			service: accountPositionCloseService(
				now,
				runningRecord,
				[]domainpaper.PositionClose{close},
				&fakeEquityEventRepository{events: []domainpaper.EquityEvent{func() domainpaper.EquityEvent {
					event := appEquityEvent(now)
					event.EventID = "paper_equity_existing_0001"
					return event
				}()}},
			),
			req:        validReq,
			wantErrSub: "already has equity event",
		},
		{
			name: "existing ledger breaks continuity",
			service: accountPositionCloseService(
				now,
				runningRecord,
				[]domainpaper.PositionClose{func() domainpaper.PositionClose {
					next := close
					next.CloseID = "paper_close_app_0002"
					next.PositionID = "paper_position_app_0002"
					next.ClosedAt = close.ClosedAt.Add(time.Hour)
					return next
				}()},
				&fakeEquityEventRepository{events: []domainpaper.EquityEvent{func() domainpaper.EquityEvent {
					event := appEquityEvent(now)
					event.EquityBefore = event.EquityBefore.Add(decimal.NewFromInt(1))
					event.EquityAfter = event.EquityBefore.Add(event.NetPnL)
					return event
				}()}},
			),
			req: func() apppaper.AccountPositionCloseRequest {
				req := validReq
				req.CloseID = "paper_close_app_0002"
				req.EventID = "paper_equity_app_0002"
				return req
			}(),
			wantErrSub: "continuity",
		},
		{
			name: "close occurred before latest ledger event",
			service: accountPositionCloseService(
				now,
				runningRecord,
				[]domainpaper.PositionClose{func() domainpaper.PositionClose {
					older := close
					older.CloseID = "paper_close_app_0002"
					older.PositionID = "paper_position_app_0002"
					older.ClosedAt = close.ClosedAt.Add(-time.Minute)
					return older
				}()},
				&fakeEquityEventRepository{events: []domainpaper.EquityEvent{appEquityEvent(now)}},
			),
			req: func() apppaper.AccountPositionCloseRequest {
				req := validReq
				req.CloseID = "paper_close_app_0002"
				req.EventID = "paper_equity_app_0002"
				return req
			}(),
			wantErrSub: "occurred before",
		},
		{
			name:       "equity list error",
			service:    accountPositionCloseService(now, runningRecord, []domainpaper.PositionClose{close}, &fakeEquityEventRepository{listErr: repositoryErr}),
			req:        validReq,
			wantErrSub: repositoryErr.Error(),
		},
		{
			name:       "equity record error",
			service:    accountPositionCloseService(now, runningRecord, []domainpaper.PositionClose{close}, &fakeEquityEventRepository{recordErr: repositoryErr}),
			req:        validReq,
			wantErrSub: repositoryErr.Error(),
		},
		{
			name: "missing equity repository",
			service: apppaper.NewService(
				&fakeRunRepository{},
				&fakeResultRepository{},
				apppaper.WithValidationRecordRepository(&fakeValidationRecordRepository{records: []domainpaper.ValidationRecord{runningRecord}}),
				apppaper.WithPositionCloseRepository(&fakePositionCloseRepository{closes: []domainpaper.PositionClose{close}}),
				apppaper.WithClock(clock.FixedClock{Time: now.Add(5 * time.Minute)}),
			),
			req:        validReq,
			wantErrSub: "equity event repository",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.service.AccountPositionClose(context.Background(), tt.req)
			if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
		})
	}
}

func accountPositionCloseService(
	now time.Time,
	record domainpaper.ValidationRecord,
	closes []domainpaper.PositionClose,
	equity *fakeEquityEventRepository,
) *apppaper.Service {
	return apppaper.NewService(
		&fakeRunRepository{},
		&fakeResultRepository{},
		apppaper.WithValidationRecordRepository(&fakeValidationRecordRepository{records: []domainpaper.ValidationRecord{record}}),
		apppaper.WithPositionCloseRepository(&fakePositionCloseRepository{closes: closes}),
		apppaper.WithEquityEventRepository(equity),
		apppaper.WithClock(clock.FixedClock{Time: now.Add(5 * time.Minute)}),
	)
}

type fakeEquityEventRepository struct {
	event     domainpaper.EquityEvent
	events    []domainpaper.EquityEvent
	queries   []domainpaper.EquityEventQuery
	stats     domainpaper.EquityEventStats
	calls     int
	listErr   error
	recordErr error
}

func (r *fakeEquityEventRepository) RecordEquityEvent(_ context.Context, event domainpaper.EquityEvent) (domainpaper.EquityEventStats, error) {
	r.calls++
	r.event = event
	if r.recordErr != nil {
		return domainpaper.EquityEventStats{}, r.recordErr
	}
	if r.stats.Skipped == 0 {
		r.events = append(r.events, event)
	}
	return r.stats, nil
}

func (r *fakeEquityEventRepository) ListEquityEvents(_ context.Context, query domainpaper.EquityEventQuery) ([]domainpaper.EquityEvent, error) {
	r.queries = append(r.queries, query)
	if r.listErr != nil {
		return nil, r.listErr
	}
	var out []domainpaper.EquityEvent
	for _, event := range r.events {
		if query.EventID != "" && event.EventID != query.EventID {
			continue
		}
		if query.ValidationID != "" && event.ValidationID != query.ValidationID {
			continue
		}
		if query.CloseID != "" && event.CloseID != query.CloseID {
			continue
		}
		if query.PositionID != "" && event.PositionID != query.PositionID {
			continue
		}
		out = append(out, event)
		if query.Limit > 0 && len(out) == query.Limit {
			break
		}
	}
	return out, nil
}

func appEquityEvent(now time.Time) domainpaper.EquityEvent {
	close := appPositionClose(now)
	return domainpaper.EquityEvent{
		EventID:      "paper_equity_app_0001",
		ValidationID: close.ValidationID,
		CloseID:      close.CloseID,
		PositionID:   close.PositionID,
		Exchange:     close.Exchange,
		Category:     close.Category,
		Symbol:       close.Symbol,
		Interval:     close.Interval,
		Sequence:     1,
		NetPnL:       close.NetPnL,
		Fees:         close.Fees,
		EquityBefore: decimal.RequireFromString("1000"),
		EquityAfter:  decimal.RequireFromString("1389.7"),
		OccurredAt:   close.ClosedAt,
		RecordedAt:   now.Add(5 * time.Minute),
	}
}
