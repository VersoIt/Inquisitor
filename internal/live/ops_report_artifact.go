package live

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

const LiveOpsReportArtifactSchemaVersion = "inquisitor.live_ops_report.v1"

const DefaultLiveOpsReportArtifactMaxAge = 10 * time.Minute

type LiveOpsReportArtifact struct {
	SchemaVersion    string                                 `json:"schema_version"`
	CreatedAt        time.Time                              `json:"created_at"`
	ConfigPath       string                                 `json:"config_path"`
	Status           LiveOpsStatus                          `json:"status"`
	Summary          LiveOpsReportArtifactSummary           `json:"summary"`
	FailedChecks     []string                               `json:"failed_checks,omitempty"`
	Checks           []LiveOpsReportArtifactCheck           `json:"checks"`
	Pending          LiveOpsReportArtifactPending           `json:"pending"`
	Audit            LiveOpsReportArtifactAudit             `json:"audit"`
	KillSwitch       LiveOpsReportArtifactKillSwitch        `json:"kill_switch"`
	PositionDrift    *LiveOpsReportArtifactPositionDrift    `json:"position_drift,omitempty"`
	FirstOrderReview *LiveOpsReportArtifactFirstOrderReview `json:"first_order_review,omitempty"`
}

type LiveOpsReportArtifactSummary struct {
	Total  int `json:"total"`
	Passed int `json:"passed"`
	Warned int `json:"warned"`
	Failed int `json:"failed"`
}

type LiveOpsReportArtifactCheck struct {
	Name    string               `json:"name"`
	Status  ReadinessCheckStatus `json:"status"`
	Details string               `json:"details"`
}

type LiveOpsReportArtifactPending struct {
	Symbol         string     `json:"symbol,omitempty"`
	Limit          int        `json:"limit"`
	Total          int        `json:"total"`
	NextDecisionID string     `json:"next_decision_id,omitempty"`
	NextSymbol     string     `json:"next_symbol,omitempty"`
	OldestAt       *time.Time `json:"oldest_at,omitempty"`
	NewestAt       *time.Time `json:"newest_at,omitempty"`
}

type LiveOpsReportArtifactAudit struct {
	Limit                  int                       `json:"limit"`
	Total                  int                       `json:"total"`
	Running                int                       `json:"running"`
	Completed              int                       `json:"completed"`
	Failed                 int                       `json:"failed"`
	ReviewStatus           LiveLoopAuditReviewStatus `json:"review_status"`
	ReviewRunID            string                    `json:"review_run_id,omitempty"`
	ReviewReason           string                    `json:"review_reason"`
	OperatorActionRequired bool                      `json:"operator_action_required"`
}

type LiveOpsReportArtifactKillSwitch struct {
	Active    bool       `json:"active"`
	Reason    string     `json:"reason,omitempty"`
	Source    string     `json:"source,omitempty"`
	UpdatedAt *time.Time `json:"updated_at,omitempty"`
}

type LiveOpsReportArtifactPositionDrift struct {
	Status       LiveOpsStatus                            `json:"status"`
	Summary      LiveOpsReportArtifactSummary             `json:"summary"`
	FailedChecks []string                                 `json:"failed_checks,omitempty"`
	Checks       []LiveOpsReportArtifactCheck             `json:"checks"`
	Comparisons  []LiveOpsReportArtifactPositionDriftItem `json:"comparisons"`
}

type LiveOpsReportArtifactPositionDriftItem struct {
	Exchange    string                                 `json:"exchange"`
	Category    string                                 `json:"category"`
	Symbol      string                                 `json:"symbol"`
	Status      LiveOpsStatus                          `json:"status"`
	HasBaseline bool                                   `json:"has_baseline"`
	Current     LiveOpsReportArtifactPositionSnapshot  `json:"current"`
	Baseline    *LiveOpsReportArtifactPositionSnapshot `json:"baseline,omitempty"`
}

