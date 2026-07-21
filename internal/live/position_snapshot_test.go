package live_test

import (
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/VersoIt/Inquisitor/internal/live"
)

func TestNewPositionSnapshotNormalizesOpenPosition(t *testing.T) {
	observedAt := time.Date(2026, 7, 22, 12, 0, 0, 0, time.FixedZone("UTC+3", 3*60*60))

	got, err := live.NewPositionSnapshot(live.PositionSnapshotInput{
		Exchange:              " BYBIT ",
		Category:              " LINEAR ",
		Symbol:                " btcusdt ",
		Side:                  " long ",
		Size:                  decimal.RequireFromString("0.25"),
		AveragePrice:          decimal.RequireFromString("100001"),
		PositionValue:         decimal.RequireFromString("25000.25"),
		MarkPrice:             decimal.RequireFromString("100100"),
		LiquidationPrice:      decimal.RequireFromString("50000"),
		Leverage:              decimal.RequireFromString("1"),
		UnrealisedPnL:         decimal.RequireFromString("-3.5"),
		CurrentRealisedPnL:    decimal.RequireFromString("1.25"),
		CumulativeRealisedPnL: decimal.RequireFromString("10"),
		ExchangeStatus:        " normal ",
		PositionIndex:         0,
		Sequence:              123,
		ExchangeCreatedAt:     observedAt.Add(-2 * time.Second),
		ExchangeUpdatedAt:     observedAt.Add(-time.Second),
		ObservedAt:            observedAt,
	})
	if err != nil {
		t.Fatalf("new position snapshot: %v", err)
	}

	if got.Exchange != "bybit" || got.Category != "linear" || got.Symbol != "BTCUSDT" ||
		!got.Open || got.Side != live.OrderSideLong || got.ExchangeStatus != live.ExchangePositionStatusNormal {
		t.Fatalf("snapshot not normalized: %#v", got)
	}
	if got.ObservedAt.Location() != time.UTC || got.ExchangeCreatedAt.Location() != time.UTC || got.ExchangeUpdatedAt.Location() != time.UTC {
		t.Fatalf("snapshot times must be UTC: %#v", got)
	}
}

func TestNewPositionSnapshotAllowsFlatPosition(t *testing.T) {
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)

	got, err := live.NewPositionSnapshot(live.PositionSnapshotInput{
		Exchange:       "bybit",
		Category:       "linear",
		Symbol:         "BTCUSDT",
		Size:           decimal.Zero,
		ExchangeStatus: live.ExchangePositionStatusNormal,
		ObservedAt:     now,
	})
	if err != nil {
		t.Fatalf("new flat position snapshot: %v", err)
	}
	if got.Open || got.Side != "" || !got.Size.IsZero() {
		t.Fatalf("flat snapshot mismatch: %#v", got)
	}
}

