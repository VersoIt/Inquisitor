package paper

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	domainpaper "github.com/VersoIt/Inquisitor/internal/paper"
)

const maxEquityLedgerEvents = 100_000

type AccountPositionCloseRequest struct {
	ValidationID string
	EventID      string
	CloseID      string
}

type AccountPositionCloseResult struct {
	Record domainpaper.ValidationRecord
	Close  domainpaper.PositionClose
	Event  domainpaper.EquityEvent
	Stats  domainpaper.EquityEventStats
}

func (s *Service) AccountPositionClose(ctx context.Context, req AccountPositionCloseRequest) (AccountPositionCloseResult, error) {
	if err := ctx.Err(); err != nil {
		return AccountPositionCloseResult{}, err
	}
	if s == nil || s.records == nil {
		return AccountPositionCloseResult{}, fmt.Errorf("paper equity accounting service requires validation record repository")
	}
	if s.closes == nil {
		return AccountPositionCloseResult{}, fmt.Errorf("paper equity accounting service requires position close repository")
	}
	if s.equity == nil {
		return AccountPositionCloseResult{}, fmt.Errorf("paper equity accounting service requires equity event repository")
	}
	if s.clock == nil {
		return AccountPositionCloseResult{}, fmt.Errorf("paper equity accounting service requires clock")
	}

	validationID := strings.TrimSpace(req.ValidationID)
	close, err := s.loadPositionClose(ctx, validationID, req.CloseID)
	if err != nil {
		return AccountPositionCloseResult{}, err
	}
	if validationID != "" && close.ValidationID != validationID {
		return AccountPositionCloseResult{}, fmt.Errorf("paper position close %q belongs to validation %q, not %q", close.CloseID, close.ValidationID, validationID)
	}
	record, err := s.loadValidationRecord(ctx, close.ValidationID)
	if err != nil {
		return AccountPositionCloseResult{}, err
	}
	if record.Status != domainpaper.ValidationStatusRunning {
		return AccountPositionCloseResult{}, fmt.Errorf("paper equity accounting requires RUNNING validation status")
	}
	if existing, ok, err := s.loadExistingEquityEventForClose(ctx, close.ValidationID, close.CloseID, req.EventID); err != nil {
		return AccountPositionCloseResult{}, err
	} else if ok {
		stats, err := s.equity.RecordEquityEvent(ctx, existing)
		if err != nil {
			return AccountPositionCloseResult{}, fmt.Errorf("record paper equity event %q: %w", existing.EventID, err)
		}
		return AccountPositionCloseResult{Record: record, Close: close, Event: existing, Stats: stats}, nil
	}

	events, err := s.equity.ListEquityEvents(ctx, domainpaper.EquityEventQuery{
		ValidationID: close.ValidationID,
		Limit:        maxEquityLedgerEvents + 1,
	})
	if err != nil {
		return AccountPositionCloseResult{}, fmt.Errorf("load paper equity ledger %q: %w", close.ValidationID, err)
	}
	if len(events) > maxEquityLedgerEvents {
		return AccountPositionCloseResult{}, fmt.Errorf("paper equity accounting exceeds %d event safety limit", maxEquityLedgerEvents)
	}
	if err := domainpaper.ValidateEquityEventSequence(close.ValidationID, record.InitialBalance, events); err != nil {
		return AccountPositionCloseResult{}, err
	}
	sequence, equityBefore, latestOccurredAt := nextEquityEventState(record.InitialBalance, events)
	if !latestOccurredAt.IsZero() && close.ClosedAt.Before(latestOccurredAt) {
		return AccountPositionCloseResult{}, fmt.Errorf("paper equity accounting close %q occurred before latest equity event", close.CloseID)
	}

	event, err := domainpaper.NewEquityEvent(domainpaper.EquityEventInput{
		EventID:      req.EventID,
		Close:        close,
		Sequence:     sequence,
		EquityBefore: equityBefore,
		RecordedAt:   s.clock.Now(),
	})
	if err != nil {
		return AccountPositionCloseResult{}, err
	}
	stats, err := s.equity.RecordEquityEvent(ctx, event)
	if err != nil {
		return AccountPositionCloseResult{}, fmt.Errorf("record paper equity event %q: %w", event.EventID, err)
	}
	return AccountPositionCloseResult{Record: record, Close: close, Event: event, Stats: stats}, nil
}

func (s *Service) loadPositionClose(ctx context.Context, validationID string, closeID string) (domainpaper.PositionClose, error) {
	closeID = strings.TrimSpace(closeID)
	if closeID == "" {
		return domainpaper.PositionClose{}, fmt.Errorf("close_id is required")
	}
	closes, err := s.closes.ListPositionCloses(ctx, domainpaper.PositionCloseQuery{
		ValidationID: validationID,
		CloseID:      closeID,
		Limit:        2,
	})
	if err != nil {
		return domainpaper.PositionClose{}, fmt.Errorf("load paper position close %q: %w", closeID, err)
	}
	if len(closes) == 0 {
		return domainpaper.PositionClose{}, fmt.Errorf("paper position close %q not found", closeID)
	}
	if len(closes) > 1 {
		return domainpaper.PositionClose{}, fmt.Errorf("paper position close %q is ambiguous", closeID)
	}
	return closes[0], nil
}

func (s *Service) loadExistingEquityEventForClose(ctx context.Context, validationID string, closeID string, eventID string) (domainpaper.EquityEvent, bool, error) {
	existing, err := s.equity.ListEquityEvents(ctx, domainpaper.EquityEventQuery{
		ValidationID: validationID,
		CloseID:      closeID,
		Limit:        2,
	})
	if err != nil {
		return domainpaper.EquityEvent{}, false, fmt.Errorf("check paper close %q equity ledger: %w", closeID, err)
	}
	if len(existing) == 0 {
		return domainpaper.EquityEvent{}, false, nil
	}
	if len(existing) > 1 {
		return domainpaper.EquityEvent{}, false, fmt.Errorf("paper close %q has an inconsistent equity ledger", closeID)
	}
	if existing[0].EventID != strings.TrimSpace(eventID) {
		return domainpaper.EquityEvent{}, false, fmt.Errorf("paper close %q already has equity event %q", closeID, existing[0].EventID)
	}
	return existing[0], true, nil
}

func nextEquityEventState(initialEquity decimal.Decimal, events []domainpaper.EquityEvent) (int, decimal.Decimal, time.Time) {
	sequence := 1
	equityBefore := initialEquity
	var latestOccurredAt time.Time
	for _, event := range events {
		if event.Sequence >= sequence {
			sequence = event.Sequence + 1
			equityBefore = event.EquityAfter
			latestOccurredAt = event.OccurredAt
		}
	}
	return sequence, equityBefore, latestOccurredAt
}
