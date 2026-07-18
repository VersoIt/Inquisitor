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
	"github.com/VersoIt/Inquisitor/internal/marketdata"
	domainpaper "github.com/VersoIt/Inquisitor/internal/paper"
)

func TestServicePreflightPaperExecutionCycleSummarizesScopeWithoutWrites(t *testing.T) {
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	quoteTime := now.Add(time.Minute)
	record := testValidationRecord(t, "research_app_0001", now.Add(-2*time.Hour), domainpaper.ValidationStatusRunning)

	pendingTicket := appOrderTicket(now)
	pendingTicket.ValidationID = record.ValidationID
	filledTicket := appOrderTicket(now)
	filledTicket.TicketID = "paper_ticket_app_0002"
	filledTicket.DecisionID = "risk_decision_app_0002"
	filledTicket.IntentID = "risk_intent_app_0002"
	filledTicket.ValidationID = record.ValidationID

	existingFill := appOrderFill(now)
	existingFill.FillID = "paper_fill_app_0002"
	existingFill.TicketID = filledTicket.TicketID
	existingFill.DecisionID = filledTicket.DecisionID
	existingFill.IntentID = filledTicket.IntentID
	existingFill.ValidationID = record.ValidationID

	activePosition := appOpenPosition(now)
	activePosition.ValidationID = record.ValidationID
	closedPosition := appOpenPosition(now)
	closedPosition.PositionID = "paper_position_app_0002"
	closedPosition.FillID = existingFill.FillID
	closedPosition.TicketID = filledTicket.TicketID
	closedPosition.DecisionID = filledTicket.DecisionID
	closedPosition.IntentID = filledTicket.IntentID
	closedPosition.ValidationID = record.ValidationID
	existingClose := appPositionClose(now)
	existingClose.CloseID = "paper_close_app_0002"
	existingClose.PositionID = closedPosition.PositionID
	existingClose.EntryFillID = closedPosition.FillID
	existingClose.TicketID = closedPosition.TicketID
	existingClose.DecisionID = closedPosition.DecisionID
	existingClose.IntentID = closedPosition.IntentID
	existingClose.ValidationID = record.ValidationID

	fills := &fakeOrderFillRepository{fills: []domainpaper.OrderFill{existingFill}}
	positions := &fakeOpenPositionRepository{positions: []domainpaper.OpenPosition{activePosition, closedPosition}}
	closes := &fakePositionCloseRepository{closes: []domainpaper.PositionClose{existingClose}}
	orderbooks := &fakeOrderbookSnapshotRepository{
		snapshots: []marketdata.OrderbookSnapshot{appOrderbookSnapshot(quoteTime, "100000", "100200")},
	}
	service := preflightService(
		now,
		record,
		[]domainpaper.OrderTicket{pendingTicket, filledTicket},
		fills,
		positions,
		closes,
		orderbooks,
	)

	got, err := service.PreflightPaperExecutionCycle(context.Background(), apppaper.PreflightPaperExecutionCycleRequest{
		ValidationID:      record.ValidationID,
		Exchange:          "BYBIT",
		Category:          "LINEAR",
		Symbol:            "btcusdt",
		Interval:          "1",
		AsOf:              quoteTime.Add(time.Second),
		MaxStaleness:      30 * time.Second,
		MaxSpreadBPS:      decimal.RequireFromString("250"),
		PendingScanLimit:  10,
		PositionScanLimit: 10,
		QuoteScanLimit:    10,
	})
	if err != nil {
		t.Fatalf("preflight paper execution cycle: %v", err)
	}

	if !got.QuoteSourced || got.Quote.MidPrice.String() != "100100" {
		t.Fatalf("quote mismatch: %#v", got.Quote)
	}
	if got.PendingTickets != 1 || got.ScannedTickets != 2 || got.FilledTickets != 1 {
		t.Fatalf("pending counters mismatch: %#v", got)
	}
	if got.ScannedPositions != 2 || got.ActivePositions != 1 || got.ClosedPositions != 1 {
		t.Fatalf("position counters mismatch: %#v", got)
	}
	if fills.calls != 0 || positions.calls != 0 || closes.calls != 0 {
		t.Fatalf("preflight must not write: fill_calls=%d position_calls=%d close_calls=%d", fills.calls, positions.calls, closes.calls)
	}
	if len(orderbooks.queries) != 1 || len(fills.queries) != 2 || len(positions.queries) != 1 || len(closes.queries) != 2 {
		t.Fatalf("query counters mismatch: orderbooks=%#v fills=%#v positions=%#v closes=%#v", orderbooks.queries, fills.queries, positions.queries, closes.queries)
	}
}

