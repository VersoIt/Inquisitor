package live

import (
	"context"
	"fmt"
	"strings"

	domainlive "github.com/VersoIt/Inquisitor/internal/live"
)

type LiveLoopAuditReportRequest struct {
	RunID             string
	Status            domainlive.LiveLoopRunStatus
	Limit             int
	IncludeIterations bool
}

type LiveLoopAuditReportSummary struct {
	Total     int
	Running   int
	Completed int
	Failed    int
}

type LiveLoopAuditReport struct {
	Query   domainlive.LiveLoopAuditQuery
	Summary LiveLoopAuditReportSummary
	Runs    []domainlive.LiveLoopRunAudit
}

func (s *Service) BuildLiveLoopAuditReport(
	ctx context.Context,
	req LiveLoopAuditReportRequest,
) (LiveLoopAuditReport, error) {
	if err := ctx.Err(); err != nil {
		return LiveLoopAuditReport{}, err
	}
	if s == nil || s.loopAuditReader == nil {
		return LiveLoopAuditReport{}, fmt.Errorf("live loop audit report requires audit reader")
	}

	query := domainlive.LiveLoopAuditQuery{
		RunID:             strings.TrimSpace(req.RunID),
		Status:            req.Status,
		Limit:             req.Limit,
		IncludeIterations: req.IncludeIterations,
	}
	if query.Limit == 0 {
		query.Limit = 10
	}
	if err := domainlive.ValidateLiveLoopAuditQuery(query); err != nil {
		return LiveLoopAuditReport{}, err
	}
	runs, err := s.loopAuditReader.ListLiveLoopRunAudits(ctx, query)
	if err != nil {
		return LiveLoopAuditReport{}, fmt.Errorf("list live loop audit runs: %w", err)
	}
	if err := domainlive.ValidateLiveLoopRunAudits(runs); err != nil {
		return LiveLoopAuditReport{}, err
	}

	report := LiveLoopAuditReport{
		Query: query,
		Runs:  append([]domainlive.LiveLoopRunAudit(nil), runs...),
	}
	for _, run := range runs {
		report.Summary.Total++
		switch run.Status {
		case domainlive.LiveLoopRunStatusRunning:
			report.Summary.Running++
		case domainlive.LiveLoopRunStatusCompleted:
			report.Summary.Completed++
		case domainlive.LiveLoopRunStatusFailed:
			report.Summary.Failed++
		}
	}
	return report, nil
}