func TestValidatePositionSnapshotQueryRejectsUnsafeInputsTableDriven(t *testing.T) {
	tests := []struct {
		name       string
		query      live.PositionSnapshotQuery
		wantErrSub string
	}{
		{name: "valid", query: validPositionSnapshotQuery()},
		{name: "missing exchange", query: mutatePositionSnapshotQuery(func(q *live.PositionSnapshotQuery) { q.Exchange = "" }), wantErrSub: "exchange"},
		{name: "uppercase exchange", query: mutatePositionSnapshotQuery(func(q *live.PositionSnapshotQuery) { q.Exchange = "BYBIT" }), wantErrSub: "exchange"},
		{name: "missing category", query: mutatePositionSnapshotQuery(func(q *live.PositionSnapshotQuery) { q.Category = "" }), wantErrSub: "category"},
		{name: "lowercase symbol", query: mutatePositionSnapshotQuery(func(q *live.PositionSnapshotQuery) { q.Symbol = "btcusdt" }), wantErrSub: "symbol"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := live.ValidatePositionSnapshotQuery(tt.query)
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

func TestValidatePositionSnapshotRejectsUnsafeInputsTableDriven(t *testing.T) {
	tests := []struct {
		name       string
		mutate     func(*live.PositionSnapshot)
		wantErrSub string
	}{
		{"open flag mismatch", func(s *live.PositionSnapshot) { s.Open = false }, "open"},
		{"negative size", func(s *live.PositionSnapshot) { s.Size = decimal.RequireFromString("-0.1") }, "size"},
		{"open missing side", func(s *live.PositionSnapshot) { s.Side = "" }, "side"},
		{"unknown status", func(s *live.PositionSnapshot) { s.ExchangeStatus = "BROKEN" }, "exchange_status"},
		{"open zero average price", func(s *live.PositionSnapshot) { s.AveragePrice = decimal.Zero }, "average_price"},
		{"open zero position value", func(s *live.PositionSnapshot) { s.PositionValue = decimal.Zero }, "position_value"},
		{"negative mark price", func(s *live.PositionSnapshot) { s.MarkPrice = decimal.RequireFromString("-1") }, "mark_price"},
		{"negative liquidation price", func(s *live.PositionSnapshot) { s.LiquidationPrice = decimal.RequireFromString("-1") }, "liquidation_price"},
		{"negative leverage", func(s *live.PositionSnapshot) { s.Leverage = decimal.RequireFromString("-1") }, "leverage"},
		{"negative position index", func(s *live.PositionSnapshot) { s.PositionIndex = -1 }, "position_index"},
		{"missing observed time", func(s *live.PositionSnapshot) { s.ObservedAt = time.Time{} }, "observed_at"},
		{"updated before created", func(s *live.PositionSnapshot) { s.ExchangeUpdatedAt = s.ExchangeCreatedAt.Add(-time.Second) }, "exchange_updated_at"},
		{"flat with side", func(s *live.PositionSnapshot) {
			*s = validFlatPositionSnapshot()
			s.Side = live.OrderSideLong
		}, "side"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			snapshot := validOpenPositionSnapshot()
			tt.mutate(&snapshot)

			err := live.ValidatePositionSnapshot(snapshot)
			if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
		})
	}
}

func TestKnownExchangePositionStatusCoversBybitLifecycle(t *testing.T) {
	for _, status := range []live.ExchangePositionStatus{
		live.ExchangePositionStatusNormal,
		live.ExchangePositionStatusLiq,
		live.ExchangePositionStatusAdl,
	} {
		if !live.KnownExchangePositionStatus(status) {
			t.Fatalf("status should be known: %s", status)
		}
	}
}

func validPositionSnapshotQuery() live.PositionSnapshotQuery {
	return live.PositionSnapshotQuery{
		Exchange: "bybit",
		Category: "linear",
		Symbol:   "BTCUSDT",
	}
}

func mutatePositionSnapshotQuery(mutate func(*live.PositionSnapshotQuery)) live.PositionSnapshotQuery {
	query := validPositionSnapshotQuery()
	mutate(&query)
	return query
}

func validOpenPositionSnapshot() live.PositionSnapshot {
	return live.PositionSnapshot{
		Exchange:              "bybit",
		Category:              "linear",
		Symbol:                "BTCUSDT",
		Open:                  true,
		Side:                  live.OrderSideLong,
		Size:                  decimal.RequireFromString("0.25"),
		AveragePrice:          decimal.RequireFromString("100001"),
		PositionValue:         decimal.RequireFromString("25000.25"),
		MarkPrice:             decimal.RequireFromString("100100"),
		LiquidationPrice:      decimal.RequireFromString("50000"),
		Leverage:              decimal.RequireFromString("1"),
		UnrealisedPnL:         decimal.RequireFromString("-3.5"),
		CurrentRealisedPnL:    decimal.RequireFromString("1.25"),
		CumulativeRealisedPnL: decimal.RequireFromString("10"),
		ExchangeStatus:        live.ExchangePositionStatusNormal,
		PositionIndex:         0,
		Sequence:              123,
		ExchangeCreatedAt:     time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC),
		ExchangeUpdatedAt:     time.Date(2026, 7, 22, 12, 0, 1, 0, time.UTC),
		ObservedAt:            time.Date(2026, 7, 22, 12, 0, 2, 0, time.UTC),
	}
}

func validFlatPositionSnapshot() live.PositionSnapshot {
	return live.PositionSnapshot{
		Exchange:       "bybit",
		Category:       "linear",
		Symbol:         "BTCUSDT",
		Open:           false,
		Size:           decimal.Zero,
		ExchangeStatus: live.ExchangePositionStatusNormal,
		ObservedAt:     time.Date(2026, 7, 22, 12, 0, 2, 0, time.UTC),
	}
}
