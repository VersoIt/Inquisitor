package live

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

func DefaultLiveDeploymentMicroCapitalLimitUSDT() decimal.Decimal {
	return decimal.NewFromInt(100)
}

type LiveDeploymentCheckRequest struct {
	ConfigPath                string
	PlanFilePath              string
	PlanFileSHA256            string
	PlanArtifact              LiveOrderPlanArtifact
	ReadinessArtifact         LiveReadinessArtifact
	AuditArtifact             LiveLoopAuditArtifact
	Now                       time.Time
	MaxPlanArtifactAge        time.Duration
	MaxReadinessArtifactAge   time.Duration
	MaxAuditArtifactAge       time.Duration
	Execute                   bool
	SubaccountConfirmed       bool
	DecisionID                string
	SelectPending             bool
	PendingSymbol             string
	MaxInitialLiveCapitalUSDT decimal.Decimal
	MicroCapitalLimitUSDT     decimal.Decimal
	MaxIterations             int
	MaxRuntime                time.Duration
	IterationTimeout          time.Duration
}

type LiveDeploymentCheckReport struct {
	Ready              bool
	Summary            ReadinessCheckSummary
	Checks             []ReadinessCheck
	SelectedDecisionID string
	PendingQuery       PendingLiveDecisionQuery
}

func BuildLiveDeploymentCheckReport(req LiveDeploymentCheckRequest) (LiveDeploymentCheckReport, error) {
	selectedDecisionID, pendingQuery, selectionErr := ResolveLiveReadinessHandoffExecutionSelection(
		req.PlanArtifact,
		req.DecisionID,
		req.SelectPending,
		req.PendingSymbol,
	)

	report := LiveDeploymentCheckReport{
		SelectedDecisionID: selectedDecisionID,
		PendingQuery:       pendingQuery,
		Checks: []ReadinessCheck{
			liveDeploymentSourceCheck(req, selectedDecisionID, pendingQuery, selectionErr),
			liveDeploymentArmingCheck(req),
			liveDeploymentBoundsCheck(req),
			liveDeploymentCapitalCheck(req),
			liveDeploymentArtifactFreshnessCheck(req),
			liveDeploymentReadinessReviewCheck(req),
		},
	}
	if selectionErr == nil {
		report.Checks = append(report.Checks, liveDeploymentHandoffCheck(req, selectedDecisionID, pendingQuery))
	}
	if err := ValidateReadinessChecks(report.Checks); err != nil {
		return LiveDeploymentCheckReport{}, err
	}
	report.Summary = SummarizeReadinessChecks(report.Checks)
	report.Ready = ReadinessChecksReady(report.Checks)
	return report, nil
}

func liveDeploymentSourceCheck(
	req LiveDeploymentCheckRequest,
	selectedDecisionID string,
	pendingQuery PendingLiveDecisionQuery,
	selectionErr error,
) ReadinessCheck {
	if selectionErr != nil {
		return NewReadinessCheck("live_loop_source", ReadinessCheckStatusFail, selectionErr.Error())
	}
	if req.SelectPending {
		symbol := pendingQuery.Symbol
		if symbol == "" {
			symbol = "any symbol"
		}
		return NewReadinessCheck(
			"live_loop_source",
			ReadinessCheckStatusPass,
			fmt.Sprintf("select-pending source will process %s with pending symbol %s", selectedDecisionID, symbol),
		)
	}
	return NewReadinessCheck(
		"live_loop_source",
		ReadinessCheckStatusPass,
		fmt.Sprintf("explicit decision source will process %s", selectedDecisionID),
	)
}

func liveDeploymentArmingCheck(req LiveDeploymentCheckRequest) ReadinessCheck {
	var problems []string
	if !req.Execute {
		problems = append(problems, "-execute=true is required")
	}
	if !req.SubaccountConfirmed {
		problems = append(problems, "-subaccount-confirmed is required")
	}
	if len(problems) > 0 {
		return NewReadinessCheck("live_loop_armed", ReadinessCheckStatusFail, strings.Join(problems, "; "))
	}
	return NewReadinessCheck("live_loop_armed", ReadinessCheckStatusPass, "execute flag and dedicated subaccount confirmation are present")
}

