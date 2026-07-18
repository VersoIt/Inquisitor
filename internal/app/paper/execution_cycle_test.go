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

func TestServiceRunPaperExecutionCycleSettlesExitBeforeEntry(t *testing.T) {
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	position := appOpenPosition(now)
	ticket := appOrderTicket(now)
	record := testValidationRecord(t, "research_app_0001", now.Add(-2*time.Hour), domainpaper.ValidationStatusRunning)
	record.ValidationID = position.ValidationID
	fills := &fakeOrderFillRepository{}
	positions := &fakeOpenPositionRepository{positions: []domainpaper.OpenPosition{position}}
	closes := &fakePositionCloseRepository{stats: domainpaper.PositionCloseStats{Inserted: 1}}
	equity := &fakeEquityEventRepository{stats: domainpaper.EquityEventStats{Inserted: 1}}
	quoteTime := now.Add(4 * time.Minute)
	orderbooks := &fakeOrderbookSnapshotRepository{
		snapshots: []marketdata.OrderbookSnapshot{appOrderbookSnapshot(quoteTime, "101900", "102100")},
	}
	service := executionCycleService(now, record, []domainpaper.OrderTicket{ticket}, fills, positions, closes, equity, orderbooks)

	got, err := service.RunPaperExecutionCycle(context.Background(), apppaper.RunPaperExecutionCycleRequest{
		ValidationID:      record.ValidationID,
		Symbol:            "BTCUSDT",
		Interval:          "1",
		Liquidity:         backtest.LiquidityTaker,
		Costs:             marketExecutionCosts(t),
		AsOf:              quoteTime.Add(time.Second),
		MaxStaleness:      30 * time.Second,
		MaxSpreadBPS:      decimal.RequireFromString("250"),
		PendingScanLimit:  10,
		PositionScanLimit: 10,
		QuoteScanLimit:    10,
	})
	if err != nil {
		t.Fatalf("run paper execution cycle: %v", err)
	}

	if got.Action != apppaper.PaperExecutionCycleActionExit || !got.Exit.ExitTriggered || got.Entry.Position.PositionID != "" {
		t.Fatalf("exit-first action mismatch: %#v", got)
	}
	if got.EntryChecked || fills.calls != 0 || positions.calls != 0 {
		t.Fatalf("cycle must not enter after exit: entry_checked=%t fill_calls=%d position_calls=%d", got.EntryChecked, fills.calls, positions.calls)
	}
	if closes.calls != 1 || equity.calls != 1 || got.Exit.CloseStats.Inserted != 1 || got.Exit.EquityStats.Inserted != 1 {
		t.Fatalf("exit repository mismatch: close_calls=%d equity_calls=%d exit=%#v", closes.calls, equity.calls, got.Exit)
	}
}

func TestServiceRunPaperExecutionCycleSkipsEntryWhenActivePositionHasNoExitTrigger(t *testing.T) {
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	position := appOpenPosition(now)
	ticket := appOrderTicket(now)
	record := testValidationRecord(t, "research_app_0001", now.Add(-2*time.Hour), domainpaper.ValidationStatusRunning)
	record.ValidationID = position.ValidationID
	fills := &fakeOrderFillRepository{}
	positions := &fakeOpenPositionRepository{positions: []domainpaper.OpenPosition{position}}
	closes := &fakePositionCloseRepository{}
	equity := &fakeEquityEventRepository{}
	quoteTime := now.Add(2 * time.Minute)
	orderbooks := &fakeOrderbookSnapshotRepository{
		snapshots: []marketdata.OrderbookSnapshot{appOrderbookSnapshot(quoteTime, "100400", "100600")},
	}
	service := executionCycleService(now, record, []domainpaper.OrderTicket{ticket}, fills, positions, closes, equity, orderbooks)

	got, err := service.RunPaperExecutionCycle(context.Background(), apppaper.RunPaperExecutionCycleRequest{
		ValidationID:      record.ValidationID,
		Symbol:            "BTCUSDT",
		Interval:          "1",
		Liquidity:         backtest.LiquidityTaker,
		Costs:             marketExecutionCosts(t),
		AsOf:              quoteTime.Add(time.Second),
		MaxStaleness:      30 * time.Second,
		MaxSpreadBPS:      decimal.RequireFromString("250"),
		PendingScanLimit:  10,
		PositionScanLimit: 10,
		QuoteScanLimit:    10,
	})
	if err != nil {
		t.Fatalf("run guarded paper cycle: %v", err)
	}

	if got.Action != apppaper.PaperExecutionCycleActionNone || got.SkipReason != apppaper.PaperExecutionCycleSkipNoExitTrigger {
		t.Fatalf("active-position skip mismatch: %#v", got)
	}
	if got.EntryChecked || fills.calls != 0 || positions.calls != 0 || closes.calls != 0 || equity.calls != 0 {
		t.Fatalf("active position must block entry writes: got=%#v fill_calls=%d position_calls=%d close_calls=%d equity_calls=%d", got, fills.calls, positions.calls, closes.calls, equity.calls)
	}
}

