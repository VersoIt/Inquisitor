package live

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

const LiveDeploymentCheckArtifactSchemaVersion = "inquisitor.live_deployment_check.v1"

const DefaultLiveDeploymentCheckArtifactMaxAge = 10 * time.Minute

type LiveDeploymentCheckArtifact struct {
	SchemaVersion string                               `json:"schema_version"`
	CreatedAt     time.Time                            `json:"created_at"`
	ConfigPath    string                               `json:"config_path"`
	Ready         bool                                 `json:"ready"`
	Summary       LiveDeploymentCheckArtifactSummary   `json:"summary"`
	FailedChecks  []string                             `json:"failed_checks,omitempty"`
	Checks        []LiveDeploymentCheckArtifactCheck   `json:"checks"`
	PlanFile      LiveDeploymentCheckArtifactPlanFile  `json:"plan_file"`
	ReadinessFile LiveDeploymentCheckArtifactReadiness `json:"readiness_file"`
	AuditFile     LiveDeploymentCheckArtifactAudit     `json:"audit_file"`
	Execution     LiveDeploymentCheckArtifactExecution `json:"execution"`
}

type LiveDeploymentCheckArtifactSummary struct {
	Total  int `json:"total"`
	Passed int `json:"passed"`
	Warned int `json:"warned"`
	Failed int `json:"failed"`
}

type LiveDeploymentCheckArtifactCheck struct {
	Name    string               `json:"name"`
	Status  ReadinessCheckStatus `json:"status"`
	Details string               `json:"details"`
}

type LiveDeploymentCheckArtifactPlanFile struct {
	Path          string `json:"path"`
	SHA256        string `json:"sha256"`
	SchemaVersion string `json:"schema_version"`
	Source        string `json:"source"`
	PendingSymbol string `json:"pending_symbol,omitempty"`
	DecisionID    string `json:"decision_id"`
	SubmissionID  string `json:"submission_id"`
	ClientOrderID string `json:"client_order_id"`
	Symbol        string `json:"symbol"`
	Notional      string `json:"notional"`
	Leverage      string `json:"leverage"`
	MaxAge        string `json:"max_age"`
}

type LiveDeploymentCheckArtifactReadiness struct {
	Path          string    `json:"path"`
	SHA256        string    `json:"sha256"`
	SchemaVersion string    `json:"schema_version"`
	CreatedAt     time.Time `json:"created_at"`
	Ready         bool      `json:"ready"`
	MaxAge        string    `json:"max_age"`
}

type LiveDeploymentCheckArtifactAudit struct {
	Path                   string                    `json:"path"`
	SHA256                 string                    `json:"sha256"`
	SchemaVersion          string                    `json:"schema_version"`
	CreatedAt              time.Time                 `json:"created_at"`
	ReviewStatus           LiveLoopAuditReviewStatus `json:"review_status"`
	OperatorActionRequired bool                      `json:"operator_action_required"`
	MaxAge                 string                    `json:"max_age"`
}

type LiveDeploymentCheckArtifactExecution struct {
	Execute                   bool   `json:"execute"`
	SubaccountConfirmed       bool   `json:"subaccount_confirmed"`
	SelectPending             bool   `json:"select_pending"`
	PendingSymbol             string `json:"pending_symbol,omitempty"`
	DecisionID                string `json:"decision_id,omitempty"`
	SelectedDecisionID        string `json:"selected_decision_id"`
	MaxInitialLiveCapitalUSDT string `json:"max_initial_live_capital_usdt"`
	MicroCapitalLimitUSDT     string `json:"micro_capital_limit_usdt"`
	MaxIterations             int    `json:"max_iterations"`
	MaxRuntime                string `json:"max_runtime"`
	IterationTimeout          string `json:"iteration_timeout"`
}

type BuildLiveDeploymentCheckArtifactRequest struct {
	Report              LiveDeploymentCheckReport
	Deployment          LiveDeploymentCheckRequest
	CreatedAt           time.Time
	ConfigPath          string
	PlanFilePath        string
	PlanFileSHA256      string
	ReadinessFilePath   string
	ReadinessFileSHA256 string
	AuditFilePath       string
	AuditFileSHA256     string
}

