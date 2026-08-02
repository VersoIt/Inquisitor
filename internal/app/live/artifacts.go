package live

import (
	"strings"
	"time"

	domainlive "github.com/VersoIt/Inquisitor/internal/live"
)

type BuildLiveReadinessArtifactRequest struct {
	Report         LiveReadinessReport
	Readiness      BuildLiveReadinessReportRequest
	CreatedAt      time.Time
	ConfigPath     string
	PlanFilePath   string
	PlanFileSHA256 string
}

type BuildLiveLoopAuditArtifactRequest struct {
	Report     LiveLoopAuditReport
	CreatedAt  time.Time
	ConfigPath string
}

type BuildLiveOpsReportArtifactRequest struct {
	Report                     LiveOpsReport
	CreatedAt                  time.Time
	ConfigPath                 string
	FirstOrderReviewFilePath   string
	FirstOrderReviewFileSHA256 string
}

func BuildLiveOrderPlanArtifact(
	source string,
	pendingSymbol string,
	runID string,
	plan BuildLiveOrderPlanResult,
) (domainlive.LiveOrderPlanArtifact, error) {
	submission := plan.Submission
	decision := plan.Decision
	artifact := domainlive.LiveOrderPlanArtifact{
		SchemaVersion:       domainlive.LiveOrderPlanArtifactSchemaVersion,
		Source:              strings.TrimSpace(source),
		PendingSymbol:       strings.TrimSpace(pendingSymbol),
		RunID:               strings.TrimSpace(runID),
		DecisionID:          submission.DecisionID,
		SubmissionID:        submission.SubmissionID,
		ClientOrderID:       submission.ClientOrderID,
		Exchange:            submission.Exchange,
		Category:            submission.Category,
		Symbol:              submission.Symbol,
		Side:                submission.Side,
		OrderType:           submission.Type,
		TimeInForce:         submission.TimeInForce,
		LimitPrice:          submission.LimitPrice.String(),
		Quantity:            submission.Quantity.String(),
		EntryPrice:          submission.ReferencePrice.String(),
		Notional:            submission.Notional.String(),
		MaxLoss:             submission.MaxLoss.String(),
		StopLoss:            submission.StopLoss.String(),
		TakeProfit:          submission.TakeProfit.String(),
		Leverage:            submission.Leverage.String(),
		Confidence:          submission.Confidence,
		DecisionCreatedAt:   decision.Decision.CreatedAt,
		RecordedAt:          decision.RecordedAt,
		SubmissionCreatedAt: submission.CreatedAt,
		Reserved:            plan.SubmissionReserved,
		ExchangeContacted:   plan.ExchangeContacted,
		OrderSubmitted:      plan.OrderSubmitted,
	}
	if err := domainlive.ValidateLiveOrderPlanArtifact(artifact); err != nil {
		return domainlive.LiveOrderPlanArtifact{}, err
	}
	return artifact, nil
}

