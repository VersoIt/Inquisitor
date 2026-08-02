package live_test

import (
	"strings"
	"testing"
	"time"

	domainlive "github.com/VersoIt/Inquisitor/internal/live"
)

func TestValidateLiveReadinessArtifactHandoffTableDriven(t *testing.T) {
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	plan := validLiveOrderPlanArtifact(now)
	planPath := "artifacts/live-order-plan.json"
	planSHA256 := strings.Repeat("c", 64)
	validArtifact := validLiveReadinessHandoffArtifact(now, planPath, planSHA256, plan)
	validAuditArtifact := validLiveReadinessHandoffAuditArtifact(now, validArtifact.ConfigPath)
	validArtifact.Audit = liveReadinessHandoffAuditFromArtifact(validAuditArtifact)
	validExecution := domainlive.LiveReadinessArtifactHandoffExecution{
		ConfigPath:         validArtifact.ConfigPath,
		PlanPath:           planPath,
		HasPlanArtifact:    true,
		PlanArtifact:       plan,
		PlanFileSHA256:     planSHA256,
		SelectedDecisionID: plan.DecisionID,
	}

	tests := []struct {
		name       string
		mutate     func(*domainlive.LiveReadinessArtifact, *domainlive.LiveReadinessArtifactHandoffExecution)
		wantErrSub string
	}{
		{name: "valid explicit handoff with plan file"},
		{name: "valid handoff with audit artifact", mutate: func(_ *domainlive.LiveReadinessArtifact, e *domainlive.LiveReadinessArtifactHandoffExecution) {
			e.HasAuditArtifact = true
			e.AuditArtifact = validAuditArtifact
		}},
		{name: "valid readiness-only fallback without plan file", mutate: func(a *domainlive.LiveReadinessArtifact, e *domainlive.LiveReadinessArtifactHandoffExecution) {
			a.PlanFile = nil
			e.HasPlanArtifact = false
			e.PlanArtifact = domainlive.LiveOrderPlanArtifact{}
			e.PlanPath = ""
			e.PlanFileSHA256 = ""
		}},
		{name: "not ready stops handoff", mutate: func(a *domainlive.LiveReadinessArtifact, _ *domainlive.LiveReadinessArtifactHandoffExecution) {
			a.Ready = false
			a.Summary.Passed = 0
			a.Summary.Failed = 1
			a.FailedChecks = []string{"live_order_plan_artifact"}
			a.Checks[0].Status = domainlive.ReadinessCheckStatusFail
		}, wantErrSub: "ready"},
		{name: "config mismatch", mutate: func(_ *domainlive.LiveReadinessArtifact, e *domainlive.LiveReadinessArtifactHandoffExecution) {
			e.ConfigPath = "configs/other-live.yaml"
		}, wantErrSub: "config_path"},
		{name: "pending must be required", mutate: func(a *domainlive.LiveReadinessArtifact, _ *domainlive.LiveReadinessArtifactHandoffExecution) {
			a.Pending.Required = false
		}, wantErrSub: "pending readiness must be required"},
		{name: "pending total must be nonzero", mutate: func(a *domainlive.LiveReadinessArtifact, _ *domainlive.LiveReadinessArtifactHandoffExecution) {
			a.Pending.Total = 0
		}, wantErrSub: "pending readiness must include"},
		{name: "selected decision mismatch", mutate: func(_ *domainlive.LiveReadinessArtifact, e *domainlive.LiveReadinessArtifactHandoffExecution) {
			e.SelectedDecisionID = "risk_decision_live_artifact_0002"
		}, wantErrSub: "next_decision_id"},
		{name: "readiness plan requires plan file", mutate: func(_ *domainlive.LiveReadinessArtifact, e *domainlive.LiveReadinessArtifactHandoffExecution) {
			e.HasPlanArtifact = false
			e.PlanArtifact = domainlive.LiveOrderPlanArtifact{}
			e.PlanPath = ""
			e.PlanFileSHA256 = ""
		}, wantErrSub: "requires -plan-file"},
		{name: "plan file requires readiness plan", mutate: func(a *domainlive.LiveReadinessArtifact, _ *domainlive.LiveReadinessArtifactHandoffExecution) {
			a.PlanFile = nil
		}, wantErrSub: "plan_file is required"},
		{name: "plan path mismatch", mutate: func(_ *domainlive.LiveReadinessArtifact, e *domainlive.LiveReadinessArtifactHandoffExecution) {
			e.PlanPath = "artifacts/other-plan.json"
		}, wantErrSub: "plan_file.path"},
		{name: "plan sha mismatch", mutate: func(_ *domainlive.LiveReadinessArtifact, e *domainlive.LiveReadinessArtifactHandoffExecution) {
			e.PlanFileSHA256 = strings.Repeat("d", 64)
		}, wantErrSub: "plan_file.sha256"},
		{name: "plan metadata mismatch", mutate: func(a *domainlive.LiveReadinessArtifact, _ *domainlive.LiveReadinessArtifactHandoffExecution) {
			a.PlanFile.DecisionID = "risk_decision_live_artifact_0002"
		}, wantErrSub: "plan_file.decision_id"},
		{name: "audit artifact config mismatch", mutate: func(_ *domainlive.LiveReadinessArtifact, e *domainlive.LiveReadinessArtifactHandoffExecution) {
			e.HasAuditArtifact = true
			e.AuditArtifact = validAuditArtifact
			e.AuditArtifact.ConfigPath = "configs/other-live.yaml"
		}, wantErrSub: "audit config_path"},
		{name: "audit artifact summary mismatch", mutate: func(a *domainlive.LiveReadinessArtifact, e *domainlive.LiveReadinessArtifactHandoffExecution) {
			e.HasAuditArtifact = true
			e.AuditArtifact = validAuditArtifact
			a.Audit.Completed = 1
		}, wantErrSub: "audit.completed"},
		{name: "invalid audit artifact fails closed", mutate: func(_ *domainlive.LiveReadinessArtifact, e *domainlive.LiveReadinessArtifactHandoffExecution) {
			e.HasAuditArtifact = true
			e.AuditArtifact = validAuditArtifact
			e.AuditArtifact.SchemaVersion = "old"
		}, wantErrSub: "schema_version"},
		{name: "select pending symbol mismatch", mutate: func(_ *domainlive.LiveReadinessArtifact, e *domainlive.LiveReadinessArtifactHandoffExecution) {
			e.SelectPending = true
			e.PendingQuery = domainlive.PendingLiveDecisionQuery{Symbol: "ETHUSDT", Limit: 1}
		}, wantErrSub: "selector symbol"},
		{name: "select pending query must be valid", mutate: func(_ *domainlive.LiveReadinessArtifact, e *domainlive.LiveReadinessArtifactHandoffExecution) {
			e.SelectPending = true
			e.PendingQuery = domainlive.PendingLiveDecisionQuery{Symbol: " ethusdt ", Limit: 1}
		}, wantErrSub: "pending live decision query"},
		{name: "valid select pending handoff with matching selector", mutate: func(a *domainlive.LiveReadinessArtifact, e *domainlive.LiveReadinessArtifactHandoffExecution) {
			a.PlanFile.Source = domainlive.LiveOrderPlanArtifactSourceSelectPending
			a.PlanFile.PendingSymbol = plan.Symbol
			e.PlanArtifact.Source = domainlive.LiveOrderPlanArtifactSourceSelectPending
			e.PlanArtifact.PendingSymbol = plan.Symbol
			e.SelectPending = true
			e.PendingQuery = domainlive.PendingLiveDecisionQuery{Symbol: plan.Symbol, Limit: 1}
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			artifact := cloneLiveReadinessHandoffArtifact(validArtifact)
			execution := validExecution
			if tt.mutate != nil {
				tt.mutate(&artifact, &execution)
			}
			err := domainlive.ValidateLiveReadinessArtifactHandoff(artifact, execution)
			if tt.wantErrSub != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("validate readiness handoff: %v", err)
			}
		})
	}
}

