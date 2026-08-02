package live

import (
	"context"
	"fmt"
	"strings"
	"time"

	domainlive "github.com/VersoIt/Inquisitor/internal/live"
	domainrisk "github.com/VersoIt/Inquisitor/internal/risk"
)

type LiveOpsReportRequest struct {
	PendingSymbol                   string
	PendingLimit                    int
	AuditLimit                      int
	HasFirstOrderReviewArtifact     bool
	FirstOrderReviewArtifact        domainlive.LiveFirstOrderReviewArtifact
	RequireFirstOrderReviewArtifact bool
	MaxFirstOrderReviewArtifactAge  time.Duration
	PositionDriftQueries            []domainlive.PositionSnapshotQuery
	PositionDriftCurrentMaxAge      time.Duration
	PositionDriftBaselineMaxAge     time.Duration
}

type LiveOpsReport struct {
	Status              domainlive.LiveOpsStatus
	Summary             domainlive.ReadinessCheckSummary
	Checks              []domainlive.ReadinessCheck
	Pending             PendingLiveDecisionReport
	Audit               LiveLoopAuditReport
	KillSwitch          domainrisk.KillSwitchState
	HasFirstOrderReview bool
	FirstOrderReview    domainlive.LiveFirstOrderReviewArtifact
	HasPositionDrift    bool
	PositionDrift       LivePositionDriftReport
}

func (s *Service) BuildLiveOpsReport(ctx context.Context, req LiveOpsReportRequest) (LiveOpsReport, error) {
	if err := ctx.Err(); err != nil {
		return LiveOpsReport{}, err
	}
	if err := s.requireLiveOpsDependencies(); err != nil {
		return LiveOpsReport{}, err
	}

	pendingLimit := req.PendingLimit
	if pendingLimit == 0 {
		pendingLimit = 10
	}
	auditLimit := req.AuditLimit
	if auditLimit == 0 {
		auditLimit = 10
	}

	report := LiveOpsReport{}
	killSwitch, err := s.killSwitch.CurrentKillSwitchState(ctx)
	if err != nil {
		return report, fmt.Errorf("load kill switch for live ops report: %w", err)
	}
	if err := domainrisk.ValidateKillSwitchState(killSwitch); err != nil {
		return report, err
	}
	report.KillSwitch = killSwitch
	report.Checks = append(report.Checks, liveReadinessKillSwitchCheck(killSwitch))

	pending, err := s.BuildPendingLiveDecisionReport(ctx, PendingLiveDecisionReportRequest{
		Symbol: req.PendingSymbol,
		Limit:  pendingLimit,
	})
	if err != nil {
		return report, err
	}
	report.Pending = pending
	report.Checks = append(report.Checks, liveReadinessPendingDecisionCheck(BuildLiveReadinessReportRequest{}, pending))

	audit, err := s.BuildLiveLoopAuditReport(ctx, LiveLoopAuditReportRequest{
		Limit:             auditLimit,
		IncludeIterations: true,
	})
	if err != nil {
		return report, err
	}
	report.Audit = audit
	report.Checks = append(report.Checks, liveReadinessAuditCheck(audit))

	if len(req.PositionDriftQueries) > 0 {
		drift, err := s.BuildLivePositionDriftReport(ctx, LivePositionDriftReportRequest{
			Queries:        req.PositionDriftQueries,
			CurrentMaxAge:  req.PositionDriftCurrentMaxAge,
			BaselineMaxAge: req.PositionDriftBaselineMaxAge,
		})
		if err != nil {
			return report, err
		}
		report.HasPositionDrift = true
		report.PositionDrift = drift
		report.Checks = append(report.Checks, drift.Checks...)
	}

	if req.HasFirstOrderReviewArtifact || req.RequireFirstOrderReviewArtifact {
		report.HasFirstOrderReview = req.HasFirstOrderReviewArtifact
		report.FirstOrderReview = req.FirstOrderReviewArtifact
		report.Checks = append(report.Checks, s.liveOpsFirstOrderReviewCheck(req))
	}

	if err := domainlive.ValidateReadinessChecks(report.Checks); err != nil {
		return LiveOpsReport{}, err
	}
	status, err := domainlive.SummarizeLiveOpsStatus(report.Checks)
	if err != nil {
		return LiveOpsReport{}, err
	}
	report.Summary = domainlive.SummarizeReadinessChecks(report.Checks)
	report.Status = status
	return report, nil
}

func (s *Service) requireLiveOpsDependencies() error {
	var problems []string
	if s == nil {
		return fmt.Errorf("live ops report requires service")
	}
	if s.killSwitch == nil {
		problems = append(problems, "kill switch repository")
	}
	if s.pendingDecisions == nil {
		problems = append(problems, "pending decision reader")
	}
	if s.loopAuditReader == nil {
		problems = append(problems, "live loop audit reader")
	}
	if len(problems) > 0 {
		return fmt.Errorf("live ops report requires %s", strings.Join(problems, ", "))
	}
	return nil
}

func (s *Service) liveOpsFirstOrderReviewCheck(req LiveOpsReportRequest) domainlive.ReadinessCheck {
	if !req.HasFirstOrderReviewArtifact {
		return domainlive.NewReadinessCheck(
			"first_order_review",
			domainlive.ReadinessCheckStatusFail,
			"first-order review artifact is required",
		)
	}
	if err := domainlive.ValidateLiveFirstOrderReviewArtifact(req.FirstOrderReviewArtifact); err != nil {
		return domainlive.NewReadinessCheck("first_order_review", domainlive.ReadinessCheckStatusFail, err.Error())
	}
	maxAge := req.MaxFirstOrderReviewArtifactAge
	if maxAge == 0 {
		maxAge = domainlive.DefaultLiveFirstOrderReviewArtifactMaxAge
	}
	if err := domainlive.ValidateLiveFirstOrderReviewArtifactFreshness(req.FirstOrderReviewArtifact, s.clock.Now(), maxAge); err != nil {
		return domainlive.NewReadinessCheck("first_order_review", domainlive.ReadinessCheckStatusFail, err.Error())
	}
	if !req.FirstOrderReviewArtifact.Ready {
		return domainlive.NewReadinessCheck(
			"first_order_review",
			domainlive.ReadinessCheckStatusFail,
			fmt.Sprintf("first-order review failed: %s", strings.Join(req.FirstOrderReviewArtifact.FailedChecks, ", ")),
		)
	}
	return domainlive.NewReadinessCheck(
		"first_order_review",
		domainlive.ReadinessCheckStatusPass,
		fmt.Sprintf("first-order review passed for client_order_id %s", req.FirstOrderReviewArtifact.Evidence.ClientOrderID),
	)
}
