package live

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

const LiveFirstOrderReviewArtifactSchemaVersion = "inquisitor.live_first_order_review.v1"

type LiveFirstOrderReviewArtifact struct {
	SchemaVersion string                               `json:"schema_version"`
	CreatedAt     time.Time                            `json:"created_at"`
	ConfigPath    string                               `json:"config_path"`
	Ready         bool                                 `json:"ready"`
	Summary       LiveFirstOrderReviewArtifactSummary  `json:"summary"`
	FailedChecks  []string                             `json:"failed_checks,omitempty"`
	Checks        []LiveFirstOrderReviewArtifactCheck  `json:"checks"`
	PlanFile      LiveFirstOrderReviewArtifactPlanFile `json:"plan_file"`
	Evidence      LiveFirstOrderReviewArtifactEvidence `json:"evidence"`
}

type LiveFirstOrderReviewArtifactSummary struct {
	Total  int `json:"total"`
	Passed int `json:"passed"`
	Warned int `json:"warned"`
	Failed int `json:"failed"`
}

type LiveFirstOrderReviewArtifactCheck struct {
	Name    string               `json:"name"`
	Status  ReadinessCheckStatus `json:"status"`
	Details string               `json:"details"`
}

type LiveFirstOrderReviewArtifactPlanFile struct {
	Path          string `json:"path"`
	SHA256        string `json:"sha256"`
	SchemaVersion string `json:"schema_version"`
	Source        string `json:"source"`
	DecisionID    string `json:"decision_id"`
	SubmissionID  string `json:"submission_id"`
	ClientOrderID string `json:"client_order_id"`
	Symbol        string `json:"symbol"`
}

type LiveFirstOrderReviewArtifactEvidence struct {
	RunID              string              `json:"run_id"`
	DecisionID         string              `json:"decision_id"`
	SubmissionID       string              `json:"submission_id"`
	ClientOrderID      string              `json:"client_order_id"`
	ExchangeOrderID    string              `json:"exchange_order_id,omitempty"`
	LatestOrderStatus  ExchangeOrderStatus `json:"latest_order_status,omitempty"`
	LatestPositionOpen bool                `json:"latest_position_open"`
	LatestPositionSize string              `json:"latest_position_size,omitempty"`
	StatusLimit        int                 `json:"status_limit"`
	PositionLimit      int                 `json:"position_limit"`
}

type BuildLiveFirstOrderReviewArtifactRequest struct {
	Report         LiveFirstOrderReviewReport
	Query          LiveFirstOrderReviewEvidenceQuery
	CreatedAt      time.Time
	ConfigPath     string
	PlanFilePath   string
	PlanFileSHA256 string
}

func BuildLiveFirstOrderReviewArtifact(req BuildLiveFirstOrderReviewArtifactRequest) (LiveFirstOrderReviewArtifact, error) {
	if err := ValidateLiveFirstOrderReviewReport(req.Report); err != nil {
		return LiveFirstOrderReviewArtifact{}, err
	}
	if req.Query.StatusLimit == 0 {
		req.Query.StatusLimit = DefaultLiveFirstOrderReviewStatusLimit
	}
	if req.Query.PositionLimit == 0 {
		req.Query.PositionLimit = DefaultLiveFirstOrderReviewPositionLimit
	}
	if err := ValidateLiveFirstOrderReviewEvidenceQuery(req.Query); err != nil {
		return LiveFirstOrderReviewArtifact{}, err
	}

	plan := req.Query.PlanArtifact
	artifact := LiveFirstOrderReviewArtifact{
		SchemaVersion: LiveFirstOrderReviewArtifactSchemaVersion,
		CreatedAt:     req.CreatedAt.UTC(),
		ConfigPath:    strings.TrimSpace(req.ConfigPath),
		Ready:         req.Report.Ready,
		Summary: LiveFirstOrderReviewArtifactSummary{
			Total:  req.Report.Summary.Total,
			Passed: req.Report.Summary.Passed,
			Warned: req.Report.Summary.Warned,
			Failed: req.Report.Summary.Failed,
		},
		PlanFile: LiveFirstOrderReviewArtifactPlanFile{
			Path:          strings.TrimSpace(req.PlanFilePath),
			SHA256:        strings.TrimSpace(req.PlanFileSHA256),
			SchemaVersion: plan.SchemaVersion,
			Source:        plan.Source,
			DecisionID:    plan.DecisionID,
			SubmissionID:  plan.SubmissionID,
			ClientOrderID: plan.ClientOrderID,
			Symbol:        plan.Symbol,
		},
		Evidence: LiveFirstOrderReviewArtifactEvidence{
			RunID:              req.Report.RunID,
			DecisionID:         req.Report.DecisionID,
			SubmissionID:       req.Report.SubmissionID,
			ClientOrderID:      req.Report.ClientOrderID,
			ExchangeOrderID:    req.Report.ExchangeOrderID,
			LatestOrderStatus:  req.Report.LatestOrderStatus,
			LatestPositionOpen: req.Report.LatestPositionOpen,
			LatestPositionSize: req.Report.LatestPositionSize,
			StatusLimit:        req.Query.StatusLimit,
			PositionLimit:      req.Query.PositionLimit,
		},
	}
	for _, check := range req.Report.Checks {
		artifact.Checks = append(artifact.Checks, LiveFirstOrderReviewArtifactCheck{
			Name:    check.Name,
			Status:  check.Status,
			Details: check.Details,
		})
		if check.Status == ReadinessCheckStatusFail {
			artifact.FailedChecks = append(artifact.FailedChecks, check.Name)
		}
	}
	if err := ValidateLiveFirstOrderReviewArtifact(artifact); err != nil {
		return LiveFirstOrderReviewArtifact{}, err
	}
	return artifact, nil
}