func BuildLiveReadinessArtifact(req BuildLiveReadinessArtifactRequest) (domainlive.LiveReadinessArtifact, error) {
	pendingLimit := req.Readiness.PendingLimit
	if pendingLimit == 0 {
		pendingLimit = 1
	}
	auditLimit := req.Readiness.AuditLimit
	if auditLimit == 0 {
		auditLimit = 10
	}
	maxPlanArtifactAge := req.Readiness.MaxPlanArtifactAge
	if maxPlanArtifactAge == 0 {
		maxPlanArtifactAge = domainlive.DefaultLiveOrderPlanArtifactMaxAge
	}
	artifact := domainlive.LiveReadinessArtifact{
		SchemaVersion: domainlive.LiveReadinessArtifactSchemaVersion,
		CreatedAt:     req.CreatedAt.UTC(),
		ConfigPath:    strings.TrimSpace(req.ConfigPath),
		Ready:         req.Report.Ready,
		Summary: domainlive.LiveReadinessArtifactSummary{
			Total:  req.Report.Summary.Total,
			Passed: req.Report.Summary.Passed,
			Warned: req.Report.Summary.Warned,
			Failed: req.Report.Summary.Failed,
		},
		Pending: domainlive.LiveReadinessArtifactPending{
			Symbol:         req.Readiness.PendingSymbol,
			Limit:          pendingLimit,
			Required:       req.Readiness.RequirePendingDecision,
			Total:          req.Report.Pending.Summary.Total,
			NextDecisionID: req.Report.NextDecisionID,
			NextSymbol:     req.Report.NextSymbol,
			OldestAt:       liveArtifactTimePointer(req.Report.Pending.Summary.OldestAt),
			NewestAt:       liveArtifactTimePointer(req.Report.Pending.Summary.NewestAt),
		},
		Audit: domainlive.LiveReadinessArtifactAudit{
			Limit:                  auditLimit,
			Total:                  req.Report.Audit.Summary.Total,
			Running:                req.Report.Audit.Summary.Running,
			Completed:              req.Report.Audit.Summary.Completed,
			Failed:                 req.Report.Audit.Summary.Failed,
			ReviewStatus:           req.Report.Audit.Summary.ReviewStatus,
			ReviewRunID:            req.Report.Audit.Summary.ReviewRunID,
			ReviewReason:           req.Report.Audit.Summary.ReviewReason,
			OperatorActionRequired: req.Report.Audit.Summary.OperatorActionRequired,
		},
		KillSwitch: domainlive.LiveReadinessArtifactKillSwitch{
			Active:    req.Report.KillSwitch.Active,
			Reason:    req.Report.KillSwitch.Reason,
			Source:    req.Report.KillSwitch.Source,
			UpdatedAt: liveArtifactTimePointer(req.Report.KillSwitch.UpdatedAt),
		},
	}
	for _, check := range req.Report.Checks {
		artifact.Checks = append(artifact.Checks, domainlive.LiveReadinessArtifactCheck{
			Name:    check.Name,
			Status:  check.Status,
			Details: check.Details,
		})
		if check.Status == domainlive.ReadinessCheckStatusFail {
			artifact.FailedChecks = append(artifact.FailedChecks, check.Name)
		}
	}
	if req.Readiness.HasPlanArtifact {
		artifact.PlanFile = &domainlive.LiveReadinessArtifactPlanFile{
			Path:          strings.TrimSpace(req.PlanFilePath),
			SHA256:        strings.TrimSpace(req.PlanFileSHA256),
			SchemaVersion: req.Readiness.PlanArtifact.SchemaVersion,
			Source:        req.Readiness.PlanArtifact.Source,
			PendingSymbol: req.Readiness.PlanArtifact.PendingSymbol,
			DecisionID:    req.Readiness.PlanArtifact.DecisionID,
			SubmissionID:  req.Readiness.PlanArtifact.SubmissionID,
			ClientOrderID: req.Readiness.PlanArtifact.ClientOrderID,
			Symbol:        req.Readiness.PlanArtifact.Symbol,
			MaxAge:        maxPlanArtifactAge.String(),
		}
	}
	if err := domainlive.ValidateLiveReadinessArtifact(artifact); err != nil {
		return domainlive.LiveReadinessArtifact{}, err
	}
	return artifact, nil
}

func BuildLiveLoopAuditArtifact(req BuildLiveLoopAuditArtifactRequest) (domainlive.LiveLoopAuditArtifact, error) {
	queryLimit := req.Report.Query.Limit
	if queryLimit == 0 {
		queryLimit = 10
	}
	artifact := domainlive.LiveLoopAuditArtifact{
		SchemaVersion: domainlive.LiveLoopAuditArtifactSchemaVersion,
		CreatedAt:     req.CreatedAt.UTC(),
		ConfigPath:    strings.TrimSpace(req.ConfigPath),
		Query: domainlive.LiveLoopAuditArtifactQuery{
			RunID:             req.Report.Query.RunID,
			Status:            req.Report.Query.Status,
			Limit:             queryLimit,
			IncludeIterations: req.Report.Query.IncludeIterations,
		},
		Summary: domainlive.LiveLoopAuditArtifactSummary{
			Total:                  req.Report.Summary.Total,
			Running:                req.Report.Summary.Running,
			Completed:              req.Report.Summary.Completed,
			Failed:                 req.Report.Summary.Failed,
			ReviewStatus:           req.Report.Summary.ReviewStatus,
			ReviewRunID:            req.Report.Summary.ReviewRunID,
			ReviewReason:           req.Report.Summary.ReviewReason,
			OperatorActionRequired: req.Report.Summary.OperatorActionRequired,
		},
	}
	for _, run := range req.Report.Runs {
		artifact.Runs = append(artifact.Runs, buildLiveLoopAuditArtifactRun(run, req.Report.Query.IncludeIterations))
	}
	if err := domainlive.ValidateLiveLoopAuditArtifact(artifact); err != nil {
		return domainlive.LiveLoopAuditArtifact{}, err
	}
	return artifact, nil
}