type LiveDeploymentCheckArtifactHandoffExecution struct {
	ConfigPath                string
	PlanPath                  string
	PlanFileSHA256            string
	PlanArtifact              LiveOrderPlanArtifact
	ReadinessPath             string
	ReadinessFileSHA256       string
	ReadinessArtifact         LiveReadinessArtifact
	AuditPath                 string
	AuditFileSHA256           string
	AuditArtifact             LiveLoopAuditArtifact
	Execute                   bool
	SubaccountConfirmed       bool
	SelectPending             bool
	PendingQuery              PendingLiveDecisionQuery
	DecisionID                string
	SelectedDecisionID        string
	MaxInitialLiveCapitalUSDT decimal.Decimal
	MaxIterations             int
	MaxRuntime                time.Duration
	IterationTimeout          time.Duration
}

func BuildLiveDeploymentCheckArtifact(req BuildLiveDeploymentCheckArtifactRequest) (LiveDeploymentCheckArtifact, error) {
	maxPlanAge := liveDeploymentArtifactDurationOrDefault(req.Deployment.MaxPlanArtifactAge, DefaultLiveOrderPlanArtifactMaxAge)
	maxReadinessAge := liveDeploymentArtifactDurationOrDefault(req.Deployment.MaxReadinessArtifactAge, DefaultLiveReadinessArtifactMaxAge)
	maxAuditAge := liveDeploymentArtifactDurationOrDefault(req.Deployment.MaxAuditArtifactAge, DefaultLiveLoopAuditArtifactMaxAge)
	microLimit := req.Deployment.MicroCapitalLimitUSDT
	if microLimit.IsZero() {
		microLimit = DefaultLiveDeploymentMicroCapitalLimitUSDT()
	}

	artifact := LiveDeploymentCheckArtifact{
		SchemaVersion: LiveDeploymentCheckArtifactSchemaVersion,
		CreatedAt:     req.CreatedAt.UTC(),
		ConfigPath:    strings.TrimSpace(req.ConfigPath),
		Ready:         req.Report.Ready,
		Summary: LiveDeploymentCheckArtifactSummary{
			Total:  req.Report.Summary.Total,
			Passed: req.Report.Summary.Passed,
			Warned: req.Report.Summary.Warned,
			Failed: req.Report.Summary.Failed,
		},
		PlanFile: LiveDeploymentCheckArtifactPlanFile{
			Path:          strings.TrimSpace(req.PlanFilePath),
			SHA256:        strings.TrimSpace(req.PlanFileSHA256),
			SchemaVersion: req.Deployment.PlanArtifact.SchemaVersion,
			Source:        req.Deployment.PlanArtifact.Source,
			PendingSymbol: req.Deployment.PlanArtifact.PendingSymbol,
			DecisionID:    req.Deployment.PlanArtifact.DecisionID,
			SubmissionID:  req.Deployment.PlanArtifact.SubmissionID,
			ClientOrderID: req.Deployment.PlanArtifact.ClientOrderID,
			Symbol:        req.Deployment.PlanArtifact.Symbol,
			Notional:      req.Deployment.PlanArtifact.Notional,
			Leverage:      req.Deployment.PlanArtifact.Leverage,
			MaxAge:        maxPlanAge.String(),
		},
		ReadinessFile: LiveDeploymentCheckArtifactReadiness{
			Path:          strings.TrimSpace(req.ReadinessFilePath),
			SHA256:        strings.TrimSpace(req.ReadinessFileSHA256),
			SchemaVersion: req.Deployment.ReadinessArtifact.SchemaVersion,
			CreatedAt:     req.Deployment.ReadinessArtifact.CreatedAt.UTC(),
			Ready:         req.Deployment.ReadinessArtifact.Ready,
			MaxAge:        maxReadinessAge.String(),
		},
		AuditFile: LiveDeploymentCheckArtifactAudit{
			Path:                   strings.TrimSpace(req.AuditFilePath),
			SHA256:                 strings.TrimSpace(req.AuditFileSHA256),
			SchemaVersion:          req.Deployment.AuditArtifact.SchemaVersion,
			CreatedAt:              req.Deployment.AuditArtifact.CreatedAt.UTC(),
			ReviewStatus:           req.Deployment.AuditArtifact.Summary.ReviewStatus,
			OperatorActionRequired: req.Deployment.AuditArtifact.Summary.OperatorActionRequired,
			MaxAge:                 maxAuditAge.String(),
		},
		Execution: LiveDeploymentCheckArtifactExecution{
			Execute:                   req.Deployment.Execute,
			SubaccountConfirmed:       req.Deployment.SubaccountConfirmed,
			SelectPending:             req.Deployment.SelectPending,
			PendingSymbol:             strings.ToUpper(strings.TrimSpace(req.Deployment.PendingSymbol)),
			DecisionID:                strings.TrimSpace(req.Deployment.DecisionID),
			SelectedDecisionID:        req.Report.SelectedDecisionID,
			MaxInitialLiveCapitalUSDT: req.Deployment.MaxInitialLiveCapitalUSDT.String(),
			MicroCapitalLimitUSDT:     microLimit.String(),
			MaxIterations:             req.Deployment.MaxIterations,
			MaxRuntime:                req.Deployment.MaxRuntime.String(),
			IterationTimeout:          req.Deployment.IterationTimeout.String(),
		},
	}
	for _, check := range req.Report.Checks {
		artifact.Checks = append(artifact.Checks, LiveDeploymentCheckArtifactCheck{
			Name:    check.Name,
			Status:  check.Status,
			Details: check.Details,
		})
		if check.Status == ReadinessCheckStatusFail {
			artifact.FailedChecks = append(artifact.FailedChecks, check.Name)
		}
	}
	if err := ValidateLiveDeploymentCheckArtifact(artifact); err != nil {
		return LiveDeploymentCheckArtifact{}, err
	}
	return artifact, nil
}

