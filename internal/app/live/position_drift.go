package live

import (
	"context"
	"fmt"
	"strings"
	"time"

	domainlive "github.com/VersoIt/Inquisitor/internal/live"
)

type LivePositionDriftReportRequest struct {
	Queries        []domainlive.PositionSnapshotQuery
	CurrentMaxAge  time.Duration
	BaselineMaxAge time.Duration
}

type LivePositionDriftReport struct {
	Status      domainlive.LiveOpsStatus
	Summary     domainlive.ReadinessCheckSummary
	Checks      []domainlive.ReadinessCheck
	Comparisons []domainlive.PositionDriftComparison
}

func (s *Service) BuildLivePositionDriftReport(
	ctx context.Context,
	req LivePositionDriftReportRequest,
) (LivePositionDriftReport, error) {
	if err := ctx.Err(); err != nil {
		return LivePositionDriftReport{}, err
	}
	if err := s.requireLivePositionDriftDependencies(); err != nil {
		return LivePositionDriftReport{}, err
	}
	if len(req.Queries) == 0 {
		return LivePositionDriftReport{}, fmt.Errorf("live position drift report requires at least one position query")
	}

	now := s.clock.Now()
	currentMaxAge := req.CurrentMaxAge
	if currentMaxAge == 0 {
		currentMaxAge = domainlive.DefaultPositionDriftCurrentMaxAge
	}
	baselineMaxAge := req.BaselineMaxAge
	if baselineMaxAge == 0 {
		baselineMaxAge = domainlive.DefaultPositionDriftBaselineMaxAge
	}

	report := LivePositionDriftReport{}
	for _, query := range req.Queries {
		if err := domainlive.ValidatePositionSnapshotQuery(query); err != nil {
			return LivePositionDriftReport{}, err
		}
		current, err := s.positionReader.GetPositionSnapshot(ctx, query)
		if err != nil {
			return LivePositionDriftReport{}, fmt.Errorf("read current live position snapshot %s: %w", livePositionDriftQueryLabel(query), err)
		}
		baseline, hasBaseline, err := s.positionHistory.GetLatestPositionSnapshot(ctx, query)
		if err != nil {
			return LivePositionDriftReport{}, fmt.Errorf("read DB live position baseline %s: %w", livePositionDriftQueryLabel(query), err)
		}
		comparison, err := domainlive.BuildPositionDriftComparison(domainlive.PositionDriftComparisonRequest{
			Query:          query,
			Current:        current,
			HasBaseline:    hasBaseline,
			Baseline:       baseline,
			Now:            now,
			CurrentMaxAge:  currentMaxAge,
			BaselineMaxAge: baselineMaxAge,
		})
		if err != nil {
			return LivePositionDriftReport{}, err
		}
		report.Comparisons = append(report.Comparisons, comparison)
		report.Checks = append(report.Checks, comparison.Checks...)
	}
	if err := domainlive.ValidateReadinessChecks(report.Checks); err != nil {
		return LivePositionDriftReport{}, err
	}
	status, err := domainlive.SummarizeLiveOpsStatus(report.Checks)
	if err != nil {
		return LivePositionDriftReport{}, err
	}
	report.Summary = domainlive.SummarizeReadinessChecks(report.Checks)
	report.Status = status
	return report, nil
}

func (s *Service) requireLivePositionDriftDependencies() error {
	var problems []string
	if s == nil {
		return fmt.Errorf("live position drift report requires service")
	}
	if s.positionReader == nil {
		problems = append(problems, "position snapshot reader")
	}
	if s.positionHistory == nil {
		problems = append(problems, "position snapshot history reader")
	}
	if s.clock == nil {
		problems = append(problems, "clock")
	}
	if len(problems) > 0 {
		return fmt.Errorf("live position drift report requires %s", strings.Join(problems, ", "))
	}
	return nil
}

func livePositionDriftQueryLabel(query domainlive.PositionSnapshotQuery) string {
	return strings.TrimSpace(query.Exchange) + "/" + strings.TrimSpace(query.Category) + "/" + strings.TrimSpace(query.Symbol)
}
