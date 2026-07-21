package live_test

import (
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/VersoIt/Inquisitor/internal/live"
)

func TestNewOrderStatusSnapshotNormalizesExchangeState(t *testing.T) {
	observedAt := time.Date(2026, 7, 22, 12, 0, 0, 0, time.FixedZone("UTC+3", 3*60*60))

	got, err := live.NewOrderStatusSnapshot(live.OrderStatusSnapshotInput{
		ClientOrderID:              " live_client_0001 ",
		ExchangeOrderID:            " bybit_order_0001 ",
		Exchange:                   " BYBIT ",
		Category:                   " LINEAR ",
		Symbol:                     " btcusdt ",
		Side:                       " long ",
		Type:                       " limit ",
		TimeInForce:                " post-only ",
		ExchangeStatus:             " PartiallyFilled ",
		RejectReason:               " EC_NoError ",
		Quantity:                   decimal.RequireFromString("0.5"),
		Price:                      decimal.RequireFromString("100000"),
		AveragePrice:               decimal.RequireFromString("100001"),
		LeavesQuantity:             decimal.RequireFromString("0.2"),
		CumulativeExecutedQuantity: decimal.RequireFromString("0.3"),
		CumulativeExecutedValue:    decimal.RequireFromString("30000.3"),
		CumulativeFee:              decimal.RequireFromString("18"),
		ExchangeCreatedAt:          observedAt.Add(-2 * time.Second),
		ExchangeUpdatedAt:          observedAt.Add(-time.Second),
		ObservedAt:                 observedAt,
	})
	if err != nil {
		t.Fatalf("new order status snapshot: %v", err)
	}

	if got.ClientOrderID != "live_client_0001" || got.ExchangeOrderID != "bybit_order_0001" ||
		got.Exchange != "bybit" || got.Category != "linear" || got.Symbol != "BTCUSDT" ||
		got.Side != live.OrderSideLong || got.Type != live.OrderTypeLimit ||
		got.TimeInForce != live.TimeInForcePostOnly || got.ExchangeStatus != live.ExchangeOrderStatusPartiallyFilled {
		t.Fatalf("snapshot not normalized: %#v", got)
	}
	if got.ObservedAt.Location() != time.UTC || got.ExchangeCreatedAt.Location() != time.UTC || got.ExchangeUpdatedAt.Location() != time.UTC {
		t.Fatalf("snapshot times must be UTC: %#v", got)
	}
}

