package live

import (
	"context"
	"fmt"
	"strings"
	"time"

	domainlive "github.com/VersoIt/Inquisitor/internal/live"
)

type PendingLiveDecisionReportRequest struct {
	Symbol string
	Limit  int
}

type PendingLiveDecisionReportSummary struct {
	Total      int
	OldestAt   time.Time
	NewestAt   time.Time
	NextID     string
	NextSymbol string
}

type PendingLiveDecisionReport struct {
	Query      domainlive.PendingLiveDecisionQuery
	Summary    PendingLiveDecisionReportSummary
	Candidates []domainlive.PendingLiveDecision
}

func (s *Service) BuildPendingLiveDecisionReport(
	ctx context.Context,
	req PendingLiveDecisionReportRequest,
) (PendingLiveDecisionReport, error) {
	if err := ctx.Err(); err != nil {
		return PendingLiveDecisionReport{}, err
	}
	if s == nil || s.pendingDecisions == nil {
		return PendingLiveDecisionReport{}, fmt.Errorf("pending live decision report requires pending decision reader")
	}
	query := domainlive.PendingLiveDecisionQuery{
		Symbol: strings.ToUpper(strings.TrimSpace(req.Symbol)),
		Limit:  req.Limit,
	}
	if req.Symbol != strings.TrimSpace(req.Symbol) {
		query.Symbol = req.Symbol
	}
	if query.Limit == 0 {
		query.Limit = 10
	}
	if err := domainlive.ValidatePendingLiveDecisionQuery(query); err != nil {
		return PendingLiveDecisionReport{}, err
	}
	candidates, err := s.pendingDecisions.ListPendingLiveDecisions(ctx, query)
	if err != nil {
		return PendingLiveDecisionReport{}, fmt.Errorf("list pending live decisions: %w", err)
	}
	if err := domainlive.ValidatePendingLiveDecisions(candidates); err != nil {
		return PendingLiveDecisionReport{}, err
	}
	report := PendingLiveDecisionReport{
		Query:      query,
		Candidates: append([]domainlive.PendingLiveDecision(nil), candidates...),
	}
	for index, candidate := range candidates {
		createdAt := candidate.Decision.Decision.CreatedAt
		if index == 0 {
			report.Summary.NextID = candidate.Decision.DecisionID
			report.Summary.NextSymbol = candidate.Decision.Symbol
			report.Summary.OldestAt = createdAt
			report.Summary.NewestAt = createdAt
		}
		if !createdAt.IsZero() && (report.Summary.OldestAt.IsZero() || createdAt.Before(report.Summary.OldestAt)) {
			report.Summary.OldestAt = createdAt
		}
		if createdAt.After(report.Summary.NewestAt) {
			report.Summary.NewestAt = createdAt
		}
		report.Summary.Total++
	}
	return report, nil
}
