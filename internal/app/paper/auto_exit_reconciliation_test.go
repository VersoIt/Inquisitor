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

func TestServiceReconcilePaperExitWithQuoteSettlesLongTakeProfit(t *testing.T) {
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	position := appOpenPosition(now)
	record := testValidationRecord(t, "research_app_0001", now.Add(-2*time.Hour), domainpaper.ValidationStatusRunning)
	record.ValidationID = position.ValidationID
	quoteTime := now.Add(4 * time.Minute)
	closes := &fakePositionCloseRepository{stats: domainpaper.PositionCloseStats{Inserted: 1}}
	equity := &fakeEquityEventRepository{stats: domainpaper.EquityEventStats{Inserted: 1}}
	orderbooks := &fakeOrderbookSnapshotRepository{
		snapshots: []marketdata.OrderbookSnapshot{appOrderbookSnapshot(quoteTime, "101900", "102100")},
	}
	service := autoExitReconciliationService(now, record, []domainpaper.OpenPosition{position}, closes, equity, orderbooks)

	got, err := service.ReconcilePaperExitWithQuote(context.Background(), apppaper.ReconcilePaperExitWithQuoteRequest{
		ValidationID:      position.ValidationID,
		Symbol:            "BTCUSDT",
		Interval:          "1",
		Liquidity:         backtest.LiquidityTaker,
		Costs:             marketExecutionCosts(t),
		AsOf:              quoteTime.Add(2 * time.Second),
		MaxStaleness:      30 * time.Second,
		MaxSpreadBPS:      decimal.RequireFromString("250"),
		PositionScanLimit: 10,
		QuoteScanLimit:    20,
	})
	if err != nil {
		t.Fatalf("reconcile paper exit with quote: %v", err)
	}

	if !got.PositionFound || !got.QuoteSourced || !got.ExitTriggered || got.UsedExistingClose {
		t.Fatalf("exit flags mismatch: %#v", got)
	}
	if got.CloseReason != domainpaper.PositionCloseReasonTakeProfit || got.Close.CloseReason != domainpaper.PositionCloseReasonTakeProfit {
		t.Fatalf("close reason mismatch: %#v", got)
	}
	if got.Close.CloseID != position.PositionID+"_close" || got.Event.EventID != position.PositionID+"_close_equity" {
		t.Fatalf("default ids mismatch: close=%q event=%q", got.Close.CloseID, got.Event.EventID)
	}
	if !got.Close.ExitMidPrice.Equal(got.Quote.MidPrice) || !got.Close.ExitPrice.Equal(decimal.RequireFromString("101949")) ||
		!got.Event.EquityAfter.Equal(decimal.RequireFromString("1888.9003")) {
		t.Fatalf("settlement accounting mismatch: quote=%#v close=%#v event=%#v", got.Quote, got.Close, got.Event)
	}
	if got.ScannedPositions != 1 || got.CheckedPositions != 1 || got.ClosedPositions != 0 {
		t.Fatalf("scan counters mismatch: %#v", got)
	}
	if closes.calls != 1 || equity.calls != 1 || got.CloseStats.Inserted != 1 || got.EquityStats.Inserted != 1 {
		t.Fatalf("repository stats mismatch: close_calls=%d equity_calls=%d close_stats=%#v equity_stats=%#v", closes.calls, equity.calls, got.CloseStats, got.EquityStats)
	}
	if len(orderbooks.queries) != 1 || orderbooks.queries[0].Limit != 20 {
		t.Fatalf("quote query mismatch: %#v", orderbooks.queries)
	}
}

