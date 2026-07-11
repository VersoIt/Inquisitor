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

func TestServiceSettlePositionCloseRecordsCloseAndEquity(t *testing.T) {
	now := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	position := appOpenPosition(now)
	record := testValidationRecord(t, "research_app_0001", now.Add(-2*time.Hour), domainpaper.ValidationStatusRunning)
	record.ValidationID = position.ValidationID
	closes := &fakePositionCloseRepository{stats: domainpaper.PositionCloseStats{Inserted: 1}}
	equity := &fakeEquityEventRepository{stats: domainpaper.EquityEventStats{Inserted: 1}}
	service := settlePositionCloseService(now, record, []domainpaper.OpenPosition{position}, closes, equity)

	got, err := service.SettlePositionClose(context.Background(), apppaper.SettlePositionCloseRequest{
		EventID: " paper_equity_app_0001 ",
		Close:   appClosePositionRequest(now),
	})
	if err != nil {
		t.Fatalf("settle position close: %v", err)
	}

	if got.Record.ValidationID != record.ValidationID || got.Position.PositionID != position.PositionID {
		t.Fatalf("context mismatch: %#v", got)
	}
	if got.Close.CloseID != "paper_close_app_0001" || got.Event.EventID != "paper_equity_app_0001" ||
		got.Event.CloseID != got.Close.CloseID || got.Event.Sequence != 1 {
		t.Fatalf("settlement identity mismatch: %#v", got)
	}
	if !got.Event.EquityBefore.Equal(record.InitialBalance) ||
		!got.Event.EquityAfter.Equal(record.InitialBalance.Add(got.Close.NetPnL)) {
		t.Fatalf("settlement equity mismatch: close=%#v event=%#v", got.Close, got.Event)
	}
	if got.CloseStats.Inserted != 1 || got.EquityStats.Inserted != 1 || closes.calls != 1 || equity.calls != 1 {
		t.Fatalf("repository stats mismatch: close_stats=%#v equity_stats=%#v close_calls=%d equity_calls=%d", got.CloseStats, got.EquityStats, closes.calls, equity.calls)
	}
}

func TestServiceSettlePositionCloseCompletesAccountingAfterIdempotentClose(t *testing.T) {
	now := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	position := appOpenPosition(now)
	close := appPositionClose(now)
	record := testValidationRecord(t, "research_app_0001", now.Add(-2*time.Hour), domainpaper.ValidationStatusRunning)
	record.ValidationID = position.ValidationID
	closes := &fakePositionCloseRepository{
		closes: []domainpaper.PositionClose{close},
		stats:  domainpaper.PositionCloseStats{Skipped: 1},
	}
	equity := &fakeEquityEventRepository{stats: domainpaper.EquityEventStats{Inserted: 1}}
	service := settlePositionCloseService(now, record, []domainpaper.OpenPosition{position}, closes, equity)

	got, err := service.SettlePositionClose(context.Background(), apppaper.SettlePositionCloseRequest{
		EventID: "paper_equity_app_0001",
		Close:   appClosePositionRequest(now),
	})
	if err != nil {
		t.Fatalf("settle position close retry: %v", err)
	}

	if got.CloseStats.Skipped != 1 || got.EquityStats.Inserted != 1 || got.Event.CloseID != close.CloseID {
		t.Fatalf("retry settlement mismatch: %#v", got)
	}
}

func TestServiceSettlePositionCloseRejectsUnsafeInputsTableDriven(t *testing.T) {
	now := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	position := appOpenPosition(now)
	record := testValidationRecord(t, "research_app_0001", now.Add(-2*time.Hour), domainpaper.ValidationStatusRunning)
	record.ValidationID = position.ValidationID
	repositoryErr := errors.New("postgres unavailable")
	validReq := apppaper.SettlePositionCloseRequest{
		EventID: "paper_equity_app_0001",
		Close:   appClosePositionRequest(now),
	}

	tests := []struct {
		name              string
		positions         []domainpaper.OpenPosition
		closes            *fakePositionCloseRepository
		equity            *fakeEquityEventRepository
		req               apppaper.SettlePositionCloseRequest
		wantErrSub        string
		wantCloseCalls    int
		wantAccountingHit bool
	}{
		{
			name:              "missing event id does not write close",
			positions:         []domainpaper.OpenPosition{position},
			closes:            &fakePositionCloseRepository{},
			equity:            &fakeEquityEventRepository{},
			req:               func() apppaper.SettlePositionCloseRequest { req := validReq; req.EventID = ""; return req }(),
			wantErrSub:        "event_id",
			wantCloseCalls:    0,
			wantAccountingHit: false,
		},
		{
			name:              "close failure does not account equity",
			positions:         nil,
			closes:            &fakePositionCloseRepository{},
			equity:            &fakeEquityEventRepository{},
			req:               validReq,
			wantErrSub:        "not found",
			wantCloseCalls:    0,
			wantAccountingHit: false,
		},
		{
			name:              "accounting failure is surfaced after close write",
			positions:         []domainpaper.OpenPosition{position},
			closes:            &fakePositionCloseRepository{stats: domainpaper.PositionCloseStats{Inserted: 1}},
			equity:            &fakeEquityEventRepository{recordErr: repositoryErr},
			req:               validReq,
			wantErrSub:        repositoryErr.Error(),
			wantCloseCalls:    1,
			wantAccountingHit: true,
		},
		{
			name:      "settlement uses conservative close validation",
			positions: []domainpaper.OpenPosition{position},
			closes:    &fakePositionCloseRepository{},
			equity:    &fakeEquityEventRepository{},
			req: func() apppaper.SettlePositionCloseRequest {
				req := validReq
				req.Close.ExitPrice = decimal.RequireFromString("101050")
				req.Close.ExitFee = decimal.RequireFromString("30.315")
				return req
			}(),
			wantErrSub:        "LONG",
			wantCloseCalls:    0,
			wantAccountingHit: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := settlePositionCloseService(now, record, tt.positions, tt.closes, tt.equity)

			_, err := service.SettlePositionClose(context.Background(), tt.req)
			if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
			if tt.closes.calls != tt.wantCloseCalls {
				t.Fatalf("close calls mismatch: got %d want %d", tt.closes.calls, tt.wantCloseCalls)
			}
			if (tt.equity.calls > 0) != tt.wantAccountingHit {
				t.Fatalf("equity accounting call mismatch: calls=%d want_hit=%t", tt.equity.calls, tt.wantAccountingHit)
			}
		})
	}
}

func settlePositionCloseService(
	now time.Time,
	record domainpaper.ValidationRecord,
	positions []domainpaper.OpenPosition,
	closes *fakePositionCloseRepository,
	equity *fakeEquityEventRepository,
) *apppaper.Service {
	return apppaper.NewService(
		&fakeRunRepository{},
		&fakeResultRepository{},
		apppaper.WithValidationRecordRepository(&fakeValidationRecordRepository{records: []domainpaper.ValidationRecord{record}}),
		apppaper.WithOpenPositionRepository(&fakeOpenPositionRepository{positions: positions}),
		apppaper.WithPositionCloseRepository(closes),
		apppaper.WithEquityEventRepository(equity),
		apppaper.WithClock(clock.FixedClock{Time: now.Add(5 * time.Minute)}),
	)
}
