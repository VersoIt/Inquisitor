package validator_test

import (
	"errors"
	"testing"
	"time"

	"github.com/VersoIt/Inquisitor/internal/marketdata"
	"github.com/VersoIt/Inquisitor/internal/marketdata/validator"
)

func TestValidateDataQualityEventTableDriven(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(*marketdata.DataQualityEvent)
		wantErr   bool
		wantCodes []string
	}{
		{
			name: "accepts valid event",
		},
		{
			name: "requires identity",
			mutate: func(event *marketdata.DataQualityEvent) {
				event.Exchange = ""
				event.Symbol = ""
			},
			wantErr:   true,
			wantCodes: []string{"required"},
		},
		{
			name: "requires event metadata",
			mutate: func(event *marketdata.DataQualityEvent) {
				event.EventType = ""
				event.Severity = ""
				event.Message = ""
			},
			wantErr:   true,
			wantCodes: []string{"required"},
		},
		{
			name: "rejects unsupported event type and severity",
			mutate: func(event *marketdata.DataQualityEvent) {
				event.EventType = "UNKNOWN"
				event.Severity = "severe-ish"
			},
			wantErr:   true,
			wantCodes: []string{"unsupported"},
		},
		{
			name: "requires created timestamp",
			mutate: func(event *marketdata.DataQualityEvent) {
				event.CreatedAt = time.Time{}
			},
			wantErr:   true,
			wantCodes: []string{"required"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			event := validDataQualityEvent()
			if tt.mutate != nil {
				tt.mutate(&event)
			}

			err := validator.ValidateDataQualityEvent(event)
			if !tt.wantErr {
				if err != nil {
					t.Fatalf("expected valid event, got %v", err)
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

func validDataQualityEvent() marketdata.DataQualityEvent {
	return marketdata.DataQualityEvent{
		Exchange:  "bybit",
		Symbol:    "BTCUSDT",
		Interval:  "1",
		EventType: marketdata.DataQualityEventCandleGap,
		Severity:  marketdata.DataQualitySeverityWarning,
		Message:   "missing candles detected",
		DataJSON:  []byte(`{"missing_candles":1}`),
		CreatedAt: time.Date(2026, 6, 5, 0, 0, 0, 0, time.UTC),
	}
}
