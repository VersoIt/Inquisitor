package live

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	domainlive "github.com/VersoIt/Inquisitor/internal/live"
	domainrisk "github.com/VersoIt/Inquisitor/internal/risk"
)

type BuildLiveReadinessReportRequest struct {
	TradingEnabled              bool
	TradingMode                 string
	AllowLive                   bool
	RequireEnvConfirmation      bool
	ConfirmationEnv             string
	ConfirmationAccepted        bool
	APIKeyEnv                   string
	APIKeyPresent               bool
	APISecretEnv                string
	APISecretPresent            bool
	RequireSubaccount           bool
	SubaccountConfirmed         bool
	WithdrawalPermissionAllowed bool
	InitialLiveCapitalUSDT      decimal.Decimal
	MaxInitialLiveCapitalUSDT   decimal.Decimal
	DatabaseMaxOpenConns        int
	PendingSymbol               string
	PendingLimit                int
	AuditLimit                  int
	RequirePendingDecision      bool
	HasPlanArtifact             bool
	PlanArtifact                domainlive.LiveOrderPlanArtifact
	MaxPlanArtifactAge          time.Duration
}

type LiveReadinessReport struct {
	Ready          bool
	Summary        domainlive.ReadinessCheckSummary
	Checks         []domainlive.ReadinessCheck
	Pending        PendingLiveDecisionReport
	Audit          LiveLoopAuditReport
	KillSwitch     domainrisk.KillSwitchState
	NextDecisionID string
	NextSymbol     string
}