func ValidateLiveDeploymentCheckArtifact(artifact LiveDeploymentCheckArtifact) error {
	var problems []string
	if artifact.SchemaVersion != LiveDeploymentCheckArtifactSchemaVersion {
		problems = append(problems, "schema_version must be "+LiveDeploymentCheckArtifactSchemaVersion)
	}
	if artifact.CreatedAt.IsZero() {
		problems = append(problems, "created_at is required")
	}
	if strings.TrimSpace(artifact.ConfigPath) == "" {
		problems = append(problems, "config_path is required")
	} else if artifact.ConfigPath != strings.TrimSpace(artifact.ConfigPath) {
		problems = append(problems, "config_path must be trimmed")
	}

	checks := make([]ReadinessCheck, 0, len(artifact.Checks))
	var failedChecks []string
	for index, check := range artifact.Checks {
		domainCheck := ReadinessCheck{
			Name:    check.Name,
			Status:  check.Status,
			Details: check.Details,
		}
		if err := ValidateReadinessCheck(domainCheck); err != nil {
			problems = append(problems, fmt.Sprintf("checks[%d]: %v", index, err))
			continue
		}
		checks = append(checks, domainCheck)
		if check.Status == ReadinessCheckStatusFail {
			failedChecks = append(failedChecks, check.Name)
		}
	}
	if len(checks) == 0 {
		problems = append(problems, "checks are required")
	} else if len(checks) == len(artifact.Checks) {
		summary := SummarizeReadinessChecks(checks)
		if summary.Total != artifact.Summary.Total ||
			summary.Passed != artifact.Summary.Passed ||
			summary.Warned != artifact.Summary.Warned ||
			summary.Failed != artifact.Summary.Failed {
			problems = append(problems, "summary must match checks")
		}
		if ReadinessChecksReady(checks) != artifact.Ready {
			problems = append(problems, "ready must match checks")
		}
		if !sameStringSet(failedChecks, artifact.FailedChecks) {
			problems = append(problems, "failed_checks must match failing checks")
		}
	}
	problems = append(problems, validateLiveDeploymentPlanFileArtifactProblems(artifact.PlanFile)...)
	problems = append(problems, validateLiveDeploymentReadinessFileArtifactProblems(artifact.ReadinessFile)...)
	problems = append(problems, validateLiveDeploymentAuditFileArtifactProblems(artifact.AuditFile)...)
	problems = append(problems, validateLiveDeploymentExecutionArtifactProblems(artifact.Execution, artifact.Ready)...)
	if len(problems) > 0 {
		return errors.New("live deployment check artifact validation failed: " + strings.Join(problems, "; "))
	}
	return nil
}

