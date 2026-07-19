package paper_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	apppaper "github.com/VersoIt/Inquisitor/internal/app/paper"
	"github.com/VersoIt/Inquisitor/internal/backtest"
	"github.com/VersoIt/Inquisitor/internal/clock"
	"github.com/VersoIt/Inquisitor/internal/marketdata"
	domainpaper "github.com/VersoIt/Inquisitor/internal/paper"
)

func TestServiceReconcilePaperEntryWithQuoteSelectsPendingTicketSourcesQuoteAndOpensPosition(t *testing.T) {
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	ticket := appOrderTicket(now)
	record := testValidationRecord(t, "research_app_0001", now.Add(-2*time.Hour), domainpaper.ValidationStatusRunning)
	record.ValidationID = ticket.ValidationID
	quoteTime := now.Add(time.Minute)
	orderbooks := &fakeOrderbookSnapshotRepository{
		snapshots: []marketdata.OrderbookSnapshot{appOrderbookSnapshot(quoteTime, "100000", "100200")},
	}
	fills := &fakeOrderFillRepository{stats: domainpaper.OrderFillStats{Inserted: 1}}
	positions := &fakeOpenPositionRepository{stats: domainpaper.OpenPositionStats{Inserted: 1}}
	service := autoEntryReconciliationService(now, record, []domainpaper.OrderTicket{ticket}, fills, positions, orderbooks)

	got, err := service.ReconcilePaperEntryWithQuote(context.Background(), apppaper.ReconcilePaperEntryWithQuoteRequest{
		ValidationID:     ticket.ValidationID,
		Symbol:           "BTCUSDT",
		Interval:         "1",
		Liquidity:        backtest.LiquidityTaker,
		Costs:            marketExecutionCosts(t),
		AsOf:             quoteTime.Add(2 * time.Second),
		MaxStaleness:     30 * time.Second,
		MaxSpreadBPS:     decimal.RequireFromString("250"),
		PendingScanLimit: 10,
		QuoteScanLimit:   20,
	})
	if err != nil {
		t.Fatalf("reconcile paper entry with quote: %v", err)
	}

	if got.Record.ValidationID != record.ValidationID || got.Ticket.TicketID != ticket.TicketID {
		t.Fatalf("context mismatch: %#v", got)
	}
	if !got.QuoteSourced || got.UsedExistingFill {
		t.Fatalf("quote/retry flags mismatch: %#v", got)
	}
	if got.Fill.FillID != ticket.TicketID+"_fill" || got.Position.PositionID != ticket.TicketID+"_position" {
		t.Fatalf("default ids mismatch: fill=%q position=%q", got.Fill.FillID, got.Position.PositionID)
	}
	if !got.Fill.MidPrice.Equal(got.Quote.MidPrice) || !got.Position.EntryPrice.Equal(got.Fill.ExecutedPrice) ||
		!got.Position.OpenRisk.Equal(decimal.RequireFromString("575.025")) {
		t.Fatalf("entry accounting mismatch: quote=%#v fill=%#v position=%#v", got.Quote, got.Fill, got.Position)
	}
	if got.FillStats.Inserted != 1 || got.PositionStats.Inserted != 1 || fills.calls != 1 || positions.calls != 1 {
		t.Fatalf("repository stats mismatch: fill_stats=%#v position_stats=%#v fill_calls=%d position_calls=%d", got.FillStats, got.PositionStats, fills.calls, positions.calls)
	}
	if len(orderbooks.queries) != 1 || orderbooks.queries[0].Limit != 20 || !orderbooks.queries[0].End.Equal(quoteTime.Add(2*time.Second).Add(time.Nanosecond)) {
		t.Fatalf("quote query mismatch: %#v", orderbooks.queries)
	}
	if len(fills.queries) < 2 || fills.queries[0].ValidationID != ticket.ValidationID ||
		fills.queries[0].TicketID != ticket.TicketID || fills.queries[0].Limit != 2 ||
		fills.queries[1].ValidationID != ticket.ValidationID || fills.queries[1].TicketID != ticket.TicketID || fills.queries[1].Limit != 2 {
		t.Fatalf("expected pending and pre-quote fill checks, got %#v", fills.queries)
	}
}