func BuildLiveOpsReportArtifact(req BuildLiveOpsReportArtifactRequest) (domainlive.LiveOpsReportArtifact, error) {
	pendingLimit := req.Report.Pending.Query.Limit
	if pendingLimit == 0 {
		pendingLimit = 10
	}
	auditLimit := req.Report.Audit.Query.Limit
	if auditLimit == 0 {
		auditLimit = 10
	}

	artifact := domainlive.LiveOpsReportArtifact{
		SchemaVersion: domainlive.LiveOpsReportArtifactSchemaVersion,
		CreatedAt:     req.CreatedAt.UTC(),
		ConfigPath:    strings.TrimSpace(req.ConfigPath),
		Status:        req.Report.Status,
		Summary: domainlive.LiveOpsReportArtifactSummary{
			Total:  req.Report.Summary.Total,
			Passed: req.Report.Summary.Passed,
			Warned: req.Report.Summary.Warned,
			Failed: req.Report.Summary.Failed,
		},
		Pending: domainlive.LiveOpsReportArtifactPending{
			Symbol:         req.Report.Pending.Query.Symbol,
			Limit:          pendingLimit,
			Total:          req.Report.Pending.Summary.Total,
			NextDecisionID: req.Report.Pending.Summary.NextID,
			NextSymbol:     req.Report.Pending.Summary.NextSymbol,
			OldestAt:       liveArtifactTimePointer(req.Report.Pending.Summary.OldestAt),
			NewestAt:       liveArtifactTimePointer(req.Report.Pending.Summary.NewestAt),
		},
		Audit: domainlive.LiveOpsReportArtifactAudit{
			Limit:                  auditLimit,
			Total:                  req.Report.Audit.Summary.Total,
			Running:                req.Report.Audit.Summary.Running,
			Completed:              req.Report.Audit.Summary.Completed,
			Failed:                 req.Report.Audit.Summary.Failed,
			ReviewStatus:           req.Report.Audit.Summary.ReviewStatus,
			ReviewRunID:            req.Report.Audit.Summary.ReviewRunID,
			ReviewReason:           req.Report.Audit.Summary.ReviewReason,
			OperatorActionRequired: req.Report.Audit.Summary.OperatorActionRequired,
		},
		KillSwitch: domainlive.LiveOpsReportArtifactKillSwitch{
			Active:    req.Report.KillSwitch.Active,
			Reason:    req.Report.KillSwitch.Reason,
			Source:    req.Report.KillSwitch.Source,
			UpdatedAt: liveArtifactTimePointer(req.Report.KillSwitch.UpdatedAt),
		},
	}
	for _, check := range req.Report.Checks {
		artifact.Checks = append(artifact.Checks, domainlive.LiveOpsReportArtifactCheck{
			Name:    check.Name,
			Status:  check.Status,
			Details: check.Details,
		})
		if check.Status == domainlive.ReadinessCheckStatusFail {
			artifact.FailedChecks = append(artifact.FailedChecks, check.Name)
		}
	}
	if req.Report.HasFirstOrderReview {
		review := req.Report.FirstOrderReview
		artifact.FirstOrderReview = &domainlive.LiveOpsReportArtifactFirstOrderReview{
			Path:               strings.TrimSpace(req.FirstOrderReviewFilePath),
			SHA256:             strings.TrimSpace(req.FirstOrderReviewFileSHA256),
			SchemaVersion:      review.SchemaVersion,
			CreatedAt:          review.CreatedAt,
			Ready:              review.Ready,
			Summary:            review.Summary,
			FailedChecks:       append([]string(nil), review.FailedChecks...),
			RunID:              review.Evidence.RunID,
			DecisionID:         review.Evidence.DecisionID,
			SubmissionID:       review.Evidence.SubmissionID,
			ClientOrderID:      review.Evidence.ClientOrderID,
			ExchangeOrderID:    review.Evidence.ExchangeOrderID,
			LatestOrderStatus:  review.Evidence.LatestOrderStatus,
			LatestPositionOpen: review.Evidence.LatestPositionOpen,
			LatestPositionSize: review.Evidence.LatestPositionSize,
		}
	}
	if req.Report.HasPositionDrift {
		artifact.PositionDrift = buildLiveOpsReportArtifactPositionDrift(req.Report.PositionDrift)
	}
	if err := domainlive.ValidateLiveOpsReportArtifact(artifact); err != nil {
		return domainlive.LiveOpsReportArtifact{}, err
	}
	return artifact, nil
}

