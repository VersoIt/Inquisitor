package paper_test

import (
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/VersoIt/Inquisitor/internal/paper"
)

func TestNewEquityEventFromPositionCloseComputesEquity(t *testing.T) {
	close := validPositionClose()
	recordedAt := close.ClosedAt.Add(time.Minute)

	got, err := paper.NewEquityEvent(paper.EquityEventInput{
		EventID:      " paper_equity_0001 ",
		Close:        close,
		Sequence:     1,
		EquityBefore: decimal.RequireFromString("1000"),
		RecordedAt:   recordedAt,
	})
	if err != nil {
		t.Fatalf("new equity event: %v", err)
	}

	if got.EventID != "paper_equity_0001" || got.CloseID != close.CloseID || got.PositionID != close.PositionID ||
		got.ValidationID != close.ValidationID || got.Sequence != 1 {
		t.Fatalf("identity mismatch: %#v", got)
	}
	if !got.NetPnL.Equal(close.NetPnL) || !got.Fees.Equal(close.Fees) ||
		!got.EquityBefore.Equal(decimal.RequireFromString("1000")) ||
		!got.EquityAfter.Equal(decimal.RequireFromString("1389.7")) {
		t.Fatalf("equity math mismatch: %#v", got)
	}
	if !got.OccurredAt.Equal(close.ClosedAt.UTC()) || !got.RecordedAt.Equal(recordedAt.UTC()) {
		t.Fatalf("timestamp mismatch: %#v", got)
	}
}

func TestValidateEquityEventRejectsInvalidInputsTableDriven(t *testing.T) {
	valid := validEquityEvent()
	tests := []struct {
		name       string
		mutate     func(*paper.EquityEvent)
		wantErrSub string
	}{
		{"missing event id", func(e *paper.EquityEvent) { e.EventID = " " }, "event_id"},
		{"missing validation id", func(e *paper.EquityEvent) { e.ValidationID = "" }, "validation_id"},
		{"missing close id", func(e *paper.EquityEvent) { e.CloseID = "" }, "close_id"},
		{"missing position id", func(e *paper.EquityEvent) { e.PositionID = "" }, "position_id"},
		{"uppercase exchange", func(e *paper.EquityEvent) { e.Exchange = "BYBIT" }, "exchange"},
		{"lowercase symbol", func(e *paper.EquityEvent) { e.Symbol = "btcusdt" }, "symbol"},
		{"unsupported interval", func(e *paper.EquityEvent) { e.Interval = "2" }, "interval"},
		{"zero sequence", func(e *paper.EquityEvent) { e.Sequence = 0 }, "sequence"},
		{"negative fees", func(e *paper.EquityEvent) { e.Fees = decimal.RequireFromString("-1") }, "fees"},
		{"zero equity before", func(e *paper.EquityEvent) { e.EquityBefore = decimal.Zero }, "equity_before"},
		{"equity after mismatch", func(e *paper.EquityEvent) { e.EquityAfter = e.EquityAfter.Add(decimal.RequireFromString("0.01")) }, "equity_after"},
		{"negative equity after", func(e *paper.EquityEvent) {
			e.NetPnL = decimal.RequireFromString("-1001")
			e.EquityAfter = decimal.RequireFromString("-1")
		}, "equity_after"},
		{"missing occurred at", func(e *paper.EquityEvent) { e.OccurredAt = time.Time{} }, "occurred_at"},
		{"missing recorded at", func(e *paper.EquityEvent) { e.RecordedAt = time.Time{} }, "recorded_at"},
		{"recorded before occurred", func(e *paper.EquityEvent) { e.RecordedAt = e.OccurredAt.Add(-time.Nanosecond) }, "recorded_at"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			event := valid
			tt.mutate(&event)

			err := paper.ValidateEquityEvent(event)
			if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
		})
	}
}

