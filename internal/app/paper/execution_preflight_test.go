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
	domainrisk "github.com/VersoIt/Inquisitor/internal/risk"
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
	existingEvent := appEquityEventForClose(now, existingClose, "paper_equity_app_0002")

	fills := &fakeOrderFillRepository{fills: []domainpaper.OrderFill{existingFill}}
	positions := &fakeOpenPositionRepository{positions: []domainpaper.OpenPosition{activePosition, closedPosition}}
	closes := &fakePositionCloseRepository{closes: []domainpaper.PositionClose{existingClose}}
	equity := &fakeEquityEventRepository{events: []domainpaper.EquityEvent{existingEvent}}
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
		equity,
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
	if got.AccountedClosedPositions != 1 || got.UnaccountedClosedPositions != 0 {
		t.Fatalf("equity ledger counters mismatch: %#v", got)
	}
	if got.KillSwitchActive || got.EntryBlockedByKillSwitch || got.KillSwitchReason != "" || got.KillSwitchSource != "" {
		t.Fatalf("inactive kill switch preflight mismatch: %#v", got)
	}
	if fills.calls != 0 || positions.calls != 0 || closes.calls != 0 || equity.calls != 0 {
		t.Fatalf("preflight must not write: fill_calls=%d position_calls=%d close_calls=%d equity_calls=%d", fills.calls, positions.calls, closes.calls, equity.calls)
	}
	if len(orderbooks.queries) != 1 || len(fills.queries) != 2 || len(positions.queries) != 1 ||
		len(closes.queries) != 2 || len(equity.queries) != 1 {
		t.Fatalf("query counters mismatch: orderbooks=%#v fills=%#v positions=%#v closes=%#v equity=%#v", orderbooks.queries, fills.queries, positions.queries, closes.queries, equity.queries)
	}
}

func TestServicePreflightPaperExecutionCycleChecksKillSwitchBeforePendingEntryTableDriven(t *testing.T) {
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	quoteTime := now.Add(time.Minute)
	record := testValidationRecord(t, "research_app_0001", now.Add(-2*time.Hour), domainpaper.ValidationStatusRunning)
	ticket := appOrderTicket(now)
	ticket.ValidationID = record.ValidationID
	position := appOpenPosition(now)
	position.ValidationID = record.ValidationID
	close := appPositionClose(now)
	close.ValidationID = record.ValidationID
	close.PositionID = position.PositionID
	repositoryErr := errors.New("postgres unavailable")
	activeKillSwitch := domainrisk.KillSwitchState{
		Active:    true,
		Reason:    "operator emergency stop",
		Source:    "operator",
		UpdatedAt: now.Add(time.Minute),
	}
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
		name              string
		tickets           []domainpaper.OrderTicket
		positions         []domainpaper.OpenPosition
		closes            []domainpaper.PositionClose
		equity            []domainpaper.EquityEvent
		killSwitchState   domainrisk.KillSwitchState
		killSwitchErr     error
		wantErrSub        string
		wantBlocked       bool
		wantActive        bool
		wantUnaccounted   int
		wantQuoteQueries  int
		wantKillSwitchHit int
	}{
		{
			name:              "active kill switch blocks pending entry",
			tickets:           []domainpaper.OrderTicket{ticket},
			killSwitchState:   activeKillSwitch,
			wantErrSub:        "kill switch",
			wantBlocked:       true,
			wantActive:        true,
			wantQuoteQueries:  1,
			wantKillSwitchHit: 1,
		},
		{
			name:              "active kill switch does not block active position inspection",
			tickets:           []domainpaper.OrderTicket{ticket},
			positions:         []domainpaper.OpenPosition{position},
			killSwitchState:   activeKillSwitch,
			wantActive:        true,
			wantQuoteQueries:  1,
			wantKillSwitchHit: 1,
		},
		{
			name:              "active kill switch does not block unaccounted close recovery",
			tickets:           []domainpaper.OrderTicket{ticket},
			positions:         []domainpaper.OpenPosition{position},
			closes:            []domainpaper.PositionClose{close},
			killSwitchState:   activeKillSwitch,
			wantActive:        true,
			wantUnaccounted:   1,
			wantQuoteQueries:  1,
			wantKillSwitchHit: 1,
		},
		{
			name:              "inactive kill switch allows pending entry preflight",
			tickets:           []domainpaper.OrderTicket{ticket},
			wantQuoteQueries:  1,
			wantKillSwitchHit: 1,
		},
		{
			name:              "kill switch lookup failure fails closed",
			tickets:           []domainpaper.OrderTicket{ticket},
			killSwitchErr:     repositoryErr,
			wantErrSub:        repositoryErr.Error(),
			wantKillSwitchHit: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			killSwitch := &fakePaperKillSwitchRepository{state: tt.killSwitchState, err: tt.killSwitchErr}
			orderbooks := &fakeOrderbookSnapshotRepository{snapshots: []marketdata.OrderbookSnapshot{appOrderbookSnapshot(quoteTime, "100000", "100200")}}
			service := preflightService(
				now,
				record,
				tt.tickets,
				&fakeOrderFillRepository{},
				&fakeOpenPositionRepository{positions: tt.positions},
				&fakePositionCloseRepository{closes: tt.closes},
				&fakeEquityEventRepository{events: tt.equity},
				orderbooks,
				killSwitch,
			)

			got, err := service.PreflightPaperExecutionCycle(context.Background(), validReq)
			if tt.wantErrSub != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
				}
			} else if err != nil {
				t.Fatalf("preflight paper execution cycle: %v", err)
			}
			if got.KillSwitchActive != tt.wantActive || got.EntryBlockedByKillSwitch != tt.wantBlocked ||
				got.UnaccountedClosedPositions != tt.wantUnaccounted {
				t.Fatalf("kill-switch preflight result mismatch: %#v", got)
			}
			if tt.wantActive && (got.KillSwitchReason != "operator emergency stop" || got.KillSwitchSource != "operator") {
				t.Fatalf("kill-switch metadata mismatch: %#v", got)
			}
			if len(orderbooks.queries) != tt.wantQuoteQueries {
				t.Fatalf("quote query count mismatch: got %d want %d queries=%#v", len(orderbooks.queries), tt.wantQuoteQueries, orderbooks.queries)
			}
			if killSwitch.currentCalls != tt.wantKillSwitchHit {
				t.Fatalf("kill switch query count mismatch: got %d want %d", killSwitch.currentCalls, tt.wantKillSwitchHit)
			}
		})
	}
}

