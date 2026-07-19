package live_test

import (
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/VersoIt/Inquisitor/internal/live"
)

func TestNewOrderSubmissionNormalizesLiveEntry(t *testing.T) {
	createdAt := time.Date(2026, 7, 19, 12, 0, 0, 0, time.FixedZone("UTC+3", 3*60*60))

	got, err := live.NewOrderSubmission(live.OrderSubmissionInput{
		SubmissionID:     " live_submission_0001 ",
		ClientOrderID:    " live_client_0001 ",
		DecisionID:       " risk_decision_0001 ",
		DecisionApproved: true,
		IntentID:         " risk_intent_0001 ",
		RiskMode:         " live ",
		Exchange:         " BYBIT ",
		Category:         " LINEAR ",
		Symbol:           " btcusdt ",
		Side:             " long ",
		Type:             " market ",
		TimeInForce:      " ioc ",
		Quantity:         decimal.RequireFromString("0.5"),
		ReferencePrice:   decimal.RequireFromString("100000"),
		StopLoss:         decimal.RequireFromString("99000"),
		TakeProfit:       decimal.RequireFromString("102000"),
		Leverage:         decimal.RequireFromString("1"),
		MaxLoss:          decimal.RequireFromString("500"),
		Confidence:       82,
		Reason:           " risk_checks_passed ",
		CreatedAt:        createdAt,
	})
	if err != nil {
		t.Fatalf("new live order submission: %v", err)
	}

	if got.SubmissionID != "live_submission_0001" || got.ClientOrderID != "live_client_0001" ||
		got.DecisionID != "risk_decision_0001" || !got.DecisionApproved || got.IntentID != "risk_intent_0001" {
		t.Fatalf("identity not normalized: %#v", got)
	}
	if got.RiskMode != live.RiskModeLive || got.Exchange != "bybit" || got.Category != "linear" ||
		got.Symbol != "BTCUSDT" || got.Side != live.OrderSideLong ||
		got.Type != live.OrderTypeMarket || got.TimeInForce != live.TimeInForceIOC {
		t.Fatalf("execution scope not normalized: %#v", got)
	}
	if !got.Notional.Equal(decimal.RequireFromString("50000")) || !got.MaxLoss.Equal(decimal.RequireFromString("500")) ||
		got.Confidence != 82 || got.Reason != "risk_checks_passed" {
		t.Fatalf("risk snapshot mismatch: %#v", got)
	}
	if got.CreatedAt.Location() != time.UTC {
		t.Fatalf("created_at must be UTC, got %s", got.CreatedAt)
	}
}

func TestValidateOrderSubmissionAcceptsShortLimitAndReduceOnlyExit(t *testing.T) {
	tests := []struct {
		name       string
		submission live.OrderSubmission
	}{
		{
			name: "short limit entry",
			submission: func() live.OrderSubmission {
				submission := validOrderSubmission()
				submission.Side = live.OrderSideShort
				submission.Type = live.OrderTypeLimit
				submission.TimeInForce = live.TimeInForcePostOnly
				submission.LimitPrice = decimal.RequireFromString("99950")
				submission.StopLoss = decimal.RequireFromString("101000")
				submission.TakeProfit = decimal.RequireFromString("98000")
				submission.MaxLoss = decimal.RequireFromString("500")
				return submission
			}(),
		},
		{
			name: "reduce only market exit",
			submission: func() live.OrderSubmission {
				submission := validOrderSubmission()
				submission.ReduceOnly = true
				submission.StopLoss = decimal.Zero
				submission.TakeProfit = decimal.Zero
				submission.MaxLoss = decimal.Zero
				submission.Reason = "position_exit"
				return submission
			}(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := live.ValidateOrderSubmission(tt.submission); err != nil {
				t.Fatalf("validate live order submission: %v", err)
			}
		})
	}
}

