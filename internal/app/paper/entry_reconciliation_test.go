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
	domainpaper "github.com/VersoIt/Inquisitor/internal/paper"
)

func TestServiceReconcileTicketFillAtMarketRecordsFillAndOpenPosition(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	ticket := appOrderTicket(now)
	record := testValidationRecord(t, "research_app_0001", now.Add(-2*time.Hour), domainpaper.ValidationStatusRunning)
	record.ValidationID = ticket.ValidationID
	fills := &fakeOrderFillRepository{stats: domainpaper.OrderFillStats{Inserted: 1}}
	positions := &fakeOpenPositionRepository{stats: domainpaper.OpenPositionStats{Inserted: 1}}
	service := entryReconciliationService(now, record, []domainpaper.OrderTicket{ticket}, fills, positions)

	got, err := service.ReconcileTicketFillAtMarket(context.Background(), apppaper.ReconcileTicketFillAtMarketRequest{
		FillID:     "paper_fill_app_0001",
		PositionID: "paper_position_app_0001",
		TicketID:   ticket.TicketID,
		Liquidity:  backtest.LiquidityTaker,
		MidPrice:   decimal.RequireFromString("100000"),
		Costs:      marketExecutionCosts(t),
		FilledAt:   now.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("reconcile ticket fill: %v", err)
	}

	if got.Record.ValidationID != record.ValidationID || got.Ticket.TicketID != ticket.TicketID {
		t.Fatalf("context mismatch: %#v", got)
	}
	if got.Fill.FillID != "paper_fill_app_0001" || got.Position.PositionID != "paper_position_app_0001" ||
		got.Position.FillID != got.Fill.FillID || got.Position.TicketID != ticket.TicketID {
		t.Fatalf("entry identity mismatch: %#v", got)
	}
	if !got.Fill.ExecutedPrice.Equal(decimal.RequireFromString("100050")) ||
		!got.Position.EntryPrice.Equal(got.Fill.ExecutedPrice) ||
		!got.Position.EntryFee.Equal(got.Fill.Fee) ||
		!got.Position.OpenRisk.Equal(decimal.RequireFromString("525")) {
		t.Fatalf("entry accounting mismatch: fill=%#v position=%#v", got.Fill, got.Position)
	}
	if got.FillStats.Inserted != 1 || got.PositionStats.Inserted != 1 || fills.calls != 1 || positions.calls != 1 {
		t.Fatalf("repository stats mismatch: fill_stats=%#v position_stats=%#v fill_calls=%d position_calls=%d", got.FillStats, got.PositionStats, fills.calls, positions.calls)
	}
}

func TestServiceReconcileTicketFillAtMarketCompletesOpenAfterIdempotentFill(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	ticket := appOrderTicket(now)
	fill := appOrderFill(now)
	record := testValidationRecord(t, "research_app_0001", now.Add(-2*time.Hour), domainpaper.ValidationStatusRunning)
	record.ValidationID = ticket.ValidationID
	fills := &fakeOrderFillRepository{
		fills: []domainpaper.OrderFill{fill},
		stats: domainpaper.OrderFillStats{Skipped: 1},
	}
	positions := &fakeOpenPositionRepository{stats: domainpaper.OpenPositionStats{Inserted: 1}}
	service := entryReconciliationService(now, record, []domainpaper.OrderTicket{ticket}, fills, positions)

	got, err := service.ReconcileTicketFillAtMarket(context.Background(), apppaper.ReconcileTicketFillAtMarketRequest{
		FillID:     fill.FillID,
		PositionID: "paper_position_app_0001",
		TicketID:   ticket.TicketID,
		Liquidity:  backtest.LiquidityTaker,
		MidPrice:   fill.MidPrice,
		Costs:      marketExecutionCosts(t),
		FilledAt:   fill.FilledAt,
	})
	if err != nil {
		t.Fatalf("reconcile ticket fill retry: %v", err)
	}

	if got.FillStats.Skipped != 1 || got.PositionStats.Inserted != 1 || got.Position.FillID != fill.FillID {
		t.Fatalf("retry reconciliation mismatch: %#v", got)
	}
}

func TestServiceReconcileTicketFillAtMarketRequiresPositionRepositoryBeforeFill(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	ticket := appOrderTicket(now)
	record := testValidationRecord(t, "research_app_0001", now.Add(-2*time.Hour), domainpaper.ValidationStatusRunning)
	record.ValidationID = ticket.ValidationID
	fills := &fakeOrderFillRepository{stats: domainpaper.OrderFillStats{Inserted: 1}}
	service := apppaper.NewService(
		&fakeRunRepository{},
		&fakeResultRepository{},
		apppaper.WithValidationRecordRepository(&fakeValidationRecordRepository{records: []domainpaper.ValidationRecord{record}}),
		apppaper.WithOrderTicketRepository(&fakeOrderTicketRepository{tickets: []domainpaper.OrderTicket{ticket}}),
		apppaper.WithOrderFillRepository(fills),
		apppaper.WithClock(clock.FixedClock{Time: now.Add(3 * time.Minute)}),
	)

	_, err := service.ReconcileTicketFillAtMarket(context.Background(), apppaper.ReconcileTicketFillAtMarketRequest{
		FillID:     "paper_fill_app_0001",
		PositionID: "paper_position_app_0001",
		TicketID:   ticket.TicketID,
		Liquidity:  backtest.LiquidityTaker,
		MidPrice:   decimal.RequireFromString("100000"),
		Costs:      marketExecutionCosts(t),
		FilledAt:   now.Add(time.Minute),
	})
	if err == nil || !strings.Contains(err.Error(), "open position repository") {
		t.Fatalf("expected open position repository error, got %v", err)
	}
	if fills.calls != 0 {
		t.Fatalf("missing position repository must not write fill, calls=%d", fills.calls)
	}
}

func TestServiceReconcileTicketFillAtMarketRejectsUnsafeInputsTableDriven(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	ticket := appOrderTicket(now)
	record := testValidationRecord(t, "research_app_0001", now.Add(-2*time.Hour), domainpaper.ValidationStatusRunning)
	record.ValidationID = ticket.ValidationID
	repositoryErr := errors.New("postgres unavailable")
	validReq := apppaper.ReconcileTicketFillAtMarketRequest{
		FillID:     "paper_fill_app_0001",
		PositionID: "paper_position_app_0001",
		TicketID:   ticket.TicketID,
		Liquidity:  backtest.LiquidityTaker,
		MidPrice:   decimal.RequireFromString("100000"),
		Costs:      marketExecutionCosts(t),
		FilledAt:   now.Add(time.Minute),
	}

	tests := []struct {
		name              string
		tickets           []domainpaper.OrderTicket
		fills             *fakeOrderFillRepository
		positions         *fakeOpenPositionRepository
		req               apppaper.ReconcileTicketFillAtMarketRequest
		wantErrSub        string
		wantFillCalls     int
		wantPositionCalls int
	}{
		{
			name:              "missing position id does not write fill",
			tickets:           []domainpaper.OrderTicket{ticket},
			fills:             &fakeOrderFillRepository{},
			positions:         &fakeOpenPositionRepository{},
			req:               func() apppaper.ReconcileTicketFillAtMarketRequest { req := validReq; req.PositionID = ""; return req }(),
			wantErrSub:        "position_id",
			wantFillCalls:     0,
			wantPositionCalls: 0,
		},
		{
			name:              "fill failure does not open position",
			tickets:           nil,
			fills:             &fakeOrderFillRepository{},
			positions:         &fakeOpenPositionRepository{},
			req:               validReq,
			wantErrSub:        "not found",
			wantFillCalls:     0,
			wantPositionCalls: 0,
		},
		{
			name:              "position failure is surfaced after fill write",
			tickets:           []domainpaper.OrderTicket{ticket},
			fills:             &fakeOrderFillRepository{stats: domainpaper.OrderFillStats{Inserted: 1}},
			positions:         &fakeOpenPositionRepository{err: repositoryErr},
			req:               validReq,
			wantErrSub:        repositoryErr.Error(),
			wantFillCalls:     1,
			wantPositionCalls: 0,
		},
		{
			name:      "bad market costs do not write fill",
			tickets:   []domainpaper.OrderTicket{ticket},
			fills:     &fakeOrderFillRepository{},
			positions: &fakeOpenPositionRepository{},
			req: func() apppaper.ReconcileTicketFillAtMarketRequest {
				req := validReq
				req.Costs.TakerFeeBPS = decimal.RequireFromString("-1")
				return req
			}(),
			wantErrSub:        "taker_fee_bps",
			wantFillCalls:     0,
			wantPositionCalls: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := entryReconciliationService(now, record, tt.tickets, tt.fills, tt.positions)

			_, err := service.ReconcileTicketFillAtMarket(context.Background(), tt.req)
			if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
			if tt.fills.calls != tt.wantFillCalls {
				t.Fatalf("fill calls mismatch: got %d want %d", tt.fills.calls, tt.wantFillCalls)
			}
			if tt.positions.calls != tt.wantPositionCalls {
				t.Fatalf("position calls mismatch: got %d want %d", tt.positions.calls, tt.wantPositionCalls)
			}
		})
	}
}

func entryReconciliationService(
	now time.Time,
	record domainpaper.ValidationRecord,
	tickets []domainpaper.OrderTicket,
	fills *fakeOrderFillRepository,
	positions *fakeOpenPositionRepository,
) *apppaper.Service {
	return apppaper.NewService(
		&fakeRunRepository{},
		&fakeResultRepository{},
		apppaper.WithValidationRecordRepository(&fakeValidationRecordRepository{records: []domainpaper.ValidationRecord{record}}),
		apppaper.WithOrderTicketRepository(&fakeOrderTicketRepository{tickets: tickets}),
		apppaper.WithOrderFillRepository(fills),
		apppaper.WithOpenPositionRepository(positions),
		apppaper.WithClock(clock.FixedClock{Time: now.Add(3 * time.Minute)}),
	)
}