func ValidateLiveDeploymentCheckArtifactHandoff(
	artifact LiveDeploymentCheckArtifact,
	execution LiveDeploymentCheckArtifactHandoffExecution,
) error {
	if err := ValidateLiveDeploymentCheckArtifact(artifact); err != nil {
		return err
	}
	var problems []string
	if !artifact.Ready {
		problems = append(problems, "ready must be true")
	}
	if artifact.Summary.Failed != 0 {
		problems = append(problems, fmt.Sprintf("failed checks must be zero, got %d", artifact.Summary.Failed))
	}
	if artifact.Summary.Warned != 0 {
		problems = append(problems, fmt.Sprintf("warnings must be zero, got %d", artifact.Summary.Warned))
	}
	if !sameLiveReadinessHandoffPath(artifact.ConfigPath, execution.ConfigPath) {
		problems = append(problems, fmt.Sprintf("config_path %q does not match execution config %q", artifact.ConfigPath, strings.TrimSpace(execution.ConfigPath)))
	}
	problems = append(problems, liveDeploymentCheckArtifactHandoffPlanProblems(artifact.PlanFile, execution)...)
	problems = append(problems, liveDeploymentCheckArtifactHandoffReadinessProblems(artifact.ReadinessFile, execution)...)
	problems = append(problems, liveDeploymentCheckArtifactHandoffAuditProblems(artifact.AuditFile, execution)...)
	problems = append(problems, liveDeploymentCheckArtifactHandoffExecutionProblems(artifact.Execution, execution)...)
	if len(problems) > 0 {
		return errors.New("live deployment check artifact handoff validation failed: " + strings.Join(problems, "; "))
	}
	return nil
}

func ValidateLiveDeploymentCheckArtifactFreshness(
	artifact LiveDeploymentCheckArtifact,
	now time.Time,
	maxAge time.Duration,
) error {
	if err := ValidateLiveDeploymentCheckArtifact(artifact); err != nil {
		return err
	}
	var problems []string
	if now.IsZero() {
		problems = append(problems, "now is required")
	}
	if maxAge <= 0 {
		problems = append(problems, "max_age must be positive")
	}
	if len(problems) == 0 {
		age := now.UTC().Sub(artifact.CreatedAt.UTC())
		if age < 0 {
			problems = append(problems, "created_at must not be in the future")
		}
		if age > maxAge {
			problems = append(problems, fmt.Sprintf("artifact is stale: age=%s max=%s", age, maxAge))
		}
	}
	if len(problems) > 0 {
		return errors.New("live deployment check artifact freshness validation failed: " + strings.Join(problems, "; "))
	}
	return nil
}

func liveDeploymentCheckArtifactHandoffPlanProblems(
	plan LiveDeploymentCheckArtifactPlanFile,
	execution LiveDeploymentCheckArtifactHandoffExecution,
) []string {
	var problems []string
	if err := ValidateLiveOrderPlanArtifact(execution.PlanArtifact); err != nil {
		return []string{err.Error()}
	}
	if !sameLiveReadinessHandoffPath(plan.Path, execution.PlanPath) {
		problems = append(problems, fmt.Sprintf("plan_file.path %q does not match -plan-file %q", plan.Path, strings.TrimSpace(execution.PlanPath)))
	}
	if plan.SHA256 != strings.TrimSpace(execution.PlanFileSHA256) {
		problems = append(problems, fmt.Sprintf("plan_file.sha256 %q does not match -plan-file sha256 %q", plan.SHA256, strings.TrimSpace(execution.PlanFileSHA256)))
	}
	compareText := map[string][2]string{
		"plan_file.schema_version":  {plan.SchemaVersion, execution.PlanArtifact.SchemaVersion},
		"plan_file.source":          {plan.Source, execution.PlanArtifact.Source},
		"plan_file.pending_symbol":  {plan.PendingSymbol, execution.PlanArtifact.PendingSymbol},
		"plan_file.decision_id":     {plan.DecisionID, execution.PlanArtifact.DecisionID},
		"plan_file.submission_id":   {plan.SubmissionID, execution.PlanArtifact.SubmissionID},
		"plan_file.client_order_id": {plan.ClientOrderID, execution.PlanArtifact.ClientOrderID},
		"plan_file.symbol":          {plan.Symbol, execution.PlanArtifact.Symbol},
		"plan_file.notional":        {plan.Notional, execution.PlanArtifact.Notional},
		"plan_file.leverage":        {plan.Leverage, execution.PlanArtifact.Leverage},
	}
	for field, values := range compareText {
		if values[0] != values[1] {
			problems = append(problems, fmt.Sprintf("%s %q does not match plan artifact %q", field, values[0], values[1]))
		}
	}
	return problems
}