func validLiveReadinessHandoffArtifact(
	createdAt time.Time,
	planPath string,
	planSHA256 string,
	plan domainlive.LiveOrderPlanArtifact,
) domainlive.LiveReadinessArtifact {
	return domainlive.LiveReadinessArtifact{
		SchemaVersion: domainlive.LiveReadinessArtifactSchemaVersion,
		CreatedAt:     createdAt,
		ConfigPath:    "configs/live.local.yaml",
		Ready:         true,
		Summary: domainlive.LiveReadinessArtifactSummary{
			Total:  1,
			Passed: 1,
		},
		Checks: []domainlive.LiveReadinessArtifactCheck{{
			Name:    "live_order_plan_artifact",
			Status:  domainlive.ReadinessCheckStatusPass,
			Details: "artifact matches current PostgreSQL risk snapshot",
		}},
		Pending: domainlive.LiveReadinessArtifactPending{
			Symbol:         plan.Symbol,
			Limit:          1,
			Required:       true,
			Total:          1,
			NextDecisionID: plan.DecisionID,
			NextSymbol:     plan.Symbol,
		},
		Audit: domainlive.LiveReadinessArtifactAudit{
			Limit: 10,
		},
		KillSwitch: domainlive.LiveReadinessArtifactKillSwitch{},
		PlanFile: &domainlive.LiveReadinessArtifactPlanFile{
			Path:          strings.TrimSpace(planPath),
			SHA256:        strings.TrimSpace(planSHA256),
			SchemaVersion: plan.SchemaVersion,
			Source:        plan.Source,
			PendingSymbol: plan.PendingSymbol,
			DecisionID:    plan.DecisionID,
			SubmissionID:  plan.SubmissionID,
			ClientOrderID: plan.ClientOrderID,
			Symbol:        plan.Symbol,
			MaxAge:        domainlive.DefaultLiveOrderPlanArtifactMaxAge.String(),
		},
	}
}

