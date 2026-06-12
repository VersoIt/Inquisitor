package research

import (
	"context"
	"fmt"
	"strings"

	domainresearch "github.com/VersoIt/Inquisitor/internal/research"
)

type RecordResultRequest struct {
	RunID       string
	FinalStatus domainresearch.Status
	Outcome     domainresearch.Outcome
	Summary     string
	Metrics     domainresearch.Metrics
	Reasons     []string
}

type RecordResultResult struct {
	Run    domainresearch.Run
	Result domainresearch.Result
	Stats  domainresearch.RecordResultStats
}

func (s *Service) RecordResult(ctx context.Context, req RecordResultRequest) (RecordResultResult, error) {
	if err := ctx.Err(); err != nil {
		return RecordResultResult{}, err
	}
	if s == nil || s.runs == nil {
		return RecordResultResult{}, fmt.Errorf("research result service requires research run repository")
	}
	if s.results == nil {
		return RecordResultResult{}, fmt.Errorf("research result service requires result recorder")
	}
	if s.clock == nil {
		return RecordResultResult{}, fmt.Errorf("research result service requires clock")
	}

	runID := strings.TrimSpace(req.RunID)
	if runID == "" {
		return RecordResultResult{}, fmt.Errorf("run_id is required")
	}
	runs, err := s.runs.ListRuns(ctx, domainresearch.Query{
		RunID: runID,
		Limit: 2,
	})
	if err != nil {
		return RecordResultResult{}, fmt.Errorf("load research run %q: %w", runID, err)
	}
	if len(runs) == 0 {
		return RecordResultResult{}, fmt.Errorf("research run %q not found", runID)
	}
	if len(runs) > 1 {
		return RecordResultResult{}, fmt.Errorf("research run %q is ambiguous", runID)
	}

	result, err := domainresearch.NewResult(domainresearch.ResultInput{
		RunID:       runs[0].RunID,
		FinalStatus: req.FinalStatus,
		Outcome:     req.Outcome,
		Summary:     req.Summary,
		Metrics:     req.Metrics,
		Reasons:     req.Reasons,
		RecordedAt:  s.clock.Now(),
	})
	if err != nil {
		return RecordResultResult{}, err
	}
	finalRun, err := domainresearch.FinalizeRun(runs[0], result)
	if err != nil {
		return RecordResultResult{}, err
	}

	stats, err := s.results.RecordResult(ctx, finalRun, result)
	if err != nil {
		return RecordResultResult{}, fmt.Errorf("record research result %q: %w", finalRun.RunID, err)
	}
	return RecordResultResult{
		Run:    finalRun,
		Result: result,
		Stats:  stats,
	}, nil
}
