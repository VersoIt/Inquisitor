package realtime_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/VersoIt/Inquisitor/internal/marketdata"
	"github.com/VersoIt/Inquisitor/internal/marketdata/realtime"
)

func TestOrderbookBookApplyTableDriven(t *testing.T) {
	now := time.Date(2026, 6, 10, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name        string
		updates     []marketdata.Orderbook
		wantBids    []marketdata.OrderbookLevel
		wantAsks    []marketdata.OrderbookLevel
		wantUpdate  int64
		wantSeq     int64
		wantErrSub  string
		wantErrCode string
	}{
		{
			name: "snapshot initializes book",
			updates: []marketdata.Orderbook{
				testBookSnapshot(now),
			},
			wantBids: []marketdata.OrderbookLevel{
				{Price: decimal.RequireFromString("99.5"), Quantity: decimal.RequireFromString("2")},
				{Price: decimal.RequireFromString("99"), Quantity: decimal.RequireFromString("1")},
			},
			wantAsks: []marketdata.OrderbookLevel{
				{Price: decimal.RequireFromString("100.5"), Quantity: decimal.RequireFromString("3")},
				{Price: decimal.RequireFromString("101"), Quantity: decimal.RequireFromString("1")},
			},
			wantUpdate: 10,
			wantSeq:    100,
		},
		{
			name: "delta deletes updates and inserts levels",
			updates: []marketdata.Orderbook{
				testBookSnapshot(now),
				testBookDelta(now.Add(time.Second),
					[]marketdata.OrderbookLevel{
						{Price: decimal.RequireFromString("99.5"), Quantity: decimal.Zero},
						{Price: decimal.RequireFromString("98.5"), Quantity: decimal.RequireFromString("5")},
					},
					[]marketdata.OrderbookLevel{
						{Price: decimal.RequireFromString("101"), Quantity: decimal.RequireFromString("2")},
						{Price: decimal.RequireFromString("100.25"), Quantity: decimal.RequireFromString("4")},
					},
				),
			},
			wantBids: []marketdata.OrderbookLevel{
				{Price: decimal.RequireFromString("99"), Quantity: decimal.RequireFromString("1")},
				{Price: decimal.RequireFromString("98.5"), Quantity: decimal.RequireFromString("5")},
			},
			wantAsks: []marketdata.OrderbookLevel{
				{Price: decimal.RequireFromString("100.25"), Quantity: decimal.RequireFromString("4")},
				{Price: decimal.RequireFromString("100.5"), Quantity: decimal.RequireFromString("3")},
				{Price: decimal.RequireFromString("101"), Quantity: decimal.RequireFromString("2")},
			},
			wantUpdate: 11,
			wantSeq:    101,
		},
		{
			name: "new snapshot resets previous local book",
			updates: []marketdata.Orderbook{
				testBookSnapshot(now),
				testBookDelta(now.Add(time.Second), []marketdata.OrderbookLevel{
					{Price: decimal.RequireFromString("99.5"), Quantity: decimal.Zero},
				}, nil),
				func() marketdata.Orderbook {
					book := testBookSnapshot(now.Add(2 * time.Second))
					book.Bids = []marketdata.OrderbookLevel{{Price: decimal.RequireFromString("88"), Quantity: decimal.RequireFromString("1")}}
					book.Asks = []marketdata.OrderbookLevel{{Price: decimal.RequireFromString("89"), Quantity: decimal.RequireFromString("1")}}
					book.UpdateID = 99
					book.Sequence = 199
					return book
				}(),
			},
			wantBids: []marketdata.OrderbookLevel{
				{Price: decimal.RequireFromString("88"), Quantity: decimal.RequireFromString("1")},
			},
			wantAsks: []marketdata.OrderbookLevel{
				{Price: decimal.RequireFromString("89"), Quantity: decimal.RequireFromString("1")},
			},
			wantUpdate: 99,
			wantSeq:    199,
		},
		{
			name: "rejects delta before snapshot",
			updates: []marketdata.Orderbook{
				testBookDelta(now, nil, nil),
			},
			wantErrSub: "before snapshot",
		},
		{
			name: "rejects identity mismatch",
			updates: []marketdata.Orderbook{
				testBookSnapshot(now),
				func() marketdata.Orderbook {
					book := testBookDelta(now.Add(time.Second), nil, nil)
					book.Symbol = "ETHUSDT"
					return book
				}(),
			},
			wantErrSub: "identity mismatch",
		},
		{
			name: "rejects crossed book after delta",
			updates: []marketdata.Orderbook{
				testBookSnapshot(now),
				testBookDelta(now.Add(time.Second), []marketdata.OrderbookLevel{
					{Price: decimal.RequireFromString("101"), Quantity: decimal.RequireFromString("1")},
				}, nil),
			},
			wantErrCode: "crossed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var book realtime.OrderbookBook
			var got marketdata.Orderbook
			var err error
			for _, update := range tt.updates {
				got, err = book.Apply(update)
				if err != nil {
					break
				}
			}

			if tt.wantErrSub != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
				}
				return
			}
			if tt.wantErrCode != "" {
				assertOrderbookValidationCode(t, err, tt.wantErrCode)
				return
			}
			if err != nil {
				t.Fatalf("apply orderbook updates: %v", err)
			}
			assertLevels(t, got.Bids, tt.wantBids)
			assertLevels(t, got.Asks, tt.wantAsks)
			if got.Type != "snapshot" {
				t.Fatalf("expected full local book to be snapshot, got %q", got.Type)
			}
			if got.UpdateID != tt.wantUpdate || got.Sequence != tt.wantSeq {
				t.Fatalf("unexpected update ids: got update=%d seq=%d want update=%d seq=%d", got.UpdateID, got.Sequence, tt.wantUpdate, tt.wantSeq)
			}
		})
	}
}

