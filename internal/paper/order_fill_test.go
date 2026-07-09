package paper_test

import (
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/VersoIt/Inquisitor/internal/backtest"
	"github.com/VersoIt/Inquisitor/internal/paper"
)

func TestNewOrderFillCopiesTicketAndNormalizesExecution(t *testing.T) {
	ticket := validOrderTicket()
	filledAt := ticket.CreatedAt.Add(time.Minute)
	recordedAt := filledAt.Add(time.Second)

	got, err := paper.NewOrderFill(paper.OrderFillInput{
		FillID:        " paper_fill_0001 ",
		Ticket:        ticket,
		Liquidity:     " taker ",
		MidPrice:      decimal.RequireFromString("100000"),
		ExecutedPrice: decimal.RequireFromString("100050"),
		Fee:           decimal.RequireFromString("30.015"),
		FeeBPS:        decimal.RequireFromString("6"),
		SpreadBPS:     decimal.RequireFromString("2"),
		SlippageBPS:   decimal.RequireFromString("3"),
		FilledAt:      filledAt,
		RecordedAt:    recordedAt,
	})
	if err != nil {
		t.Fatalf("new order fill: %v", err)
	}

	if got.FillID != "paper_fill_0001" || got.TicketID != ticket.TicketID || got.ValidationID != ticket.ValidationID ||
		got.DecisionID != ticket.DecisionID || got.IntentID != ticket.IntentID {
		t.Fatalf("identity not copied from ticket: %#v", got)
	}
	if got.Exchange != ticket.Exchange || got.Category != ticket.Category || got.Symbol != ticket.Symbol ||
		got.Interval != ticket.Interval || got.Side != ticket.Side || got.Liquidity != backtest.LiquidityTaker {
		t.Fatalf("market scope not normalized/copied: %#v", got)
	}
	if !got.Quantity.Equal(ticket.Quantity) || !got.Notional.Equal(decimal.RequireFromString("50025")) {
		t.Fatalf("quantity/notional mismatch: quantity=%s notional=%s", got.Quantity, got.Notional)
	}
	if got.FilledAt.Location() != time.UTC || got.RecordedAt.Location() != time.UTC {
		t.Fatalf("timestamps must be UTC: filled=%s recorded=%s", got.FilledAt, got.RecordedAt)
	}
}

func TestValidateOrderFillAcceptsConservativeShortEntry(t *testing.T) {
	fill := validOrderFill()
	fill.Side = paper.OrderSideShort
	fill.MidPrice = decimal.RequireFromString("100000")
	fill.ExecutedPrice = decimal.RequireFromString("99950")
	fill.Notional = fill.ExecutedPrice.Mul(fill.Quantity)
	fill.Fee = fill.Notional.Mul(fill.FeeBPS).Div(decimal.RequireFromString("10000"))

	if err := paper.ValidateOrderFill(fill); err != nil {
		t.Fatalf("validate short order fill: %v", err)
	}
}

func TestValidateOrderFillRejectsInvalidInputsTableDriven(t *testing.T) {
	valid := validOrderFill()
	tests := []struct {
		name       string
		mutate     func(*paper.OrderFill)
		wantErrSub string
	}{
		{"missing fill id", func(f *paper.OrderFill) { f.FillID = " " }, "fill_id"},
		{"missing ticket id", func(f *paper.OrderFill) { f.TicketID = "" }, "ticket_id"},
		{"uppercase exchange", func(f *paper.OrderFill) { f.Exchange = "BYBIT" }, "exchange"},
		{"lowercase symbol", func(f *paper.OrderFill) { f.Symbol = "btcusdt" }, "symbol"},
		{"unsupported interval", func(f *paper.OrderFill) { f.Interval = "2" }, "interval"},
		{"unknown side", func(f *paper.OrderFill) { f.Side = "BUY" }, "side"},
		{"unknown liquidity", func(f *paper.OrderFill) { f.Liquidity = "POST_ONLY" }, "liquidity"},
		{"zero mid price", func(f *paper.OrderFill) { f.MidPrice = decimal.Zero }, "mid_price"},
		{"zero executed price", func(f *paper.OrderFill) { f.ExecutedPrice = decimal.Zero }, "executed_price"},
		{"zero quantity", func(f *paper.OrderFill) { f.Quantity = decimal.Zero }, "quantity"},
		{"notional mismatch", func(f *paper.OrderFill) { f.Notional = decimal.RequireFromString("1") }, "notional"},
		{"negative fee", func(f *paper.OrderFill) { f.Fee = decimal.RequireFromString("-1") }, "fee"},
		{"negative fee bps", func(f *paper.OrderFill) { f.FeeBPS = decimal.RequireFromString("-1") }, "fee_bps"},
		{"fee math mismatch", func(f *paper.OrderFill) { f.Fee = decimal.RequireFromString("1") }, "fee"},
		{"negative spread bps", func(f *paper.OrderFill) { f.SpreadBPS = decimal.RequireFromString("-1") }, "spread_bps"},
		{"negative slippage bps", func(f *paper.OrderFill) { f.SlippageBPS = decimal.RequireFromString("-1") }, "slippage_bps"},
		{"long improved versus mid", func(f *paper.OrderFill) {
			f.ExecutedPrice = decimal.RequireFromString("99950")
			f.Notional = f.ExecutedPrice.Mul(f.Quantity)
			f.Fee = f.Notional.Mul(f.FeeBPS).Div(decimal.RequireFromString("10000"))
		}, "LONG"},
		{"short improved versus mid", func(f *paper.OrderFill) {
			f.Side = paper.OrderSideShort
			f.ExecutedPrice = decimal.RequireFromString("100050")
			f.Notional = f.ExecutedPrice.Mul(f.Quantity)
			f.Fee = f.Notional.Mul(f.FeeBPS).Div(decimal.RequireFromString("10000"))
		}, "SHORT"},
		{"missing filled at", func(f *paper.OrderFill) { f.FilledAt = time.Time{} }, "filled_at"},
		{"recorded before fill", func(f *paper.OrderFill) { f.RecordedAt = f.FilledAt.Add(-time.Nanosecond) }, "recorded_at"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fill := valid
			tt.mutate(&fill)

			err := paper.ValidateOrderFill(fill)
			if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
		})
	}
}

