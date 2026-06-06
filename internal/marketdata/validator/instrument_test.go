package validator_test

import (
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/VersoIt/Inquisitor/internal/marketdata"
	"github.com/VersoIt/Inquisitor/internal/marketdata/validator"
)

func TestValidateInstrumentTableDriven(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(*marketdata.Instrument)
		wantCodes []string
		wantErr   bool
	}{
		{
			name: "accepts valid instrument",
		},
		{
			name: "requires identity fields",
			mutate: func(instrument *marketdata.Instrument) {
				instrument.Exchange = ""
				instrument.Symbol = ""
			},
			wantErr:   true,
			wantCodes: []string{"required"},
		},
		{
			name: "rejects invalid trading constraints",
			mutate: func(instrument *marketdata.Instrument) {
				instrument.TickSize = decimal.Zero
				instrument.QtyStep = decimal.RequireFromString("-0.01")
				instrument.MaxOrderQty = decimal.RequireFromString("0.001")
				instrument.MinOrderQty = decimal.RequireFromString("0.01")
			},
			wantErr:   true,
			wantCodes: []string{"must_be_positive", "less_than_min_order_qty"},
		},
		{
			name: "requires updated timestamp",
			mutate: func(instrument *marketdata.Instrument) {
				instrument.UpdatedAt = time.Time{}
			},
			wantErr:   true,
			wantCodes: []string{"required"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			instrument := validInstrument("BTCUSDT")
			if tt.mutate != nil {
				tt.mutate(&instrument)
			}

			err := validator.ValidateInstrument(instrument)
			if !tt.wantErr {
				if err != nil {
					t.Fatalf("expected valid instrument, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("expected validation error")
			}

			var validationErr validator.CandleValidationError
			if !errors.As(err, &validationErr) {
				t.Fatalf("expected CandleValidationError, got %T", err)
			}
			for _, code := range tt.wantCodes {
				assertProblemCode(t, validationErr.Problems, code)
			}
		})
	}
}

func TestValidateInstrumentsRejectsDuplicateIdentity(t *testing.T) {
	err := validator.ValidateInstruments([]marketdata.Instrument{
		validInstrument("BTCUSDT"),
		validInstrument("BTCUSDT"),
	})
	if err == nil {
		t.Fatal("expected duplicate instrument error")
	}

	var validationErr validator.CandleValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("expected CandleValidationError, got %T", err)
	}
	assertProblemCode(t, validationErr.Problems, "duplicate")
}

func validInstrument(symbol string) marketdata.Instrument {
	return marketdata.Instrument{
		Exchange:           "bybit",
		Category:           "linear",
		Symbol:             symbol,
		BaseCoin:           symbol[:3],
		QuoteCoin:          "USDT",
		Status:             "Trading",
		TickSize:           decimal.RequireFromString("0.10"),
		QtyStep:            decimal.RequireFromString("0.001"),
		MinOrderQty:        decimal.RequireFromString("0.001"),
		MaxOrderQty:        decimal.RequireFromString("100"),
		MaxMarketOrderQty:  decimal.RequireFromString("50"),
		MinNotionalValue:   decimal.RequireFromString("5"),
		PriceScale:         2,
		LeverageFilterJSON: []byte(`{"maxLeverage":"100"}`),
		PriceFilterJSON:    []byte(`{"tickSize":"0.10"}`),
		LotSizeFilterJSON:  []byte(`{"qtyStep":"0.001"}`),
		RawJSON:            []byte(`{"symbol":"BTCUSDT"}`),
		UpdatedAt:          time.Date(2026, 6, 5, 0, 0, 0, 0, time.UTC),
	}
}
