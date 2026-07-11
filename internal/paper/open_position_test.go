package paper_test

import (
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/VersoIt/Inquisitor/internal/paper"
)

func TestNewOpenPositionCopiesTicketFillAndComputesActualOpenRisk(t *testing.T) {
	ticket := validOrderTicket()
	fill := validOrderFill()
	recordedAt := fill.RecordedAt.Add(time.Second)

	got, err := paper.NewOpenPosition(paper.OpenPositionInput{
		PositionID: " paper_position_0001 ",
		Ticket:     ticket,
		Fill:       fill,
		RecordedAt: recordedAt,
	})
	if err != nil {
		t.Fatalf("new open position: %v", err)
	}

	if got.PositionID != "paper_position_0001" || got.FillID != fill.FillID || got.TicketID != ticket.TicketID ||
		got.ValidationID != ticket.ValidationID || got.DecisionID != ticket.DecisionID || got.IntentID != ticket.IntentID {
		t.Fatalf("identity mismatch: %#v", got)
	}
	if got.Exchange != ticket.Exchange || got.Category != ticket.Category || got.Symbol != ticket.Symbol ||
		got.Interval != ticket.Interval || got.Side != ticket.Side {
		t.Fatalf("market scope mismatch: %#v", got)
	}
	if !got.EntryPrice.Equal(fill.ExecutedPrice) || !got.EntryNotional.Equal(fill.Notional) || !got.EntryFee.Equal(fill.Fee) {
		t.Fatalf("entry fill values mismatch: %#v", got)
	}
	if !got.PlannedMaxLoss.Equal(decimal.RequireFromString("500")) || !got.OpenRisk.Equal(decimal.RequireFromString("525")) {
		t.Fatalf("risk values mismatch: planned=%s open=%s", got.PlannedMaxLoss, got.OpenRisk)
	}
	if got.OpenedAt.Location() != time.UTC || got.RecordedAt.Location() != time.UTC {
		t.Fatalf("timestamps must be UTC: opened=%s recorded=%s", got.OpenedAt, got.RecordedAt)
	}
}

func TestValidateOpenPositionAcceptsShortGeometry(t *testing.T) {
	position := validOpenPosition()
	position.Side = paper.OrderSideShort
	position.EntryPrice = decimal.RequireFromString("99950")
	position.EntryNotional = position.EntryPrice.Mul(position.Quantity)
	position.StopLoss = decimal.RequireFromString("101000")
	position.TakeProfit = decimal.RequireFromString("98000")
	position.PlannedMaxLoss = decimal.RequireFromString("500")
	position.OpenRisk = position.StopLoss.Sub(position.EntryPrice).Mul(position.Quantity)

	if err := paper.ValidateOpenPosition(position); err != nil {
		t.Fatalf("validate short open position: %v", err)
	}
}

func TestNewOpenPositionRejectsMismatchedFillAndTicketTableDriven(t *testing.T) {
	ticket := validOrderTicket()
	valid := validOrderFill()
	tests := []struct {
		name       string
		mutate     func(*paper.OrderFill)
		wantErrSub string
	}{
		{"ticket id mismatch", func(f *paper.OrderFill) { f.TicketID = "paper_ticket_other" }, "ticket_id"},
		{"validation id mismatch", func(f *paper.OrderFill) { f.ValidationID = "paper_validation_other" }, "validation_id"},
		{"decision id mismatch", func(f *paper.OrderFill) { f.DecisionID = "risk_decision_other" }, "decision_id"},
		{"intent id mismatch", func(f *paper.OrderFill) { f.IntentID = "risk_intent_other" }, "intent_id"},
		{"market scope mismatch", func(f *paper.OrderFill) { f.Symbol = "ETHUSDT" }, "market scope"},
		{"quantity mismatch", func(f *paper.OrderFill) {
			f.Quantity = decimal.RequireFromString("0.4")
			f.Notional = f.ExecutedPrice.Mul(f.Quantity)
			f.Fee = f.Notional.Mul(f.FeeBPS).Div(decimal.RequireFromString("10000"))
		}, "quantity"},
		{"fill before ticket", func(f *paper.OrderFill) {
			f.FilledAt = ticket.CreatedAt.Add(-time.Nanosecond)
			f.RecordedAt = ticket.CreatedAt.Add(time.Second)
		}, "filled_at"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fill := valid
			tt.mutate(&fill)

			_, err := paper.NewOpenPosition(paper.OpenPositionInput{
				PositionID: "paper_position_0001",
				Ticket:     ticket,
				Fill:       fill,
				RecordedAt: fill.RecordedAt.Add(time.Second),
			})
			if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
		})
	}
}

