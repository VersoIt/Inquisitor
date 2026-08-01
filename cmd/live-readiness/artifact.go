package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	applive "github.com/VersoIt/Inquisitor/internal/app/live"
	domainlive "github.com/VersoIt/Inquisitor/internal/live"
)

const liveReadinessArtifactSchemaVersion = "inquisitor.live_readiness.v1"

type liveReadinessArtifact struct {
	SchemaVersion string                          `json:"schema_version"`
	CreatedAt     time.Time                       `json:"created_at"`
	ConfigPath    string                          `json:"config_path"`
	Ready         bool                            `json:"ready"`
	Summary       liveReadinessArtifactSummary    `json:"summary"`
	FailedChecks  []string                        `json:"failed_checks,omitempty"`
	Checks        []liveReadinessArtifactCheck    `json:"checks"`
	Pending       liveReadinessArtifactPending    `json:"pending"`
	Audit         liveReadinessArtifactAudit      `json:"audit"`
	KillSwitch    liveReadinessArtifactKillSwitch `json:"kill_switch"`
	PlanFile      *liveReadinessArtifactPlanFile  `json:"plan_file,omitempty"`
}

type liveReadinessArtifactSummary struct {
	Total  int `json:"total"`
	Passed int `json:"passed"`
	Warned int `json:"warned"`
	Failed int `json:"failed"`
}

type liveReadinessArtifactCheck struct {
	Name    string                          `json:"name"`
	Status  domainlive.ReadinessCheckStatus `json:"status"`
	Details string                          `json:"details"`
}

type liveReadinessArtifactPending struct {
	Symbol         string     `json:"symbol,omitempty"`
	Limit          int        `json:"limit"`
	Required       bool       `json:"required"`
	Total          int        `json:"total"`
	NextDecisionID string     `json:"next_decision_id,omitempty"`
	NextSymbol     string     `json:"next_symbol,omitempty"`
	OldestAt       *time.Time `json:"oldest_at,omitempty"`
	NewestAt       *time.Time `json:"newest_at,omitempty"`
}

type liveReadinessArtifactAudit struct {
	Limit     int `json:"limit"`
	Total     int `json:"total"`
	Running   int `json:"running"`
	Completed int `json:"completed"`
	Failed    int `json:"failed"`
}

type liveReadinessArtifactKillSwitch struct {
	Active    bool       `json:"active"`
	Reason    string     `json:"reason,omitempty"`
	Source    string     `json:"source,omitempty"`
	UpdatedAt *time.Time `json:"updated_at,omitempty"`
}

type liveReadinessArtifactPlanFile struct {
	Path          string `json:"path"`
	SchemaVersion string `json:"schema_version"`
	Source        string `json:"source"`
	PendingSymbol string `json:"pending_symbol,omitempty"`
	DecisionID    string `json:"decision_id"`
	SubmissionID  string `json:"submission_id"`
	ClientOrderID string `json:"client_order_id"`
	Symbol        string `json:"symbol"`
	MaxAge        string `json:"max_age"`
}

func liveReadinessArtifactPathFromFlag(path string) (string, error) {
	trimmedPath := strings.TrimSpace(path)
	if trimmedPath == "" {
		return "", nil
	}
	if path != trimmedPath {
		return "", fmt.Errorf("artifact-path must be trimmed")
	}
	return trimmedPath, nil
}