func buildLiveOpsReportArtifactPositionDrift(report LivePositionDriftReport) *domainlive.LiveOpsReportArtifactPositionDrift {
	drift := &domainlive.LiveOpsReportArtifactPositionDrift{
		Status: report.Status,
		Summary: domainlive.LiveOpsReportArtifactSummary{
			Total:  report.Summary.Total,
			Passed: report.Summary.Passed,
			Warned: report.Summary.Warned,
			Failed: report.Summary.Failed,
		},
	}
	for _, check := range report.Checks {
		drift.Checks = append(drift.Checks, domainlive.LiveOpsReportArtifactCheck{
			Name:    check.Name,
			Status:  check.Status,
			Details: check.Details,
		})
		if check.Status == domainlive.ReadinessCheckStatusFail {
			drift.FailedChecks = append(drift.FailedChecks, check.Name)
		}
	}
	for _, comparison := range report.Comparisons {
		item := domainlive.LiveOpsReportArtifactPositionDriftItem{
			Exchange:    comparison.Query.Exchange,
			Category:    comparison.Query.Category,
			Symbol:      comparison.Query.Symbol,
			Status:      comparison.Status,
			HasBaseline: comparison.HasBaseline,
			Current:     buildLiveOpsReportArtifactPositionSnapshot(comparison.Current),
		}
		if comparison.HasBaseline {
			baseline := buildLiveOpsReportArtifactPositionSnapshot(comparison.Baseline)
			item.Baseline = &baseline
		}
		drift.Comparisons = append(drift.Comparisons, item)
	}
	return drift
}

func buildLiveOpsReportArtifactPositionSnapshot(snapshot domainlive.PositionSnapshot) domainlive.LiveOpsReportArtifactPositionSnapshot {
	return domainlive.LiveOpsReportArtifactPositionSnapshot{
		Open:               snapshot.Open,
		Side:               snapshot.Side,
		Size:               snapshot.Size.String(),
		AveragePrice:       snapshot.AveragePrice.String(),
		Leverage:           snapshot.Leverage.String(),
		ExchangeStatus:     snapshot.ExchangeStatus,
		PositionIndex:      snapshot.PositionIndex,
		ExchangeReduceOnly: snapshot.ExchangeReduceOnly,
		ExchangeCreatedAt:  liveArtifactTimePointer(snapshot.ExchangeCreatedAt),
		ObservedAt:         snapshot.ObservedAt,
	}
}

func buildLiveLoopAuditArtifactRun(
	run domainlive.LiveLoopRunAudit,
	includeIterations bool,
) domainlive.LiveLoopAuditArtifactRun {
	artifactRun := domainlive.LiveLoopAuditArtifactRun{
		RunID:                 run.RunID,
		StartedAt:             run.StartedAt,
		FinishedAt:            liveArtifactTimePointer(run.FinishedAt),
		Status:                run.Status,
		MaxIterations:         run.MaxIterations,
		MaxRuntime:            run.MaxRuntime.String(),
		IterationTimeout:      run.IterationTimeout.String(),
		PreflightChecked:      run.PreflightChecked,
		PreflightReady:        run.PreflightReady,
		IterationsAttempted:   run.IterationsAttempted,
		IterationsSucceeded:   run.IterationsSucceeded,
		StopReason:            run.StopReason,
		StopDetails:           run.StopDetails,
		Error:                 run.Error,
		CompletedWithinBounds: run.CompletedWithinBounds,
	}
	if includeIterations {
		for _, iteration := range run.Iterations {
			artifactRun.Iterations = append(artifactRun.Iterations, domainlive.LiveLoopAuditArtifactIteration{
				Iteration:         iteration.Iteration,
				Action:            iteration.Action,
				RequestStop:       iteration.RequestStop,
				Reason:            iteration.Reason,
				DecisionID:        iteration.DecisionID,
				SubmissionID:      iteration.SubmissionID,
				ClientOrderID:     iteration.ClientOrderID,
				ExchangeSubmitted: iteration.ExchangeSubmitted,
				AlreadySubmitted:  iteration.AlreadySubmitted,
				StartedAt:         iteration.StartedAt,
				FinishedAt:        iteration.FinishedAt,
			})
		}
	}
	return artifactRun
}

func liveArtifactTimePointer(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	utc := value.UTC()
	return &utc
}