func TestValidateOrderSubmissionRejectsUnsafeInputsTableDriven(t *testing.T) {
	tests := []struct {
		name       string
		mutate     func(*live.OrderSubmission)
		wantErrSub string
	}{
		{"missing submission id", func(s *live.OrderSubmission) { s.SubmissionID = " " }, "submission_id"},
		{"missing client order id", func(s *live.OrderSubmission) { s.ClientOrderID = "" }, "client_order_id"},
		{"missing decision id", func(s *live.OrderSubmission) { s.DecisionID = "" }, "decision_id"},
		{"rejected decision", func(s *live.OrderSubmission) { s.DecisionApproved = false }, "decision_approved"},
		{"missing intent id", func(s *live.OrderSubmission) { s.IntentID = "" }, "intent_id"},
		{"paper risk mode", func(s *live.OrderSubmission) { s.RiskMode = "PAPER" }, "risk_mode"},
		{"uppercase exchange", func(s *live.OrderSubmission) { s.Exchange = "BYBIT" }, "exchange"},
		{"uppercase category", func(s *live.OrderSubmission) { s.Category = "LINEAR" }, "category"},
		{"lowercase symbol", func(s *live.OrderSubmission) { s.Symbol = "btcusdt" }, "symbol"},
		{"unknown side", func(s *live.OrderSubmission) { s.Side = "BUY" }, "side"},
		{"unknown type", func(s *live.OrderSubmission) { s.Type = "STOP" }, "type"},
		{"unknown time in force", func(s *live.OrderSubmission) { s.TimeInForce = "DAY" }, "time_in_force"},
		{"zero quantity", func(s *live.OrderSubmission) { s.Quantity = decimal.Zero; s.Notional = decimal.Zero }, "quantity"},
		{"zero reference price", func(s *live.OrderSubmission) { s.ReferencePrice = decimal.Zero; s.Notional = decimal.Zero }, "reference_price"},
		{"notional mismatch", func(s *live.OrderSubmission) { s.Notional = decimal.RequireFromString("1") }, "notional"},
		{"market includes limit price", func(s *live.OrderSubmission) { s.LimitPrice = decimal.RequireFromString("100000") }, "market order"},
		{"market GTC rejected", func(s *live.OrderSubmission) { s.TimeInForce = live.TimeInForceGTC }, "market order time_in_force"},
		{"limit missing price", func(s *live.OrderSubmission) { s.Type = live.OrderTypeLimit; s.TimeInForce = live.TimeInForceGTC }, "limit_price"},
		{"reduce only post only rejected", func(s *live.OrderSubmission) {
			s.Type = live.OrderTypeLimit
			s.TimeInForce = live.TimeInForcePostOnly
			s.LimitPrice = decimal.RequireFromString("100000")
			s.ReduceOnly = true
			s.StopLoss = decimal.Zero
			s.TakeProfit = decimal.Zero
			s.MaxLoss = decimal.Zero
		}, "reduce_only"},
		{"zero leverage", func(s *live.OrderSubmission) { s.Leverage = decimal.Zero }, "leverage"},
		{"invalid confidence", func(s *live.OrderSubmission) { s.Confidence = 101 }, "confidence"},
		{"missing reason", func(s *live.OrderSubmission) { s.Reason = "" }, "reason"},
		{"entry missing stop", func(s *live.OrderSubmission) { s.StopLoss = decimal.Zero }, "stop_loss"},
		{"negative take profit", func(s *live.OrderSubmission) { s.TakeProfit = decimal.RequireFromString("-1") }, "take_profit"},
		{"max loss mismatch", func(s *live.OrderSubmission) { s.MaxLoss = decimal.RequireFromString("499") }, "max_loss"},
		{"long stop above entry", func(s *live.OrderSubmission) { s.StopLoss = decimal.RequireFromString("101000") }, "stop_loss"},
		{"long take profit below entry", func(s *live.OrderSubmission) { s.TakeProfit = decimal.RequireFromString("99000") }, "take_profit"},
		{"short stop below entry", func(s *live.OrderSubmission) {
			s.Side = live.OrderSideShort
			s.StopLoss = decimal.RequireFromString("99000")
		}, "stop_loss"},
		{"reduce only carries stop", func(s *live.OrderSubmission) { s.ReduceOnly = true }, "reduce_only"},
		{"missing created at", func(s *live.OrderSubmission) { s.CreatedAt = time.Time{} }, "created_at"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			submission := validOrderSubmission()
			tt.mutate(&submission)

			err := live.ValidateOrderSubmission(submission)
			if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
		})
	}
}