func TestServiceRunPaperExecutionCycleEntersWhenNoActivePositionAndPendingTicket(t *testing.T) {
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	ticket := appOrderTicket(now)
	record := testValidationRecord(t, "research_app_0001", now.Add(-2*time.Hour), domainpaper.ValidationStatusRunning)
	record.ValidationID = ticket.ValidationID
	fills := &fakeOrderFillRepository{stats: domainpaper.OrderFillStats{Inserted: 1}}
	positions := &fakeOpenPositionRepository{stats: domainpaper.OpenPositionStats{Inserted: 1}}
	quoteTime := now.Add(time.Minute)
	orderbooks := &fakeOrderbookSnapshotRepository{
		snapshots: []marketdata.OrderbookSnapshot{appOrderbookSnapshot(quoteTime, "100000", "100200")},
	}
	service := executionCycleService(
		now,
		record,
		[]domainpaper.OrderTicket{ticket},
		fills,
		positions,
		&fakePositionCloseRepository{},
		&fakeEquityEventRepository{},
		orderbooks,
	)

	got, err := service.RunPaperExecutionCycle(context.Background(), apppaper.RunPaperExecutionCycleRequest{
		ValidationID:      record.ValidationID,
		Symbol:            "BTCUSDT",
		Interval:          "1",
		Liquidity:         backtest.LiquidityTaker,
		Costs:             marketExecutionCosts(t),
		AsOf:              quoteTime.Add(time.Second),
		MaxStaleness:      30 * time.Second,
		MaxSpreadBPS:      decimal.RequireFromString("250"),
		PendingScanLimit:  10,
		PositionScanLimit: 10,
		QuoteScanLimit:    10,
	})
	if err != nil {
		t.Fatalf("run entry paper cycle: %v", err)
	}

	if got.Action != apppaper.PaperExecutionCycleActionEntry || !got.Entry.QuoteSourced ||
		got.Entry.Position.PositionID != ticket.TicketID+"_position" {
		t.Fatalf("entry cycle mismatch: %#v", got)
	}
	if got.PendingTickets != 1 || !got.EntryChecked || fills.calls != 1 || positions.calls != 1 {
		t.Fatalf("entry counters mismatch: got=%#v fill_calls=%d position_calls=%d", got, fills.calls, positions.calls)
	}
	if len(orderbooks.queries) != 1 {
		t.Fatalf("entry-only cycle should source one quote, got %#v", orderbooks.queries)
	}
}

