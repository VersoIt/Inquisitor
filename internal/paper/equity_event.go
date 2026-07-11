package paper

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/VersoIt/Inquisitor/internal/marketdata"
)

type EquityEvent struct {
	EventID      string
	ValidationID string
	CloseID      string
	PositionID   string
	Exchange     string
	Category     string
	Symbol       string
	Interval     string
	Sequence     int
	NetPnL       decimal.Decimal
	Fees         decimal.Decimal
	EquityBefore decimal.Decimal
	EquityAfter  decimal.Decimal
	OccurredAt   time.Time
	RecordedAt   time.Time
}

type EquityEventInput struct {
	EventID      string
	Close        PositionClose
	Sequence     int
	EquityBefore decimal.Decimal
	RecordedAt   time.Time
}

type EquityEventStats struct {
	Inserted int
	Skipped  int
}

type EquityEventQuery struct {
	EventID      string
	ValidationID string
	CloseID      string
	PositionID   string
	Symbol       string
	Interval     string
	Start        time.Time
	End          time.Time
	Limit        int
}

type EquityEventRepository interface {
	RecordEquityEvent(ctx context.Context, event EquityEvent) (EquityEventStats, error)
	ListEquityEvents(ctx context.Context, query EquityEventQuery) ([]EquityEvent, error)
}

func NewEquityEvent(input EquityEventInput) (EquityEvent, error) {
	if err := ValidatePositionClose(input.Close); err != nil {
		return EquityEvent{}, fmt.Errorf("paper equity event requires valid position close: %w", err)
	}
	event := EquityEvent{
		EventID:      strings.TrimSpace(input.EventID),
		ValidationID: input.Close.ValidationID,
		CloseID:      input.Close.CloseID,
		PositionID:   input.Close.PositionID,
		Exchange:     input.Close.Exchange,
		Category:     input.Close.Category,
		Symbol:       input.Close.Symbol,
		Interval:     input.Close.Interval,
		Sequence:     input.Sequence,
		NetPnL:       input.Close.NetPnL,
		Fees:         input.Close.Fees,
		EquityBefore: input.EquityBefore,
		EquityAfter:  input.EquityBefore.Add(input.Close.NetPnL),
		OccurredAt:   input.Close.ClosedAt.UTC(),
		RecordedAt:   input.RecordedAt.UTC(),
	}
	if err := ValidateEquityEvent(event); err != nil {
		return EquityEvent{}, err
	}
	return event, nil
}

func (s EquityEventStats) Total() int {
	return s.Inserted + s.Skipped
}

func ValidateEquityEvent(event EquityEvent) error {
	var problems []string
	addRequired := func(field, value string) {
		if strings.TrimSpace(value) == "" {
			problems = append(problems, field+" is required")
		}
	}

	addRequired("event_id", event.EventID)
	addRequired("validation_id", event.ValidationID)
	addRequired("close_id", event.CloseID)
	addRequired("position_id", event.PositionID)
	addRequired("exchange", event.Exchange)
	addRequired("category", event.Category)
	addRequired("symbol", event.Symbol)
	addRequired("interval", event.Interval)
	if trimmed := strings.TrimSpace(event.Exchange); trimmed != "" && trimmed != strings.ToLower(trimmed) {
		problems = append(problems, "exchange must be lowercase")
	}
	if trimmed := strings.TrimSpace(event.Category); trimmed != "" && trimmed != strings.ToLower(trimmed) {
		problems = append(problems, "category must be lowercase")
	}
	if trimmed := strings.TrimSpace(event.Symbol); trimmed != "" && trimmed != strings.ToUpper(trimmed) {
		problems = append(problems, "symbol must be uppercase")
	}
	if strings.TrimSpace(event.Interval) != "" {
		if _, err := marketdata.IntervalDuration(strings.TrimSpace(event.Interval)); err != nil {
			problems = append(problems, "interval is unsupported")
		}
	}
	if event.Sequence <= 0 {
		problems = append(problems, "sequence must be positive")
	}
	if event.Fees.IsNegative() {
		problems = append(problems, "fees must be greater than or equal to zero")
	}
	if event.EquityBefore.LessThanOrEqual(decimal.Zero) {
		problems = append(problems, "equity_before must be positive")
	}
	expectedEquityAfter := event.EquityBefore.Add(event.NetPnL)
	if !event.EquityAfter.Equal(expectedEquityAfter) {
		problems = append(problems, "equity_after must equal equity_before plus net_pnl")
	}
	if event.EquityAfter.IsNegative() {
		problems = append(problems, "equity_after must be greater than or equal to zero")
	}
	if event.OccurredAt.IsZero() {
		problems = append(problems, "occurred_at is required")
	}
	if event.RecordedAt.IsZero() {
		problems = append(problems, "recorded_at is required")
	}
	if !event.OccurredAt.IsZero() && !event.RecordedAt.IsZero() && event.RecordedAt.Before(event.OccurredAt) {
		problems = append(problems, "recorded_at must not be before occurred_at")
	}
	if len(problems) > 0 {
		return errors.New("paper equity event validation failed: " + strings.Join(problems, "; "))
	}
	return nil
}