func validLiveReadinessHandoffAuditArtifact(
	createdAt time.Time,
	configPath string,
) domainlive.LiveLoopAuditArtifact {
	return domainlive.LiveLoopAuditArtifact{
		SchemaVersion: domainlive.LiveLoopAuditArtifactSchemaVersion,
		CreatedAt:     createdAt,
		ConfigPath:    configPath,
		Query: domainlive.LiveLoopAuditArtifactQuery{
			Limit:             10,
			IncludeIterations: true,
		},
		Summary: domainlive.LiveLoopAuditArtifactSummary{
			ReviewStatus:           domainlive.LiveLoopAuditReviewStatusClear,
			ReviewReason:           "no recent live-loop audit runs found",
			OperatorActionRequired: false,
		},
	}
}

func liveReadinessHandoffAuditFromArtifact(artifact domainlive.LiveLoopAuditArtifact) domainlive.LiveReadinessArtifactAudit {
	return domainlive.LiveReadinessArtifactAudit{
		Limit:                  artifact.Query.Limit,
		Total:                  artifact.Summary.Total,
		Running:                artifact.Summary.Running,
		Completed:              artifact.Summary.Completed,
		Failed:                 artifact.Summary.Failed,
		ReviewStatus:           artifact.Summary.ReviewStatus,
		ReviewRunID:            artifact.Summary.ReviewRunID,
		ReviewReason:           artifact.Summary.ReviewReason,
		OperatorActionRequired: artifact.Summary.OperatorActionRequired,
	}
}

func cloneLiveReadinessHandoffArtifact(artifact domainlive.LiveReadinessArtifact) domainlive.LiveReadinessArtifact {
	artifact.FailedChecks = append([]string(nil), artifact.FailedChecks...)
	artifact.Checks = append([]domainlive.LiveReadinessArtifactCheck(nil), artifact.Checks...)
	if artifact.PlanFile != nil {
		plan := *artifact.PlanFile
		artifact.PlanFile = &plan
	}
	return artifact
}