func TestServiceRunPaperExecutionCycleSkipsWhenNoOpenOrPending(t *testing.T) {
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	record := testValidationRecord(t, "research_app_0001", now.Add(-2*time.Hour), domainpaper.ValidationStatusRunning)
	fills := &fakeOrderFillRepository{}
	orderbooks := &fakeOrderbookSnapshotRepository{err: errors.New("must not be called")}
	service := executionCycleService(
		now,
		record,
		nil,
		fills,
		&fakeOpenPositionRepository{},
		&fakePositionCloseRepository{},
		&fakeEquityEventRepository{},
		orderbooks,
	)

	got, err := service.RunPaperExecutionCycle(context.Background(), apppaper.RunPaperExecutionCycleRequest{
		ValidationID:      record.ValidationID,
		Symbol:            "BTCUSDT",
		Interval:          "1",
		Liquidity:         backtest.LiquidityTaker,
		Costs:             marketExecutionCosts(t),
		AsOf:              now.Add(time.Minute),
		MaxStaleness:      30 * time.Second,
		MaxSpreadBPS:      decimal.RequireFromString("250"),
		PendingScanLimit:  10,
		PositionScanLimit: 10,
	})
	if err != nil {
		t.Fatalf("run empty paper cycle: %v", err)
	}

	if got.Action != apppaper.PaperExecutionCycleActionNone || got.SkipReason != apppaper.PaperExecutionCycleSkipNoOpenOrPending ||
		!got.EntryChecked || got.PendingTickets != 0 || len(orderbooks.queries) != 0 {
		t.Fatalf("empty cycle mismatch: %#v quote_queries=%#v", got, orderbooks.queries)
	}
}

func TestServiceRunPaperExecutionCycleRecoversExistingCloseAccountingBeforeEntry(t *testing.T) {
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	position := appOpenPosition(now)
	close := appPositionClose(now)
	ticket := appOrderTicket(now)
	record := testValidationRecord(t, "research_app_0001", now.Add(-2*time.Hour), domainpaper.ValidationStatusRunning)
	record.ValidationID = position.ValidationID
	fills := &fakeOrderFillRepository{}
	equity := &fakeEquityEventRepository{stats: domainpaper.EquityEventStats{Inserted: 1}}
	orderbooks := &fakeOrderbookSnapshotRepository{err: errors.New("must not be called")}
	service := executionCycleService(
		now,
		record,
		[]domainpaper.OrderTicket{ticket},
		fills,
		&fakeOpenPositionRepository{positions: []domainpaper.OpenPosition{position}},
		&fakePositionCloseRepository{closes: []domainpaper.PositionClose{close}},
		equity,
		orderbooks,
	)

	got, err := service.RunPaperExecutionCycle(context.Background(), apppaper.RunPaperExecutionCycleRequest{
		ValidationID:      record.ValidationID,
		Symbol:            "BTCUSDT",
		Interval:          "1",
		Liquidity:         backtest.LiquidityTaker,
		Costs:             marketExecutionCosts(t),
		AsOf:              now.Add(4 * time.Minute),
		MaxStaleness:      30 * time.Second,
		MaxSpreadBPS:      decimal.RequireFromString("250"),
		PendingScanLimit:  10,
		PositionScanLimit: 10,
	})
	if err != nil {
		t.Fatalf("run recovery paper cycle: %v", err)
	}

	if got.Action != apppaper.PaperExecutionCycleActionExit || !got.Exit.UsedExistingClose || !got.Exit.ExitTriggered {
		t.Fatalf("recovery cycle mismatch: %#v", got)
	}
	if got.EntryChecked || fills.calls != 0 || equity.calls != 1 || len(orderbooks.queries) != 0 {
		t.Fatalf("recovery must not enter or quote: got=%#v fill_calls=%d equity_calls=%d quote_queries=%#v", got, fills.calls, equity.calls, orderbooks.queries)
	}
}