func liveDeploymentCheckArtifactHandoffReadinessProblems(
	readiness LiveDeploymentCheckArtifactReadiness,
	execution LiveDeploymentCheckArtifactHandoffExecution,
) []string {
	var problems []string
	if err := ValidateLiveReadinessArtifact(execution.ReadinessArtifact); err != nil {
		return []string{err.Error()}
	}
	if !sameLiveReadinessHandoffPath(readiness.Path, execution.ReadinessPath) {
		problems = append(problems, fmt.Sprintf("readiness_file.path %q does not match -readiness-file %q", readiness.Path, strings.TrimSpace(execution.ReadinessPath)))
	}
	if readiness.SHA256 != strings.TrimSpace(execution.ReadinessFileSHA256) {
		problems = append(problems, fmt.Sprintf("readiness_file.sha256 %q does not match -readiness-file sha256 %q", readiness.SHA256, strings.TrimSpace(execution.ReadinessFileSHA256)))
	}
	if readiness.SchemaVersion != execution.ReadinessArtifact.SchemaVersion {
		problems = append(problems, fmt.Sprintf("readiness_file.schema_version %q does not match readiness artifact %q", readiness.SchemaVersion, execution.ReadinessArtifact.SchemaVersion))
	}
	if !readiness.CreatedAt.Equal(execution.ReadinessArtifact.CreatedAt.UTC()) {
		problems = append(problems, "readiness_file.created_at does not match readiness artifact")
	}
	if readiness.Ready != execution.ReadinessArtifact.Ready {
		problems = append(problems, fmt.Sprintf("readiness_file.ready %t does not match readiness artifact %t", readiness.Ready, execution.ReadinessArtifact.Ready))
	}
	return problems
}

func liveDeploymentCheckArtifactHandoffAuditProblems(
	audit LiveDeploymentCheckArtifactAudit,
	execution LiveDeploymentCheckArtifactHandoffExecution,
) []string {
	var problems []string
	if err := ValidateLiveLoopAuditArtifact(execution.AuditArtifact); err != nil {
		return []string{err.Error()}
	}
	if !sameLiveReadinessHandoffPath(audit.Path, execution.AuditPath) {
		problems = append(problems, fmt.Sprintf("audit_file.path %q does not match -audit-file %q", audit.Path, strings.TrimSpace(execution.AuditPath)))
	}
	if audit.SHA256 != strings.TrimSpace(execution.AuditFileSHA256) {
		problems = append(problems, fmt.Sprintf("audit_file.sha256 %q does not match -audit-file sha256 %q", audit.SHA256, strings.TrimSpace(execution.AuditFileSHA256)))
	}
	if audit.SchemaVersion != execution.AuditArtifact.SchemaVersion {
		problems = append(problems, fmt.Sprintf("audit_file.schema_version %q does not match audit artifact %q", audit.SchemaVersion, execution.AuditArtifact.SchemaVersion))
	}
	if !audit.CreatedAt.Equal(execution.AuditArtifact.CreatedAt.UTC()) {
		problems = append(problems, "audit_file.created_at does not match audit artifact")
	}
	if audit.ReviewStatus != execution.AuditArtifact.Summary.ReviewStatus {
		problems = append(problems, fmt.Sprintf("audit_file.review_status %q does not match audit artifact %q", audit.ReviewStatus, execution.AuditArtifact.Summary.ReviewStatus))
	}
	if audit.OperatorActionRequired != execution.AuditArtifact.Summary.OperatorActionRequired {
		problems = append(problems, fmt.Sprintf("audit_file.operator_action_required %t does not match audit artifact %t", audit.OperatorActionRequired, execution.AuditArtifact.Summary.OperatorActionRequired))
	}
	return problems
}