func TestServiceReconcilePaperEntryWithQuoteScopesExplicitTicketLookupToValidation(t *testing.T) {
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	ticket := appOrderTicket(now)
	record := testValidationRecord(t, "research_app_0001", now.Add(-2*time.Hour), domainpaper.ValidationStatusRunning)
	record.ValidationID = ticket.ValidationID
	foreignTicket := ticket
	foreignTicket.ValidationID = "paper_validation_foreign_0001"
	tickets := &fakeOrderTicketRepository{tickets: []domainpaper.OrderTicket{foreignTicket, ticket}}
	fills := &fakeOrderFillRepository{stats: domainpaper.OrderFillStats{Inserted: 1}}
	positions := &fakeOpenPositionRepository{stats: domainpaper.OpenPositionStats{Inserted: 1}}
	quoteTime := now.Add(time.Minute)
	service := apppaper.NewService(
		&fakeRunRepository{},
		&fakeResultRepository{},
		apppaper.WithValidationRecordRepository(&fakeValidationRecordRepository{records: []domainpaper.ValidationRecord{record}}),
		apppaper.WithOrderTicketRepository(tickets),
		apppaper.WithOrderFillRepository(fills),
		apppaper.WithOpenPositionRepository(positions),
		apppaper.WithKillSwitchRepository(&fakePaperKillSwitchRepository{}),
		apppaper.WithOrderbookSnapshotRepository(&fakeOrderbookSnapshotRepository{
			snapshots: []marketdata.OrderbookSnapshot{appOrderbookSnapshot(quoteTime, "100000", "100200")},
		}),
		apppaper.WithClock(clock.FixedClock{Time: now.Add(3 * time.Minute)}),
	)

	got, err := service.ReconcilePaperEntryWithQuote(context.Background(), apppaper.ReconcilePaperEntryWithQuoteRequest{
		ValidationID:   " " + ticket.ValidationID + " ",
		TicketID:       ticket.TicketID,
		Liquidity:      backtest.LiquidityTaker,
		Costs:          marketExecutionCosts(t),
		AsOf:           quoteTime.Add(time.Second),
		MaxStaleness:   30 * time.Second,
		MaxSpreadBPS:   decimal.RequireFromString("250"),
		QuoteScanLimit: 10,
	})
	if err != nil {
		t.Fatalf("reconcile explicit paper entry with foreign ticket present: %v", err)
	}

	if got.Ticket.ValidationID != record.ValidationID || got.Fill.ValidationID != record.ValidationID ||
		got.Position.ValidationID != record.ValidationID || !got.QuoteSourced {
		t.Fatalf("scoped explicit entry result mismatch: %#v", got)
	}
	if len(tickets.queries) != 4 {
		t.Fatalf("expected explicit, simulate, record, and open ticket lookups, got %#v", tickets.queries)
	}
	for index, query := range tickets.queries {
		if query.ValidationID != record.ValidationID || query.TicketID != ticket.TicketID || query.Limit != 2 {
			t.Fatalf("ticket lookup[%d] must be scoped to validation: %#v", index, query)
		}
	}
}

func TestServiceReconcilePaperEntryWithQuoteScopesExistingFillLookupToValidation(t *testing.T) {
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	ticket := appOrderTicket(now)
	record := testValidationRecord(t, "research_app_0001", now.Add(-2*time.Hour), domainpaper.ValidationStatusRunning)
	record.ValidationID = ticket.ValidationID
	foreignFill := appOrderFill(now)
	foreignFill.FillID = "paper_fill_foreign_0001"
	foreignFill.ValidationID = "paper_validation_foreign_0001"
	foreignFill.TicketID = ticket.TicketID
	quoteTime := now.Add(time.Minute)
	fills := &fakeOrderFillRepository{
		fills: []domainpaper.OrderFill{foreignFill},
		stats: domainpaper.OrderFillStats{Inserted: 1},
	}
	positions := &fakeOpenPositionRepository{stats: domainpaper.OpenPositionStats{Inserted: 1}}
	orderbooks := &fakeOrderbookSnapshotRepository{
		snapshots: []marketdata.OrderbookSnapshot{appOrderbookSnapshot(quoteTime, "100000", "100200")},
	}
	service := autoEntryReconciliationService(now, record, []domainpaper.OrderTicket{ticket}, fills, positions, orderbooks)

	got, err := service.ReconcilePaperEntryWithQuote(context.Background(), apppaper.ReconcilePaperEntryWithQuoteRequest{
		ValidationID:   ticket.ValidationID,
		TicketID:       ticket.TicketID,
		Liquidity:      backtest.LiquidityTaker,
		Costs:          marketExecutionCosts(t),
		AsOf:           quoteTime.Add(time.Second),
		MaxStaleness:   30 * time.Second,
		MaxSpreadBPS:   decimal.RequireFromString("250"),
		QuoteScanLimit: 10,
	})
	if err != nil {
		t.Fatalf("reconcile paper entry with foreign fill present: %v", err)
	}

	if got.UsedExistingFill || !got.QuoteSourced || got.Fill.ValidationID != record.ValidationID ||
		got.Fill.FillID == foreignFill.FillID || got.Position.ValidationID != record.ValidationID {
		t.Fatalf("scoped entry result mismatch: %#v", got)
	}
	if fills.calls != 1 || positions.calls != 1 || len(orderbooks.queries) != 1 {
		t.Fatalf("entry repository call mismatch: fill_calls=%d position_calls=%d quote_queries=%#v", fills.calls, positions.calls, orderbooks.queries)
	}
	if len(fills.queries) < 2 {
		t.Fatalf("expected existing-fill and record-fill lookups, got %#v", fills.queries)
	}
	for index, query := range fills.queries[:2] {
		if query.ValidationID != record.ValidationID || query.TicketID != ticket.TicketID || query.Limit != 2 {
			t.Fatalf("fill lookup[%d] must be scoped to validation: %#v", index, query)
		}
	}
}