func liveReadinessArtifactFromReport(
	report applive.LiveReadinessReport,
	req applive.BuildLiveReadinessReportRequest,
	createdAt time.Time,
	configPath string,
	planFilePath string,
	hasPlanArtifact bool,
) liveReadinessArtifact {
	artifact := liveReadinessArtifact{
		SchemaVersion: liveReadinessArtifactSchemaVersion,
		CreatedAt:     createdAt.UTC(),
		ConfigPath:    strings.TrimSpace(configPath),
		Ready:         report.Ready,
		Summary: liveReadinessArtifactSummary{
			Total:  report.Summary.Total,
			Passed: report.Summary.Passed,
			Warned: report.Summary.Warned,
			Failed: report.Summary.Failed,
		},
		Pending: liveReadinessArtifactPending{
			Symbol:         req.PendingSymbol,
			Limit:          req.PendingLimit,
			Required:       req.RequirePendingDecision,
			Total:          report.Pending.Summary.Total,
			NextDecisionID: report.NextDecisionID,
			NextSymbol:     report.NextSymbol,
			OldestAt:       liveReadinessTimePointer(report.Pending.Summary.OldestAt),
			NewestAt:       liveReadinessTimePointer(report.Pending.Summary.NewestAt),
		},
		Audit: liveReadinessArtifactAudit{
			Limit:     req.AuditLimit,
			Total:     report.Audit.Summary.Total,
			Running:   report.Audit.Summary.Running,
			Completed: report.Audit.Summary.Completed,
			Failed:    report.Audit.Summary.Failed,
		},
		KillSwitch: liveReadinessArtifactKillSwitch{
			Active:    report.KillSwitch.Active,
			Reason:    report.KillSwitch.Reason,
			Source:    report.KillSwitch.Source,
			UpdatedAt: liveReadinessTimePointer(report.KillSwitch.UpdatedAt),
		},
	}
	for _, check := range report.Checks {
		artifact.Checks = append(artifact.Checks, liveReadinessArtifactCheck{
			Name:    check.Name,
			Status:  check.Status,
			Details: check.Details,
		})
		if check.Status == domainlive.ReadinessCheckStatusFail {
			artifact.FailedChecks = append(artifact.FailedChecks, check.Name)
		}
	}
	if hasPlanArtifact {
		artifact.PlanFile = &liveReadinessArtifactPlanFile{
			Path:          strings.TrimSpace(planFilePath),
			SchemaVersion: req.PlanArtifact.SchemaVersion,
			Source:        req.PlanArtifact.Source,
			PendingSymbol: req.PlanArtifact.PendingSymbol,
			DecisionID:    req.PlanArtifact.DecisionID,
			SubmissionID:  req.PlanArtifact.SubmissionID,
			ClientOrderID: req.PlanArtifact.ClientOrderID,
			Symbol:        req.PlanArtifact.Symbol,
			MaxAge:        req.MaxPlanArtifactAge.String(),
		}
	}
	return artifact
}

func liveReadinessTimePointer(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	utc := value.UTC()
	return &utc
}

func validateLiveReadinessArtifact(artifact liveReadinessArtifact) error {
	var problems []string
	if artifact.SchemaVersion != liveReadinessArtifactSchemaVersion {
		problems = append(problems, "schema_version must be "+liveReadinessArtifactSchemaVersion)
	}
	if artifact.CreatedAt.IsZero() {
		problems = append(problems, "created_at is required")
	}
	if strings.TrimSpace(artifact.ConfigPath) == "" {
		problems = append(problems, "config_path is required")
	}
	checks := make([]domainlive.ReadinessCheck, 0, len(artifact.Checks))
	for _, check := range artifact.Checks {
		checks = append(checks, domainlive.ReadinessCheck{
			Name:    check.Name,
			Status:  check.Status,
			Details: check.Details,
		})
	}
	if err := domainlive.ValidateReadinessChecks(checks); err != nil {
		problems = append(problems, err.Error())
	} else {
		summary := domainlive.SummarizeReadinessChecks(checks)
		if summary.Total != artifact.Summary.Total ||
			summary.Passed != artifact.Summary.Passed ||
			summary.Warned != artifact.Summary.Warned ||
			summary.Failed != artifact.Summary.Failed {
			problems = append(problems, "summary must match checks")
		}
		if domainlive.ReadinessChecksReady(checks) != artifact.Ready {
			problems = append(problems, "ready must match checks")
		}
	}
	if artifact.Pending.Limit <= 0 {
		problems = append(problems, "pending.limit must be positive")
	}
	if artifact.Audit.Limit <= 0 {
		problems = append(problems, "audit.limit must be positive")
	}
	if artifact.PlanFile != nil {
		if strings.TrimSpace(artifact.PlanFile.Path) == "" {
			problems = append(problems, "plan_file.path is required")
		}
		if strings.TrimSpace(artifact.PlanFile.DecisionID) == "" {
			problems = append(problems, "plan_file.decision_id is required")
		}
		if strings.TrimSpace(artifact.PlanFile.MaxAge) == "" {
			problems = append(problems, "plan_file.max_age is required")
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("live readiness artifact validation failed: %s", strings.Join(problems, "; "))
	}
	return nil
}

func writeLiveReadinessArtifact(path string, artifact liveReadinessArtifact) error {
	trimmedPath := strings.TrimSpace(path)
	if trimmedPath == "" {
		return fmt.Errorf("artifact-path is required")
	}
	if path != trimmedPath {
		return fmt.Errorf("artifact-path must be trimmed")
	}
	if err := validateLiveReadinessArtifact(artifact); err != nil {
		return err
	}
	payload, err := json.MarshalIndent(artifact, "", "  ")
	if err != nil {
		return fmt.Errorf("encode live readiness artifact: %w", err)
	}
	payload = append(payload, '\n')
	if dir := filepath.Dir(trimmedPath); dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("create live readiness artifact directory %q: %w", dir, err)
		}
	}
	if err := os.WriteFile(trimmedPath, payload, 0o600); err != nil {
		return fmt.Errorf("write live readiness artifact %q: %w", trimmedPath, err)
	}
	return nil
}