func liveDeploymentCheckArtifactHandoffExecutionProblems(
	artifactExecution LiveDeploymentCheckArtifactExecution,
	execution LiveDeploymentCheckArtifactHandoffExecution,
) []string {
	var problems []string
	if artifactExecution.Execute != execution.Execute {
		problems = append(problems, fmt.Sprintf("execution.execute %t does not match live-loop execute %t", artifactExecution.Execute, execution.Execute))
	}
	if artifactExecution.SubaccountConfirmed != execution.SubaccountConfirmed {
		problems = append(problems, fmt.Sprintf("execution.subaccount_confirmed %t does not match live-loop subaccount confirmation %t", artifactExecution.SubaccountConfirmed, execution.SubaccountConfirmed))
	}
	if artifactExecution.SelectPending != execution.SelectPending {
		problems = append(problems, fmt.Sprintf("execution.select_pending %t does not match live-loop select-pending %t", artifactExecution.SelectPending, execution.SelectPending))
	}
	expectedPendingSymbol := ""
	if execution.SelectPending {
		if err := ValidatePendingLiveDecisionQuery(execution.PendingQuery); err != nil {
			problems = append(problems, err.Error())
		}
		expectedPendingSymbol = execution.PendingQuery.Symbol
	}
	if artifactExecution.PendingSymbol != "" && artifactExecution.PendingSymbol != expectedPendingSymbol {
		problems = append(problems, fmt.Sprintf("execution.pending_symbol %q does not match live-loop execution %q", artifactExecution.PendingSymbol, expectedPendingSymbol))
	}
	if artifactExecution.DecisionID != "" && artifactExecution.DecisionID != strings.TrimSpace(execution.DecisionID) {
		problems = append(problems, fmt.Sprintf("execution.decision_id %q does not match live-loop execution %q", artifactExecution.DecisionID, strings.TrimSpace(execution.DecisionID)))
	}
	if artifactExecution.SelectedDecisionID != strings.TrimSpace(execution.SelectedDecisionID) {
		problems = append(problems, fmt.Sprintf("execution.selected_decision_id %q does not match live-loop execution %q", artifactExecution.SelectedDecisionID, strings.TrimSpace(execution.SelectedDecisionID)))
	}
	maxCapital, err := decimal.NewFromString(strings.TrimSpace(artifactExecution.MaxInitialLiveCapitalUSDT))
	if err != nil {
		problems = append(problems, "execution.max_initial_live_capital_usdt must be a decimal")
	} else if !maxCapital.Equal(execution.MaxInitialLiveCapitalUSDT) {
		problems = append(problems, fmt.Sprintf("execution.max_initial_live_capital_usdt %s does not match live-loop max initial capital %s", maxCapital, execution.MaxInitialLiveCapitalUSDT))
	}
	if artifactExecution.MaxIterations != execution.MaxIterations {
		problems = append(problems, fmt.Sprintf("execution.max_iterations %d does not match live-loop max iterations %d", artifactExecution.MaxIterations, execution.MaxIterations))
	}
	maxRuntime, maxRuntimeErr := time.ParseDuration(strings.TrimSpace(artifactExecution.MaxRuntime))
	if maxRuntimeErr != nil {
		problems = append(problems, "execution.max_runtime must be a duration")
	} else if maxRuntime != execution.MaxRuntime {
		problems = append(problems, fmt.Sprintf("execution.max_runtime %s does not match live-loop max runtime %s", maxRuntime, execution.MaxRuntime))
	}
	iterationTimeout, timeoutErr := time.ParseDuration(strings.TrimSpace(artifactExecution.IterationTimeout))
	if timeoutErr != nil {
		problems = append(problems, "execution.iteration_timeout must be a duration")
	} else if iterationTimeout != execution.IterationTimeout {
		problems = append(problems, fmt.Sprintf("execution.iteration_timeout %s does not match live-loop iteration timeout %s", iterationTimeout, execution.IterationTimeout))
	}
	return problems
}