func TestServicePreflightPaperExecutionCycleChecksClosedPositionEquityLedgerTableDriven(t *testing.T) {
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	quoteTime := now.Add(time.Minute)
	record := testValidationRecord(t, "research_app_0001", now.Add(-2*time.Hour), domainpaper.ValidationStatusRunning)
	position := appOpenPosition(now)
	position.ValidationID = record.ValidationID
	close := appPositionClose(now)
	close.ValidationID = record.ValidationID
	close.PositionID = position.PositionID
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
		name              string
		equity            *fakeEquityEventRepository
		wantAccounted     int
		wantUnaccounted   int
		wantErrSub        string
		wantEquityQueries int
	}{
		{
			name:              "accounted close is reported",
			equity:            &fakeEquityEventRepository{events: []domainpaper.EquityEvent{appEquityEventForClose(now, close, "paper_equity_app_0001")}},
			wantAccounted:     1,
			wantEquityQueries: 1,
		},
		{
			name:              "unaccounted close is reported without writing",
			equity:            &fakeEquityEventRepository{},
			wantUnaccounted:   1,
			wantEquityQueries: 1,
		},
		{
			name:              "equity list failure is reported",
			equity:            &fakeEquityEventRepository{listErr: repositoryErr},
			wantErrSub:        repositoryErr.Error(),
			wantEquityQueries: 1,
		},
		{
			name: "duplicate equity events fail closed",
			equity: &fakeEquityEventRepository{events: []domainpaper.EquityEvent{
				appEquityEventForClose(now, close, "paper_equity_app_0001"),
				appEquityEventForClose(now, close, "paper_equity_app_0002"),
			}},
			wantErrSub:        "inconsistent equity ledger",
			wantEquityQueries: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fills := &fakeOrderFillRepository{}
			positions := &fakeOpenPositionRepository{positions: []domainpaper.OpenPosition{position}}
			closes := &fakePositionCloseRepository{closes: []domainpaper.PositionClose{close}}
			service := preflightService(
				now,
				record,
				nil,
				fills,
				positions,
				closes,
				tt.equity,
				&fakeOrderbookSnapshotRepository{snapshots: []marketdata.OrderbookSnapshot{appOrderbookSnapshot(quoteTime, "100000", "100200")}},
			)

			got, err := service.PreflightPaperExecutionCycle(context.Background(), validReq)
			if tt.wantErrSub != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
				}
			} else if err != nil {
				t.Fatalf("preflight paper execution cycle: %v", err)
			}
			if got.AccountedClosedPositions != tt.wantAccounted || got.UnaccountedClosedPositions != tt.wantUnaccounted {
				t.Fatalf("equity counters mismatch: got=%#v want_accounted=%d want_unaccounted=%d", got, tt.wantAccounted, tt.wantUnaccounted)
			}
			if len(tt.equity.queries) != tt.wantEquityQueries {
				t.Fatalf("equity query count mismatch: got %d want %d queries=%#v", len(tt.equity.queries), tt.wantEquityQueries, tt.equity.queries)
			}
			if fills.calls != 0 || positions.calls != 0 || closes.calls != 0 || tt.equity.calls != 0 {
				t.Fatalf("preflight must not write: fill_calls=%d position_calls=%d close_calls=%d equity_calls=%d", fills.calls, positions.calls, closes.calls, tt.equity.calls)
			}
		})
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
				&fakeEquityEventRepository{},
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

