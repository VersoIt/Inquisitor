package realtime_test

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/VersoIt/Inquisitor/internal/marketdata"
	"github.com/VersoIt/Inquisitor/internal/marketdata/realtime"
	"github.com/VersoIt/Inquisitor/internal/marketdata/validator"
)

func TestCheckFreshness(t *testing.T) {
	dataTime := time.Date(2026, 6, 7, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name         string
		dataTime     time.Time
		observedAt   time.Time
		maxStaleness time.Duration
		wantAge      time.Duration
		wantStale    bool
		wantErr      bool
	}{
		{
			name:         "fresh inside threshold",
			dataTime:     dataTime,
			observedAt:   dataTime.Add(2 * time.Second),
			maxStaleness: 3 * time.Second,
			wantAge:      2 * time.Second,
		},
		{
			name:         "stale beyond threshold",
			dataTime:     dataTime,
			observedAt:   dataTime.Add(4 * time.Second),
			maxStaleness: 3 * time.Second,
			wantAge:      4 * time.Second,
			wantStale:    true,
		},
		{
			name:         "exchange timestamp ahead is clamped to zero age",
			dataTime:     dataTime.Add(time.Second),
			observedAt:   dataTime,
			maxStaleness: 3 * time.Second,
			wantAge:      0,
		},
		{
			name:         "missing data time",
			observedAt:   dataTime,
			maxStaleness: 3 * time.Second,
			wantErr:      true,
		},
		{
			name:       "invalid policy",
			dataTime:   dataTime,
			observedAt: dataTime,
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := realtime.CheckFreshness(tt.dataTime, tt.observedAt, tt.maxStaleness)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Age != tt.wantAge {
				t.Fatalf("age mismatch: got %s want %s", got.Age, tt.wantAge)
			}
			if got.Stale != tt.wantStale {
				t.Fatalf("stale mismatch: got %v want %v", got.Stale, tt.wantStale)
			}
		})
	}
}

func TestCalculateOrderbookSpread(t *testing.T) {
	tests := []struct {
		name        string
		book        marketdata.Orderbook
		wantSpread  decimal.Decimal
		wantBPS     decimal.Decimal
		wantErrCode string
	}{
		{
			name:       "valid snapshot",
			book:       validOrderbookSnapshot(),
			wantSpread: mustDecimal("1"),
			wantBPS:    mustDecimal("100"),
		},
		{
			name: "rejects crossed snapshot",
			book: func() marketdata.Orderbook {
				book := validOrderbookSnapshot()
				book.Bids[0].Price = mustDecimal("101")
				return book
			}(),
			wantErrCode: "crossed",
		},
		{
			name: "rejects unsorted asks",
			book: func() marketdata.Orderbook {
				book := validOrderbookSnapshot()
				book.Asks = []marketdata.OrderbookLevel{
					{Price: mustDecimal("101"), Quantity: mustDecimal("1")},
					{Price: mustDecimal("100.5"), Quantity: mustDecimal("1")},
				}
				return book
			}(),
			wantErrCode: "not_sorted",
		},
		{
			name: "rejects delta because spread needs full snapshot",
			book: func() marketdata.Orderbook {
				book := validOrderbookSnapshot()
				book.Type = "delta"
				return book
			}(),
			wantErrCode: "snapshot_required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := realtime.CalculateOrderbookSpread(tt.book)
			if tt.wantErrCode != "" {
				assertOrderbookProblemCode(t, err, tt.wantErrCode)
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !got.Spread.Equal(tt.wantSpread) {
				t.Fatalf("spread mismatch: got %s want %s", got.Spread, tt.wantSpread)
			}
			if !got.SpreadBPS.Equal(tt.wantBPS) {
				t.Fatalf("spread bps mismatch: got %s want %s", got.SpreadBPS, tt.wantBPS)
			}
		})
	}
}

