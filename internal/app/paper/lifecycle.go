package paper

import (
	"context"
	"fmt"
	"strings"

	domainpaper "github.com/VersoIt/Inquisitor/internal/paper"
	domainresearch "github.com/VersoIt/Inquisitor/internal/research"
)

type LifecycleResult struct {
	Record domainpaper.ValidationRecord
	Stats  domainpaper.ValidationRecordStats
}

const maxValidationCompletionPositionScanLimit = 100_000

func (s *Service) StartValidation(ctx context.Context, validationID string) (LifecycleResult, error) {
	if err := s.validateLifecycleDependencies(ctx); err != nil {
		return LifecycleResult{}, err
	}
	if s.trades == nil {
		return LifecycleResult{}, fmt.Errorf("paper validation start requires validation trade repository")
	}
	record, err := s.loadValidationRecord(ctx, strings.TrimSpace(validationID))
	if err != nil {
		return LifecycleResult{}, err
	}
	run, err := s.loadResearchRun(ctx, record.RunID)
	if err != nil {
		return LifecycleResult{}, err
	}
	result, err := s.loadResearchResult(ctx, record.RunID)
	if err != nil {
		return LifecycleResult{}, err
	}
	if run.Status != domainresearch.StatusCompleted || result.FinalStatus != domainresearch.StatusCompleted ||
		result.Outcome != domainresearch.OutcomeCandidate || run.RunID != result.RunID || run.Status != result.FinalStatus {
		return LifecycleResult{}, fmt.Errorf("paper validation start requires matching completed CANDIDATE research result")
	}
	existingTrades, err := s.trades.ListValidationTrades(ctx, domainpaper.ValidationTradeQuery{
		ValidationID: record.ValidationID,
		Limit:        1,
	})
	if err != nil {
		return LifecycleResult{}, fmt.Errorf("check paper validation %q journal before start: %w", record.ValidationID, err)
	}
	if len(existingTrades) > 0 {
		return LifecycleResult{}, fmt.Errorf("paper validation start requires an empty journal; offline simulation trades cannot enter a live paper period")
	}

	transitioned, err := domainpaper.StartValidation(record, s.clock.Now())
	if err != nil {
		return LifecycleResult{}, err
	}
	return s.persistLifecycleTransition(ctx, transitioned, domainpaper.ValidationStatusPlanned)
}

func (s *Service) CompleteValidation(ctx context.Context, validationID string) (LifecycleResult, error) {
	if err := s.validateLifecycleDependencies(ctx); err != nil {
		return LifecycleResult{}, err
	}
	record, err := s.loadValidationRecord(ctx, strings.TrimSpace(validationID))
	if err != nil {
		return LifecycleResult{}, err
	}
	transitioned, err := domainpaper.CompleteValidation(record, s.clock.Now())
	if err != nil {
		return LifecycleResult{}, err
	}
	if err := s.validateCompletionPositionJournal(ctx, transitioned.ValidationID); err != nil {
		return LifecycleResult{}, err
	}
	return s.persistLifecycleTransition(ctx, transitioned, domainpaper.ValidationStatusRunning)
}

func (s *Service) CancelValidation(ctx context.Context, validationID, reason string) (LifecycleResult, error) {
	if err := s.validateLifecycleDependencies(ctx); err != nil {
		return LifecycleResult{}, err
	}
	record, err := s.loadValidationRecord(ctx, strings.TrimSpace(validationID))
	if err != nil {
		return LifecycleResult{}, err
	}
	previousStatus := record.Status
	transitioned, err := domainpaper.CancelValidation(record, s.clock.Now(), reason)
	if err != nil {
		return LifecycleResult{}, err
	}
	return s.persistLifecycleTransition(ctx, transitioned, previousStatus)
}

func (s *Service) validateLifecycleDependencies(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s == nil || s.records == nil {
		return fmt.Errorf("paper validation lifecycle requires validation record repository")
	}
	if s.clock == nil {
		return fmt.Errorf("paper validation lifecycle requires clock")
	}
	return nil
}

func (s *Service) validateCompletionPositionJournal(ctx context.Context, validationID string) error {
	if s.positions == nil {
		return fmt.Errorf("paper validation completion requires open position repository")
	}
	if s.closes == nil {
		return fmt.Errorf("paper validation completion requires position close repository")
	}
	if s.equity == nil {
		return fmt.Errorf("paper validation completion requires equity event repository")
	}

	positions, err := s.positions.ListOpenPositions(ctx, domainpaper.OpenPositionQuery{
		ValidationID: validationID,
		Limit:        maxValidationCompletionPositionScanLimit + 1,
	})
	if err != nil {
		return fmt.Errorf("list paper positions before validation completion: %w", err)
	}
	if len(positions) > maxValidationCompletionPositionScanLimit {
		return fmt.Errorf("paper validation completion exceeds position scan safety limit: limit=%d", maxValidationCompletionPositionScanLimit)
	}

	activePositions := 0
	for _, position := range positions {
		closes, err := s.closes.ListPositionCloses(ctx, domainpaper.PositionCloseQuery{
			ValidationID: validationID,
			PositionID:   position.PositionID,
			Limit:        2,
		})
		if err != nil {
			return fmt.Errorf("check paper position %q close status before validation completion: %w", position.PositionID, err)
		}
		if len(closes) > 1 {
			return fmt.Errorf("paper validation completion found inconsistent close journal for position %q", position.PositionID)
		}
		if len(closes) == 0 {
			activePositions++
			continue
		}

		accounted, err := s.paperExitCloseHasEquityEvent(ctx, closes[0].CloseID)
		if err != nil {
			return fmt.Errorf("check paper close %q equity status before validation completion: %w", closes[0].CloseID, err)
		}
		if !accounted {
			return fmt.Errorf("paper validation completion requires all closed positions to have equity ledger events: close_id=%q", closes[0].CloseID)
		}
	}
	if activePositions > 0 {
		return fmt.Errorf("paper validation completion requires no active open positions: active_positions=%d", activePositions)
	}
	return nil
}

func (s *Service) persistLifecycleTransition(ctx context.Context, record domainpaper.ValidationRecord, expectedStatus domainpaper.ValidationStatus) (LifecycleResult, error) {
	stats, err := s.records.TransitionValidation(ctx, record, expectedStatus)
	if err != nil {
		return LifecycleResult{}, fmt.Errorf("persist paper validation %q transition: %w", record.ValidationID, err)
	}
	return LifecycleResult{Record: record, Stats: stats}, nil
}
