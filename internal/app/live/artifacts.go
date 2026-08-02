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
			Limit:     auditLimit,
			Total:     req.Report.Audit.Summary.Total,
			Running:   req.Report.Audit.Summary.Running,
			Completed: req.Report.Audit.Summary.Completed,
			Failed:    req.Report.Audit.Summary.Failed,
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

func liveArtifactTimePointer(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	utc := value.UTC()
	return &utc
}