type LiveOpsReportArtifactPositionSnapshot struct {
	Open               bool                   `json:"open"`
	Side               OrderSide              `json:"side,omitempty"`
	Size               string                 `json:"size"`
	AveragePrice       string                 `json:"average_price"`
	Leverage           string                 `json:"leverage"`
	ExchangeStatus     ExchangePositionStatus `json:"exchange_status,omitempty"`
	PositionIndex      int                    `json:"position_index"`
	ExchangeReduceOnly bool                   `json:"exchange_reduce_only"`
	ExchangeCreatedAt  *time.Time             `json:"exchange_created_at,omitempty"`
	ObservedAt         time.Time              `json:"observed_at"`
}

type LiveOpsReportArtifactFirstOrderReview struct {
	Path               string                              `json:"path"`
	SHA256             string                              `json:"sha256"`
	SchemaVersion      string                              `json:"schema_version"`
	CreatedAt          time.Time                           `json:"created_at"`
	Ready              bool                                `json:"ready"`
	Summary            LiveFirstOrderReviewArtifactSummary `json:"summary"`
	FailedChecks       []string                            `json:"failed_checks,omitempty"`
	RunID              string                              `json:"run_id"`
	DecisionID         string                              `json:"decision_id"`
	SubmissionID       string                              `json:"submission_id"`
	ClientOrderID      string                              `json:"client_order_id"`
	ExchangeOrderID    string                              `json:"exchange_order_id,omitempty"`
	LatestOrderStatus  ExchangeOrderStatus                 `json:"latest_order_status,omitempty"`
	LatestPositionOpen bool                                `json:"latest_position_open"`
	LatestPositionSize string                              `json:"latest_position_size,omitempty"`
}

type LiveOpsReportArtifactHandoffExecution struct {
	ConfigPath string
}

func ValidateLiveOpsReportArtifact(artifact LiveOpsReportArtifact) error {
	var problems []string
	if artifact.SchemaVersion != LiveOpsReportArtifactSchemaVersion {
		problems = append(problems, "schema_version must be "+LiveOpsReportArtifactSchemaVersion)
	}
	if artifact.CreatedAt.IsZero() {
		problems = append(problems, "created_at is required")
	}
	if strings.TrimSpace(artifact.ConfigPath) == "" {
		problems = append(problems, "config_path is required")
	} else if artifact.ConfigPath != strings.TrimSpace(artifact.ConfigPath) {
		problems = append(problems, "config_path must be trimmed")
	}
	if err := ValidateLiveOpsStatus(artifact.Status); err != nil {
		problems = append(problems, err.Error())
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
		status, err := SummarizeLiveOpsStatus(checks)
		if err != nil {
			problems = append(problems, err.Error())
		} else if status != artifact.Status {
			problems = append(problems, "status must match checks")
		}
		if !sameStringSet(failedChecks, artifact.FailedChecks) {
			problems = append(problems, "failed_checks must match failing checks")
		}
	}

	problems = append(problems, validateLiveOpsReportArtifactPendingProblems(artifact.Pending)...)
	problems = append(problems, validateLiveOpsReportArtifactAuditProblems(artifact.Audit)...)
	problems = append(problems, validateLiveOpsReportArtifactKillSwitchProblems(artifact.KillSwitch)...)
	if artifact.PositionDrift != nil {
		problems = append(problems, validateLiveOpsReportArtifactPositionDriftProblems(*artifact.PositionDrift)...)
		for _, check := range artifact.PositionDrift.Checks {
			if !liveOpsReportArtifactContainsCheck(artifact.Checks, check) {
				problems = append(problems, "position_drift checks must be included in top-level checks")
				break
			}
		}
	}
	if artifact.FirstOrderReview != nil {
		problems = append(problems, validateLiveOpsReportArtifactFirstOrderReviewProblems(*artifact.FirstOrderReview)...)
		if !liveOpsReportArtifactHasCheck(artifact.Checks, "first_order_review") {
			problems = append(problems, "first_order_review check is required when first_order_review metadata is present")
		}
	}
	if len(problems) > 0 {
		return errors.New("live ops report artifact validation failed: " + strings.Join(problems, "; "))
	}
	return nil
}

