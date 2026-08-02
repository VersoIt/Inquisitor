package live

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

type LiveReadinessArtifactHandoffExecution struct {
	ConfigPath         string
	PlanPath           string
	HasPlanArtifact    bool
	PlanArtifact       LiveOrderPlanArtifact
	PlanFileSHA256     string
	HasAuditArtifact   bool
	AuditArtifact      LiveLoopAuditArtifact
	SelectPending      bool
	PendingQuery       PendingLiveDecisionQuery
	SelectedDecisionID string
}

func ResolveLiveReadinessHandoffExecutionSelection(
	plan LiveOrderPlanArtifact,
	decisionID string,
	selectPending bool,
	pendingSymbol string,
) (string, PendingLiveDecisionQuery, error) {
	if err := ValidateLiveOrderPlanArtifactExecutionSource(plan, selectPending); err != nil {
		return "", PendingLiveDecisionQuery{}, err
	}
	trimmedDecisionID := strings.TrimSpace(decisionID)
	trimmedPendingSymbol := strings.TrimSpace(pendingSymbol)
	if selectPending {
		if trimmedDecisionID != "" {
			return "", PendingLiveDecisionQuery{}, fmt.Errorf("decision-id must be empty when -select-pending is used")
		}
		if pendingSymbol != trimmedPendingSymbol {
			return "", PendingLiveDecisionQuery{}, fmt.Errorf("pending-symbol must be trimmed")
		}
		effectivePendingSymbol := strings.ToUpper(trimmedPendingSymbol)
		if effectivePendingSymbol == "" {
			effectivePendingSymbol = plan.PendingSymbol
		}
		query := PendingLiveDecisionQuery{Symbol: effectivePendingSymbol, Limit: 1}
		if err := ValidatePendingLiveDecisionQuery(query); err != nil {
			return "", PendingLiveDecisionQuery{}, err
		}
		return plan.DecisionID, query, nil
	}
	if trimmedPendingSymbol != "" {
		return "", PendingLiveDecisionQuery{}, fmt.Errorf("pending-symbol requires -select-pending")
	}
	selectedDecisionID := plan.DecisionID
	if trimmedDecisionID != "" {
		if decisionID != trimmedDecisionID {
			return "", PendingLiveDecisionQuery{}, fmt.Errorf("decision-id must be trimmed")
		}
		selectedDecisionID = trimmedDecisionID
	}
	if selectedDecisionID != plan.DecisionID {
		return "", PendingLiveDecisionQuery{}, fmt.Errorf("decision-id %q does not match plan-file decision_id %q", selectedDecisionID, plan.DecisionID)
	}
	return selectedDecisionID, PendingLiveDecisionQuery{}, nil
}

func ValidateLiveReadinessArtifactHandoff(
	artifact LiveReadinessArtifact,
	execution LiveReadinessArtifactHandoffExecution,
) error {
	if err := ValidateLiveReadinessArtifact(artifact); err != nil {
		return err
	}
	var problems []string
	if !artifact.Ready {
		problems = append(problems, "ready must be true")
	}
	if artifact.Summary.Failed != 0 {
		problems = append(problems, fmt.Sprintf("failed checks must be zero, got %d", artifact.Summary.Failed))
	}
	if artifact.ConfigPath != strings.TrimSpace(execution.ConfigPath) {
		problems = append(problems, fmt.Sprintf("config_path %q does not match execution config %q", artifact.ConfigPath, strings.TrimSpace(execution.ConfigPath)))
	}
	if !artifact.Pending.Required {
		problems = append(problems, "pending readiness must be required")
	}
	if artifact.Pending.Total == 0 {
		problems = append(problems, "pending readiness must include at least one pending decision")
	}
	selectedDecisionID := strings.TrimSpace(execution.SelectedDecisionID)
	if selectedDecisionID != "" && artifact.Pending.NextDecisionID != selectedDecisionID {
		problems = append(problems, fmt.Sprintf("pending next_decision_id %q does not match selected decision %q", artifact.Pending.NextDecisionID, selectedDecisionID))
	}
	if execution.SelectPending {
		if err := ValidatePendingLiveDecisionQuery(execution.PendingQuery); err != nil {
			problems = append(problems, err.Error())
		}
		if execution.PendingQuery.Symbol != "" && artifact.Pending.Symbol != execution.PendingQuery.Symbol {
			problems = append(problems, fmt.Sprintf("pending symbol %q does not match selector symbol %q", artifact.Pending.Symbol, execution.PendingQuery.Symbol))
		}
	}
	if execution.HasPlanArtifact {
		if artifact.PlanFile == nil {
			problems = append(problems, "plan_file is required when -plan-file is used")
		} else {
			problems = append(problems, liveReadinessArtifactHandoffPlanFileProblems(*artifact.PlanFile, execution)...)
		}
	} else if artifact.PlanFile != nil {
		problems = append(problems, "readiness-file plan_file requires -plan-file")
	}
	if execution.HasAuditArtifact {
		problems = append(problems, liveReadinessArtifactHandoffAuditProblems(artifact.Audit, execution)...)
	}
	if len(problems) > 0 {
		return errors.New("live readiness artifact handoff validation failed: " + strings.Join(problems, "; "))
	}
	return nil
}