func TestServiceRunPaperExecutionCycleNormalizesExplicitScopeBeforeScanning(t *testing.T) {
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	record := testValidationRecord(t, "research_app_0001", now.Add(-2*time.Hour), domainpaper.ValidationStatusRunning)
	fills := &fakeOrderFillRepository{}
	positions := &fakeOpenPositionRepository{}
	tickets := &fakeOrderTicketRepository{}
	service := apppaper.NewService(
		&fakeRunRepository{},
		&fakeResultRepository{},
		apppaper.WithValidationRecordRepository(&fakeValidationRecordRepository{records: []domainpaper.ValidationRecord{record}}),
		apppaper.WithOrderTicketRepository(tickets),
		apppaper.WithOrderFillRepository(fills),
		apppaper.WithOpenPositionRepository(positions),
		apppaper.WithPositionCloseRepository(&fakePositionCloseRepository{}),
		apppaper.WithEquityEventRepository(&fakeEquityEventRepository{}),
		apppaper.WithClock(clock.FixedClock{Time: now.Add(5 * time.Minute)}),
	)

	got, err := service.RunPaperExecutionCycle(context.Background(), apppaper.RunPaperExecutionCycleRequest{
		ValidationID:      " " + record.ValidationID + " ",
		Symbol:            " btcusdt ",
		Interval:          " 1 ",
		Liquidity:         backtest.LiquidityTaker,
		Costs:             marketExecutionCosts(t),
		AsOf:              now.Add(time.Minute),
		MaxStaleness:      30 * time.Second,
		MaxSpreadBPS:      decimal.RequireFromString("250"),
		PendingScanLimit:  10,
		PositionScanLimit: 10,
		QuoteScanLimit:    10,
	})
	if err != nil {
		t.Fatalf("run normalized paper cycle: %v", err)
	}

	if got.Action != apppaper.PaperExecutionCycleActionNone || got.SkipReason != apppaper.PaperExecutionCycleSkipNoOpenOrPending {
		t.Fatalf("normalized empty cycle mismatch: %#v", got)
	}
	if len(positions.queries) != 1 || positions.queries[0].ValidationID != record.ValidationID ||
		positions.queries[0].Symbol != "BTCUSDT" || positions.queries[0].Interval != "1" {
		t.Fatalf("position query scope mismatch: %#v", positions.queries)
	}
	if len(tickets.queries) != 1 || tickets.queries[0].ValidationID != record.ValidationID ||
		tickets.queries[0].Symbol != "BTCUSDT" || tickets.queries[0].Interval != "1" {
		t.Fatalf("ticket query scope mismatch: %#v", tickets.queries)
	}
	if fills.calls != 0 {
		t.Fatalf("empty normalized cycle must not write fills: calls=%d", fills.calls)
	}
}