func TestOrderbookBookRejectedDeltaKeepsLastValidSnapshot(t *testing.T) {
	now := time.Date(2026, 6, 10, 0, 0, 0, 0, time.UTC)
	var book realtime.OrderbookBook

	if _, err := book.Apply(testBookSnapshot(now)); err != nil {
		t.Fatalf("apply snapshot: %v", err)
	}

	_, err := book.Apply(testBookDelta(now.Add(time.Second), []marketdata.OrderbookLevel{
		{Price: decimal.RequireFromString("101"), Quantity: decimal.RequireFromString("1")},
	}, nil))
	assertOrderbookValidationCode(t, err, "crossed")

	got, err := book.Apply(testBookDelta(now.Add(2*time.Second), []marketdata.OrderbookLevel{
		{Price: decimal.RequireFromString("99.5"), Quantity: decimal.Zero},
		{Price: decimal.RequireFromString("98.5"), Quantity: decimal.RequireFromString("5")},
	}, nil))
	if err != nil {
		t.Fatalf("apply valid delta after rejected delta: %v", err)
	}

	assertLevels(t, got.Bids, []marketdata.OrderbookLevel{
		{Price: decimal.RequireFromString("99"), Quantity: decimal.RequireFromString("1")},
		{Price: decimal.RequireFromString("98.5"), Quantity: decimal.RequireFromString("5")},
	})
	assertLevels(t, got.Asks, []marketdata.OrderbookLevel{
		{Price: decimal.RequireFromString("100.5"), Quantity: decimal.RequireFromString("3")},
		{Price: decimal.RequireFromString("101"), Quantity: decimal.RequireFromString("1")},
	})
}

func testBookSnapshot(ts time.Time) marketdata.Orderbook {
	return marketdata.Orderbook{
		Exchange: "bybit",
		Category: "linear",
		Symbol:   "BTCUSDT",
		Type:     "snapshot",
		Bids: []marketdata.OrderbookLevel{
			{Price: decimal.RequireFromString("99.5"), Quantity: decimal.RequireFromString("2")},
			{Price: decimal.RequireFromString("99"), Quantity: decimal.RequireFromString("1")},
		},
		Asks: []marketdata.OrderbookLevel{
			{Price: decimal.RequireFromString("100.5"), Quantity: decimal.RequireFromString("3")},
			{Price: decimal.RequireFromString("101"), Quantity: decimal.RequireFromString("1")},
		},
		UpdateID:           10,
		Sequence:           100,
		ExchangeTime:       ts,
		MatchingEngineTime: ts.Add(-10 * time.Millisecond),
	}
}

func testBookDelta(ts time.Time, bids, asks []marketdata.OrderbookLevel) marketdata.Orderbook {
	return marketdata.Orderbook{
		Exchange:           "bybit",
		Category:           "linear",
		Symbol:             "BTCUSDT",
		Type:               "delta",
		Bids:               bids,
		Asks:               asks,
		UpdateID:           11,
		Sequence:           101,
		ExchangeTime:       ts,
		MatchingEngineTime: ts.Add(-10 * time.Millisecond),
	}
}

func assertLevels(t *testing.T, got, want []marketdata.OrderbookLevel) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("level count mismatch: got %#v want %#v", got, want)
	}
	for i := range want {
		if !got[i].Price.Equal(want[i].Price) || !got[i].Quantity.Equal(want[i].Quantity) {
			t.Fatalf("level[%d] mismatch: got %s/%s want %s/%s", i, got[i].Price, got[i].Quantity, want[i].Price, want[i].Quantity)
		}
	}
}

func assertOrderbookValidationCode(t *testing.T, err error, code string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected validation error with code %q", code)
	}
	var validationErr realtime.OrderbookValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("expected OrderbookValidationError, got %T", err)
	}
	for _, problem := range validationErr.Problems {
		if problem.Code == code {
			return
		}
	}
	t.Fatalf("expected problem code %q in %#v", code, validationErr.Problems)
}