func TestServiceReconcilePaperExitWithQuoteSettlesShortStopLoss(t *testing.T) {
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	position := appShortOpenPosition(t, now)
	record := testValidationRecord(t, "research_app_0001", now.Add(-2*time.Hour), domainpaper.ValidationStatusRunning)
	record.ValidationID = position.ValidationID
	quoteTime := now.Add(4 * time.Minute)
	service := autoExitReconciliationService(
		now,
		record,
		[]domainpaper.OpenPosition{position},
		&fakePositionCloseRepository{stats: domainpaper.PositionCloseStats{Inserted: 1}},
		&fakeEquityEventRepository{stats: domainpaper.EquityEventStats{Inserted: 1}},
		&fakeOrderbookSnapshotRepository{snapshots: []marketdata.OrderbookSnapshot{appOrderbookSnapshot(quoteTime, "100900", "101100")}},
	)

	got, err := service.ReconcilePaperExitWithQuote(context.Background(), apppaper.ReconcilePaperExitWithQuoteRequest{
		ValidationID:      position.ValidationID,
		PositionID:        position.PositionID,
		Liquidity:         backtest.LiquidityTaker,
		Costs:             marketExecutionCosts(t),
		AsOf:              quoteTime.Add(time.Second),
		MaxStaleness:      30 * time.Second,
		MaxSpreadBPS:      decimal.RequireFromString("250"),
		PositionScanLimit: 10,
		QuoteScanLimit:    10,
	})
	if err != nil {
		t.Fatalf("reconcile short stop-loss exit: %v", err)
	}

	if !got.ExitTriggered || got.CloseReason != domainpaper.PositionCloseReasonStopLoss ||
		!got.Close.ExitPrice.GreaterThan(got.Close.ExitMidPrice) || !got.Close.NetPnL.IsNegative() {
		t.Fatalf("short stop-loss settlement mismatch: %#v", got)
	}
}

func TestServiceReconcilePaperExitWithQuoteSkipsWhenNoExitTrigger(t *testing.T) {
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	position := appOpenPosition(now)
	record := testValidationRecord(t, "research_app_0001", now.Add(-2*time.Hour), domainpaper.ValidationStatusRunning)
	record.ValidationID = position.ValidationID
	quoteTime := now.Add(2 * time.Minute)
	closes := &fakePositionCloseRepository{}
	equity := &fakeEquityEventRepository{}
	orderbooks := &fakeOrderbookSnapshotRepository{
		snapshots: []marketdata.OrderbookSnapshot{appOrderbookSnapshot(quoteTime, "100400", "100600")},
	}
	service := autoExitReconciliationService(now, record, []domainpaper.OpenPosition{position}, closes, equity, orderbooks)

	got, err := service.ReconcilePaperExitWithQuote(context.Background(), apppaper.ReconcilePaperExitWithQuoteRequest{
		ValidationID:      position.ValidationID,
		PositionID:        position.PositionID,
		Liquidity:         backtest.LiquidityTaker,
		Costs:             marketExecutionCosts(t),
		AsOf:              quoteTime.Add(time.Second),
		MaxStaleness:      30 * time.Second,
		MaxSpreadBPS:      decimal.RequireFromString("250"),
		PositionScanLimit: 10,
		QuoteScanLimit:    10,
	})
	if err != nil {
		t.Fatalf("reconcile no-trigger exit: %v", err)
	}

	if !got.PositionFound || !got.QuoteSourced || got.ExitTriggered || got.Close.CloseID != "" || got.Event.EventID != "" {
		t.Fatalf("no-trigger result mismatch: %#v", got)
	}
	if closes.calls != 0 || equity.calls != 0 {
		t.Fatalf("no trigger must not write close/equity: close_calls=%d equity_calls=%d", closes.calls, equity.calls)
	}
	if len(closes.queries) != 1 || closes.queries[0].ValidationID != record.ValidationID ||
		closes.queries[0].PositionID != position.PositionID || closes.queries[0].Limit != 2 {
		t.Fatalf("explicit exit close lookup must be scoped to validation: %#v", closes.queries)
	}
}