func TestServiceReconcilePaperEntryWithQuoteCompletesOpenAfterExistingFillWithoutQuote(t *testing.T) {
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	ticket := appOrderTicket(now)
	fill := appOrderFill(now)
	record := testValidationRecord(t, "research_app_0001", now.Add(-2*time.Hour), domainpaper.ValidationStatusRunning)
	record.ValidationID = ticket.ValidationID
	orderbooks := &fakeOrderbookSnapshotRepository{err: errors.New("must not be called")}
	positions := &fakeOpenPositionRepository{stats: domainpaper.OpenPositionStats{Inserted: 1}}
	service := autoEntryReconciliationService(
		now,
		record,
		[]domainpaper.OrderTicket{ticket},
		&fakeOrderFillRepository{fills: []domainpaper.OrderFill{fill}},
		positions,
		orderbooks,
	)

	got, err := service.ReconcilePaperEntryWithQuote(context.Background(), apppaper.ReconcilePaperEntryWithQuoteRequest{
		ValidationID: ticket.ValidationID,
		TicketID:     ticket.TicketID,
		Liquidity:    backtest.LiquidityTaker,
		Costs:        marketExecutionCosts(t),
		AsOf:         now.Add(2 * time.Minute),
		MaxStaleness: 30 * time.Second,
		MaxSpreadBPS: decimal.RequireFromString("250"),
	})
	if err != nil {
		t.Fatalf("reconcile existing paper entry: %v", err)
	}

	if !got.UsedExistingFill || got.QuoteSourced || got.Fill.FillID != fill.FillID || got.Position.FillID != fill.FillID {
		t.Fatalf("existing fill continuation mismatch: %#v", got)
	}
	if len(orderbooks.queries) != 0 {
		t.Fatalf("existing fill retry must not source quote, got %#v", orderbooks.queries)
	}
	if positions.calls != 1 || got.PositionStats.Inserted != 1 {
		t.Fatalf("position write mismatch: calls=%d stats=%#v", positions.calls, got.PositionStats)
	}
}