func validateLiveDeploymentPlanFileArtifactProblems(plan LiveDeploymentCheckArtifactPlanFile) []string {
	var problems []string
	required := map[string]string{
		"plan_file.path":            plan.Path,
		"plan_file.sha256":          plan.SHA256,
		"plan_file.schema_version":  plan.SchemaVersion,
		"plan_file.source":          plan.Source,
		"plan_file.decision_id":     plan.DecisionID,
		"plan_file.submission_id":   plan.SubmissionID,
		"plan_file.client_order_id": plan.ClientOrderID,
		"plan_file.symbol":          plan.Symbol,
		"plan_file.notional":        plan.Notional,
		"plan_file.leverage":        plan.Leverage,
		"plan_file.max_age":         plan.MaxAge,
	}
	for field, value := range required {
		if strings.TrimSpace(value) == "" {
			problems = append(problems, field+" is required")
			continue
		}
		if value != strings.TrimSpace(value) {
			problems = append(problems, field+" must be trimmed")
		}
	}
	if plan.SchemaVersion != LiveOrderPlanArtifactSchemaVersion {
		problems = append(problems, "plan_file.schema_version must be "+LiveOrderPlanArtifactSchemaVersion)
	}
	if strings.TrimSpace(plan.SHA256) != "" && !isLowerHexSHA256(plan.SHA256) {
		problems = append(problems, "plan_file.sha256 must be a lowercase SHA-256 hex digest")
	}
	switch plan.Source {
	case LiveOrderPlanArtifactSourceDecisionID, LiveOrderPlanArtifactSourceSelectPending:
	default:
		problems = append(problems, "plan_file.source must be decision-id or select-pending")
	}
	if plan.PendingSymbol != strings.ToUpper(strings.TrimSpace(plan.PendingSymbol)) {
		problems = append(problems, "plan_file.pending_symbol must be uppercase and trimmed")
	}
	if plan.Symbol != strings.ToUpper(strings.TrimSpace(plan.Symbol)) {
		problems = append(problems, "plan_file.symbol must be uppercase and trimmed")
	}
	if _, err := time.ParseDuration(strings.TrimSpace(plan.MaxAge)); err != nil {
		problems = append(problems, "plan_file.max_age must be a duration")
	}
	if _, err := decimalFromLiveOrderPlanArtifact("plan_file.notional", plan.Notional); err != nil {
		problems = append(problems, err.Error())
	}
	if _, err := decimalFromLiveOrderPlanArtifact("plan_file.leverage", plan.Leverage); err != nil {
		problems = append(problems, err.Error())
	}
	return problems
}

func validateLiveDeploymentReadinessFileArtifactProblems(readiness LiveDeploymentCheckArtifactReadiness) []string {
	var problems []string
	required := map[string]string{
		"readiness_file.path":           readiness.Path,
		"readiness_file.sha256":         readiness.SHA256,
		"readiness_file.schema_version": readiness.SchemaVersion,
		"readiness_file.max_age":        readiness.MaxAge,
	}
	for field, value := range required {
		if strings.TrimSpace(value) == "" {
			problems = append(problems, field+" is required")
			continue
		}
		if value != strings.TrimSpace(value) {
			problems = append(problems, field+" must be trimmed")
		}
	}
	if readiness.SchemaVersion != LiveReadinessArtifactSchemaVersion {
		problems = append(problems, "readiness_file.schema_version must be "+LiveReadinessArtifactSchemaVersion)
	}
	if strings.TrimSpace(readiness.SHA256) != "" && !isLowerHexSHA256(readiness.SHA256) {
		problems = append(problems, "readiness_file.sha256 must be a lowercase SHA-256 hex digest")
	}
	if readiness.CreatedAt.IsZero() {
		problems = append(problems, "readiness_file.created_at is required")
	}
	if _, err := time.ParseDuration(strings.TrimSpace(readiness.MaxAge)); err != nil {
		problems = append(problems, "readiness_file.max_age must be a duration")
	}
	return problems
}