func liveDeploymentBoundsCheck(req LiveDeploymentCheckRequest) ReadinessCheck {
	var problems []string
	if req.MaxIterations != 1 {
		problems = append(problems, fmt.Sprintf("max_iterations must be 1 for first live micro order, got %d", req.MaxIterations))
	}
	if req.MaxRuntime <= 0 {
		problems = append(problems, "max_runtime must be positive")
	}
	if req.IterationTimeout <= 0 {
		problems = append(problems, "iteration_timeout must be positive")
	}
	if req.MaxRuntime > 0 && req.IterationTimeout > req.MaxRuntime {
		problems = append(problems, fmt.Sprintf("iteration_timeout %s exceeds max_runtime %s", req.IterationTimeout, req.MaxRuntime))
	}
	if len(problems) > 0 {
		return NewReadinessCheck("live_loop_bounds", ReadinessCheckStatusFail, strings.Join(problems, "; "))
	}
	return NewReadinessCheck(
		"live_loop_bounds",
		ReadinessCheckStatusPass,
		fmt.Sprintf("bounded to %d iteration with max runtime %s and iteration timeout %s", req.MaxIterations, req.MaxRuntime, req.IterationTimeout),
	)
}

func liveDeploymentCapitalCheck(req LiveDeploymentCheckRequest) ReadinessCheck {
	microLimit := req.MicroCapitalLimitUSDT
	if microLimit.IsZero() {
		microLimit = DefaultLiveDeploymentMicroCapitalLimitUSDT()
	}
	var problems []string
	if req.MaxInitialLiveCapitalUSDT.LessThanOrEqual(decimal.Zero) {
		problems = append(problems, "max_initial_live_capital_usdt must be positive")
	}
	if microLimit.LessThanOrEqual(decimal.Zero) {
		problems = append(problems, "micro_capital_limit_usdt must be positive")
	}
	if len(problems) == 0 && req.MaxInitialLiveCapitalUSDT.GreaterThan(microLimit) {
		problems = append(problems, fmt.Sprintf("max_initial_live_capital_usdt %s exceeds live micro limit %s", req.MaxInitialLiveCapitalUSDT, microLimit))
	}
	plannedNotional, notionalErr := decimalFromLiveOrderPlanArtifact("notional", req.PlanArtifact.Notional)
	if notionalErr != nil {
		problems = append(problems, "planned "+notionalErr.Error())
	} else if microLimit.GreaterThan(decimal.Zero) && plannedNotional.GreaterThan(microLimit) {
		problems = append(problems, fmt.Sprintf("planned notional %s exceeds live micro limit %s", plannedNotional, microLimit))
	}
	plannedLeverage, leverageErr := decimalFromLiveOrderPlanArtifact("leverage", req.PlanArtifact.Leverage)
	if leverageErr != nil {
		problems = append(problems, "planned "+leverageErr.Error())
	} else if plannedLeverage.GreaterThan(decimal.NewFromInt(1)) {
		problems = append(problems, fmt.Sprintf("planned leverage %s exceeds first live micro limit 1", plannedLeverage))
	}
	if len(problems) > 0 {
		return NewReadinessCheck("live_micro_capital", ReadinessCheckStatusFail, strings.Join(problems, "; "))
	}
	return NewReadinessCheck(
		"live_micro_capital",
		ReadinessCheckStatusPass,
		fmt.Sprintf("max initial live capital %s and planned notional %s are within live micro limit %s with leverage %s", req.MaxInitialLiveCapitalUSDT, plannedNotional, microLimit, plannedLeverage),
	)
}

func liveDeploymentArtifactFreshnessCheck(req LiveDeploymentCheckRequest) ReadinessCheck {
	var problems []string
	if err := ValidateLiveOrderPlanArtifactFreshness(req.PlanArtifact, req.Now, req.MaxPlanArtifactAge); err != nil {
		problems = append(problems, err.Error())
	}
	if err := ValidateLiveReadinessArtifactFreshness(req.ReadinessArtifact, req.Now, req.MaxReadinessArtifactAge); err != nil {
		problems = append(problems, err.Error())
	}
	if err := ValidateLiveLoopAuditArtifactFreshness(req.AuditArtifact, req.Now, req.MaxAuditArtifactAge); err != nil {
		problems = append(problems, err.Error())
	}
	if len(problems) > 0 {
		return NewReadinessCheck("deployment_artifact_freshness", ReadinessCheckStatusFail, strings.Join(problems, "; "))
	}
	return NewReadinessCheck("deployment_artifact_freshness", ReadinessCheckStatusPass, "plan, readiness, and audit artifacts are fresh")
}