func (s *Service) BuildLiveReadinessReport(
	ctx context.Context,
	req BuildLiveReadinessReportRequest,
) (LiveReadinessReport, error) {
	if err := ctx.Err(); err != nil {
		return LiveReadinessReport{}, err
	}
	if err := s.requireLiveReadinessDependencies(); err != nil {
		return LiveReadinessReport{}, err
	}
	pendingLimit := req.PendingLimit
	if pendingLimit == 0 {
		pendingLimit = 1
	}
	auditLimit := req.AuditLimit
	if auditLimit == 0 {
		auditLimit = 10
	}

	report := LiveReadinessReport{}
	report.Checks = append(report.Checks,
		liveReadinessConfigCheck(req),
		liveReadinessOperatorConfirmationCheck(req),
		liveReadinessCapitalCheck(req),
		liveReadinessDatabasePoolCheck(req),
	)

	killSwitch, err := s.killSwitch.CurrentKillSwitchState(ctx)
	if err != nil {
		return report, fmt.Errorf("load kill switch for live readiness: %w", err)
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
	report.NextDecisionID = pending.Summary.NextID
	report.NextSymbol = pending.Summary.NextSymbol
	report.Checks = append(report.Checks, liveReadinessPendingDecisionCheck(req, pending))

	if req.HasPlanArtifact {
		check, err := s.liveReadinessPlanArtifactCheck(ctx, req.PlanArtifact, req.MaxPlanArtifactAge, pending)
		if err != nil {
			return report, err
		}
		report.Checks = append(report.Checks, check)
	}

	audit, err := s.BuildLiveLoopAuditReport(ctx, LiveLoopAuditReportRequest{
		Limit:             auditLimit,
		IncludeIterations: true,
	})
	if err != nil {
		return report, err
	}
	report.Audit = audit
	report.Checks = append(report.Checks, liveReadinessAuditCheck(audit))

	if err := domainlive.ValidateReadinessChecks(report.Checks); err != nil {
		return LiveReadinessReport{}, err
	}
	report.Summary = domainlive.SummarizeReadinessChecks(report.Checks)
	report.Ready = domainlive.ReadinessChecksReady(report.Checks)
	return report, nil
}

func (s *Service) requireLiveReadinessDependencies() error {
	var problems []string
	if s == nil {
		return fmt.Errorf("live readiness report requires service")
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
		return fmt.Errorf("live readiness report requires %s", strings.Join(problems, ", "))
	}
	return nil
}

func liveReadinessConfigCheck(req BuildLiveReadinessReportRequest) domainlive.ReadinessCheck {
	var problems []string
	if !req.TradingEnabled {
		problems = append(problems, "trading.enabled=false")
	}
	if strings.ToLower(strings.TrimSpace(req.TradingMode)) != "live" {
		problems = append(problems, "trading.mode must be live")
	}
	if !req.AllowLive {
		problems = append(problems, "trading.allow_live=false")
	}
	if !req.RequireEnvConfirmation {
		problems = append(problems, "live.require_env_confirmation=false")
	}
	if !req.RequireSubaccount {
		problems = append(problems, "live.require_subaccount=false")
	}
	if req.WithdrawalPermissionAllowed {
		problems = append(problems, "withdrawal permission allowed")
	}
	if len(problems) > 0 {
		return domainlive.NewReadinessCheck("live_config", domainlive.ReadinessCheckStatusFail, strings.Join(problems, "; "))
	}
	return domainlive.NewReadinessCheck("live_config", domainlive.ReadinessCheckStatusPass, "live trading config is explicitly enabled with required safety gates")
}

func liveReadinessOperatorConfirmationCheck(req BuildLiveReadinessReportRequest) domainlive.ReadinessCheck {
	var problems []string
	if strings.TrimSpace(req.ConfirmationEnv) == "" {
		problems = append(problems, "confirmation env is required")
	}
	if req.RequireEnvConfirmation && !req.ConfirmationAccepted {
		problems = append(problems, "live confirmation env is not accepted")
	}
	if strings.TrimSpace(req.APIKeyEnv) == "" || !req.APIKeyPresent {
		problems = append(problems, "live API key is missing")
	}
	if strings.TrimSpace(req.APISecretEnv) == "" || !req.APISecretPresent {
		problems = append(problems, "live API secret is missing")
	}
	if req.RequireSubaccount && !req.SubaccountConfirmed {
		problems = append(problems, "dedicated live subaccount is not confirmed")
	}
	if len(problems) > 0 {
		return domainlive.NewReadinessCheck("operator_confirmation", domainlive.ReadinessCheckStatusFail, strings.Join(problems, "; "))
	}
	return domainlive.NewReadinessCheck("operator_confirmation", domainlive.ReadinessCheckStatusPass, "operator confirmation, credentials, and subaccount acknowledgement are present")
}

func liveReadinessCapitalCheck(req BuildLiveReadinessReportRequest) domainlive.ReadinessCheck {
	if req.InitialLiveCapitalUSDT.LessThanOrEqual(decimal.Zero) {
		return domainlive.NewReadinessCheck("capital_cap", domainlive.ReadinessCheckStatusFail, "initial live capital must be positive")
	}
	if req.MaxInitialLiveCapitalUSDT.LessThanOrEqual(decimal.Zero) {
		return domainlive.NewReadinessCheck("capital_cap", domainlive.ReadinessCheckStatusFail, "max initial live capital must be positive")
	}
	if req.InitialLiveCapitalUSDT.GreaterThan(req.MaxInitialLiveCapitalUSDT) {
		return domainlive.NewReadinessCheck(
			"capital_cap",
			domainlive.ReadinessCheckStatusFail,
			fmt.Sprintf("initial live capital %s exceeds max %s", req.InitialLiveCapitalUSDT, req.MaxInitialLiveCapitalUSDT),
		)
	}
	return domainlive.NewReadinessCheck(
		"capital_cap",
		domainlive.ReadinessCheckStatusPass,
		fmt.Sprintf("initial live capital %s is within max %s", req.InitialLiveCapitalUSDT, req.MaxInitialLiveCapitalUSDT),
	)
}

func liveReadinessDatabasePoolCheck(req BuildLiveReadinessReportRequest) domainlive.ReadinessCheck {
	if req.DatabaseMaxOpenConns == 1 {
		return domainlive.NewReadinessCheck("database_pool", domainlive.ReadinessCheckStatusFail, "database.max_open_conns=1 cannot safely hold the pending selector advisory lock")
	}
	return domainlive.NewReadinessCheck("database_pool", domainlive.ReadinessCheckStatusPass, "database pool can support the pending selector advisory lock")
}

func liveReadinessKillSwitchCheck(state domainrisk.KillSwitchState) domainlive.ReadinessCheck {
	if state.Active {
		return domainlive.NewReadinessCheck(
			"kill_switch",
			domainlive.ReadinessCheckStatusFail,
			fmt.Sprintf("kill switch active: reason=%q source=%q", state.Reason, state.Source),
		)
	}
	return domainlive.NewReadinessCheck("kill_switch", domainlive.ReadinessCheckStatusPass, "kill switch is inactive")
}

func liveReadinessPendingDecisionCheck(
	req BuildLiveReadinessReportRequest,
	pending PendingLiveDecisionReport,
) domainlive.ReadinessCheck {
	if pending.Summary.Total == 0 {
		status := domainlive.ReadinessCheckStatusWarn
		if req.RequirePendingDecision {
			status = domainlive.ReadinessCheckStatusFail
		}
		return domainlive.NewReadinessCheck("pending_live_decision", status, "no pending approved LIVE decisions without submissions")
	}
	return domainlive.NewReadinessCheck(
		"pending_live_decision",
		domainlive.ReadinessCheckStatusPass,
		fmt.Sprintf("next decision %s for %s is pending", pending.Summary.NextID, pending.Summary.NextSymbol),
	)
}

func (s *Service) liveReadinessPlanArtifactCheck(
	ctx context.Context,
	artifact domainlive.LiveOrderPlanArtifact,
	maxAge time.Duration,
	pending PendingLiveDecisionReport,
) (domainlive.ReadinessCheck, error) {
	if err := domainlive.ValidateLiveOrderPlanArtifact(artifact); err != nil {
		return domainlive.NewReadinessCheck("live_order_plan_artifact", domainlive.ReadinessCheckStatusFail, err.Error()), nil
	}
	if s == nil || s.clock == nil {
		return domainlive.ReadinessCheck{}, fmt.Errorf("live readiness plan artifact check requires clock")
	}
	if maxAge == 0 {
		maxAge = domainlive.DefaultLiveOrderPlanArtifactMaxAge
	}
	if err := domainlive.ValidateLiveOrderPlanArtifactFreshness(artifact, s.clock.Now(), maxAge); err != nil {
		return domainlive.NewReadinessCheck("live_order_plan_artifact", domainlive.ReadinessCheckStatusFail, err.Error()), nil
	}
	if artifact.Source == domainlive.LiveOrderPlanArtifactSourceSelectPending {
		if pending.Summary.Total == 0 {
			return domainlive.NewReadinessCheck(
				"live_order_plan_artifact",
				domainlive.ReadinessCheckStatusFail,
				"artifact was built from FIFO pending source, but no pending LIVE decision is currently available",
			), nil
		}
		if artifact.DecisionID != pending.Summary.NextID {
			return domainlive.NewReadinessCheck(
				"live_order_plan_artifact",
				domainlive.ReadinessCheckStatusFail,
				fmt.Sprintf("artifact decision %s is no longer the next FIFO pending decision %s", artifact.DecisionID, pending.Summary.NextID),
			), nil
		}
	}
	if s == nil || s.riskDecisions == nil {
		return domainlive.ReadinessCheck{}, fmt.Errorf("live readiness plan artifact check requires risk decision reader")
	}
	limitPrice, err := decimal.NewFromString(strings.TrimSpace(artifact.LimitPrice))
	if err != nil {
		return domainlive.NewReadinessCheck(
			"live_order_plan_artifact",
			domainlive.ReadinessCheckStatusFail,
			fmt.Sprintf("artifact limit_price must be decimal: %v", err),
		), nil
	}
	currentPlan, err := s.BuildLiveOrderPlan(ctx, BuildLiveOrderPlanRequest{
		DecisionID:    artifact.DecisionID,
		SubmissionID:  artifact.SubmissionID,
		ClientOrderID: artifact.ClientOrderID,
		Exchange:      artifact.Exchange,
		Category:      artifact.Category,
		Type:          artifact.OrderType,
		TimeInForce:   artifact.TimeInForce,
		LimitPrice:    limitPrice,
	})
	if err != nil {
		return domainlive.NewReadinessCheck(
			"live_order_plan_artifact",
			domainlive.ReadinessCheckStatusFail,
			fmt.Sprintf("artifact current plan could not be rebuilt: %v", err),
		), nil
	}
	if err := domainlive.ValidateLiveOrderPlanArtifactSnapshot(artifact, domainlive.LiveOrderPlanArtifactSnapshot{
		RunID:             artifact.RunID,
		Submission:        currentPlan.Submission,
		DecisionCreatedAt: currentPlan.Decision.Decision.CreatedAt,
		RecordedAt:        currentPlan.Decision.RecordedAt,
	}); err != nil {
		return domainlive.NewReadinessCheck("live_order_plan_artifact", domainlive.ReadinessCheckStatusFail, err.Error()), nil
	}
	return domainlive.NewReadinessCheck(
		"live_order_plan_artifact",
		domainlive.ReadinessCheckStatusPass,
		fmt.Sprintf("artifact matches current PostgreSQL risk snapshot for decision %s", artifact.DecisionID),
	), nil
}

func liveReadinessAuditCheck(audit LiveLoopAuditReport) domainlive.ReadinessCheck {
	details := strings.TrimSpace(audit.Summary.ReviewReason)
	switch audit.Summary.ReviewStatus {
	case domainlive.LiveLoopAuditReviewStatusBlocked:
		if details == "" {
			details = "recent live-loop audit contains RUNNING runs"
		}
		return domainlive.NewReadinessCheck("recent_live_loop_audit", domainlive.ReadinessCheckStatusFail, details)
	case domainlive.LiveLoopAuditReviewStatusReview:
		if details == "" {
			details = "recent live-loop audit contains FAILED runs that require operator review"
		}
		return domainlive.NewReadinessCheck("recent_live_loop_audit", domainlive.ReadinessCheckStatusWarn, details)
	case domainlive.LiveLoopAuditReviewStatusClear:
		if details == "" {
			details = "recent live-loop audit has no running or failed runs"
		}
		return domainlive.NewReadinessCheck("recent_live_loop_audit", domainlive.ReadinessCheckStatusPass, details)
	default:
		if audit.Summary.Running > 0 {
			return domainlive.NewReadinessCheck("recent_live_loop_audit", domainlive.ReadinessCheckStatusFail, "recent live-loop audit contains RUNNING runs")
		}
		if audit.Summary.Failed > 0 {
			return domainlive.NewReadinessCheck("recent_live_loop_audit", domainlive.ReadinessCheckStatusWarn, "recent live-loop audit contains FAILED runs that require operator review")
		}
		return domainlive.NewReadinessCheck("recent_live_loop_audit", domainlive.ReadinessCheckStatusPass, "recent live-loop audit has no running or failed runs")
	}
}