func TestServiceReconcilePaperExitWithQuoteScopesExplicitPositionLookupToValidation(t *testing.T) {
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	position := appOpenPosition(now)
	record := testValidationRecord(t, "research_app_0001", now.Add(-2*time.Hour), domainpaper.ValidationStatusRunning)
	record.ValidationID = position.ValidationID
	foreignPosition := position
	foreignPosition.ValidationID = "paper_validation_foreign_0001"
	positions := &fakeOpenPositionRepository{positions: []domainpaper.OpenPosition{foreignPosition, position}}
	closes := &fakePositionCloseRepository{}
	equity := &fakeEquityEventRepository{}
	quoteTime := now.Add(2 * time.Minute)
	service := apppaper.NewService(
		&fakeRunRepository{},
		&fakeResultRepository{},
		apppaper.WithValidationRecordRepository(&fakeValidationRecordRepository{records: []domainpaper.ValidationRecord{record}}),
		apppaper.WithOpenPositionRepository(positions),
		apppaper.WithPositionCloseRepository(closes),
		apppaper.WithEquityEventRepository(equity),
		apppaper.WithOrderbookSnapshotRepository(&fakeOrderbookSnapshotRepository{
			snapshots: []marketdata.OrderbookSnapshot{appOrderbookSnapshot(quoteTime, "100400", "100600")},
		}),
		apppaper.WithClock(clock.FixedClock{Time: now.Add(5 * time.Minute)}),
	)

	got, err := service.ReconcilePaperExitWithQuote(context.Background(), apppaper.ReconcilePaperExitWithQuoteRequest{
		ValidationID:      " " + record.ValidationID + " ",
		PositionID:        position.PositionID,
		Liquidity:         backtest.LiquidityTaker,
		Costs:             marketExecutionCosts(t),
		AsOf:              quoteTime.Add(time.Second),
		MaxStaleness:      30 * time.Second,
		MaxSpreadBPS:      decimal.RequireFromString("250"),
		PositionScanLimit: 10,
		QuoteScanLimit:    10,
	})
	if err != nil {
		t.Fatalf("reconcile explicit exit with foreign position present: %v", err)
	}

	if !got.PositionFound || got.Position.ValidationID != record.ValidationID || got.ExitTriggered {
		t.Fatalf("scoped explicit exit result mismatch: %#v", got)
	}
	if len(positions.queries) != 1 {
		t.Fatalf("expected one explicit position lookup, got %#v", positions.queries)
	}
	if positions.queries[0].ValidationID != record.ValidationID || positions.queries[0].PositionID != position.PositionID || positions.queries[0].Limit != 2 {
		t.Fatalf("position lookup must be scoped to validation: %#v", positions.queries[0])
	}
	if closes.calls != 0 || equity.calls != 0 {
		t.Fatalf("no trigger must not write close/equity: close_calls=%d equity_calls=%d", closes.calls, equity.calls)
	}
}

func TestServiceReconcilePaperExitWithQuoteSkipsClosedPositionsAndSettlesNextOpen(t *testing.T) {
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	closedPosition := appOpenPosition(now)
	openPosition := appOpenPosition(now)
	openPosition.PositionID = "paper_position_app_0002"
	openPosition.FillID = "paper_fill_app_0002"
	openPosition.TicketID = "paper_ticket_app_0002"
	record := testValidationRecord(t, "research_app_0001", now.Add(-2*time.Hour), domainpaper.ValidationStatusRunning)
	record.ValidationID = closedPosition.ValidationID
	quoteTime := now.Add(4 * time.Minute)
	close := appPositionClose(now)
	close.PositionID = closedPosition.PositionID
	closes := &fakePositionCloseRepository{
		closes: []domainpaper.PositionClose{close},
		stats:  domainpaper.PositionCloseStats{Inserted: 1},
	}
	equity := &fakeEquityEventRepository{
		events: []domainpaper.EquityEvent{appEquityEvent(now)},
		stats:  domainpaper.EquityEventStats{Inserted: 1},
	}
	orderbooks := &fakeOrderbookSnapshotRepository{
		snapshots: []marketdata.OrderbookSnapshot{appOrderbookSnapshot(quoteTime, "101900", "102100")},
	}
	service := autoExitReconciliationService(now, record, []domainpaper.OpenPosition{closedPosition, openPosition}, closes, equity, orderbooks)

	got, err := service.ReconcilePaperExitWithQuote(context.Background(), apppaper.ReconcilePaperExitWithQuoteRequest{
		ValidationID:      record.ValidationID,
		Liquidity:         backtest.LiquidityTaker,
		Costs:             marketExecutionCosts(t),
		AsOf:              quoteTime.Add(time.Second),
		MaxStaleness:      30 * time.Second,
		MaxSpreadBPS:      decimal.RequireFromString("250"),
		PositionScanLimit: 10,
		QuoteScanLimit:    10,
	})
	if err != nil {
		t.Fatalf("reconcile scanned exit: %v", err)
	}

	if !got.ExitTriggered || got.Position.PositionID != openPosition.PositionID ||
		got.ScannedPositions != 2 || got.ClosedPositions != 1 || got.CheckedPositions != 1 {
		t.Fatalf("scanned exit mismatch: %#v", got)
	}
}

