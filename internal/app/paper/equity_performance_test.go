package paper_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	apppaper "github.com/VersoIt/Inquisitor/internal/app/paper"
	"github.com/VersoIt/Inquisitor/internal/clock"
	domainpaper "github.com/VersoIt/Inquisitor/internal/paper"
)

func TestServiceBuildEquityPerformanceReportPersistsDailySnapshots(t *testing.T) {
	now := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	first := appEquityEvent(now)
	second := appNextEquityEvent(now, first)
	record := testValidationRecord(t, "research_app_0001", now.Add(-2*time.Hour), domainpaper.ValidationStatusRunning)
	record.ValidationID = first.ValidationID
	equity := &fakeEquityEventRepository{events: []domainpaper.EquityEvent{second, first}}
	performance := &fakeDailyPerformanceRepository{stats: domainpaper.DailyPerformanceStats{Inserted: 1}}
	service := equityPerformanceService(now, record, equity, performance)

	got, err := service.BuildEquityPerformanceReport(context.Background(), apppaper.EquityPerformanceReportRequest{
		ValidationID: " paper_validation_app_0001 ",
		RecordDaily:  true,
	})
	if err != nil {
		t.Fatalf("build equity performance report: %v", err)
	}

	if got.Record.ValidationID != record.ValidationID || len(got.Events) != 2 || got.Summary.Trades != 2 ||
		!got.Summary.FinalEquity.Equal(second.EquityAfter) {
		t.Fatalf("report mismatch: %#v", got)
	}
	if len(got.Daily) != 1 || got.DailyStats.Inserted != 1 || performance.calls != 1 || len(performance.records) != 1 {
		t.Fatalf("daily persistence mismatch: daily=%#v stats=%#v performance=%#v", got.Daily, got.DailyStats, performance)
	}
	if len(equity.queries) != 1 || equity.queries[0].ValidationID != record.ValidationID || equity.queries[0].Limit <= 100000 {
		t.Fatalf("equity query mismatch: %#v", equity.queries)
	}
}

func TestServiceBuildEquityPerformanceReportHandlesEmptyLedger(t *testing.T) {
	now := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	record := testValidationRecord(t, "research_app_0001", now.Add(-2*time.Hour), domainpaper.ValidationStatusRunning)
	equity := &fakeEquityEventRepository{}
	service := equityPerformanceService(now, record, equity, nil)

	got, err := service.BuildEquityPerformanceReport(context.Background(), apppaper.EquityPerformanceReportRequest{
		ValidationID: record.ValidationID,
		RecordDaily:  true,
	})
	if err != nil {
		t.Fatalf("build empty equity performance report: %v", err)
	}

	if got.Summary.Trades != 0 || !got.Summary.InitialEquity.Equal(record.InitialBalance) ||
		!got.Summary.FinalEquity.Equal(record.InitialBalance) || len(got.Daily) != 0 {
		t.Fatalf("empty report mismatch: %#v", got)
	}
}

func TestServiceBuildEquityPerformanceReportRejectsUnsafeInputsTableDriven(t *testing.T) {
	now := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	first := appEquityEvent(now)
	record := testValidationRecord(t, "research_app_0001", now.Add(-2*time.Hour), domainpaper.ValidationStatusRunning)
	record.ValidationID = first.ValidationID
	repositoryErr := errors.New("postgres unavailable")
	validReq := apppaper.EquityPerformanceReportRequest{ValidationID: record.ValidationID}

	tests := []struct {
		name       string
		service    *apppaper.Service
		req        apppaper.EquityPerformanceReportRequest
		wantErrSub string
	}{
		{
			name:       "missing validation id",
			service:    equityPerformanceService(now, record, &fakeEquityEventRepository{}, nil),
			req:        apppaper.EquityPerformanceReportRequest{},
			wantErrSub: "validation_id",
		},
		{
			name:       "validation not found",
			service:    equityPerformanceService(now, domainpaper.ValidationRecord{}, &fakeEquityEventRepository{}, nil),
			req:        validReq,
			wantErrSub: "not found",
		},
		{
			name:       "equity list error",
			service:    equityPerformanceService(now, record, &fakeEquityEventRepository{listErr: repositoryErr}, nil),
			req:        validReq,
			wantErrSub: repositoryErr.Error(),
		},
		{
			name: "broken equity sequence",
			service: equityPerformanceService(now, record, &fakeEquityEventRepository{events: []domainpaper.EquityEvent{func() domainpaper.EquityEvent {
				event := first
				event.Sequence = 2
				return event
			}()}}, nil),
			req:        validReq,
			wantErrSub: "sequence",
		},
		{
			name:       "record daily missing repository",
			service:    equityPerformanceService(now, record, &fakeEquityEventRepository{events: []domainpaper.EquityEvent{first}}, nil),
			req:        apppaper.EquityPerformanceReportRequest{ValidationID: record.ValidationID, RecordDaily: true},
			wantErrSub: "daily performance repository",
		},
		{
			name:       "record daily repository error",
			service:    equityPerformanceService(now, record, &fakeEquityEventRepository{events: []domainpaper.EquityEvent{first}}, &fakeDailyPerformanceRepository{err: repositoryErr}),
			req:        apppaper.EquityPerformanceReportRequest{ValidationID: record.ValidationID, RecordDaily: true},
			wantErrSub: repositoryErr.Error(),
		},
		{
			name: "missing equity repository",
			service: apppaper.NewService(
				&fakeRunRepository{},
				&fakeResultRepository{},
				apppaper.WithValidationRecordRepository(&fakeValidationRecordRepository{records: []domainpaper.ValidationRecord{record}}),
				apppaper.WithClock(clock.FixedClock{Time: now.Add(10 * time.Minute)}),
			),
			req:        validReq,
			wantErrSub: "equity event repository",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.service.BuildEquityPerformanceReport(context.Background(), tt.req)
			if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
		})
	}
}

func equityPerformanceService(
	now time.Time,
	record domainpaper.ValidationRecord,
	equity *fakeEquityEventRepository,
	performance *fakeDailyPerformanceRepository,
) *apppaper.Service {
	var records []domainpaper.ValidationRecord
	if record.ValidationID != "" {
		records = []domainpaper.ValidationRecord{record}
	}
	options := []apppaper.Option{
		apppaper.WithValidationRecordRepository(&fakeValidationRecordRepository{records: records}),
		apppaper.WithEquityEventRepository(equity),
		apppaper.WithClock(clock.FixedClock{Time: now.Add(10 * time.Minute)}),
	}
	if performance != nil {
		options = append(options, apppaper.WithDailyPerformanceRepository(performance))
	}
	return apppaper.NewService(&fakeRunRepository{}, &fakeResultRepository{}, options...)
}

func appNextEquityEvent(now time.Time, previous domainpaper.EquityEvent) domainpaper.EquityEvent {
	event := appEquityEvent(now)
	event.EventID = "paper_equity_app_0002"
	event.CloseID = "paper_close_app_0002"
	event.PositionID = "paper_position_app_0002"
	event.Sequence = previous.Sequence + 1
	event.EquityBefore = previous.EquityAfter
	event.EquityAfter = event.EquityBefore.Add(event.NetPnL)
	event.OccurredAt = previous.OccurredAt.Add(time.Minute)
	event.RecordedAt = event.OccurredAt.Add(time.Minute)
	return event
}