func ValidateLiveOpsReportArtifactHandoff(
	artifact LiveOpsReportArtifact,
	execution LiveOpsReportArtifactHandoffExecution,
) error {
	if err := ValidateLiveOpsReportArtifact(artifact); err != nil {
		return err
	}
	var problems []string
	if strings.TrimSpace(execution.ConfigPath) == "" {
		problems = append(problems, "execution config_path is required")
	} else if !sameLiveReadinessHandoffPath(artifact.ConfigPath, execution.ConfigPath) {
		problems = append(problems, fmt.Sprintf(
			"config_path %q does not match execution config %q",
			artifact.ConfigPath,
			strings.TrimSpace(execution.ConfigPath),
		))
	}
	if artifact.Status != LiveOpsStatusClear {
		problems = append(problems, fmt.Sprintf(
			"status must be CLEAR before live handoff, got %s",
			artifact.Status,
		))
	}
	if artifact.Summary.Warned != 0 {
		problems = append(problems, fmt.Sprintf("warnings must be zero, got %d", artifact.Summary.Warned))
	}
	if artifact.Summary.Failed != 0 {
		problems = append(problems, fmt.Sprintf("failed checks must be zero, got %d", artifact.Summary.Failed))
	}
	if len(problems) > 0 {
		return errors.New("live ops report artifact handoff validation failed: " + strings.Join(problems, "; "))
	}
	return nil
}

func ValidateLiveOpsReportArtifactFreshness(
	artifact LiveOpsReportArtifact,
	now time.Time,
	maxAge time.Duration,
) error {
	if err := ValidateLiveOpsReportArtifact(artifact); err != nil {
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
		return errors.New("live ops report artifact freshness validation failed: " + strings.Join(problems, "; "))
	}
	return nil
}

func validateLiveOpsReportArtifactPendingProblems(pending LiveOpsReportArtifactPending) []string {
	var problems []string
	if pending.Limit <= 0 || pending.Limit > 100 {
		problems = append(problems, "pending.limit must be between 1 and 100")
	}
	if pending.Total < 0 {
		problems = append(problems, "pending.total must be non-negative")
	}
	if pending.Symbol != strings.ToUpper(strings.TrimSpace(pending.Symbol)) {
		problems = append(problems, "pending.symbol must be uppercase and trimmed")
	}
	if pending.NextSymbol != strings.ToUpper(strings.TrimSpace(pending.NextSymbol)) {
		problems = append(problems, "pending.next_symbol must be uppercase and trimmed")
	}
	if pending.Total == 0 {
		if pending.NextDecisionID != "" || pending.NextSymbol != "" || pending.OldestAt != nil || pending.NewestAt != nil {
			problems = append(problems, "pending next metadata must be empty when pending.total is zero")
		}
		return problems
	}
	if strings.TrimSpace(pending.NextDecisionID) == "" {
		problems = append(problems, "pending.next_decision_id is required when pending.total is positive")
	} else if pending.NextDecisionID != strings.TrimSpace(pending.NextDecisionID) {
		problems = append(problems, "pending.next_decision_id must be trimmed")
	}
	if strings.TrimSpace(pending.NextSymbol) == "" {
		problems = append(problems, "pending.next_symbol is required when pending.total is positive")
	}
	if pending.OldestAt == nil || pending.NewestAt == nil {
		problems = append(problems, "pending oldest_at and newest_at are required when pending.total is positive")
	} else if pending.NewestAt.Before(*pending.OldestAt) {
		problems = append(problems, "pending.newest_at must not be before pending.oldest_at")
	}
	return problems
}