func TestNewOrderAcknowledgementNormalizesAcceptedOrder(t *testing.T) {
	receivedAt := time.Date(2026, 7, 19, 12, 0, 0, 0, time.FixedZone("UTC+3", 3*60*60))

	got, err := live.NewOrderAcknowledgement(live.OrderAcknowledgementInput{
		SubmissionID:    " live_submission_0001 ",
		ClientOrderID:   " live_client_0001 ",
		Exchange:        " BYBIT ",
		ExchangeOrderID: " bybit_order_0001 ",
		Status:          " accepted ",
		ReceivedAt:      receivedAt,
	})
	if err != nil {
		t.Fatalf("new live order acknowledgement: %v", err)
	}

	if got.SubmissionID != "live_submission_0001" || got.ClientOrderID != "live_client_0001" ||
		got.Exchange != "bybit" || got.ExchangeOrderID != "bybit_order_0001" ||
		got.Status != live.OrderStatusAccepted {
		t.Fatalf("acknowledgement not normalized: %#v", got)
	}
	if got.ReceivedAt.Location() != time.UTC {
		t.Fatalf("received_at must be UTC, got %s", got.ReceivedAt)
	}
}

func TestValidateOrderAcknowledgementRejectsInvalidInputsTableDriven(t *testing.T) {
	tests := []struct {
		name       string
		mutate     func(*live.OrderAcknowledgement)
		wantErrSub string
	}{
		{"missing submission id", func(a *live.OrderAcknowledgement) { a.SubmissionID = "" }, "submission_id"},
		{"missing client order id", func(a *live.OrderAcknowledgement) { a.ClientOrderID = "" }, "client_order_id"},
		{"uppercase exchange", func(a *live.OrderAcknowledgement) { a.Exchange = "BYBIT" }, "exchange"},
		{"unknown status", func(a *live.OrderAcknowledgement) { a.Status = "PENDING" }, "status"},
		{"accepted missing exchange id", func(a *live.OrderAcknowledgement) { a.ExchangeOrderID = "" }, "exchange_order_id"},
		{"accepted with reject reason", func(a *live.OrderAcknowledgement) { a.RejectReason = "bad request" }, "reject_reason"},
		{"rejected with exchange id", func(a *live.OrderAcknowledgement) {
			a.Status = live.OrderStatusRejected
			a.ExchangeOrderID = "bybit_order_0001"
			a.RejectReason = "insufficient balance"
		}, "exchange_order_id"},
		{"rejected missing reason", func(a *live.OrderAcknowledgement) {
			a.Status = live.OrderStatusRejected
			a.ExchangeOrderID = ""
		}, "reject_reason"},
		{"missing received at", func(a *live.OrderAcknowledgement) { a.ReceivedAt = time.Time{} }, "received_at"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			acknowledgement := validOrderAcknowledgement()
			tt.mutate(&acknowledgement)

			err := live.ValidateOrderAcknowledgement(acknowledgement)
			if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
		})
	}
}

func TestOrderStatsTotal(t *testing.T) {
	if got := (live.OrderSubmissionStats{Inserted: 2, Skipped: 3}).Total(); got != 5 {
		t.Fatalf("submission stats total mismatch: %d", got)
	}
	if got := (live.OrderAcknowledgementStats{Inserted: 1, Skipped: 4}).Total(); got != 5 {
		t.Fatalf("acknowledgement stats total mismatch: %d", got)
	}
}

func validOrderSubmission() live.OrderSubmission {
	return live.OrderSubmission{
		SubmissionID:     "live_submission_0001",
		ClientOrderID:    "live_client_0001",
		DecisionID:       "risk_decision_0001",
		DecisionApproved: true,
		IntentID:         "risk_intent_0001",
		RiskMode:         live.RiskModeLive,
		Exchange:         "bybit",
		Category:         "linear",
		Symbol:           "BTCUSDT",
		Side:             live.OrderSideLong,
		Type:             live.OrderTypeMarket,
		TimeInForce:      live.TimeInForceIOC,
		Quantity:         decimal.RequireFromString("0.5"),
		ReferencePrice:   decimal.RequireFromString("100000"),
		StopLoss:         decimal.RequireFromString("99000"),
		TakeProfit:       decimal.RequireFromString("102000"),
		Leverage:         decimal.RequireFromString("1"),
		MaxLoss:          decimal.RequireFromString("500"),
		Notional:         decimal.RequireFromString("50000"),
		Confidence:       82,
		Reason:           "risk_checks_passed",
		CreatedAt:        time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC),
	}
}

func validOrderAcknowledgement() live.OrderAcknowledgement {
	return live.OrderAcknowledgement{
		SubmissionID:    "live_submission_0001",
		ClientOrderID:   "live_client_0001",
		Exchange:        "bybit",
		ExchangeOrderID: "bybit_order_0001",
		Status:          live.OrderStatusAccepted,
		ReceivedAt:      time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC),
	}
}
