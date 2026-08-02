package live

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

const LiveReadinessArtifactSchemaVersion = "inquisitor.live_readiness.v1"

const DefaultLiveReadinessArtifactMaxAge = 10 * time.Minute

type LiveReadinessArtifact struct {
	SchemaVersion string                          `json:"schema_version"`
	CreatedAt     time.Time                       `json:"created_at"`
	ConfigPath    string                          `json:"config_path"`
	Ready         bool                            `json:"ready"`
	Summary       LiveReadinessArtifactSummary    `json:"summary"`
	FailedChecks  []string                        `json:"failed_checks,omitempty"`
	Checks        []LiveReadinessArtifactCheck    `json:"checks"`
	Pending       LiveReadinessArtifactPending    `json:"pending"`
	Audit         LiveReadinessArtifactAudit      `json:"audit"`
	KillSwitch    LiveReadinessArtifactKillSwitch `json:"kill_switch"`
	PlanFile      *LiveReadinessArtifactPlanFile  `json:"plan_file,omitempty"`
}

type LiveReadinessArtifactSummary struct {
	Total  int `json:"total"`
	Passed int `json:"passed"`
	Warned int `json:"warned"`
	Failed int `json:"failed"`
}

type LiveReadinessArtifactCheck struct {
	Name    string               `json:"name"`
	Status  ReadinessCheckStatus `json:"status"`
	Details string               `json:"details"`
}

type LiveReadinessArtifactPending struct {
	Symbol         string     `json:"symbol,omitempty"`
	Limit          int        `json:"limit"`
	Required       bool       `json:"required"`
	Total          int        `json:"total"`
	NextDecisionID string     `json:"next_decision_id,omitempty"`
	NextSymbol     string     `json:"next_symbol,omitempty"`
	OldestAt       *time.Time `json:"oldest_at,omitempty"`
	NewestAt       *time.Time `json:"newest_at,omitempty"`
}

type LiveReadinessArtifactAudit struct {
	Limit     int `json:"limit"`
	Total     int `json:"total"`
	Running   int `json:"running"`
	Completed int `json:"completed"`
	Failed    int `json:"failed"`
}

type LiveReadinessArtifactKillSwitch struct {
	Active    bool       `json:"active"`
	Reason    string     `json:"reason,omitempty"`
	Source    string     `json:"source,omitempty"`
	UpdatedAt *time.Time `json:"updated_at,omitempty"`
}

type LiveReadinessArtifactPlanFile struct {
	Path          string `json:"path"`
	SHA256        string `json:"sha256"`
	SchemaVersion string `json:"schema_version"`
	Source        string `json:"source"`
	PendingSymbol string `json:"pending_symbol,omitempty"`
	DecisionID    string `json:"decision_id"`
	SubmissionID  string `json:"submission_id"`
	ClientOrderID string `json:"client_order_id"`
	Symbol        string `json:"symbol"`
	MaxAge        string `json:"max_age"`
}

func ValidateLiveReadinessArtifact(artifact LiveReadinessArtifact) error {
	var problems []string
	if artifact.SchemaVersion != LiveReadinessArtifactSchemaVersion {
		problems = append(problems, "schema_version must be "+LiveReadinessArtifactSchemaVersion)
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
	if artifact.Pending.Limit <= 0 {
		problems = append(problems, "pending.limit must be positive")
	}
	if artifact.Pending.Symbol != strings.ToUpper(strings.TrimSpace(artifact.Pending.Symbol)) {
		problems = append(problems, "pending.symbol must be uppercase and trimmed")
	}
	if artifact.Pending.NextSymbol != strings.ToUpper(strings.TrimSpace(artifact.Pending.NextSymbol)) {
		problems = append(problems, "pending.next_symbol must be uppercase and trimmed")
	}
	if artifact.Audit.Limit <= 0 {
		problems = append(problems, "audit.limit must be positive")
	}
	if artifact.KillSwitch.Active && artifact.KillSwitch.UpdatedAt == nil {
		problems = append(problems, "active kill_switch requires updated_at")
	}
	if artifact.KillSwitch.Source != strings.ToLower(strings.TrimSpace(artifact.KillSwitch.Source)) {
		problems = append(problems, "kill_switch.source must be lowercase and trimmed")
	}
	if artifact.PlanFile != nil {
		problems = append(problems, validateLiveReadinessArtifactPlanFileProblems(*artifact.PlanFile)...)
	}
	if len(problems) > 0 {
		return errors.New("live readiness artifact validation failed: " + strings.Join(problems, "; "))
	}
	return nil
}

func ValidateLiveReadinessArtifactFreshness(
	artifact LiveReadinessArtifact,
	now time.Time,
	maxAge time.Duration,
) error {
	if err := ValidateLiveReadinessArtifact(artifact); err != nil {
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
		return errors.New("live readiness artifact freshness validation failed: " + strings.Join(problems, "; "))
	}
	return nil
}

func validateLiveReadinessArtifactPlanFileProblems(plan LiveReadinessArtifactPlanFile) []string {
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
	if plan.Source == LiveOrderPlanArtifactSourceDecisionID && plan.PendingSymbol != "" {
		problems = append(problems, "plan_file.pending_symbol must be empty when source is decision-id")
	}
	if plan.Source == LiveOrderPlanArtifactSourceSelectPending &&
		plan.PendingSymbol != "" &&
		plan.PendingSymbol != plan.Symbol {
		problems = append(problems, "plan_file.pending_symbol must match symbol when source is select-pending")
	}
	if _, err := time.ParseDuration(strings.TrimSpace(plan.MaxAge)); err != nil {
		problems = append(problems, "plan_file.max_age must be a duration")
	}
	return problems
}

func isLowerHexSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, char := range value {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}

func sameStringSet(left []string, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	counts := make(map[string]int, len(left))
	for _, value := range left {
		counts[value]++
	}
	for _, value := range right {
		counts[value]--
		if counts[value] < 0 {
			return false
		}
	}
	return true
}