func TestAssessOrderbookSnapshot(t *testing.T) {
	dataTime := time.Date(2026, 6, 7, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name              string
		book              marketdata.Orderbook
		observedAt        time.Time
		policy            realtime.QualityPolicy
		wantValid         bool
		wantStale         bool
		wantSpreadTooWide bool
		wantEventTypes    []string
		wantPolicyError   bool
		wantPayloadKey    string
		wantPayloadValue  string
	}{
		{
			name:       "healthy snapshot has no events",
			book:       validOrderbookSnapshotWithTime(dataTime),
			observedAt: dataTime.Add(time.Second),
			policy: realtime.QualityPolicy{
				MaxStaleness: 3 * time.Second,
				MaxSpreadBPS: mustDecimal("150"),
			},
			wantValid: true,
		},
		{
			name:       "stale and too wide snapshot emits quality events",
			book:       validOrderbookSnapshotWithTime(dataTime),
			observedAt: dataTime.Add(5 * time.Second),
			policy: realtime.QualityPolicy{
				MaxStaleness: 3 * time.Second,
				MaxSpreadBPS: mustDecimal("50"),
			},
			wantValid:         true,
			wantStale:         true,
			wantSpreadTooWide: true,
			wantEventTypes: []string{
				marketdata.DataQualityEventStaleData,
				marketdata.DataQualityEventSpreadTooWide,
			},
			wantPayloadKey:   "spread_bps",
			wantPayloadValue: "100",
		},
		{
			name: "invalid snapshot emits critical quality event",
			book: func() marketdata.Orderbook {
				book := validOrderbookSnapshotWithTime(dataTime)
				book.Asks = nil
				return book
			}(),
			observedAt: dataTime.Add(time.Second),
			policy: realtime.QualityPolicy{
				MaxStaleness: 3 * time.Second,
			},
			wantEventTypes: []string{marketdata.DataQualityEventOrderbookInvalid},
			wantPayloadKey: "reason",
		},
		{
			name:       "invalid policy is rejected",
			book:       validOrderbookSnapshotWithTime(dataTime),
			observedAt: dataTime.Add(time.Second),
			policy: realtime.QualityPolicy{
				MaxStaleness: -time.Second,
			},
			wantPolicyError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assessment, events, err := realtime.AssessOrderbookSnapshot(tt.book, tt.observedAt, tt.policy)
			if tt.wantPolicyError {
				if err == nil {
					t.Fatal("expected policy error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if assessment.Valid != tt.wantValid {
				t.Fatalf("valid mismatch: got %v want %v", assessment.Valid, tt.wantValid)
			}
			if assessment.Stale != tt.wantStale {
				t.Fatalf("stale mismatch: got %v want %v", assessment.Stale, tt.wantStale)
			}
			if assessment.SpreadTooWide != tt.wantSpreadTooWide {
				t.Fatalf("spread too wide mismatch: got %v want %v", assessment.SpreadTooWide, tt.wantSpreadTooWide)
			}
			assertEventTypes(t, events, tt.wantEventTypes)
			if err := validator.ValidateDataQualityEvents(events); err != nil {
				t.Fatalf("quality events should be valid: %v", err)
			}
			if tt.wantPayloadKey != "" {
				payload := map[string]string{}
				if err := json.Unmarshal(events[len(events)-1].DataJSON, &payload); err != nil {
					t.Fatalf("decode payload: %v", err)
				}
				if _, ok := payload[tt.wantPayloadKey]; !ok {
					t.Fatalf("expected payload key %q in %#v", tt.wantPayloadKey, payload)
				}
				if tt.wantPayloadValue != "" && payload[tt.wantPayloadKey] != tt.wantPayloadValue {
					t.Fatalf("payload value mismatch: got %q want %q", payload[tt.wantPayloadKey], tt.wantPayloadValue)
				}
			}
		})
	}
}

func validOrderbookSnapshot() marketdata.Orderbook {
	return validOrderbookSnapshotWithTime(time.Date(2026, 6, 7, 0, 0, 0, 0, time.UTC))
}

func validOrderbookSnapshotWithTime(exchangeTime time.Time) marketdata.Orderbook {
	return marketdata.Orderbook{
		Exchange: "bybit",
		Category: "linear",
		Symbol:   "BTCUSDT",
		Type:     "snapshot",
		Bids: []marketdata.OrderbookLevel{
			{Price: mustDecimal("99.5"), Quantity: mustDecimal("2")},
			{Price: mustDecimal("99"), Quantity: mustDecimal("1")},
		},
		Asks: []marketdata.OrderbookLevel{
			{Price: mustDecimal("100.5"), Quantity: mustDecimal("3")},
			{Price: mustDecimal("101"), Quantity: mustDecimal("1")},
		},
		UpdateID:           100,
		Sequence:           200,
		ExchangeTime:       exchangeTime,
		MatchingEngineTime: exchangeTime.Add(-10 * time.Millisecond),
	}
}

func mustDecimal(value string) decimal.Decimal {
	parsed, err := decimal.NewFromString(value)
	if err != nil {
		panic(err)
	}
	return parsed
}

func assertOrderbookProblemCode(t *testing.T, err error, code string) {
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

func assertEventTypes(t *testing.T, events []marketdata.DataQualityEvent, want []string) {
	t.Helper()
	if len(events) != len(want) {
		t.Fatalf("event count mismatch: got %d want %d (%#v)", len(events), len(want), events)
	}
	for i := range want {
		if events[i].EventType != want[i] {
			t.Fatalf("event[%d] type mismatch: got %q want %q", i, events[i].EventType, want[i])
		}
	}
}