func TestServicePreflightPaperExecutionCycleRequiresEquityRepository(t *testing.T) {
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	record := testValidationRecord(t, "research_app_0001", now.Add(-2*time.Hour), domainpaper.ValidationStatusRunning)
	service := preflightService(
		now,
		record,
		nil,
		&fakeOrderFillRepository{},
		&fakeOpenPositionRepository{},
		&fakePositionCloseRepository{},
		nil,
		&fakeOrderbookSnapshotRepository{},
	)

	_, err := service.PreflightPaperExecutionCycle(context.Background(), apppaper.PreflightPaperExecutionCycleRequest{
		ValidationID:      record.ValidationID,
		Exchange:          "bybit",
		Category:          "linear",
		Symbol:            "BTCUSDT",
		Interval:          "1",
		AsOf:              now.Add(time.Minute),
		MaxStaleness:      30 * time.Second,
		MaxSpreadBPS:      decimal.RequireFromString("250"),
		PendingScanLimit:  10,
		PositionScanLimit: 10,
		QuoteScanLimit:    10,
	})
	if err == nil || !strings.Contains(err.Error(), "equity event repository") {
		t.Fatalf("expected missing equity repository error, got %v", err)
	}
}

func TestServicePreflightPaperExecutionCycleRequiresKillSwitchRepository(t *testing.T) {
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	record := testValidationRecord(t, "research_app_0001", now.Add(-2*time.Hour), domainpaper.ValidationStatusRunning)
	service := preflightService(
		now,
		record,
		nil,
		&fakeOrderFillRepository{},
		&fakeOpenPositionRepository{},
		&fakePositionCloseRepository{},
		&fakeEquityEventRepository{},
		&fakeOrderbookSnapshotRepository{},
		nil,
	)

	_, err := service.PreflightPaperExecutionCycle(context.Background(), apppaper.PreflightPaperExecutionCycleRequest{
		ValidationID:      record.ValidationID,
		Exchange:          "bybit",
		Category:          "linear",
		Symbol:            "BTCUSDT",
		Interval:          "1",
		AsOf:              now.Add(time.Minute),
		MaxStaleness:      30 * time.Second,
		MaxSpreadBPS:      decimal.RequireFromString("250"),
		PendingScanLimit:  10,
		PositionScanLimit: 10,
		QuoteScanLimit:    10,
	})
	if err == nil || !strings.Contains(err.Error(), "kill switch repository") {
		t.Fatalf("expected missing kill switch repository error, got %v", err)
	}
}

func preflightService(
	now time.Time,
	record domainpaper.ValidationRecord,
	tickets []domainpaper.OrderTicket,
	fills *fakeOrderFillRepository,
	positions *fakeOpenPositionRepository,
	closes *fakePositionCloseRepository,
	equity *fakeEquityEventRepository,
	orderbooks *fakeOrderbookSnapshotRepository,
	killSwitches ...*fakePaperKillSwitchRepository,
) *apppaper.Service {
	options := []apppaper.Option{
		apppaper.WithValidationRecordRepository(&fakeValidationRecordRepository{records: []domainpaper.ValidationRecord{record}}),
		apppaper.WithOrderTicketRepository(&fakeOrderTicketRepository{tickets: tickets}),
		apppaper.WithOrderFillRepository(fills),
		apppaper.WithOpenPositionRepository(positions),
		apppaper.WithPositionCloseRepository(closes),
		apppaper.WithOrderbookSnapshotRepository(orderbooks),
		apppaper.WithClock(clock.FixedClock{Time: now.Add(5 * time.Minute)}),
	}
	if equity != nil {
		options = append(options, apppaper.WithEquityEventRepository(equity))
	}
	killSwitch := &fakePaperKillSwitchRepository{}
	if len(killSwitches) > 0 {
		killSwitch = killSwitches[0]
	}
	if killSwitch != nil {
		options = append(options, apppaper.WithKillSwitchRepository(killSwitch))
	}
	return apppaper.NewService(&fakeRunRepository{}, &fakeResultRepository{}, options...)
}

func appEquityEventForClose(now time.Time, close domainpaper.PositionClose, eventID string) domainpaper.EquityEvent {
	event := appEquityEvent(now)
	event.EventID = eventID
	event.ValidationID = close.ValidationID
	event.CloseID = close.CloseID
	event.PositionID = close.PositionID
	event.Exchange = close.Exchange
	event.Category = close.Category
	event.Symbol = close.Symbol
	event.Interval = close.Interval
	event.NetPnL = close.NetPnL
	event.Fees = close.Fees
	event.EquityAfter = event.EquityBefore.Add(close.NetPnL)
	event.OccurredAt = close.ClosedAt
	return event
}
