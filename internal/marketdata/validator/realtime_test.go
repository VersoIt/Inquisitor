package validator_test

import (
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/VersoIt/Inquisitor/internal/marketdata"
	"github.com/VersoIt/Inquisitor/internal/marketdata/validator"
)

func TestValidatePublicTradesTableDriven(t *testing.T) {
	tests := []struct {
		name        string
		trades      []marketdata.PublicTrade
		wantErrCode string
	}{
		{
			name:   "accepts valid trade",
			trades: []marketdata.PublicTrade{validPublicTrade("trade-1")},
		},
		{
			name: "rejects unsupported side",
			trades: []marketdata.PublicTrade{func() marketdata.PublicTrade {
				trade := validPublicTrade("trade-1")
				trade.Side = "Hold"
				return trade
			}()},
			wantErrCode: "unsupported",
		},
		{
			name: "rejects duplicate trade id in batch",
			trades: []marketdata.PublicTrade{
				validPublicTrade("trade-1"),
				validPublicTrade("trade-1"),
			},
			wantErrCode: "duplicate",
		},
		{
			name: "rejects non positive quantity",
			trades: []marketdata.PublicTrade{func() marketdata.PublicTrade {
				trade := validPublicTrade("trade-1")
				trade.Quantity = decimal.Zero
				return trade
			}()},
			wantErrCode: "must_be_positive",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validator.ValidatePublicTrades(tt.trades)
			if tt.wantErrCode == "" {
				if err != nil {
					t.Fatalf("expected valid trades, got %v", err)
				}
				return
			}
			assertValidationProblemCode(t, err, tt.wantErrCode)
		})
	}
}

func TestValidateOrderbookSnapshotsTableDriven(t *testing.T) {
	tests := []struct {
		name        string
		snapshots   []marketdata.OrderbookSnapshot
		wantErrCode string
	}{
		{
			name:      "accepts valid snapshot",
			snapshots: []marketdata.OrderbookSnapshot{validOrderbookSnapshot()},
		},
		{
			name: "rejects crossed snapshot",
			snapshots: []marketdata.OrderbookSnapshot{func() marketdata.OrderbookSnapshot {
				snapshot := validOrderbookSnapshot()
				snapshot.BestBid = decimal.RequireFromString("101")
				snapshot.BestAsk = decimal.RequireFromString("100")
				return snapshot
			}()},
			wantErrCode: "crossed",
		},
		{
			name: "rejects unsorted bids",
			snapshots: []marketdata.OrderbookSnapshot{func() marketdata.OrderbookSnapshot {
				snapshot := validOrderbookSnapshot()
				snapshot.Bids = []marketdata.OrderbookLevel{
					{Price: decimal.RequireFromString("99"), Quantity: decimal.RequireFromString("1")},
					{Price: decimal.RequireFromString("100"), Quantity: decimal.RequireFromString("1")},
				}
				return snapshot
			}()},
			wantErrCode: "not_sorted",
		},
		{
			name: "rejects missing created_at",
			snapshots: []marketdata.OrderbookSnapshot{func() marketdata.OrderbookSnapshot {
				snapshot := validOrderbookSnapshot()
				snapshot.CreatedAt = time.Time{}
				return snapshot
			}()},
			wantErrCode: "required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validator.ValidateOrderbookSnapshots(tt.snapshots)
			if tt.wantErrCode == "" {
				if err != nil {
					t.Fatalf("expected valid snapshots, got %v", err)
				}
				return
			}
			assertValidationProblemCode(t, err, tt.wantErrCode)
		})
	}
}

func validPublicTrade(tradeID string) marketdata.PublicTrade {
	return marketdata.PublicTrade{
		Exchange:  "bybit",
		Category:  "linear",
		Symbol:    "BTCUSDT",
		TradeID:   tradeID,
		Side:      "Buy",
		Price:     decimal.RequireFromString("100"),
		Quantity:  decimal.RequireFromString("0.01"),
		TradeTime: time.Date(2026, 6, 7, 0, 0, 0, 0, time.UTC),
		Sequence:  100,
	}
}

func validOrderbookSnapshot() marketdata.OrderbookSnapshot {
	now := time.Date(2026, 6, 7, 0, 0, 0, 0, time.UTC)
	return marketdata.OrderbookSnapshot{
		Exchange: "bybit",
		Category: "linear",
		Symbol:   "BTCUSDT",
		Depth:    2,
		Bids: []marketdata.OrderbookLevel{
			{Price: decimal.RequireFromString("99.5"), Quantity: decimal.RequireFromString("2")},
			{Price: decimal.RequireFromString("99"), Quantity: decimal.RequireFromString("1")},
		},
		Asks: []marketdata.OrderbookLevel{
			{Price: decimal.RequireFromString("100.5"), Quantity: decimal.RequireFromString("3")},
			{Price: decimal.RequireFromString("101"), Quantity: decimal.RequireFromString("1")},
		},
		BestBid:            decimal.RequireFromString("99.5"),
		BestAsk:            decimal.RequireFromString("100.5"),
		Spread:             decimal.RequireFromString("1"),
		SpreadBPS:          decimal.RequireFromString("100"),
		UpdateID:           1,
		Sequence:           2,
		ExchangeTime:       now,
		MatchingEngineTime: now.Add(-10 * time.Millisecond),
		CreatedAt:          now.Add(time.Second),
	}
}

func assertValidationProblemCode(t *testing.T, err error, code string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected validation error with code %q", code)
	}

	var validationErr validator.CandleValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("expected CandleValidationError, got %T", err)
	}
	for _, problem := range validationErr.Problems {
		if problem.Code == code {
			return
		}
	}
	t.Fatalf("expected problem code %q in %#v", code, validationErr.Problems)
}