func TestServiceReconcilePaperExitWithQuoteScopesExistingCloseLookupToValidation(t *testing.T) {
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	position := appOpenPosition(now)
	record := testValidationRecord(t, "research_app_0001", now.Add(-2*time.Hour), domainpaper.ValidationStatusRunning)
	record.ValidationID = position.ValidationID
	foreignClose := appPositionClose(now)
	foreignClose.CloseID = "paper_close_foreign_0001"
	foreignClose.ValidationID = "paper_validation_foreign_0001"
	foreignClose.PositionID = position.PositionID
	quoteTime := now.Add(4 * time.Minute)
	closes := &fakePositionCloseRepository{
		closes: []domainpaper.PositionClose{foreignClose},
		stats:  domainpaper.PositionCloseStats{Inserted: 1},
	}
	equity := &fakeEquityEventRepository{stats: domainpaper.EquityEventStats{Inserted: 1}}
	orderbooks := &fakeOrderbookSnapshotRepository{
		snapshots: []marketdata.OrderbookSnapshot{appOrderbookSnapshot(quoteTime, "101900", "102100")},
	}
	service := autoExitReconciliationService(now, record, []domainpaper.OpenPosition{position}, closes, equity, orderbooks)

	got, err := service.ReconcilePaperExitWithQuote(context.Background(), apppaper.ReconcilePaperExitWithQuoteRequest{
		ValidationID:      position.ValidationID,
		Liquidity:         backtest.LiquidityTaker,
		Costs:             marketExecutionCosts(t),
		AsOf:              quoteTime.Add(time.Second),
		MaxStaleness:      30 * time.Second,
		MaxSpreadBPS:      decimal.RequireFromString("250"),
		PositionScanLimit: 10,
		QuoteScanLimit:    10,
	})
	if err != nil {
		t.Fatalf("reconcile paper exit with foreign close present: %v", err)
	}

	if !got.ExitTriggered || got.UsedExistingClose || got.Close.ValidationID != record.ValidationID ||
		got.Close.CloseID == foreignClose.CloseID {
		t.Fatalf("scoped exit result mismatch: %#v", got)
	}
	if got.ScannedPositions != 1 || got.CheckedPositions != 1 || got.ClosedPositions != 0 {
		t.Fatalf("scoped exit counters mismatch: %#v", got)
	}
	if len(closes.queries) < 2 {
		t.Fatalf("expected existing-close and close-position lookups, got %#v", closes.queries)
	}
	for index, query := range closes.queries[:2] {
		if query.ValidationID != record.ValidationID || query.PositionID != position.PositionID || query.Limit != 2 {
			t.Fatalf("close lookup[%d] must be scoped to validation: %#v", index, query)
		}
	}
}

func TestServiceReconcilePaperExitWithQuoteAccountsScannedExistingCloseWithoutQuote(t *testing.T) {
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	position := appOpenPosition(now)
	close := appPositionClose(now)
	record := testValidationRecord(t, "research_app_0001", now.Add(-2*time.Hour), domainpaper.ValidationStatusRunning)
	record.ValidationID = position.ValidationID
	orderbooks := &fakeOrderbookSnapshotRepository{err: errors.New("must not be called")}
	equity := &fakeEquityEventRepository{stats: domainpaper.EquityEventStats{Inserted: 1}}
	service := autoExitReconciliationService(
		now,
		record,
		[]domainpaper.OpenPosition{position},
		&fakePositionCloseRepository{closes: []domainpaper.PositionClose{close}},
		equity,
		orderbooks,
	)

	got, err := service.ReconcilePaperExitWithQuote(context.Background(), apppaper.ReconcilePaperExitWithQuoteRequest{
		ValidationID: position.ValidationID,
		CloseID:      close.CloseID,
		Liquidity:    backtest.LiquidityTaker,
		Costs:        marketExecutionCosts(t),
		AsOf:         now.Add(3 * time.Minute),
		MaxStaleness: 30 * time.Second,
		MaxSpreadBPS: decimal.RequireFromString("250"),
	})
	if err != nil {
		t.Fatalf("account existing close: %v", err)
	}

	if !got.UsedExistingClose || got.QuoteSourced || !got.ExitTriggered || got.Close.CloseID != close.CloseID {
		t.Fatalf("existing close continuation mismatch: %#v", got)
	}
	if !got.PositionFound || got.ScannedPositions != 1 || got.ClosedPositions != 1 || got.CheckedPositions != 0 {
		t.Fatalf("existing close scan counters mismatch: %#v", got)
	}
	if got.Event.EventID != close.CloseID+"_equity" || equity.calls != 1 || got.EquityStats.Inserted != 1 {
		t.Fatalf("equity accounting mismatch: event=%#v stats=%#v calls=%d", got.Event, got.EquityStats, equity.calls)
	}
	if len(orderbooks.queries) != 0 {
		t.Fatalf("existing close retry must not source quote, got %#v", orderbooks.queries)
	}
}