func ValidateEquityEvents(events []EquityEvent) error {
	for index, event := range events {
		if err := ValidateEquityEvent(event); err != nil {
			return fmt.Errorf("paper_equity_event[%d]: %w", index, err)
		}
	}
	return nil
}

func ValidateEquityEventSequence(validationID string, initialEquity decimal.Decimal, events []EquityEvent) error {
	validationID = strings.TrimSpace(validationID)
	if validationID == "" {
		return errors.New("paper equity event sequence failed: validation_id is required")
	}
	if initialEquity.LessThanOrEqual(decimal.Zero) {
		return errors.New("paper equity event sequence failed: initial_equity must be positive")
	}
	if len(events) == 0 {
		return nil
	}

	ordered := append([]EquityEvent(nil), events...)
	slices.SortStableFunc(ordered, func(left, right EquityEvent) int {
		if left.Sequence != right.Sequence {
			return left.Sequence - right.Sequence
		}
		return strings.Compare(left.EventID, right.EventID)
	})
	seenEvents := make(map[string]struct{}, len(ordered))
	seenCloses := make(map[string]struct{}, len(ordered))
	seenPositions := make(map[string]struct{}, len(ordered))
	equity := initialEquity
	var previousOccurredAt time.Time
	for index, event := range ordered {
		if err := ValidateEquityEvent(event); err != nil {
			return fmt.Errorf("paper equity event sequence failed: event[%d]: %w", index, err)
		}
		if event.ValidationID != validationID {
			return fmt.Errorf("paper equity event sequence failed: event[%d] validation_id mismatch", index)
		}
		if event.Sequence != index+1 {
			return fmt.Errorf("paper equity event sequence failed: event[%d] sequence must be %d", index, index+1)
		}
		if _, exists := seenEvents[event.EventID]; exists {
			return fmt.Errorf("paper equity event sequence failed: duplicate event_id %q", event.EventID)
		}
		seenEvents[event.EventID] = struct{}{}
		if _, exists := seenCloses[event.CloseID]; exists {
			return fmt.Errorf("paper equity event sequence failed: duplicate close_id %q", event.CloseID)
		}
		seenCloses[event.CloseID] = struct{}{}
		if _, exists := seenPositions[event.PositionID]; exists {
			return fmt.Errorf("paper equity event sequence failed: duplicate position_id %q", event.PositionID)
		}
		seenPositions[event.PositionID] = struct{}{}
		if !event.EquityBefore.Equal(equity) {
			return fmt.Errorf("paper equity event sequence failed: event[%d] breaks equity continuity", index)
		}
		if index > 0 && event.OccurredAt.Before(previousOccurredAt) {
			return fmt.Errorf("paper equity event sequence failed: event[%d] occurred_at is not monotonic", index)
		}
		equity = event.EquityAfter
		previousOccurredAt = event.OccurredAt
	}
	return nil
}

func ValidateEquityEventQuery(query EquityEventQuery) error {
	if strings.TrimSpace(query.Symbol) != "" && strings.TrimSpace(query.Symbol) != strings.ToUpper(strings.TrimSpace(query.Symbol)) {
		return errors.New("symbol must be uppercase")
	}
	if strings.TrimSpace(query.Interval) != "" {
		if _, err := marketdata.IntervalDuration(strings.TrimSpace(query.Interval)); err != nil {
			return errors.New("interval is unsupported")
		}
	}
	if !query.Start.IsZero() && !query.End.IsZero() && !query.End.After(query.Start) {
		return errors.New("end must be after start")
	}
	if query.Limit < 0 {
		return errors.New("limit must be greater than or equal to zero")
	}
	return nil
}