func validateLiveDeploymentAuditFileArtifactProblems(audit LiveDeploymentCheckArtifactAudit) []string {
	var problems []string
	required := map[string]string{
		"audit_file.path":           audit.Path,
		"audit_file.sha256":         audit.SHA256,
		"audit_file.schema_version": audit.SchemaVersion,
		"audit_file.max_age":        audit.MaxAge,
	}
	for field, value := range required {
		if strings.TrimSpace(value) == "" {
			problems = append(problems, field+" is required")
			continue
		}
		if value != strings.TrimSpace(value) {
			problems = append(problems, field+" must be trimmed")
		}
	}
	if audit.SchemaVersion != LiveLoopAuditArtifactSchemaVersion {
		problems = append(problems, "audit_file.schema_version must be "+LiveLoopAuditArtifactSchemaVersion)
	}
	if strings.TrimSpace(audit.SHA256) != "" && !isLowerHexSHA256(audit.SHA256) {
		problems = append(problems, "audit_file.sha256 must be a lowercase SHA-256 hex digest")
	}
	if audit.CreatedAt.IsZero() {
		problems = append(problems, "audit_file.created_at is required")
	}
	if !KnownLiveLoopAuditReviewStatus(audit.ReviewStatus) {
		problems = append(problems, "audit_file.review_status must be CLEAR, REVIEW, or BLOCKED")
	}
	if audit.ReviewStatus == LiveLoopAuditReviewStatusClear && audit.OperatorActionRequired {
		problems = append(problems, "audit_file.operator_action_required must be false when review_status is CLEAR")
	}
	if _, err := time.ParseDuration(strings.TrimSpace(audit.MaxAge)); err != nil {
		problems = append(problems, "audit_file.max_age must be a duration")
	}
	return problems
}

func validateLiveDeploymentExecutionArtifactProblems(execution LiveDeploymentCheckArtifactExecution, ready bool) []string {
	var problems []string
	if execution.PendingSymbol != strings.ToUpper(strings.TrimSpace(execution.PendingSymbol)) {
		problems = append(problems, "execution.pending_symbol must be uppercase and trimmed")
	}
	if execution.DecisionID != strings.TrimSpace(execution.DecisionID) {
		problems = append(problems, "execution.decision_id must be trimmed")
	}
	if strings.TrimSpace(execution.SelectedDecisionID) == "" {
		if ready {
			problems = append(problems, "ready execution requires selected_decision_id")
		}
	} else if execution.SelectedDecisionID != strings.TrimSpace(execution.SelectedDecisionID) {
		problems = append(problems, "execution.selected_decision_id must be trimmed")
	}
	maxCapital, err := decimal.NewFromString(strings.TrimSpace(execution.MaxInitialLiveCapitalUSDT))
	if err != nil || maxCapital.LessThanOrEqual(decimal.Zero) {
		problems = append(problems, "execution.max_initial_live_capital_usdt must be a positive decimal")
	}
	microLimit, err := decimal.NewFromString(strings.TrimSpace(execution.MicroCapitalLimitUSDT))
	if err != nil || microLimit.LessThanOrEqual(decimal.Zero) {
		problems = append(problems, "execution.micro_capital_limit_usdt must be a positive decimal")
	}
	maxRuntime, maxRuntimeErr := time.ParseDuration(strings.TrimSpace(execution.MaxRuntime))
	if maxRuntimeErr != nil || maxRuntime <= 0 {
		problems = append(problems, "execution.max_runtime must be a positive duration")
	}
	iterationTimeout, timeoutErr := time.ParseDuration(strings.TrimSpace(execution.IterationTimeout))
	if timeoutErr != nil || iterationTimeout <= 0 {
		problems = append(problems, "execution.iteration_timeout must be a positive duration")
	}
	if ready {
		if !execution.Execute {
			problems = append(problems, "ready execution requires execute=true")
		}
		if !execution.SubaccountConfirmed {
			problems = append(problems, "ready execution requires subaccount_confirmed=true")
		}
		if execution.MaxIterations != 1 {
			problems = append(problems, "ready execution requires max_iterations=1")
		}
		if maxCapital.GreaterThan(microLimit) {
			problems = append(problems, "ready execution requires max_initial_live_capital_usdt within micro_capital_limit_usdt")
		}
		if maxRuntimeErr == nil && timeoutErr == nil && iterationTimeout > maxRuntime {
			problems = append(problems, "ready execution requires iteration_timeout <= max_runtime")
		}
	}
	return problems
}

func liveDeploymentArtifactDurationOrDefault(value time.Duration, fallback time.Duration) time.Duration {
	if value > 0 {
		return value
	}
	return fallback
}