func TestServiceReconcilePaperExitWithQuoteScopesDefaultEventIDLookupToValidation(t *testing.T) {
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	position := appOpenPosition(now)
	close := appPositionClose(now)
	record := testValidationRecord(t, "research_app_0001", now.Add(-2*time.Hour), domainpaper.ValidationStatusRunning)
	record.ValidationID = position.ValidationID
	foreignEvent := appEquityEvent(now)
	foreignEvent.EventID = "paper_equity_foreign_0001"
	foreignEvent.ValidationID = "paper_validation_foreign_0001"
	foreignEvent.CloseID = close.CloseID
	foreignEvent.PositionID = close.PositionID
	equity := &fakeEquityEventRepository{
		events: []domainpaper.EquityEvent{foreignEvent},
		stats:  domainpaper.EquityEventStats{Inserted: 1},
	}
	service := autoExitReconciliationService(
		now,
		record,
		[]domainpaper.OpenPosition{position},
		&fakePositionCloseRepository{closes: []domainpaper.PositionClose{close}},
		equity,
		&fakeOrderbookSnapshotRepository{err: errors.New("must not be called")},
	)

	got, err := service.ReconcilePaperExitWithQuote(context.Background(), apppaper.ReconcilePaperExitWithQuoteRequest{
		ValidationID: position.ValidationID,
		CloseID:      close.CloseID,
		Liquidity:    backtest.LiquidityTaker,
		Costs:        marketExecutionCosts(t),
		AsOf:         now.Add(3 * time.Minute),
		MaxStaleness: 30 * time.Second,
		MaxSpreadBPS: decimal.RequireFromString("250"),
	})
	if err != nil {
		t.Fatalf("account existing close with foreign equity event present: %v", err)
	}

	if !got.UsedExistingClose || got.Event.EventID != close.CloseID+"_equity" || got.Event.ValidationID != record.ValidationID {
		t.Fatalf("default event id must be scoped to validation: %#v", got)
	}
	if equity.calls != 1 || len(equity.queries) != 4 {
		t.Fatalf("equity repository call mismatch: calls=%d queries=%#v", equity.calls, equity.queries)
	}
	for index, query := range equity.queries[:3] {
		if query.ValidationID != record.ValidationID || query.CloseID != close.CloseID || query.Limit != 2 {
			t.Fatalf("existing event lookup[%d] must be scoped to validation: %#v", index, query)
		}
	}
	if equity.queries[3].ValidationID != record.ValidationID || equity.queries[3].CloseID != "" {
		t.Fatalf("ledger lookup must stay scoped to validation only: %#v", equity.queries[3])
	}
}

func TestServiceReconcilePaperExitWithQuoteSkipsWhenNoOpenPositions(t *testing.T) {
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	record := testValidationRecord(t, "research_app_0001", now.Add(-2*time.Hour), domainpaper.ValidationStatusRunning)
	service := autoExitReconciliationService(
		now,
		record,
		nil,
		&fakePositionCloseRepository{},
		&fakeEquityEventRepository{},
		&fakeOrderbookSnapshotRepository{err: errors.New("must not be called")},
	)

	got, err := service.ReconcilePaperExitWithQuote(context.Background(), apppaper.ReconcilePaperExitWithQuoteRequest{
		ValidationID:      record.ValidationID,
		Liquidity:         backtest.LiquidityTaker,
		Costs:             marketExecutionCosts(t),
		AsOf:              now.Add(time.Minute),
		MaxStaleness:      30 * time.Second,
		MaxSpreadBPS:      decimal.RequireFromString("250"),
		PositionScanLimit: 10,
	})
	if err != nil {
		t.Fatalf("reconcile empty exit scan: %v", err)
	}

	if got.PositionFound || got.QuoteSourced || got.ExitTriggered || got.ScannedPositions != 0 {
		t.Fatalf("empty scan mismatch: %#v", got)
	}
}