func TestValidateOrderStatusQueryRejectsUnsafeInputsTableDriven(t *testing.T) {
	tests := []struct {
		name       string
		query      live.OrderStatusQuery
		wantErrSub string
	}{
		{name: "valid", query: validOrderStatusQuery()},
		{name: "missing exchange", query: mutateOrderStatusQuery(func(q *live.OrderStatusQuery) { q.Exchange = "" }), wantErrSub: "exchange"},
		{name: "uppercase exchange", query: mutateOrderStatusQuery(func(q *live.OrderStatusQuery) { q.Exchange = "BYBIT" }), wantErrSub: "exchange"},
		{name: "missing category", query: mutateOrderStatusQuery(func(q *live.OrderStatusQuery) { q.Category = "" }), wantErrSub: "category"},
		{name: "lowercase symbol", query: mutateOrderStatusQuery(func(q *live.OrderStatusQuery) { q.Symbol = "btcusdt" }), wantErrSub: "symbol"},
		{name: "missing client order id", query: mutateOrderStatusQuery(func(q *live.OrderStatusQuery) { q.ClientOrderID = "" }), wantErrSub: "client_order_id"},
		{name: "untrimmed client order id", query: mutateOrderStatusQuery(func(q *live.OrderStatusQuery) { q.ClientOrderID = " live_client_0001 " }), wantErrSub: "client_order_id"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := live.ValidateOrderStatusQuery(tt.query)
			if tt.wantErrSub == "" {
				if err != nil {
					t.Fatalf("expected query to pass, got %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
		})
	}
}

func TestValidateOrderStatusSnapshotRejectsUnsafeInputsTableDriven(t *testing.T) {
	tests := []struct {
		name       string
		mutate     func(*live.OrderStatusSnapshot)
		wantErrSub string
	}{
		{"missing exchange order id", func(s *live.OrderStatusSnapshot) { s.ExchangeOrderID = "" }, "exchange_order_id"},
		{"unknown side", func(s *live.OrderStatusSnapshot) { s.Side = "BUY" }, "side"},
		{"unknown type", func(s *live.OrderStatusSnapshot) { s.Type = "STOP" }, "type"},
		{"unknown time in force", func(s *live.OrderStatusSnapshot) { s.TimeInForce = "DAY" }, "time_in_force"},
		{"unknown status", func(s *live.OrderStatusSnapshot) { s.ExchangeStatus = "PENDING" }, "exchange_status"},
		{"zero quantity", func(s *live.OrderStatusSnapshot) { s.Quantity = decimal.Zero }, "quantity"},
		{"negative price", func(s *live.OrderStatusSnapshot) { s.Price = decimal.RequireFromString("-1") }, "price"},
		{"leaves exceed quantity", func(s *live.OrderStatusSnapshot) { s.LeavesQuantity = decimal.RequireFromString("0.6") }, "leaves_quantity"},
		{"executed exceeds quantity", func(s *live.OrderStatusSnapshot) { s.CumulativeExecutedQuantity = decimal.RequireFromString("0.6") }, "cumulative_executed_quantity"},
		{"missing created time", func(s *live.OrderStatusSnapshot) { s.ExchangeCreatedAt = time.Time{} }, "exchange_created_at"},
		{"updated before created", func(s *live.OrderStatusSnapshot) { s.ExchangeUpdatedAt = s.ExchangeCreatedAt.Add(-time.Second) }, "exchange_updated_at"},
		{"missing observed time", func(s *live.OrderStatusSnapshot) { s.ObservedAt = time.Time{} }, "observed_at"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			snapshot := validOrderStatusSnapshot()
			tt.mutate(&snapshot)

			err := live.ValidateOrderStatusSnapshot(snapshot)
			if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
		})
	}
}

func TestKnownExchangeOrderStatusCoversBybitLifecycle(t *testing.T) {
	for _, status := range []live.ExchangeOrderStatus{
		live.ExchangeOrderStatusNew,
		live.ExchangeOrderStatusPartiallyFilled,
		live.ExchangeOrderStatusUntriggered,
		live.ExchangeOrderStatusRejected,
		live.ExchangeOrderStatusPartiallyFilledCancelled,
		live.ExchangeOrderStatusFilled,
		live.ExchangeOrderStatusCancelled,
		live.ExchangeOrderStatusTriggered,
		live.ExchangeOrderStatusDeactivated,
	} {
		if !live.KnownExchangeOrderStatus(status) {
			t.Fatalf("status should be known: %s", status)
		}
	}
}

func TestOrderStatusSnapshotStatsTotal(t *testing.T) {
	if got := (live.OrderStatusSnapshotStats{Inserted: 2, Skipped: 3}).Total(); got != 5 {
		t.Fatalf("total mismatch: got %d", got)
	}
}

func validOrderStatusQuery() live.OrderStatusQuery {
	return live.OrderStatusQuery{
		Exchange:      "bybit",
		Category:      "linear",
		Symbol:        "BTCUSDT",
		ClientOrderID: "live_client_0001",
	}
}

func mutateOrderStatusQuery(mutate func(*live.OrderStatusQuery)) live.OrderStatusQuery {
	query := validOrderStatusQuery()
	mutate(&query)
	return query
}

func validOrderStatusSnapshot() live.OrderStatusSnapshot {
	return live.OrderStatusSnapshot{
		ClientOrderID:              "live_client_0001",
		ExchangeOrderID:            "bybit_order_0001",
		Exchange:                   "bybit",
		Category:                   "linear",
		Symbol:                     "BTCUSDT",
		Side:                       live.OrderSideLong,
		Type:                       live.OrderTypeMarket,
		TimeInForce:                live.TimeInForceIOC,
		ExchangeStatus:             live.ExchangeOrderStatusFilled,
		RejectReason:               "EC_NoError",
		Quantity:                   decimal.RequireFromString("0.5"),
		Price:                      decimal.Zero,
		AveragePrice:               decimal.RequireFromString("100001"),
		LeavesQuantity:             decimal.Zero,
		CumulativeExecutedQuantity: decimal.RequireFromString("0.5"),
		CumulativeExecutedValue:    decimal.RequireFromString("50000.5"),
		CumulativeFee:              decimal.RequireFromString("30"),
		ExchangeCreatedAt:          time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC),
		ExchangeUpdatedAt:          time.Date(2026, 7, 22, 12, 0, 1, 0, time.UTC),
		ObservedAt:                 time.Date(2026, 7, 22, 12, 0, 2, 0, time.UTC),
	}
}