func liveReadinessArtifactHandoffPlanFileProblems(
	readinessPlan LiveReadinessArtifactPlanFile,
	execution LiveReadinessArtifactHandoffExecution,
) []string {
	var problems []string
	if err := ValidateLiveOrderPlanArtifact(execution.PlanArtifact); err != nil {
		return []string{err.Error()}
	}
	if !sameLiveReadinessHandoffPath(readinessPlan.Path, execution.PlanPath) {
		problems = append(problems, fmt.Sprintf("plan_file.path %q does not match -plan-file %q", readinessPlan.Path, strings.TrimSpace(execution.PlanPath)))
	}
	if readinessPlan.SHA256 != strings.TrimSpace(execution.PlanFileSHA256) {
		problems = append(problems, fmt.Sprintf("plan_file.sha256 %q does not match -plan-file sha256 %q", readinessPlan.SHA256, strings.TrimSpace(execution.PlanFileSHA256)))
	}
	compareText := map[string][2]string{
		"plan_file.schema_version":  {readinessPlan.SchemaVersion, execution.PlanArtifact.SchemaVersion},
		"plan_file.source":          {readinessPlan.Source, execution.PlanArtifact.Source},
		"plan_file.pending_symbol":  {readinessPlan.PendingSymbol, execution.PlanArtifact.PendingSymbol},
		"plan_file.decision_id":     {readinessPlan.DecisionID, execution.PlanArtifact.DecisionID},
		"plan_file.submission_id":   {readinessPlan.SubmissionID, execution.PlanArtifact.SubmissionID},
		"plan_file.client_order_id": {readinessPlan.ClientOrderID, execution.PlanArtifact.ClientOrderID},
		"plan_file.symbol":          {readinessPlan.Symbol, execution.PlanArtifact.Symbol},
	}
	for field, values := range compareText {
		if values[0] != values[1] {
			problems = append(problems, fmt.Sprintf("%s %q does not match plan artifact %q", field, values[0], values[1]))
		}
	}
	return problems
}

func liveReadinessArtifactHandoffAuditProblems(
	readinessAudit LiveReadinessArtifactAudit,
	execution LiveReadinessArtifactHandoffExecution,
) []string {
	var problems []string
	if err := ValidateLiveLoopAuditArtifact(execution.AuditArtifact); err != nil {
		return []string{err.Error()}
	}
	audit := execution.AuditArtifact
	if audit.ConfigPath != strings.TrimSpace(execution.ConfigPath) {
		problems = append(problems, fmt.Sprintf("audit config_path %q does not match execution config %q", audit.ConfigPath, strings.TrimSpace(execution.ConfigPath)))
	}
	if readinessAudit.Limit != audit.Query.Limit {
		problems = append(problems, fmt.Sprintf("audit.limit %d does not match audit artifact query.limit %d", readinessAudit.Limit, audit.Query.Limit))
	}
	compareInts := map[string][2]int{
		"audit.total":     {readinessAudit.Total, audit.Summary.Total},
		"audit.running":   {readinessAudit.Running, audit.Summary.Running},
		"audit.completed": {readinessAudit.Completed, audit.Summary.Completed},
		"audit.failed":    {readinessAudit.Failed, audit.Summary.Failed},
	}
	for field, values := range compareInts {
		if values[0] != values[1] {
			problems = append(problems, fmt.Sprintf("%s %d does not match audit artifact %d", field, values[0], values[1]))
		}
	}
	compareText := map[string][2]string{
		"audit.review_status": {string(readinessAudit.ReviewStatus), string(audit.Summary.ReviewStatus)},
		"audit.review_run_id": {readinessAudit.ReviewRunID, audit.Summary.ReviewRunID},
		"audit.review_reason": {readinessAudit.ReviewReason, audit.Summary.ReviewReason},
	}
	for field, values := range compareText {
		if values[0] != values[1] {
			problems = append(problems, fmt.Sprintf("%s %q does not match audit artifact %q", field, values[0], values[1]))
		}
	}
	if readinessAudit.OperatorActionRequired != audit.Summary.OperatorActionRequired {
		problems = append(problems, fmt.Sprintf("audit.operator_action_required %t does not match audit artifact %t", readinessAudit.OperatorActionRequired, audit.Summary.OperatorActionRequired))
	}
	return problems
}

func sameLiveReadinessHandoffPath(left string, right string) bool {
	return filepath.Clean(strings.TrimSpace(left)) == filepath.Clean(strings.TrimSpace(right))
}