func TestServiceReconcilePaperExitWithQuoteRejectsUnsafeInputsTableDriven(t *testing.T) {
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	position := appOpenPosition(now)
	record := testValidationRecord(t, "research_app_0001", now.Add(-2*time.Hour), domainpaper.ValidationStatusRunning)
	record.ValidationID = position.ValidationID
	plannedRecord := record
	plannedRecord.Status = domainpaper.ValidationStatusPlanned
	plannedRecord.StartedAt = time.Time{}
	repositoryErr := errors.New("postgres unavailable")
	validReq := apppaper.ReconcilePaperExitWithQuoteRequest{
		ValidationID:      position.ValidationID,
		Liquidity:         backtest.LiquidityTaker,
		Costs:             marketExecutionCosts(t),
		AsOf:              now.Add(2 * time.Minute),
		MaxStaleness:      30 * time.Second,
		MaxSpreadBPS:      decimal.RequireFromString("250"),
		PositionScanLimit: 10,
		QuoteScanLimit:    10,
	}

	tests := []struct {
		name             string
		record           domainpaper.ValidationRecord
		positions        []domainpaper.OpenPosition
		closes           *fakePositionCloseRepository
		equity           *fakeEquityEventRepository
		orderbooks       *fakeOrderbookSnapshotRepository
		req              apppaper.ReconcilePaperExitWithQuoteRequest
		wantErrSub       string
		wantCloseCalls   int
		wantEquityCalls  int
		wantQuoteQueries int
	}{
		{
			name:       "missing validation id",
			record:     record,
			positions:  []domainpaper.OpenPosition{position},
			closes:     &fakePositionCloseRepository{},
			equity:     &fakeEquityEventRepository{},
			orderbooks: &fakeOrderbookSnapshotRepository{},
			req: func() apppaper.ReconcilePaperExitWithQuoteRequest {
				req := validReq
				req.ValidationID = ""
				return req
			}(),
			wantErrSub: "validation_id",
		},
		{
			name:       "negative position scan limit",
			record:     record,
			positions:  []domainpaper.OpenPosition{position},
			closes:     &fakePositionCloseRepository{},
			equity:     &fakeEquityEventRepository{},
			orderbooks: &fakeOrderbookSnapshotRepository{},
			req: func() apppaper.ReconcilePaperExitWithQuoteRequest {
				req := validReq
				req.PositionScanLimit = -1
				return req
			}(),
			wantErrSub: "position_scan_limit",
		},
		{
			name:       "validation is not running",
			record:     plannedRecord,
			positions:  []domainpaper.OpenPosition{position},
			closes:     &fakePositionCloseRepository{},
			equity:     &fakeEquityEventRepository{},
			orderbooks: &fakeOrderbookSnapshotRepository{},
			req:        validReq,
			wantErrSub: "RUNNING",
		},
		{
			name:   "explicit position outside validation is not found",
			record: record,
			positions: []domainpaper.OpenPosition{func() domainpaper.OpenPosition {
				other := position
				other.ValidationID = "paper_validation_other_0001"
				return other
			}()},
			closes:     &fakePositionCloseRepository{},
			equity:     &fakeEquityEventRepository{},
			orderbooks: &fakeOrderbookSnapshotRepository{},
			req: func() apppaper.ReconcilePaperExitWithQuoteRequest {
				req := validReq
				req.PositionID = position.PositionID
				return req
			}(),
			wantErrSub: "not found",
		},
		{
			name:      "existing close id mismatch",
			record:    record,
			positions: []domainpaper.OpenPosition{position},
			closes: &fakePositionCloseRepository{closes: []domainpaper.PositionClose{func() domainpaper.PositionClose {
				close := appPositionClose(now)
				close.CloseID = "paper_close_existing_0001"
				return close
			}()}},
			equity:     &fakeEquityEventRepository{},
			orderbooks: &fakeOrderbookSnapshotRepository{},
			req: func() apppaper.ReconcilePaperExitWithQuoteRequest {
				req := validReq
				req.PositionID = position.PositionID
				req.CloseID = "paper_close_requested_0001"
				return req
			}(),
			wantErrSub: "already has close",
		},
		{
			name:      "quote failure does not write close or equity",
			record:    record,
			positions: []domainpaper.OpenPosition{position},
			closes:    &fakePositionCloseRepository{},
			equity:    &fakeEquityEventRepository{},
			orderbooks: &fakeOrderbookSnapshotRepository{
				err: repositoryErr,
			},
			req:              validReq,
			wantErrSub:       repositoryErr.Error(),
			wantQuoteQueries: 1,
		},
		{
			name:       "missing orderbook repository for active position",
			record:     record,
			positions:  []domainpaper.OpenPosition{position},
			closes:     &fakePositionCloseRepository{},
			equity:     &fakeEquityEventRepository{},
			orderbooks: nil,
			req:        validReq,
			wantErrSub: "orderbook snapshot repository",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := autoExitReconciliationService(now, tt.record, tt.positions, tt.closes, tt.equity, tt.orderbooks)

			_, err := service.ReconcilePaperExitWithQuote(context.Background(), tt.req)
			if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
			if tt.closes.calls != tt.wantCloseCalls {
				t.Fatalf("close calls mismatch: got %d want %d", tt.closes.calls, tt.wantCloseCalls)
			}
			if tt.equity.calls != tt.wantEquityCalls {
				t.Fatalf("equity calls mismatch: got %d want %d", tt.equity.calls, tt.wantEquityCalls)
			}
			if tt.orderbooks != nil && len(tt.orderbooks.queries) != tt.wantQuoteQueries {
				t.Fatalf("quote query count mismatch: got %d want %d queries=%#v", len(tt.orderbooks.queries), tt.wantQuoteQueries, tt.orderbooks.queries)
			}
		})
	}
}