func TestValidateOpenPositionRejectsInvalidInputsTableDriven(t *testing.T) {
	valid := validOpenPosition()
	tests := []struct {
		name       string
		mutate     func(*paper.OpenPosition)
		wantErrSub string
	}{
		{"missing position id", func(p *paper.OpenPosition) { p.PositionID = " " }, "position_id"},
		{"missing fill id", func(p *paper.OpenPosition) { p.FillID = "" }, "fill_id"},
		{"missing ticket id", func(p *paper.OpenPosition) { p.TicketID = "" }, "ticket_id"},
		{"uppercase exchange", func(p *paper.OpenPosition) { p.Exchange = "BYBIT" }, "exchange"},
		{"lowercase symbol", func(p *paper.OpenPosition) { p.Symbol = "btcusdt" }, "symbol"},
		{"unsupported interval", func(p *paper.OpenPosition) { p.Interval = "2" }, "interval"},
		{"unknown side", func(p *paper.OpenPosition) { p.Side = "BUY" }, "side"},
		{"zero quantity", func(p *paper.OpenPosition) { p.Quantity = decimal.Zero }, "quantity"},
		{"zero entry price", func(p *paper.OpenPosition) { p.EntryPrice = decimal.Zero }, "entry_price"},
		{"entry notional mismatch", func(p *paper.OpenPosition) { p.EntryNotional = decimal.RequireFromString("1") }, "entry_notional"},
		{"negative entry fee", func(p *paper.OpenPosition) { p.EntryFee = decimal.RequireFromString("-1") }, "entry_fee"},
		{"zero stop loss", func(p *paper.OpenPosition) { p.StopLoss = decimal.Zero }, "stop_loss"},
		{"negative take profit", func(p *paper.OpenPosition) { p.TakeProfit = decimal.RequireFromString("-1") }, "take_profit"},
		{"zero leverage", func(p *paper.OpenPosition) { p.Leverage = decimal.Zero }, "leverage"},
		{"zero planned max loss", func(p *paper.OpenPosition) { p.PlannedMaxLoss = decimal.Zero }, "planned_max_loss"},
		{"open risk mismatch", func(p *paper.OpenPosition) { p.OpenRisk = decimal.RequireFromString("1") }, "open_risk"},
		{"long stop above entry", func(p *paper.OpenPosition) {
			p.StopLoss = decimal.RequireFromString("101000")
			p.OpenRisk = p.EntryPrice.Sub(p.StopLoss).Abs().Mul(p.Quantity)
		}, "stop_loss"},
		{"long take profit below entry", func(p *paper.OpenPosition) { p.TakeProfit = decimal.RequireFromString("99000") }, "take_profit"},
		{"missing opened at", func(p *paper.OpenPosition) { p.OpenedAt = time.Time{} }, "opened_at"},
		{"recorded before opened", func(p *paper.OpenPosition) { p.RecordedAt = p.OpenedAt.Add(-time.Nanosecond) }, "recorded_at"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			position := valid
			tt.mutate(&position)

			err := paper.ValidateOpenPosition(position)
			if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
		})
	}
}

func TestValidateOpenPositionQueryRejectsInvalidInputsTableDriven(t *testing.T) {
	start := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name       string
		query      paper.OpenPositionQuery
		wantErrSub string
	}{
		{"valid empty query", paper.OpenPositionQuery{}, ""},
		{"valid filtered query", paper.OpenPositionQuery{Symbol: "BTCUSDT", Interval: "1", Start: start, End: start.Add(time.Hour), Limit: 10}, ""},
		{"lowercase symbol", paper.OpenPositionQuery{Symbol: "btcusdt"}, "symbol"},
		{"unsupported interval", paper.OpenPositionQuery{Interval: "2"}, "interval"},
		{"end before start", paper.OpenPositionQuery{Start: start, End: start}, "end"},
		{"negative limit", paper.OpenPositionQuery{Limit: -1}, "limit"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := paper.ValidateOpenPositionQuery(tt.query)
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

func validOpenPosition() paper.OpenPosition {
	ticket := validOrderTicket()
	fill := validOrderFill()
	return paper.OpenPosition{
		PositionID:     "paper_position_0001",
		FillID:         fill.FillID,
		TicketID:       ticket.TicketID,
		ValidationID:   ticket.ValidationID,
		DecisionID:     ticket.DecisionID,
		IntentID:       ticket.IntentID,
		Exchange:       ticket.Exchange,
		Category:       ticket.Category,
		Symbol:         ticket.Symbol,
		Interval:       ticket.Interval,
		Side:           ticket.Side,
		Quantity:       fill.Quantity,
		EntryPrice:     fill.ExecutedPrice,
		EntryNotional:  fill.Notional,
		EntryFee:       fill.Fee,
		StopLoss:       ticket.StopLoss,
		TakeProfit:     ticket.TakeProfit,
		Leverage:       ticket.Leverage,
		PlannedMaxLoss: ticket.MaxLoss,
		OpenRisk:       fill.ExecutedPrice.Sub(ticket.StopLoss).Abs().Mul(fill.Quantity),
		OpenedAt:       fill.FilledAt,
		RecordedAt:     fill.RecordedAt.Add(time.Second),
	}
}