func ValidateLiveFirstOrderReviewArtifact(artifact LiveFirstOrderReviewArtifact) error {
	var problems []string
	if artifact.SchemaVersion != LiveFirstOrderReviewArtifactSchemaVersion {
		problems = append(problems, "schema_version must be "+LiveFirstOrderReviewArtifactSchemaVersion)
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
	problems = append(problems, validateLiveFirstOrderReviewPlanFileArtifactProblems(artifact.PlanFile)...)
	problems = append(problems, validateLiveFirstOrderReviewEvidenceArtifactProblems(artifact.Evidence, artifact.PlanFile, artifact.Ready)...)
	if len(problems) > 0 {
		return errors.New("live first-order review artifact validation failed: " + strings.Join(problems, "; "))
	}
	return nil
}

func validateLiveFirstOrderReviewPlanFileArtifactProblems(plan LiveFirstOrderReviewArtifactPlanFile) []string {
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
	}
	for field, value := range required {
		if strings.TrimSpace(value) == "" {
			problems = append(problems, field+" is required")
		}
		if value != strings.TrimSpace(value) {
			problems = append(problems, field+" must be trimmed")
		}
	}
	if plan.SchemaVersion != LiveOrderPlanArtifactSchemaVersion {
		problems = append(problems, "plan_file.schema_version must be "+LiveOrderPlanArtifactSchemaVersion)
	}
	if plan.Source != LiveOrderPlanArtifactSourceDecisionID && plan.Source != LiveOrderPlanArtifactSourceSelectPending {
		problems = append(problems, "plan_file.source must be decision-id or select-pending")
	}
	if strings.TrimSpace(plan.SHA256) != "" && !isLowerHexSHA256(plan.SHA256) {
		problems = append(problems, "plan_file.sha256 must be a lowercase SHA-256 hex digest")
	}
	if strings.TrimSpace(plan.Symbol) != "" && plan.Symbol != strings.ToUpper(strings.TrimSpace(plan.Symbol)) {
		problems = append(problems, "plan_file.symbol must be uppercase and trimmed")
	}
	return problems
}

func validateLiveFirstOrderReviewEvidenceArtifactProblems(
	evidence LiveFirstOrderReviewArtifactEvidence,
	plan LiveFirstOrderReviewArtifactPlanFile,
	ready bool,
) []string {
	var problems []string
	required := map[string]string{
		"evidence.run_id":          evidence.RunID,
		"evidence.decision_id":     evidence.DecisionID,
		"evidence.submission_id":   evidence.SubmissionID,
		"evidence.client_order_id": evidence.ClientOrderID,
	}
	for field, value := range required {
		if strings.TrimSpace(value) == "" {
			problems = append(problems, field+" is required")
		}
		if value != strings.TrimSpace(value) {
			problems = append(problems, field+" must be trimmed")
		}
	}
	if evidence.StatusLimit <= 0 || evidence.StatusLimit > 100 {
		problems = append(problems, "evidence.status_limit must be between 1 and 100")
	}
	if evidence.PositionLimit <= 0 || evidence.PositionLimit > 100 {
		problems = append(problems, "evidence.position_limit must be between 1 and 100")
	}
	metadata := map[string][2]string{
		"decision_id":     {evidence.DecisionID, plan.DecisionID},
		"submission_id":   {evidence.SubmissionID, plan.SubmissionID},
		"client_order_id": {evidence.ClientOrderID, plan.ClientOrderID},
	}
	for field, values := range metadata {
		if values[0] != values[1] {
			problems = append(problems, fmt.Sprintf("evidence.%s %q does not match plan_file.%s %q", field, values[0], field, values[1]))
		}
	}
	if ready {
		if strings.TrimSpace(evidence.ExchangeOrderID) == "" {
			problems = append(problems, "ready artifact requires evidence.exchange_order_id")
		}
		if evidence.LatestOrderStatus != ExchangeOrderStatusFilled {
			problems = append(problems, "ready artifact requires evidence.latest_order_status FILLED")
		}
		if !evidence.LatestPositionOpen {
			problems = append(problems, "ready artifact requires evidence.latest_position_open")
		}
		if strings.TrimSpace(evidence.LatestPositionSize) == "" {
			problems = append(problems, "ready artifact requires evidence.latest_position_size")
		}
	}
	return problems
}