func TestServiceReconcilePaperEntryWithQuoteUsesRequestedEntryIDs(t *testing.T) {
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	ticket := appOrderTicket(now)
	record := testValidationRecord(t, "research_app_0001", now.Add(-2*time.Hour), domainpaper.ValidationStatusRunning)
	record.ValidationID = ticket.ValidationID
	service := autoEntryReconciliationService(
		now,
		record,
		[]domainpaper.OrderTicket{ticket},
		&fakeOrderFillRepository{stats: domainpaper.OrderFillStats{Inserted: 1}},
		&fakeOpenPositionRepository{stats: domainpaper.OpenPositionStats{Inserted: 1}},
		&fakeOrderbookSnapshotRepository{snapshots: []marketdata.OrderbookSnapshot{appOrderbookSnapshot(now.Add(time.Minute), "100000", "100200")}},
	)

	got, err := service.ReconcilePaperEntryWithQuote(context.Background(), apppaper.ReconcilePaperEntryWithQuoteRequest{
		ValidationID:   ticket.ValidationID,
		TicketID:       ticket.TicketID,
		FillID:         " paper_fill_custom_0001 ",
		PositionID:     " paper_position_custom_0001 ",
		Liquidity:      backtest.LiquidityTaker,
		Costs:          marketExecutionCosts(t),
		AsOf:           now.Add(2 * time.Minute),
		MaxStaleness:   2 * time.Minute,
		MaxSpreadBPS:   decimal.RequireFromString("250"),
		QuoteScanLimit: 10,
	})
	if err != nil {
		t.Fatalf("reconcile paper entry with requested ids: %v", err)
	}

	if got.Fill.FillID != "paper_fill_custom_0001" || got.Position.PositionID != "paper_position_custom_0001" {
		t.Fatalf("requested ids mismatch: fill=%q position=%q", got.Fill.FillID, got.Position.PositionID)
	}
}

