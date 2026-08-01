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
) domainlive.LiveReadinessArtifact {
	artifact := domainlive.LiveReadinessArtifact{
		SchemaVersion: domainlive.LiveReadinessArtifactSchemaVersion,
		CreatedAt:     createdAt.UTC(),
		ConfigPath:    strings.TrimSpace(configPath),
		Ready:         report.Ready,
		Summary: domainlive.LiveReadinessArtifactSummary{
			Total:  report.Summary.Total,
			Passed: report.Summary.Passed,
			Warned: report.Summary.Warned,
			Failed: report.Summary.Failed,
		},
		Pending: domainlive.LiveReadinessArtifactPending{
			Symbol:         req.PendingSymbol,
			Limit:          req.PendingLimit,
			Required:       req.RequirePendingDecision,
			Total:          report.Pending.Summary.Total,
			NextDecisionID: report.NextDecisionID,
			NextSymbol:     report.NextSymbol,
			OldestAt:       liveReadinessTimePointer(report.Pending.Summary.OldestAt),
			NewestAt:       liveReadinessTimePointer(report.Pending.Summary.NewestAt),
		},
		Audit: domainlive.LiveReadinessArtifactAudit{
			Limit:     req.AuditLimit,
			Total:     report.Audit.Summary.Total,
			Running:   report.Audit.Summary.Running,
			Completed: report.Audit.Summary.Completed,
			Failed:    report.Audit.Summary.Failed,
		},
		KillSwitch: domainlive.LiveReadinessArtifactKillSwitch{
			Active:    report.KillSwitch.Active,
			Reason:    report.KillSwitch.Reason,
			Source:    report.KillSwitch.Source,
			UpdatedAt: liveReadinessTimePointer(report.KillSwitch.UpdatedAt),
		},
	}
	for _, check := range report.Checks {
		artifact.Checks = append(artifact.Checks, domainlive.LiveReadinessArtifactCheck{
			Name:    check.Name,
			Status:  check.Status,
			Details: check.Details,
		})
		if check.Status == domainlive.ReadinessCheckStatusFail {
			artifact.FailedChecks = append(artifact.FailedChecks, check.Name)
		}
	}
	if hasPlanArtifact {
		artifact.PlanFile = &domainlive.LiveReadinessArtifactPlanFile{
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

func writeLiveReadinessArtifact(path string, artifact domainlive.LiveReadinessArtifact) error {
	trimmedPath := strings.TrimSpace(path)
	if trimmedPath == "" {
		return fmt.Errorf("artifact-path is required")
	}
	if path != trimmedPath {
		return fmt.Errorf("artifact-path must be trimmed")
	}
	if err := domainlive.ValidateLiveReadinessArtifact(artifact); err != nil {
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
