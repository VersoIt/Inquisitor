package live

import (
	"context"
	"fmt"

	domainlive "github.com/VersoIt/Inquisitor/internal/live"
)

type LiveFirstOrderReviewReportRequest struct {
	PlanArtifact  domainlive.LiveOrderPlanArtifact
	StatusLimit   int
	PositionLimit int
}

type LiveFirstOrderReviewReport struct {
	Query  domainlive.LiveFirstOrderReviewEvidenceQuery
	Review domainlive.LiveFirstOrderReviewReport
}

func (s *Service) BuildLiveFirstOrderReviewReport(
	ctx context.Context,
	req LiveFirstOrderReviewReportRequest,
) (LiveFirstOrderReviewReport, error) {
	if err := ctx.Err(); err != nil {
		return LiveFirstOrderReviewReport{}, err
	}
	if s == nil || s.firstOrderReview == nil {
		return LiveFirstOrderReviewReport{}, fmt.Errorf("live first-order review requires evidence reader")
	}

	query := domainlive.LiveFirstOrderReviewEvidenceQuery{
		PlanArtifact:  req.PlanArtifact,
		StatusLimit:   req.StatusLimit,
		PositionLimit: req.PositionLimit,
	}
	if query.StatusLimit == 0 {
		query.StatusLimit = domainlive.DefaultLiveFirstOrderReviewStatusLimit
	}
	if query.PositionLimit == 0 {
		query.PositionLimit = domainlive.DefaultLiveFirstOrderReviewPositionLimit
	}
	if err := domainlive.ValidateLiveFirstOrderReviewEvidenceQuery(query); err != nil {
		return LiveFirstOrderReviewReport{}, err
	}

	evidence, err := s.firstOrderReview.ReadLiveFirstOrderReviewEvidence(ctx, query)
	if err != nil {
		return LiveFirstOrderReviewReport{}, fmt.Errorf("read live first-order review evidence: %w", err)
	}
	review, err := domainlive.BuildLiveFirstOrderReviewReport(evidence)
	if err != nil {
		return LiveFirstOrderReviewReport{}, err
	}
	if err := domainlive.ValidateLiveFirstOrderReviewReport(review); err != nil {
		return LiveFirstOrderReviewReport{}, err
	}
	return LiveFirstOrderReviewReport{
		Query:  query,
		Review: review,
	}, nil
}