func validateLiveOpsReportArtifactAuditProblems(audit LiveOpsReportArtifactAudit) []string {
	var problems []string
	if audit.Limit <= 0 || audit.Limit > 100 {
		problems = append(problems, "audit.limit must be between 1 and 100")
	}
	if audit.Total < 0 || audit.Running < 0 || audit.Completed < 0 || audit.Failed < 0 {
		problems = append(problems, "audit counts must be non-negative")
	}
	if audit.Total != audit.Running+audit.Completed+audit.Failed {
		problems = append(problems, "audit.total must match status counts")
	}
	if !KnownLiveLoopAuditReviewStatus(audit.ReviewStatus) {
		problems = append(problems, "audit.review_status must be CLEAR, REVIEW, or BLOCKED")
	}
	if audit.ReviewReason != strings.TrimSpace(audit.ReviewReason) {
		problems = append(problems, "audit.review_reason must be trimmed")
	}
	if strings.TrimSpace(audit.ReviewReason) == "" {
		problems = append(problems, "audit.review_reason is required")
	}
	if audit.ReviewRunID != strings.TrimSpace(audit.ReviewRunID) {
		problems = append(problems, "audit.review_run_id must be trimmed")
	}
	if audit.ReviewStatus == LiveLoopAuditReviewStatusClear && audit.ReviewRunID != "" {
		problems = append(problems, "audit.review_run_id must be empty when audit.review_status is CLEAR")
	}
	if audit.ReviewStatus != LiveLoopAuditReviewStatusClear && strings.TrimSpace(audit.ReviewRunID) == "" {
		problems = append(problems, "audit.review_run_id is required when audit.review_status requires review")
	}
	review := LiveLoopAuditReview{Status: audit.ReviewStatus}
	if audit.OperatorActionRequired != review.OperatorActionRequired() {
		problems = append(problems, "audit.operator_action_required must match audit.review_status")
	}
	return problems
}

func validateLiveOpsReportArtifactKillSwitchProblems(killSwitch LiveOpsReportArtifactKillSwitch) []string {
	var problems []string
	if killSwitch.Active && killSwitch.UpdatedAt == nil {
		problems = append(problems, "active kill_switch requires updated_at")
	}
	if killSwitch.Reason != strings.TrimSpace(killSwitch.Reason) {
		problems = append(problems, "kill_switch.reason must be trimmed")
	}
	if killSwitch.Source != strings.ToLower(strings.TrimSpace(killSwitch.Source)) {
		problems = append(problems, "kill_switch.source must be lowercase and trimmed")
	}
	if killSwitch.UpdatedAt != nil {
		if strings.TrimSpace(killSwitch.Reason) == "" {
			problems = append(problems, "kill_switch.reason is required when kill_switch.updated_at is set")
		}
		if strings.TrimSpace(killSwitch.Source) == "" {
			problems = append(problems, "kill_switch.source is required when kill_switch.updated_at is set")
		}
	}
	return problems
}

func validateLiveOpsReportArtifactPositionDriftProblems(drift LiveOpsReportArtifactPositionDrift) []string {
	var problems []string
	if err := ValidateLiveOpsStatus(drift.Status); err != nil {
		problems = append(problems, "position_drift."+err.Error())
	}
	checks := make([]ReadinessCheck, 0, len(drift.Checks))
	var failedChecks []string
	for index, check := range drift.Checks {
		domainCheck := ReadinessCheck{
			Name:    check.Name,
			Status:  check.Status,
			Details: check.Details,
		}
		if err := ValidateReadinessCheck(domainCheck); err != nil {
			problems = append(problems, fmt.Sprintf("position_drift.checks[%d]: %v", index, err))
			continue
		}
		checks = append(checks, domainCheck)
		if check.Status == ReadinessCheckStatusFail {
			failedChecks = append(failedChecks, check.Name)
		}
	}
	if len(checks) == 0 {
		problems = append(problems, "position_drift.checks are required")
	} else if len(checks) == len(drift.Checks) {
		summary := SummarizeReadinessChecks(checks)
		if summary.Total != drift.Summary.Total ||
			summary.Passed != drift.Summary.Passed ||
			summary.Warned != drift.Summary.Warned ||
			summary.Failed != drift.Summary.Failed {
			problems = append(problems, "position_drift.summary must match checks")
		}
		status, err := SummarizeLiveOpsStatus(checks)
		if err != nil {
			problems = append(problems, "position_drift."+err.Error())
		} else if status != drift.Status {
			problems = append(problems, "position_drift.status must match checks")
		}
		if !sameStringSet(failedChecks, drift.FailedChecks) {
			problems = append(problems, "position_drift.failed_checks must match failing checks")
		}
	}
	if len(drift.Comparisons) == 0 {
		problems = append(problems, "position_drift.comparisons are required")
	}
	for index, comparison := range drift.Comparisons {
		problems = append(problems, validateLiveOpsReportArtifactPositionDriftItemProblems(index, comparison)...)
	}
	return problems
}

