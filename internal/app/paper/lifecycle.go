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

func (s *Service) persistLifecycleTransition(ctx context.Context, record domainpaper.ValidationRecord, expectedStatus domainpaper.ValidationStatus) (LifecycleResult, error) {
	stats, err := s.records.TransitionValidation(ctx, record, expectedStatus)
	if err != nil {
		return LifecycleResult{}, fmt.Errorf("persist paper validation %q transition: %w", record.ValidationID, err)
	}
	return LifecycleResult{Record: record, Stats: stats}, nil
}
