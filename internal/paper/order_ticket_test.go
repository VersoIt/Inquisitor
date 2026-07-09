package paper_test

import (
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/VersoIt/Inquisitor/internal/paper"
)

func TestNewOrderTicketNormalizesExecutableRiskDecision(t *testing.T) {
	createdAt := time.Date(2026, 7, 9, 12, 0, 0, 0, time.FixedZone("UTC+3", 3*60*60))
	got, err := paper.NewOrderTicket(paper.OrderTicketInput{
		TicketID:     " paper_ticket_0001 ",
		ValidationID: " paper_validation_0001 ",
		DecisionID:   " risk_decision_0001 ",
		IntentID:     " risk_intent_0001 ",
		Exchange:     " BYBIT ",
		Category:     " LINEAR ",
		Symbol:       " btcusdt ",
		Interval:     " 1 ",
		Side:         " long ",
		Quantity:     decimal.RequireFromString("0.5"),
		EntryPrice:   decimal.RequireFromString("100000"),
		StopLoss:     decimal.RequireFromString("99000"),
		TakeProfit:   decimal.RequireFromString("102000"),
		Leverage:     decimal.RequireFromString("1"),
		MaxLoss:      decimal.RequireFromString("500"),
		Confidence:   82,
		Reason:       " risk_checks_passed ",
		CreatedAt:    createdAt,
	})
	if err != nil {
		t.Fatalf("new order ticket: %v", err)
	}

	if got.TicketID != "paper_ticket_0001" || got.ValidationID != "paper_validation_0001" || got.DecisionID != "risk_decision_0001" {
		t.Fatalf("identity not normalized: %#v", got)
	}
	if got.Exchange != "bybit" || got.Category != "linear" || got.Symbol != "BTCUSDT" || got.Interval != "1" || got.Side != paper.OrderSideLong {
		t.Fatalf("market scope not normalized: %#v", got)
	}
	if !got.MaxLoss.Equal(decimal.RequireFromString("500")) || got.Confidence != 82 || got.Reason != "risk_checks_passed" {
		t.Fatalf("risk snapshot mismatch: %#v", got)
	}
	if got.CreatedAt.Location() != time.UTC {
		t.Fatalf("created_at must be UTC, got %s", got.CreatedAt)
	}
}

func TestValidateOrderTicketAcceptsShortGeometry(t *testing.T) {
	ticket := validOrderTicket()
	ticket.Side = paper.OrderSideShort
	ticket.StopLoss = decimal.RequireFromString("101000")
	ticket.TakeProfit = decimal.RequireFromString("98000")
	ticket.MaxLoss = decimal.RequireFromString("500")

	if err := paper.ValidateOrderTicket(ticket); err != nil {
		t.Fatalf("validate short order ticket: %v", err)
	}
}

func TestValidateOrderTicketRejectsInvalidInputsTableDriven(t *testing.T) {
	valid := validOrderTicket()
	tests := []struct {
		name       string
		mutate     func(*paper.OrderTicket)
		wantErrSub string
	}{
		{"missing ticket id", func(t *paper.OrderTicket) { t.TicketID = " " }, "ticket_id"},
		{"missing validation id", func(t *paper.OrderTicket) { t.ValidationID = "" }, "validation_id"},
		{"missing decision id", func(t *paper.OrderTicket) { t.DecisionID = "" }, "decision_id"},
		{"uppercase exchange", func(t *paper.OrderTicket) { t.Exchange = "BYBIT" }, "exchange"},
		{"lowercase symbol", func(t *paper.OrderTicket) { t.Symbol = "btcusdt" }, "symbol"},
		{"unsupported interval", func(t *paper.OrderTicket) { t.Interval = "2" }, "interval"},
		{"unknown side", func(t *paper.OrderTicket) { t.Side = "BUY" }, "side"},
		{"zero quantity", func(t *paper.OrderTicket) { t.Quantity = decimal.Zero }, "quantity"},
		{"zero entry price", func(t *paper.OrderTicket) { t.EntryPrice = decimal.Zero }, "entry_price"},
		{"zero stop loss", func(t *paper.OrderTicket) { t.StopLoss = decimal.Zero }, "stop_loss"},
		{"negative take profit", func(t *paper.OrderTicket) { t.TakeProfit = decimal.RequireFromString("-1") }, "take_profit"},
		{"zero leverage", func(t *paper.OrderTicket) { t.Leverage = decimal.Zero }, "leverage"},
		{"zero max loss", func(t *paper.OrderTicket) { t.MaxLoss = decimal.Zero }, "max_loss"},
		{"invalid confidence", func(t *paper.OrderTicket) { t.Confidence = 101 }, "confidence"},
		{"missing reason", func(t *paper.OrderTicket) { t.Reason = "" }, "reason"},
		{"missing created at", func(t *paper.OrderTicket) { t.CreatedAt = time.Time{} }, "created_at"},
		{"long stop above entry", func(t *paper.OrderTicket) { t.StopLoss = decimal.RequireFromString("101000") }, "stop_loss"},
		{"long take profit below entry", func(t *paper.OrderTicket) { t.TakeProfit = decimal.RequireFromString("99000") }, "take_profit"},
		{"max loss mismatch", func(t *paper.OrderTicket) { t.MaxLoss = decimal.RequireFromString("499") }, "max_loss"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ticket := valid
			tt.mutate(&ticket)

			err := paper.ValidateOrderTicket(ticket)
			if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
		})
	}
}

func TestValidateOrderTicketQueryRejectsInvalidInputsTableDriven(t *testing.T) {
	start := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name       string
		query      paper.OrderTicketQuery
		wantErrSub string
	}{
		{"valid empty query", paper.OrderTicketQuery{}, ""},
		{"valid filtered query", paper.OrderTicketQuery{Symbol: "BTCUSDT", Interval: "1", Start: start, End: start.Add(time.Hour), Limit: 10}, ""},
		{"lowercase symbol", paper.OrderTicketQuery{Symbol: "btcusdt"}, "symbol"},
		{"unsupported interval", paper.OrderTicketQuery{Interval: "2"}, "interval"},
		{"end before start", paper.OrderTicketQuery{Start: start, End: start}, "end"},
		{"negative limit", paper.OrderTicketQuery{Limit: -1}, "limit"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := paper.ValidateOrderTicketQuery(tt.query)
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

func validOrderTicket() paper.OrderTicket {
	return paper.OrderTicket{
		TicketID:     "paper_ticket_0001",
		ValidationID: "paper_validation_0001",
		DecisionID:   "risk_decision_0001",
		IntentID:     "risk_intent_0001",
		Exchange:     "bybit",
		Category:     "linear",
		Symbol:       "BTCUSDT",
		Interval:     "1",
		Side:         paper.OrderSideLong,
		Quantity:     decimal.RequireFromString("0.5"),
		EntryPrice:   decimal.RequireFromString("100000"),
		StopLoss:     decimal.RequireFromString("99000"),
		TakeProfit:   decimal.RequireFromString("102000"),
		Leverage:     decimal.RequireFromString("1"),
		MaxLoss:      decimal.RequireFromString("500"),
		Confidence:   82,
		Reason:       "risk_checks_passed",
		CreatedAt:    time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC),
	}
}