func TestValidateEquityEventSequenceTableDriven(t *testing.T) {
	first := validEquityEvent()
	second := nextEquityEvent(t, first)
	tests := []struct {
		name       string
		events     []paper.EquityEvent
		mutate     func([]paper.EquityEvent) []paper.EquityEvent
		wantErrSub string
	}{
		{"valid ordered sequence", []paper.EquityEvent{first, second}, nil, ""},
		{"valid unordered input", []paper.EquityEvent{second, first}, nil, ""},
		{"sequence gap", []paper.EquityEvent{first, second}, func(events []paper.EquityEvent) []paper.EquityEvent { events[1].Sequence = 3; return events }, "sequence"},
		{"validation mismatch", []paper.EquityEvent{first, second}, func(events []paper.EquityEvent) []paper.EquityEvent {
			events[1].ValidationID = "paper_validation_other"
			return events
		}, "validation_id"},
		{"duplicate event id", []paper.EquityEvent{first, second}, func(events []paper.EquityEvent) []paper.EquityEvent {
			events[1].EventID = events[0].EventID
			return events
		}, "duplicate event_id"},
		{"duplicate close id", []paper.EquityEvent{first, second}, func(events []paper.EquityEvent) []paper.EquityEvent {
			events[1].CloseID = events[0].CloseID
			return events
		}, "duplicate close_id"},
		{"duplicate position id", []paper.EquityEvent{first, second}, func(events []paper.EquityEvent) []paper.EquityEvent {
			events[1].PositionID = events[0].PositionID
			return events
		}, "duplicate position_id"},
		{"equity continuity break", []paper.EquityEvent{first, second}, func(events []paper.EquityEvent) []paper.EquityEvent {
			events[1].EquityBefore = events[1].EquityBefore.Add(decimal.NewFromInt(1))
			events[1].EquityAfter = events[1].EquityBefore.Add(events[1].NetPnL)
			return events
		}, "continuity"},
		{"occurred at not monotonic", []paper.EquityEvent{first, second}, func(events []paper.EquityEvent) []paper.EquityEvent {
			events[1].OccurredAt = events[0].OccurredAt.Add(-time.Nanosecond)
			events[1].RecordedAt = events[1].OccurredAt.Add(time.Second)
			return events
		}, "occurred_at"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			events := append([]paper.EquityEvent(nil), tt.events...)
			if tt.mutate != nil {
				events = tt.mutate(events)
			}

			err := paper.ValidateEquityEventSequence(first.ValidationID, decimal.RequireFromString("1000"), events)
			if tt.wantErrSub == "" {
				if err != nil {
					t.Fatalf("validate equity event sequence: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
		})
	}
}

func TestValidateEquityEventSequenceRejectsBadInputs(t *testing.T) {
	if err := paper.ValidateEquityEventSequence(" ", decimal.NewFromInt(1000), nil); err == nil || !strings.Contains(err.Error(), "validation_id") {
		t.Fatalf("expected validation_id error, got %v", err)
	}
	if err := paper.ValidateEquityEventSequence("paper_validation_0001", decimal.Zero, nil); err == nil || !strings.Contains(err.Error(), "initial_equity") {
		t.Fatalf("expected initial_equity error, got %v", err)
	}
}

func TestValidateEquityEventQueryRejectsInvalidInputsTableDriven(t *testing.T) {
	start := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name       string
		query      paper.EquityEventQuery
		wantErrSub string
	}{
		{"valid empty query", paper.EquityEventQuery{}, ""},
		{"valid filtered query", paper.EquityEventQuery{Symbol: "BTCUSDT", Interval: "1", Start: start, End: start.Add(time.Hour), Limit: 10}, ""},
		{"lowercase symbol", paper.EquityEventQuery{Symbol: "btcusdt"}, "symbol"},
		{"unsupported interval", paper.EquityEventQuery{Interval: "2"}, "interval"},
		{"end before start", paper.EquityEventQuery{Start: start, End: start}, "end"},
		{"negative limit", paper.EquityEventQuery{Limit: -1}, "limit"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := paper.ValidateEquityEventQuery(tt.query)
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

func validEquityEvent() paper.EquityEvent {
	close := validPositionClose()
	return paper.EquityEvent{
		EventID:      "paper_equity_0001",
		ValidationID: close.ValidationID,
		CloseID:      close.CloseID,
		PositionID:   close.PositionID,
		Exchange:     close.Exchange,
		Category:     close.Category,
		Symbol:       close.Symbol,
		Interval:     close.Interval,
		Sequence:     1,
		NetPnL:       close.NetPnL,
		Fees:         close.Fees,
		EquityBefore: decimal.RequireFromString("1000"),
		EquityAfter:  decimal.RequireFromString("1389.7"),
		OccurredAt:   close.ClosedAt,
		RecordedAt:   close.RecordedAt,
	}
}

func nextEquityEvent(t *testing.T, previous paper.EquityEvent) paper.EquityEvent {
	t.Helper()

	close := validPositionClose()
	close.CloseID = "paper_close_0002"
	close.PositionID = "paper_position_0002"
	close.ClosedAt = previous.OccurredAt.Add(time.Hour)
	close.RecordedAt = close.ClosedAt.Add(time.Second)
	event, err := paper.NewEquityEvent(paper.EquityEventInput{
		EventID:      "paper_equity_0002",
		Close:        close,
		Sequence:     previous.Sequence + 1,
		EquityBefore: previous.EquityAfter,
		RecordedAt:   close.RecordedAt,
	})
	if err != nil {
		t.Fatalf("new next equity event: %v", err)
	}
	return event
}