func TestServiceRunPaperExecutionCycleRejectsUnsafeInputsTableDriven(t *testing.T) {
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	position := appOpenPosition(now)
	ticket := appOrderTicket(now)
	record := testValidationRecord(t, "research_app_0001", now.Add(-2*time.Hour), domainpaper.ValidationStatusRunning)
	record.ValidationID = position.ValidationID
	repositoryErr := errors.New("postgres unavailable")
	validReq := apppaper.RunPaperExecutionCycleRequest{
		ValidationID:      record.ValidationID,
		Symbol:            "BTCUSDT",
		Interval:          "1",
		Liquidity:         backtest.LiquidityTaker,
		Costs:             marketExecutionCosts(t),
		AsOf:              now.Add(2 * time.Minute),
		MaxStaleness:      30 * time.Second,
		MaxSpreadBPS:      decimal.RequireFromString("250"),
		PendingScanLimit:  10,
		PositionScanLimit: 10,
		QuoteScanLimit:    10,
	}

	tests := []struct {
		name             string
		tickets          []domainpaper.OrderTicket
		fills            *fakeOrderFillRepository
		positions        *fakeOpenPositionRepository
		closes           *fakePositionCloseRepository
		equity           *fakeEquityEventRepository
		orderbooks       *fakeOrderbookSnapshotRepository
		req              apppaper.RunPaperExecutionCycleRequest
		wantErrSub       string
		wantFillCalls    int
		wantCloseCalls   int
		wantEquityCalls  int
		wantQuoteQueries int
		wantNoQueries    bool
	}{
		{
			name:       "missing validation id",
			tickets:    []domainpaper.OrderTicket{ticket},
			fills:      &fakeOrderFillRepository{},
			positions:  &fakeOpenPositionRepository{},
			closes:     &fakePositionCloseRepository{},
			equity:     &fakeEquityEventRepository{},
			orderbooks: &fakeOrderbookSnapshotRepository{},
			req: func() apppaper.RunPaperExecutionCycleRequest {
				req := validReq
				req.ValidationID = ""
				return req
			}(),
			wantErrSub:    "validation_id",
			wantNoQueries: true,
		},
		{
			name:       "missing symbol",
			tickets:    []domainpaper.OrderTicket{ticket},
			fills:      &fakeOrderFillRepository{},
			positions:  &fakeOpenPositionRepository{},
			closes:     &fakePositionCloseRepository{},
			equity:     &fakeEquityEventRepository{},
			orderbooks: &fakeOrderbookSnapshotRepository{},
			req: func() apppaper.RunPaperExecutionCycleRequest {
				req := validReq
				req.Symbol = ""
				return req
			}(),
			wantErrSub:    "symbol",
			wantNoQueries: true,
		},
		{
			name:       "missing interval",
			tickets:    []domainpaper.OrderTicket{ticket},
			fills:      &fakeOrderFillRepository{},
			positions:  &fakeOpenPositionRepository{},
			closes:     &fakePositionCloseRepository{},
			equity:     &fakeEquityEventRepository{},
			orderbooks: &fakeOrderbookSnapshotRepository{},
			req: func() apppaper.RunPaperExecutionCycleRequest {
				req := validReq
				req.Interval = ""
				return req
			}(),
			wantErrSub:    "interval",
			wantNoQueries: true,
		},
		{
			name:       "negative pending scan limit",
			tickets:    []domainpaper.OrderTicket{ticket},
			fills:      &fakeOrderFillRepository{},
			positions:  &fakeOpenPositionRepository{},
			closes:     &fakePositionCloseRepository{},
			equity:     &fakeEquityEventRepository{},
			orderbooks: &fakeOrderbookSnapshotRepository{},
			req: func() apppaper.RunPaperExecutionCycleRequest {
				req := validReq
				req.PendingScanLimit = -1
				return req
			}(),
			wantErrSub:    "pending_scan_limit",
			wantNoQueries: true,
		},
		{
			name:       "negative position scan limit",
			tickets:    []domainpaper.OrderTicket{ticket},
			fills:      &fakeOrderFillRepository{},
			positions:  &fakeOpenPositionRepository{},
			closes:     &fakePositionCloseRepository{},
			equity:     &fakeEquityEventRepository{},
			orderbooks: &fakeOrderbookSnapshotRepository{},
			req: func() apppaper.RunPaperExecutionCycleRequest {
				req := validReq
				req.PositionScanLimit = -1
				return req
			}(),
			wantErrSub:    "position_scan_limit",
			wantNoQueries: true,
		},
		{
			name:       "negative quote scan limit",
			tickets:    []domainpaper.OrderTicket{ticket},
			fills:      &fakeOrderFillRepository{},
			positions:  &fakeOpenPositionRepository{},
			closes:     &fakePositionCloseRepository{},
			equity:     &fakeEquityEventRepository{},
			orderbooks: &fakeOrderbookSnapshotRepository{},
			req: func() apppaper.RunPaperExecutionCycleRequest {
				req := validReq
				req.QuoteScanLimit = -1
				return req
			}(),
			wantErrSub:    "quote_scan_limit",
			wantNoQueries: true,
		},
		{
			name:             "exit quote failure stops before entry",
			tickets:          []domainpaper.OrderTicket{ticket},
			fills:            &fakeOrderFillRepository{},
			positions:        &fakeOpenPositionRepository{positions: []domainpaper.OpenPosition{position}},
			closes:           &fakePositionCloseRepository{},
			equity:           &fakeEquityEventRepository{},
			orderbooks:       &fakeOrderbookSnapshotRepository{err: repositoryErr},
			req:              validReq,
			wantErrSub:       repositoryErr.Error(),
			wantQuoteQueries: 1,
		},
		{
			name:             "entry quote failure does not write fill or position",
			tickets:          []domainpaper.OrderTicket{ticket},
			fills:            &fakeOrderFillRepository{},
			positions:        &fakeOpenPositionRepository{},
			closes:           &fakePositionCloseRepository{},
			equity:           &fakeEquityEventRepository{},
			orderbooks:       &fakeOrderbookSnapshotRepository{err: repositoryErr},
			req:              validReq,
			wantErrSub:       repositoryErr.Error(),
			wantQuoteQueries: 1,
		},
		{
			name:       "bad costs fail before entry write",
			tickets:    []domainpaper.OrderTicket{ticket},
			fills:      &fakeOrderFillRepository{},
			positions:  &fakeOpenPositionRepository{},
			closes:     &fakePositionCloseRepository{},
			equity:     &fakeEquityEventRepository{},
			orderbooks: &fakeOrderbookSnapshotRepository{snapshots: []marketdata.OrderbookSnapshot{appOrderbookSnapshot(now.Add(time.Minute), "100000", "100200")}},
			req: func() apppaper.RunPaperExecutionCycleRequest {
				req := validReq
				req.Costs.TakerFeeBPS = decimal.RequireFromString("-1")
				req.AsOf = now.Add(time.Minute + time.Second)
				return req
			}(),
			wantErrSub:       "taker_fee_bps",
			wantQuoteQueries: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := executionCycleService(now, record, tt.tickets, tt.fills, tt.positions, tt.closes, tt.equity, tt.orderbooks)

			_, err := service.RunPaperExecutionCycle(context.Background(), tt.req)
			if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
			if tt.fills.calls != tt.wantFillCalls {
				t.Fatalf("fill calls mismatch: got %d want %d", tt.fills.calls, tt.wantFillCalls)
			}
			if tt.closes.calls != tt.wantCloseCalls {
				t.Fatalf("close calls mismatch: got %d want %d", tt.closes.calls, tt.wantCloseCalls)
			}
			if tt.equity.calls != tt.wantEquityCalls {
				t.Fatalf("equity calls mismatch: got %d want %d", tt.equity.calls, tt.wantEquityCalls)
			}
			if len(tt.orderbooks.queries) != tt.wantQuoteQueries {
				t.Fatalf("quote query count mismatch: got %d want %d queries=%#v", len(tt.orderbooks.queries), tt.wantQuoteQueries, tt.orderbooks.queries)
			}
			if tt.wantNoQueries && (len(tt.positions.queries) != 0 || len(tt.orderbooks.queries) != 0 ||
				len(tt.fills.queries) != 0 || len(tt.closes.queries) != 0 || len(tt.equity.queries) != 0) {
				t.Fatalf(
					"expected fail-fast before repository queries: positions=%#v orderbooks=%#v fills=%#v closes=%#v equity=%#v",
					tt.positions.queries,
					tt.orderbooks.queries,
					tt.fills.queries,
					tt.closes.queries,
					tt.equity.queries,
				)
			}
		})
	}
}

func executionCycleService(
	now time.Time,
	record domainpaper.ValidationRecord,
	tickets []domainpaper.OrderTicket,
	fills *fakeOrderFillRepository,
	positions *fakeOpenPositionRepository,
	closes *fakePositionCloseRepository,
	equity *fakeEquityEventRepository,
	orderbooks *fakeOrderbookSnapshotRepository,
) *apppaper.Service {
	options := []apppaper.Option{
		apppaper.WithValidationRecordRepository(&fakeValidationRecordRepository{records: []domainpaper.ValidationRecord{record}}),
		apppaper.WithOrderTicketRepository(&fakeOrderTicketRepository{tickets: tickets}),
		apppaper.WithOrderFillRepository(fills),
		apppaper.WithOpenPositionRepository(positions),
		apppaper.WithPositionCloseRepository(closes),
		apppaper.WithEquityEventRepository(equity),
		apppaper.WithClock(clock.FixedClock{Time: now.Add(5 * time.Minute)}),
	}
	if orderbooks != nil {
		options = append(options, apppaper.WithOrderbookSnapshotRepository(orderbooks))
	}
	return apppaper.NewService(&fakeRunRepository{}, &fakeResultRepository{}, options...)
}
