package paper_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	apppaper "github.com/VersoIt/Inquisitor/internal/app/paper"
	"github.com/VersoIt/Inquisitor/internal/backtest"
	"github.com/VersoIt/Inquisitor/internal/clock"
	domainpaper "github.com/VersoIt/Inquisitor/internal/paper"
)

func TestServiceSimulateOrderFillComputesConservativeEntry(t *testing.T) {
	now := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	ticket := appOrderTicket(now)
	record := testValidationRecord(t, "research_app_0001", now.Add(-2*time.Hour), domainpaper.ValidationStatusRunning)
	record.ValidationID = ticket.ValidationID
	fills := &fakeOrderFillRepository{stats: domainpaper.OrderFillStats{Inserted: 1}}
	service := orderFillService(now, record, []domainpaper.OrderTicket{ticket}, fills)

	got, err := service.SimulateOrderFill(context.Background(), apppaper.SimulateOrderFillRequest{
		FillID:    "paper_fill_app_0001",
		TicketID:  ticket.TicketID,
		Liquidity: backtest.LiquidityTaker,
		MidPrice:  decimal.RequireFromString("100000"),
		Costs:     marketExecutionCosts(t),
		FilledAt:  now.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("simulate order fill: %v", err)
	}

	if got.Fill.FillID != "paper_fill_app_0001" || got.Fill.TicketID != ticket.TicketID {
		t.Fatalf("fill identity mismatch: %#v", got.Fill)
	}
	if !got.Fill.ExecutedPrice.Equal(decimal.RequireFromString("100050")) ||
		!got.Fill.Notional.Equal(decimal.RequireFromString("50025")) ||
		!got.Fill.Fee.Equal(decimal.RequireFromString("30.015")) ||
		!got.Fill.FeeBPS.Equal(decimal.RequireFromString("6")) ||
		!got.Fill.SpreadBPS.Equal(decimal.RequireFromString("2")) ||
		!got.Fill.SlippageBPS.Equal(decimal.RequireFromString("3")) {
		t.Fatalf("fill execution mismatch: %#v", got.Fill)
	}
}

func TestServiceSimulateOrderFillUsesMakerFee(t *testing.T) {
	now := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	ticket := appOrderTicket(now)
	record := testValidationRecord(t, "research_app_0001", now.Add(-2*time.Hour), domainpaper.ValidationStatusRunning)
	record.ValidationID = ticket.ValidationID
	service := orderFillService(now, record, []domainpaper.OrderTicket{ticket}, &fakeOrderFillRepository{stats: domainpaper.OrderFillStats{Inserted: 1}})

	got, err := service.SimulateOrderFill(context.Background(), apppaper.SimulateOrderFillRequest{
		FillID:    "paper_fill_app_0001",
		TicketID:  ticket.TicketID,
		Liquidity: backtest.LiquidityMaker,
		MidPrice:  decimal.RequireFromString("100000"),
		Costs:     marketExecutionCosts(t),
		FilledAt:  now.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("simulate maker order fill: %v", err)
	}

	if got.Fill.Liquidity != backtest.LiquidityMaker || !got.Fill.FeeBPS.Equal(decimal.RequireFromString("2")) ||
		!got.Fill.Fee.Equal(decimal.RequireFromString("10.005")) {
		t.Fatalf("maker fee mismatch: %#v", got.Fill)
	}
}

func TestServiceSimulateOrderFillScopesTicketLookupToValidation(t *testing.T) {
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	ticket := appOrderTicket(now)
	record := testValidationRecord(t, "research_app_0001", now.Add(-2*time.Hour), domainpaper.ValidationStatusRunning)
	record.ValidationID = ticket.ValidationID
	foreignTicket := ticket
	foreignTicket.ValidationID = "paper_validation_foreign_0001"
	tickets := &fakeOrderTicketRepository{tickets: []domainpaper.OrderTicket{foreignTicket, ticket}}
	fills := &fakeOrderFillRepository{stats: domainpaper.OrderFillStats{Inserted: 1}}
	service := apppaper.NewService(
		&fakeRunRepository{},
		&fakeResultRepository{},
		apppaper.WithValidationRecordRepository(&fakeValidationRecordRepository{records: []domainpaper.ValidationRecord{record}}),
		apppaper.WithOrderTicketRepository(tickets),
		apppaper.WithOrderFillRepository(fills),
		apppaper.WithKillSwitchRepository(&fakePaperKillSwitchRepository{}),
		apppaper.WithClock(clock.FixedClock{Time: now.Add(2 * time.Minute)}),
	)

	got, err := service.SimulateOrderFill(context.Background(), apppaper.SimulateOrderFillRequest{
		FillID:       "paper_fill_app_0001",
		TicketID:     ticket.TicketID,
		ValidationID: " " + ticket.ValidationID + " ",
		Liquidity:    backtest.LiquidityTaker,
		MidPrice:     decimal.RequireFromString("100000"),
		Costs:        marketExecutionCosts(t),
		FilledAt:     now.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("simulate order fill with foreign ticket present: %v", err)
	}

	if got.Ticket.ValidationID != record.ValidationID || got.Fill.ValidationID != record.ValidationID || got.Stats.Inserted != 1 {
		t.Fatalf("scoped simulated fill mismatch: %#v", got)
	}
	if len(tickets.queries) != 2 {
		t.Fatalf("expected simulate and record ticket lookups, got %#v", tickets.queries)
	}
	for index, query := range tickets.queries {
		if query.ValidationID != record.ValidationID || query.TicketID != ticket.TicketID || query.Limit != 2 {
			t.Fatalf("ticket lookup[%d] must be scoped to validation: %#v", index, query)
		}
	}
}

func TestServiceSettlePositionAtMarketComputesExitAndAccountsEquity(t *testing.T) {
	now := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	position := appOpenPosition(now)
	record := testValidationRecord(t, "research_app_0001", now.Add(-2*time.Hour), domainpaper.ValidationStatusRunning)
	record.ValidationID = position.ValidationID
	service := settlePositionCloseService(
		now,
		record,
		[]domainpaper.OpenPosition{position},
		&fakePositionCloseRepository{stats: domainpaper.PositionCloseStats{Inserted: 1}},
		&fakeEquityEventRepository{stats: domainpaper.EquityEventStats{Inserted: 1}},
	)

	got, err := service.SettlePositionAtMarket(context.Background(), apppaper.SettlePositionAtMarketRequest{
		EventID:      "paper_equity_app_0001",
		CloseID:      "paper_close_app_0001",
		PositionID:   position.PositionID,
		Liquidity:    backtest.LiquidityTaker,
		ExitMidPrice: decimal.RequireFromString("101000"),
		Costs:        marketExecutionCosts(t),
		CloseReason:  domainpaper.PositionCloseReasonTakeProfit,
		ClosedAt:     position.OpenedAt.Add(2 * time.Minute),
	})
	if err != nil {
		t.Fatalf("settle position at market: %v", err)
	}

	if !got.Close.ExitPrice.Equal(decimal.RequireFromString("100949.5")) ||
		!got.Close.ExitNotional.Equal(decimal.RequireFromString("50474.75")) ||
		!got.Close.ExitFee.Equal(decimal.RequireFromString("30.28485")) ||
		!got.Close.GrossPnL.Equal(decimal.RequireFromString("449.75")) ||
		!got.Close.NetPnL.Equal(decimal.RequireFromString("389.45015")) {
		t.Fatalf("close execution mismatch: %#v", got.Close)
	}
	if !got.Event.EquityBefore.Equal(record.InitialBalance) ||
		!got.Event.EquityAfter.Equal(decimal.RequireFromString("1389.45015")) {
		t.Fatalf("equity accounting mismatch: %#v", got.Event)
	}
}

func TestMarketExecutionRejectsUnsafeInputsTableDriven(t *testing.T) {
	now := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	ticket := appOrderTicket(now)
	position := appOpenPosition(now)
	record := testValidationRecord(t, "research_app_0001", now.Add(-2*time.Hour), domainpaper.ValidationStatusRunning)
	record.ValidationID = ticket.ValidationID
	badCosts := marketExecutionCosts(t)
	badCosts.TakerFeeBPS = decimal.RequireFromString("-1")

	tests := []struct {
		name       string
		run        func() error
		wantErrSub string
	}{
		{
			name: "simulate fill rejects missing ticket id",
			run: func() error {
				service := orderFillService(now, record, []domainpaper.OrderTicket{ticket}, &fakeOrderFillRepository{})
				_, err := service.SimulateOrderFill(context.Background(), apppaper.SimulateOrderFillRequest{Costs: marketExecutionCosts(t)})
				return err
			},
			wantErrSub: "ticket_id",
		},
		{
			name: "simulate fill rejects bad costs",
			run: func() error {
				service := orderFillService(now, record, []domainpaper.OrderTicket{ticket}, &fakeOrderFillRepository{})
				_, err := service.SimulateOrderFill(context.Background(), apppaper.SimulateOrderFillRequest{
					FillID:    "paper_fill_app_0001",
					TicketID:  ticket.TicketID,
					Liquidity: backtest.LiquidityTaker,
					MidPrice:  decimal.RequireFromString("100000"),
					Costs:     badCosts,
					FilledAt:  now.Add(time.Minute),
				})
				return err
			},
			wantErrSub: "taker_fee_bps",
		},
		{
			name: "market settlement rejects missing position",
			run: func() error {
				service := settlePositionCloseService(now, record, nil, &fakePositionCloseRepository{}, &fakeEquityEventRepository{})
				_, err := service.SettlePositionAtMarket(context.Background(), apppaper.SettlePositionAtMarketRequest{
					EventID:      "paper_equity_app_0001",
					CloseID:      "paper_close_app_0001",
					PositionID:   position.PositionID,
					Liquidity:    backtest.LiquidityTaker,
					ExitMidPrice: decimal.RequireFromString("101000"),
					Costs:        marketExecutionCosts(t),
					CloseReason:  domainpaper.PositionCloseReasonTakeProfit,
					ClosedAt:     position.OpenedAt.Add(2 * time.Minute),
				})
				return err
			},
			wantErrSub: "not found",
		},
		{
			name: "market settlement rejects bad costs before close write",
			run: func() error {
				closes := &fakePositionCloseRepository{}
				service := settlePositionCloseService(now, record, []domainpaper.OpenPosition{position}, closes, &fakeEquityEventRepository{})
				_, err := service.SettlePositionAtMarket(context.Background(), apppaper.SettlePositionAtMarketRequest{
					EventID:      "paper_equity_app_0001",
					CloseID:      "paper_close_app_0001",
					PositionID:   position.PositionID,
					Liquidity:    backtest.LiquidityTaker,
					ExitMidPrice: decimal.RequireFromString("101000"),
					Costs:        badCosts,
					CloseReason:  domainpaper.PositionCloseReasonTakeProfit,
					ClosedAt:     position.OpenedAt.Add(2 * time.Minute),
				})
				if closes.calls != 0 {
					t.Fatalf("bad costs must not write close, calls=%d", closes.calls)
				}
				return err
			},
			wantErrSub: "taker_fee_bps",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.run()
			if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
		})
	}
}

func marketExecutionCosts(t *testing.T) backtest.CostModel {
	t.Helper()

	costs, err := backtest.NewCostModel(2, 6, 4, 3, 1)
	if err != nil {
		t.Fatalf("new market execution costs: %v", err)
	}
	return costs
}