func validateLiveOpsReportArtifactPositionDriftItemProblems(index int, comparison LiveOpsReportArtifactPositionDriftItem) []string {
	prefix := fmt.Sprintf("position_drift.comparisons[%d]", index)
	var problems []string
	query := PositionSnapshotQuery{
		Exchange: comparison.Exchange,
		Category: comparison.Category,
		Symbol:   comparison.Symbol,
	}
	if err := ValidatePositionSnapshotQuery(query); err != nil {
		problems = append(problems, prefix+": "+err.Error())
	}
	if err := ValidateLiveOpsStatus(comparison.Status); err != nil {
		problems = append(problems, prefix+"."+err.Error())
	}
	problems = append(problems, validateLiveOpsReportArtifactPositionSnapshotProblems(prefix+".current", comparison.Current)...)
	if comparison.HasBaseline {
		if comparison.Baseline == nil {
			problems = append(problems, prefix+".baseline is required when has_baseline is true")
		} else {
			problems = append(problems, validateLiveOpsReportArtifactPositionSnapshotProblems(prefix+".baseline", *comparison.Baseline)...)
		}
	} else if comparison.Baseline != nil {
		problems = append(problems, prefix+".baseline must be omitted when has_baseline is false")
	}
	return problems
}

func validateLiveOpsReportArtifactPositionSnapshotProblems(prefix string, snapshot LiveOpsReportArtifactPositionSnapshot) []string {
	var problems []string
	size, sizeOK := validateLiveOpsReportArtifactDecimal(prefix, "size", snapshot.Size, false, &problems)
	averagePrice, averagePriceOK := validateLiveOpsReportArtifactDecimal(prefix, "average_price", snapshot.AveragePrice, false, &problems)
	validateLiveOpsReportArtifactDecimal(prefix, "leverage", snapshot.Leverage, false, &problems)
	if sizeOK && snapshot.Open != size.IsPositive() {
		problems = append(problems, prefix+".open must match positive size")
	}
	if snapshot.Open {
		if !KnownOrderSide(snapshot.Side) {
			problems = append(problems, prefix+".side must be LONG or SHORT for open position")
		}
		if !KnownExchangePositionStatus(snapshot.ExchangeStatus) {
			problems = append(problems, prefix+".exchange_status is unknown")
		}
		if averagePriceOK && averagePrice.LessThanOrEqual(decimal.Zero) {
			problems = append(problems, prefix+".average_price must be positive for open position")
		}
		if snapshot.ExchangeCreatedAt == nil || snapshot.ExchangeCreatedAt.IsZero() {
			problems = append(problems, prefix+".exchange_created_at is required for open position")
		}
	} else {
		if strings.TrimSpace(string(snapshot.Side)) != "" {
			problems = append(problems, prefix+".side must be empty for flat position")
		}
		if snapshot.ExchangeStatus != "" && !KnownExchangePositionStatus(snapshot.ExchangeStatus) {
			problems = append(problems, prefix+".exchange_status is unknown")
		}
	}
	if snapshot.PositionIndex < 0 {
		problems = append(problems, prefix+".position_index must be non-negative")
	}
	if snapshot.ObservedAt.IsZero() {
		problems = append(problems, prefix+".observed_at is required")
	}
	return problems
}

func validateLiveOpsReportArtifactDecimal(
	prefix string,
	field string,
	value string,
	allowNegative bool,
	problems *[]string,
) (decimal.Decimal, bool) {
	trimmed := strings.TrimSpace(value)
	name := prefix + "." + field
	if trimmed == "" {
		*problems = append(*problems, name+" is required")
		return decimal.Zero, false
	}
	if value != trimmed {
		*problems = append(*problems, name+" must be trimmed")
		return decimal.Zero, false
	}
	parsed, err := decimal.NewFromString(trimmed)
	if err != nil {
		*problems = append(*problems, name+" must be a decimal")
		return decimal.Zero, false
	}
	if !allowNegative && parsed.IsNegative() {
		*problems = append(*problems, name+" must be non-negative")
		return decimal.Zero, false
	}
	return parsed, true
}