func TestNewOrderFillRejectsFillBeforeTicketCreation(t *testing.T) {
	ticket := validOrderTicket()

	_, err := paper.NewOrderFill(paper.OrderFillInput{
		FillID:        "paper_fill_0001",
		Ticket:        ticket,
		Liquidity:     backtest.LiquidityTaker,
		MidPrice:      decimal.RequireFromString("100000"),
		ExecutedPrice: decimal.RequireFromString("100050"),
		Fee:           decimal.RequireFromString("30.015"),
		FeeBPS:        decimal.RequireFromString("6"),
		SpreadBPS:     decimal.RequireFromString("2"),
		SlippageBPS:   decimal.RequireFromString("3"),
		FilledAt:      ticket.CreatedAt.Add(-time.Nanosecond),
		RecordedAt:    ticket.CreatedAt.Add(time.Second),
	})
	if err == nil || !strings.Contains(err.Error(), "ticket created_at") {
		t.Fatalf("expected ticket creation time error, got %v", err)
	}
}

func TestValidateOrderFillQueryRejectsInvalidInputsTableDriven(t *testing.T) {
	start := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name       string
		query      paper.OrderFillQuery
		wantErrSub string
	}{
		{"valid empty query", paper.OrderFillQuery{}, ""},
		{"valid filtered query", paper.OrderFillQuery{Symbol: "BTCUSDT", Interval: "1", Start: start, End: start.Add(time.Hour), Limit: 10}, ""},
		{"lowercase symbol", paper.OrderFillQuery{Symbol: "btcusdt"}, "symbol"},
		{"unsupported interval", paper.OrderFillQuery{Interval: "2"}, "interval"},
		{"end before start", paper.OrderFillQuery{Start: start, End: start}, "end"},
		{"negative limit", paper.OrderFillQuery{Limit: -1}, "limit"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := paper.ValidateOrderFillQuery(tt.query)
			if tt.wantErrSub == "" {
				if err != nil {
					t.Fatalf("validate query: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
		})
	}
}

func validOrderFill() paper.OrderFill {
	ticket := validOrderTicket()
	filledAt := ticket.CreatedAt.Add(time.Minute)
	return paper.OrderFill{
		FillID:        "paper_fill_0001",
		TicketID:      ticket.TicketID,
		ValidationID:  ticket.ValidationID,
		DecisionID:    ticket.DecisionID,
		IntentID:      ticket.IntentID,
		Exchange:      ticket.Exchange,
		Category:      ticket.Category,
		Symbol:        ticket.Symbol,
		Interval:      ticket.Interval,
		Side:          ticket.Side,
		Liquidity:     backtest.LiquidityTaker,
		MidPrice:      decimal.RequireFromString("100000"),
		ExecutedPrice: decimal.RequireFromString("100050"),
		Quantity:      ticket.Quantity,
		Notional:      decimal.RequireFromString("50025"),
		Fee:           decimal.RequireFromString("30.015"),
		FeeBPS:        decimal.RequireFromString("6"),
		SpreadBPS:     decimal.RequireFromString("2"),
		SlippageBPS:   decimal.RequireFromString("3"),
		FilledAt:      filledAt,
		RecordedAt:    filledAt.Add(time.Second),
	}
}