func TestServiceReconcilePaperEntryWithQuoteRejectsUnsafeInputsTableDriven(t *testing.T) {
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	ticket := appOrderTicket(now)
	record := testValidationRecord(t, "research_app_0001", now.Add(-2*time.Hour), domainpaper.ValidationStatusRunning)
	record.ValidationID = ticket.ValidationID
	plannedRecord := record
	plannedRecord.Status = domainpaper.ValidationStatusPlanned
	plannedRecord.StartedAt = time.Time{}
	repositoryErr := errors.New("postgres unavailable")
	validReq := apppaper.ReconcilePaperEntryWithQuoteRequest{
		ValidationID:     ticket.ValidationID,
		Liquidity:        backtest.LiquidityTaker,
		Costs:            marketExecutionCosts(t),
		AsOf:             now.Add(2 * time.Minute),
		MaxStaleness:     30 * time.Second,
		MaxSpreadBPS:     decimal.RequireFromString("250"),
		PendingScanLimit: 10,
		QuoteScanLimit:   10,
	}

	tests := []struct {
		name              string
		record            domainpaper.ValidationRecord
		tickets           []domainpaper.OrderTicket
		fills             *fakeOrderFillRepository
		positions         *fakeOpenPositionRepository
		orderbooks        *fakeOrderbookSnapshotRepository
		req               apppaper.ReconcilePaperEntryWithQuoteRequest
		wantErrSub        string
		wantFillCalls     int
		wantPositionCalls int
		wantQuoteQueries  int
	}{
		{
			name:       "missing validation id",
			record:     record,
			tickets:    []domainpaper.OrderTicket{ticket},
			fills:      &fakeOrderFillRepository{},
			positions:  &fakeOpenPositionRepository{},
			orderbooks: &fakeOrderbookSnapshotRepository{},
			req: func() apppaper.ReconcilePaperEntryWithQuoteRequest {
				req := validReq
				req.ValidationID = ""
				return req
			}(),
			wantErrSub:        "validation_id",
			wantFillCalls:     0,
			wantPositionCalls: 0,
			wantQuoteQueries:  0,
		},
		{
			name:              "no pending tickets",
			record:            record,
			tickets:           nil,
			fills:             &fakeOrderFillRepository{},
			positions:         &fakeOpenPositionRepository{},
			orderbooks:        &fakeOrderbookSnapshotRepository{},
			req:               validReq,
			wantErrSub:        "no pending",
			wantFillCalls:     0,
			wantPositionCalls: 0,
			wantQuoteQueries:  0,
		},
		{
			name:              "validation is not running",
			record:            plannedRecord,
			tickets:           []domainpaper.OrderTicket{ticket},
			fills:             &fakeOrderFillRepository{},
			positions:         &fakeOpenPositionRepository{},
			orderbooks:        &fakeOrderbookSnapshotRepository{},
			req:               validReq,
			wantErrSub:        "RUNNING",
			wantFillCalls:     0,
			wantPositionCalls: 0,
			wantQuoteQueries:  0,
		},
		{
			name:   "explicit ticket outside validation is not found",
			record: record,
			tickets: []domainpaper.OrderTicket{func() domainpaper.OrderTicket {
				other := ticket
				other.ValidationID = "paper_validation_other_0001"
				return other
			}()},
			fills:     &fakeOrderFillRepository{},
			positions: &fakeOpenPositionRepository{},
			orderbooks: &fakeOrderbookSnapshotRepository{
				snapshots: []marketdata.OrderbookSnapshot{appOrderbookSnapshot(now.Add(time.Minute), "100000", "100200")},
			},
			req: func() apppaper.ReconcilePaperEntryWithQuoteRequest {
				req := validReq
				req.TicketID = ticket.TicketID
				return req
			}(),
			wantErrSub:        "not found",
			wantFillCalls:     0,
			wantPositionCalls: 0,
			wantQuoteQueries:  0,
		},
		{
			name:    "existing fill id mismatch",
			record:  record,
			tickets: []domainpaper.OrderTicket{ticket},
			fills: &fakeOrderFillRepository{fills: []domainpaper.OrderFill{func() domainpaper.OrderFill {
				fill := appOrderFill(now)
				fill.FillID = "paper_fill_existing_0001"
				return fill
			}()}},
			positions:  &fakeOpenPositionRepository{},
			orderbooks: &fakeOrderbookSnapshotRepository{},
			req: func() apppaper.ReconcilePaperEntryWithQuoteRequest {
				req := validReq
				req.TicketID = ticket.TicketID
				req.FillID = "paper_fill_requested_0001"
				return req
			}(),
			wantErrSub:        "already has fill",
			wantFillCalls:     0,
			wantPositionCalls: 0,
			wantQuoteQueries:  0,
		},
		{
			name:              "quote failure does not write fill or position",
			record:            record,
			tickets:           []domainpaper.OrderTicket{ticket},
			fills:             &fakeOrderFillRepository{},
			positions:         &fakeOpenPositionRepository{},
			orderbooks:        &fakeOrderbookSnapshotRepository{err: repositoryErr},
			req:               validReq,
			wantErrSub:        repositoryErr.Error(),
			wantFillCalls:     0,
			wantPositionCalls: 0,
			wantQuoteQueries:  1,
		},
		{
			name:              "missing orderbook repository for new fill",
			record:            record,
			tickets:           []domainpaper.OrderTicket{ticket},
			fills:             &fakeOrderFillRepository{},
			positions:         &fakeOpenPositionRepository{},
			orderbooks:        nil,
			req:               validReq,
			wantErrSub:        "orderbook snapshot repository",
			wantFillCalls:     0,
			wantPositionCalls: 0,
			wantQuoteQueries:  0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := autoEntryReconciliationService(now, tt.record, tt.tickets, tt.fills, tt.positions, tt.orderbooks)

			_, err := service.ReconcilePaperEntryWithQuote(context.Background(), tt.req)
			if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
			if tt.fills.calls != tt.wantFillCalls {
				t.Fatalf("fill calls mismatch: got %d want %d", tt.fills.calls, tt.wantFillCalls)
			}
			if tt.positions.calls != tt.wantPositionCalls {
				t.Fatalf("position calls mismatch: got %d want %d", tt.positions.calls, tt.wantPositionCalls)
			}
			if tt.orderbooks != nil && len(tt.orderbooks.queries) != tt.wantQuoteQueries {
				t.Fatalf("quote query count mismatch: got %d want %d queries=%#v", len(tt.orderbooks.queries), tt.wantQuoteQueries, tt.orderbooks.queries)
			}
		})
	}
}

func autoEntryReconciliationService(
	now time.Time,
	record domainpaper.ValidationRecord,
	tickets []domainpaper.OrderTicket,
	fills *fakeOrderFillRepository,
	positions *fakeOpenPositionRepository,
	orderbooks *fakeOrderbookSnapshotRepository,
) *apppaper.Service {
	options := []apppaper.Option{
		apppaper.WithValidationRecordRepository(&fakeValidationRecordRepository{records: []domainpaper.ValidationRecord{record}}),
		apppaper.WithOrderTicketRepository(&fakeOrderTicketRepository{tickets: tickets}),
		apppaper.WithOrderFillRepository(fills),
		apppaper.WithOpenPositionRepository(positions),
		apppaper.WithKillSwitchRepository(&fakePaperKillSwitchRepository{}),
		apppaper.WithClock(clock.FixedClock{Time: now.Add(3 * time.Minute)}),
	}
	if orderbooks != nil {
		options = append(options, apppaper.WithOrderbookSnapshotRepository(orderbooks))
	}
	return apppaper.NewService(&fakeRunRepository{}, &fakeResultRepository{}, options...)
}