func liveDeploymentReadinessReviewCheck(req LiveDeploymentCheckRequest) ReadinessCheck {
	var problems []string
	if err := ValidateLiveReadinessArtifact(req.ReadinessArtifact); err != nil {
		problems = append(problems, err.Error())
	}
	if !req.ReadinessArtifact.Ready {
		problems = append(problems, "readiness artifact ready must be true")
	}
	if req.ReadinessArtifact.Summary.Failed != 0 {
		problems = append(problems, fmt.Sprintf("readiness failed checks must be zero, got %d", req.ReadinessArtifact.Summary.Failed))
	}
	if req.ReadinessArtifact.Summary.Warned != 0 {
		problems = append(problems, fmt.Sprintf("readiness warnings must be zero before first live order, got %d", req.ReadinessArtifact.Summary.Warned))
	}
	if req.ReadinessArtifact.Audit.ReviewStatus != LiveLoopAuditReviewStatusClear {
		problems = append(problems, fmt.Sprintf("audit review_status must be CLEAR, got %q", req.ReadinessArtifact.Audit.ReviewStatus))
	}
	if req.ReadinessArtifact.Audit.OperatorActionRequired {
		problems = append(problems, "audit operator_action_required must be false")
	}
	if req.ReadinessArtifact.KillSwitch.Active {
		problems = append(problems, "kill switch must be inactive")
	}
	if len(problems) > 0 {
		return NewReadinessCheck("readiness_operator_review", ReadinessCheckStatusFail, strings.Join(problems, "; "))
	}
	return NewReadinessCheck("readiness_operator_review", ReadinessCheckStatusPass, "readiness artifact is PASS with no warnings, audit review, or active kill switch")
}

func liveDeploymentHandoffCheck(
	req LiveDeploymentCheckRequest,
	selectedDecisionID string,
	pendingQuery PendingLiveDecisionQuery,
) ReadinessCheck {
	if err := ValidateLiveReadinessArtifactHandoff(req.ReadinessArtifact, LiveReadinessArtifactHandoffExecution{
		ConfigPath:         strings.TrimSpace(req.ConfigPath),
		PlanPath:           strings.TrimSpace(req.PlanFilePath),
		HasPlanArtifact:    true,
		PlanArtifact:       req.PlanArtifact,
		PlanFileSHA256:     strings.TrimSpace(req.PlanFileSHA256),
		HasAuditArtifact:   true,
		AuditArtifact:      req.AuditArtifact,
		SelectPending:      req.SelectPending,
		PendingQuery:       pendingQuery,
		SelectedDecisionID: selectedDecisionID,
	}); err != nil {
		return NewReadinessCheck("deployment_artifact_handoff", ReadinessCheckStatusFail, err.Error())
	}
	return NewReadinessCheck("deployment_artifact_handoff", ReadinessCheckStatusPass, "plan/readiness/audit artifacts match the requested live-loop execution")
}

func LiveDeploymentCheckFailedNames(checks []ReadinessCheck) []string {
	failed := make([]string, 0)
	for _, check := range checks {
		if check.Status == ReadinessCheckStatusFail {
			failed = append(failed, check.Name)
		}
	}
	return failed
}

func ValidateLiveDeploymentCheckReport(report LiveDeploymentCheckReport) error {
	if err := ValidateReadinessChecks(report.Checks); err != nil {
		return err
	}
	summary := SummarizeReadinessChecks(report.Checks)
	var problems []string
	if summary.Total != report.Summary.Total ||
		summary.Passed != report.Summary.Passed ||
		summary.Warned != report.Summary.Warned ||
		summary.Failed != report.Summary.Failed {
		problems = append(problems, "summary must match checks")
	}
	if ReadinessChecksReady(report.Checks) != report.Ready {
		problems = append(problems, "ready must match checks")
	}
	if strings.TrimSpace(report.SelectedDecisionID) == "" {
		problems = append(problems, "selected_decision_id is required")
	}
	if err := ValidatePendingLiveDecisionQuery(report.PendingQuery); err != nil {
		problems = append(problems, err.Error())
	}
	if len(problems) > 0 {
		return errors.New("live deployment check report validation failed: " + strings.Join(problems, "; "))
	}
	return nil
}