func TestServicePreflightPaperExecutionCycleRejectsUnsafeInputsTableDriven(t *testing.T) {
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	quoteTime := now.Add(time.Minute)
	record := testValidationRecord(t, "research_app_0001", now.Add(-2*time.Hour), domainpaper.ValidationStatusRunning)
	ticket := appOrderTicket(now)
	ticket.ValidationID = record.ValidationID
	repositoryErr := errors.New("postgres unavailable")
	validReq := apppaper.PreflightPaperExecutionCycleRequest{
		ValidationID:      record.ValidationID,
		Exchange:          "bybit",
		Category:          "linear",
		Symbol:            "BTCUSDT",
		Interval:          "1",
		AsOf:              quoteTime.Add(time.Second),
		MaxStaleness:      30 * time.Second,
		MaxSpreadBPS:      decimal.RequireFromString("250"),
		PendingScanLimit:  10,
		PositionScanLimit: 10,
		QuoteScanLimit:    10,
	}

	tests := []struct {
		name                string
		record              domainpaper.ValidationRecord
		req                 apppaper.PreflightPaperExecutionCycleRequest
		orderbooks          *fakeOrderbookSnapshotRepository
		positions           *fakeOpenPositionRepository
		wantErrSub          string
		wantQuoteQueries    int
		wantPositionQueries int
	}{
		{
			name:   "missing validation id",
			record: record,
			req: func() apppaper.PreflightPaperExecutionCycleRequest {
				req := validReq
				req.ValidationID = ""
				return req
			}(),
			orderbooks: &fakeOrderbookSnapshotRepository{},
			positions:  &fakeOpenPositionRepository{},
			wantErrSub: "validation_id",
		},
		{
			name:       "missing exchange",
			record:     record,
			req:        func() apppaper.PreflightPaperExecutionCycleRequest { req := validReq; req.Exchange = ""; return req }(),
			orderbooks: &fakeOrderbookSnapshotRepository{},
			positions:  &fakeOpenPositionRepository{},
			wantErrSub: "exchange",
		},
		{
			name:       "missing symbol",
			record:     record,
			req:        func() apppaper.PreflightPaperExecutionCycleRequest { req := validReq; req.Symbol = ""; return req }(),
			orderbooks: &fakeOrderbookSnapshotRepository{},
			positions:  &fakeOpenPositionRepository{},
			wantErrSub: "symbol",
		},
		{
			name:   "negative pending scan limit",
			record: record,
			req: func() apppaper.PreflightPaperExecutionCycleRequest {
				req := validReq
				req.PendingScanLimit = -1
				return req
			}(),
			orderbooks: &fakeOrderbookSnapshotRepository{},
			positions:  &fakeOpenPositionRepository{},
			wantErrSub: "pending_scan_limit",
		},
		{
			name:   "negative position scan limit",
			record: record,
			req: func() apppaper.PreflightPaperExecutionCycleRequest {
				req := validReq
				req.PositionScanLimit = -1
				return req
			}(),
			orderbooks: &fakeOrderbookSnapshotRepository{},
			positions:  &fakeOpenPositionRepository{},
			wantErrSub: "position_scan_limit",
		},
		{
			name:   "negative quote scan limit",
			record: record,
			req: func() apppaper.PreflightPaperExecutionCycleRequest {
				req := validReq
				req.QuoteScanLimit = -1
				return req
			}(),
			orderbooks: &fakeOrderbookSnapshotRepository{},
			positions:  &fakeOpenPositionRepository{},
			wantErrSub: "quote_scan_limit",
		},
		{
			name: "non-running validation",
			record: func() domainpaper.ValidationRecord {
				planned := record
				planned.Status = domainpaper.ValidationStatusPlanned
				planned.StartedAt = time.Time{}
				return planned
			}(),
			req:        validReq,
			orderbooks: &fakeOrderbookSnapshotRepository{},
			positions:  &fakeOpenPositionRepository{},
			wantErrSub: "RUNNING",
		},
		{
			name:             "quote failure stops before scans",
			record:           record,
			req:              validReq,
			orderbooks:       &fakeOrderbookSnapshotRepository{err: repositoryErr},
			positions:        &fakeOpenPositionRepository{},
			wantErrSub:       repositoryErr.Error(),
			wantQuoteQueries: 1,
		},
		{
			name:                "position scan failure is reported after quote and pending checks",
			record:              record,
			req:                 validReq,
			orderbooks:          &fakeOrderbookSnapshotRepository{snapshots: []marketdata.OrderbookSnapshot{appOrderbookSnapshot(quoteTime, "100000", "100200")}},
			positions:           &fakeOpenPositionRepository{err: repositoryErr},
			wantErrSub:          repositoryErr.Error(),
			wantQuoteQueries:    1,
			wantPositionQueries: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fills := &fakeOrderFillRepository{}
			service := preflightService(
				now,
				tt.record,
				[]domainpaper.OrderTicket{ticket},
				fills,
				tt.positions,
				&fakePositionCloseRepository{},
				tt.orderbooks,
			)

			_, err := service.PreflightPaperExecutionCycle(context.Background(), tt.req)
			if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
			if len(tt.orderbooks.queries) != tt.wantQuoteQueries {
				t.Fatalf("quote query count mismatch: got %d want %d queries=%#v", len(tt.orderbooks.queries), tt.wantQuoteQueries, tt.orderbooks.queries)
			}
			if len(tt.positions.queries) != tt.wantPositionQueries {
				t.Fatalf("position query count mismatch: got %d want %d queries=%#v", len(tt.positions.queries), tt.wantPositionQueries, tt.positions.queries)
			}
			if fills.calls != 0 || tt.positions.calls != 0 {
				t.Fatalf("preflight must not write: fill_calls=%d position_calls=%d", fills.calls, tt.positions.calls)
			}
		})
	}
}

func preflightService(
	now time.Time,
	record domainpaper.ValidationRecord,
	tickets []domainpaper.OrderTicket,
	fills *fakeOrderFillRepository,
	positions *fakeOpenPositionRepository,
	closes *fakePositionCloseRepository,
	orderbooks *fakeOrderbookSnapshotRepository,
) *apppaper.Service {
	return apppaper.NewService(
		&fakeRunRepository{},
		&fakeResultRepository{},
		apppaper.WithValidationRecordRepository(&fakeValidationRecordRepository{records: []domainpaper.ValidationRecord{record}}),
		apppaper.WithOrderTicketRepository(&fakeOrderTicketRepository{tickets: tickets}),
		apppaper.WithOrderFillRepository(fills),
		apppaper.WithOpenPositionRepository(positions),
		apppaper.WithPositionCloseRepository(closes),
		apppaper.WithOrderbookSnapshotRepository(orderbooks),
		apppaper.WithClock(clock.FixedClock{Time: now.Add(5 * time.Minute)}),
	)
}