func autoExitReconciliationService(
	now time.Time,
	record domainpaper.ValidationRecord,
	positions []domainpaper.OpenPosition,
	closes *fakePositionCloseRepository,
	equity *fakeEquityEventRepository,
	orderbooks *fakeOrderbookSnapshotRepository,
) *apppaper.Service {
	options := []apppaper.Option{
		apppaper.WithValidationRecordRepository(&fakeValidationRecordRepository{records: []domainpaper.ValidationRecord{record}}),
		apppaper.WithOpenPositionRepository(&fakeOpenPositionRepository{positions: positions}),
		apppaper.WithPositionCloseRepository(closes),
		apppaper.WithEquityEventRepository(equity),
		apppaper.WithClock(clock.FixedClock{Time: now.Add(5 * time.Minute)}),
	}
	if orderbooks != nil {
		options = append(options, apppaper.WithOrderbookSnapshotRepository(orderbooks))
	}
	return apppaper.NewService(&fakeRunRepository{}, &fakeResultRepository{}, options...)
}

func appShortOpenPosition(t *testing.T, now time.Time) domainpaper.OpenPosition {
	t.Helper()

	position := domainpaper.OpenPosition{
		PositionID:     "paper_position_short_0001",
		FillID:         "paper_fill_short_0001",
		TicketID:       "paper_ticket_short_0001",
		ValidationID:   "paper_validation_app_0001",
		DecisionID:     "risk_decision_short_0001",
		IntentID:       "risk_intent_short_0001",
		Exchange:       "bybit",
		Category:       "linear",
		Symbol:         "BTCUSDT",
		Interval:       "1",
		Side:           domainpaper.OrderSideShort,
		Quantity:       decimal.RequireFromString("0.5"),
		EntryPrice:     decimal.RequireFromString("99950"),
		EntryNotional:  decimal.RequireFromString("49975"),
		EntryFee:       decimal.RequireFromString("29.985"),
		StopLoss:       decimal.RequireFromString("101000"),
		TakeProfit:     decimal.RequireFromString("98000"),
		Leverage:       decimal.RequireFromString("1"),
		PlannedMaxLoss: decimal.RequireFromString("500"),
		OpenRisk:       decimal.RequireFromString("525"),
		OpenedAt:       now.Add(time.Minute),
		RecordedAt:     now.Add(2 * time.Minute),
	}
	if err := domainpaper.ValidateOpenPosition(position); err != nil {
		t.Fatalf("validate short open position fixture: %v", err)
	}
	return position
}