func validateLiveOpsReportArtifactFirstOrderReviewProblems(review LiveOpsReportArtifactFirstOrderReview) []string {
	var problems []string
	required := map[string]string{
		"first_order_review.path":            review.Path,
		"first_order_review.sha256":          review.SHA256,
		"first_order_review.schema_version":  review.SchemaVersion,
		"first_order_review.run_id":          review.RunID,
		"first_order_review.decision_id":     review.DecisionID,
		"first_order_review.submission_id":   review.SubmissionID,
		"first_order_review.client_order_id": review.ClientOrderID,
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
	if review.SchemaVersion != LiveFirstOrderReviewArtifactSchemaVersion {
		problems = append(problems, "first_order_review.schema_version must be "+LiveFirstOrderReviewArtifactSchemaVersion)
	}
	if strings.TrimSpace(review.SHA256) != "" && !isLowerHexSHA256(review.SHA256) {
		problems = append(problems, "first_order_review.sha256 must be a lowercase SHA-256 hex digest")
	}
	if review.CreatedAt.IsZero() {
		problems = append(problems, "first_order_review.created_at is required")
	}
	problems = append(problems, validateLiveOpsReportArtifactFirstOrderSummaryProblems(review)...)
	if review.Ready {
		if strings.TrimSpace(review.ExchangeOrderID) == "" {
			problems = append(problems, "ready first_order_review requires exchange_order_id")
		}
		if review.LatestOrderStatus != ExchangeOrderStatusFilled {
			problems = append(problems, "ready first_order_review requires latest_order_status FILLED")
		}
		if !review.LatestPositionOpen {
			problems = append(problems, "ready first_order_review requires latest_position_open")
		}
		if strings.TrimSpace(review.LatestPositionSize) == "" {
			problems = append(problems, "ready first_order_review requires latest_position_size")
		}
	}
	return problems
}

func validateLiveOpsReportArtifactFirstOrderSummaryProblems(review LiveOpsReportArtifactFirstOrderReview) []string {
	var problems []string
	summary := review.Summary
	if summary.Total <= 0 {
		problems = append(problems, "first_order_review.summary.total must be positive")
	}
	if summary.Passed < 0 || summary.Warned < 0 || summary.Failed < 0 {
		problems = append(problems, "first_order_review summary counts must be non-negative")
	}
	if summary.Total != summary.Passed+summary.Warned+summary.Failed {
		problems = append(problems, "first_order_review.summary.total must match status counts")
	}
	if review.Ready && summary.Failed != 0 {
		problems = append(problems, "ready first_order_review must not have failed checks")
	}
	if !review.Ready && summary.Failed == 0 {
		problems = append(problems, "failed first_order_review must have failed checks")
	}
	if summary.Failed == 0 && len(review.FailedChecks) != 0 {
		problems = append(problems, "first_order_review.failed_checks must be empty when summary.failed is zero")
	}
	if summary.Failed > 0 && len(review.FailedChecks) == 0 {
		problems = append(problems, "first_order_review.failed_checks are required when summary.failed is positive")
	}
	for index, name := range review.FailedChecks {
		if strings.TrimSpace(name) == "" {
			problems = append(problems, fmt.Sprintf("first_order_review.failed_checks[%d] is required", index))
			continue
		}
		if name != strings.TrimSpace(name) {
			problems = append(problems, fmt.Sprintf("first_order_review.failed_checks[%d] must be trimmed", index))
		}
	}
	return problems
}

func liveOpsReportArtifactHasCheck(checks []LiveOpsReportArtifactCheck, name string) bool {
	for _, check := range checks {
		if check.Name == name {
			return true
		}
	}
	return false
}

func liveOpsReportArtifactContainsCheck(checks []LiveOpsReportArtifactCheck, expected LiveOpsReportArtifactCheck) bool {
	for _, check := range checks {
		if check.Name == expected.Name && check.Status == expected.Status && check.Details == expected.Details {
			return true
		}
	}
	return false
}
