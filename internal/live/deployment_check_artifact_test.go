package live_test

import (
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	domainlive "github.com/VersoIt/Inquisitor/internal/live"
)

func TestBuildLiveDeploymentCheckArtifactTableDriven(t *testing.T) {
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	valid := validLiveDeploymentCheckArtifact(t, now)

	tests := []struct {
		name       string
		mutate     func(*domainlive.LiveDeploymentCheckArtifact)
		wantErrSub string
	}{
		{name: "valid ready deployment check artifact"},
		{name: "bad schema", mutate: func(a *domainlive.LiveDeploymentCheckArtifact) {
			a.SchemaVersion = "old"
		}, wantErrSub: "schema_version"},
		{name: "missing config path", mutate: func(a *domainlive.LiveDeploymentCheckArtifact) {
			a.ConfigPath = ""
		}, wantErrSub: "config_path"},
		{name: "summary mismatch", mutate: func(a *domainlive.LiveDeploymentCheckArtifact) {
			a.Summary.Failed = 1
		}, wantErrSub: "summary"},
		{name: "ready mismatch", mutate: func(a *domainlive.LiveDeploymentCheckArtifact) {
			a.Ready = false
		}, wantErrSub: "ready"},
		{name: "failed checks mismatch", mutate: func(a *domainlive.LiveDeploymentCheckArtifact) {
			a.Checks[0].Status = domainlive.ReadinessCheckStatusFail
			a.Summary.Passed--
			a.Summary.Failed++
			a.Ready = false
		}, wantErrSub: "failed_checks"},
		{name: "failed artifact can omit selected decision", mutate: func(a *domainlive.LiveDeploymentCheckArtifact) {
			a.Checks[0].Status = domainlive.ReadinessCheckStatusFail
			a.Summary.Passed--
			a.Summary.Failed++
			a.Ready = false
			a.FailedChecks = []string{a.Checks[0].Name}
			a.Execution.SelectedDecisionID = ""
		}},
		{name: "invalid plan file sha256", mutate: func(a *domainlive.LiveDeploymentCheckArtifact) {
			a.PlanFile.SHA256 = "ABC"
		}, wantErrSub: "plan_file.sha256"},
		{name: "untrimmed plan pending symbol", mutate: func(a *domainlive.LiveDeploymentCheckArtifact) {
			a.PlanFile.PendingSymbol = " BTCUSDT "
		}, wantErrSub: "plan_file.pending_symbol"},
		{name: "invalid readiness file sha256", mutate: func(a *domainlive.LiveDeploymentCheckArtifact) {
			a.ReadinessFile.SHA256 = "123"
		}, wantErrSub: "readiness_file.sha256"},
		{name: "invalid audit review status", mutate: func(a *domainlive.LiveDeploymentCheckArtifact) {
			a.AuditFile.ReviewStatus = "BROKEN"
		}, wantErrSub: "audit_file.review_status"},
		{name: "ready artifact requires execute", mutate: func(a *domainlive.LiveDeploymentCheckArtifact) {
			a.Execution.Execute = false
		}, wantErrSub: "execute=true"},
		{name: "ready artifact requires one iteration", mutate: func(a *domainlive.LiveDeploymentCheckArtifact) {
			a.Execution.MaxIterations = 2
		}, wantErrSub: "max_iterations=1"},
		{name: "ready artifact requires micro capital cap", mutate: func(a *domainlive.LiveDeploymentCheckArtifact) {
			a.Execution.MaxInitialLiveCapitalUSDT = "101"
		}, wantErrSub: "within micro_capital_limit"},
		{name: "ready artifact requires bounded iteration timeout", mutate: func(a *domainlive.LiveDeploymentCheckArtifact) {
			a.Execution.IterationTimeout = "20s"
		}, wantErrSub: "iteration_timeout <= max_runtime"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			artifact := valid
			artifact.FailedChecks = append([]string(nil), valid.FailedChecks...)
			artifact.Checks = append([]domainlive.LiveDeploymentCheckArtifactCheck(nil), valid.Checks...)
			if tt.mutate != nil {
				tt.mutate(&artifact)
			}

			err := domainlive.ValidateLiveDeploymentCheckArtifact(artifact)
			if tt.wantErrSub != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("validate deployment check artifact: %v", err)
			}
		})
	}
}

func TestBuildLiveDeploymentCheckArtifactPreservesSourceSelectionFailure(t *testing.T) {
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	req := validLiveDeploymentCheckArtifactRequest(t, now)
	req.Deployment.PlanArtifact.Source = domainlive.LiveOrderPlanArtifactSourceSelectPending
	req.Deployment.PlanArtifact.PendingSymbol = req.Deployment.PlanArtifact.Symbol

	report, err := domainlive.BuildLiveDeploymentCheckReport(req.Deployment)
	if err != nil {
		t.Fatalf("build deployment report: %v", err)
	}
	if report.Ready {
		t.Fatal("expected report to fail when plan source does not match execution source")
	}
	if report.SelectedDecisionID != "" {
		t.Fatalf("expected empty selected decision for source failure, got %q", report.SelectedDecisionID)
	}
	req.Report = report

	artifact, err := domainlive.BuildLiveDeploymentCheckArtifact(req)
	if err != nil {
		t.Fatalf("build deployment check artifact: %v", err)
	}
	if artifact.Execution.SelectedDecisionID != "" {
		t.Fatalf("expected empty artifact selected decision for source failure, got %q", artifact.Execution.SelectedDecisionID)
	}
	if !sameStringSliceSet(artifact.FailedChecks, []string{"live_loop_source"}) {
		t.Fatalf("failed checks mismatch: got %#v", artifact.FailedChecks)
	}
}

func TestBuildLiveDeploymentCheckArtifactPreservesFailedReport(t *testing.T) {
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	req := validLiveDeploymentCheckArtifactRequest(t, now)
	req.Deployment.Execute = false

	report, err := domainlive.BuildLiveDeploymentCheckReport(req.Deployment)
	if err != nil {
		t.Fatalf("build deployment report: %v", err)
	}
	if report.Ready {
		t.Fatal("expected report to fail when execution is not armed")
	}
	req.Report = report

	artifact, err := domainlive.BuildLiveDeploymentCheckArtifact(req)
	if err != nil {
		t.Fatalf("build deployment check artifact: %v", err)
	}
	if artifact.Ready {
		t.Fatal("expected failed artifact")
	}
	if !containsLiveDeploymentFailedCheck(report.Checks, "live_loop_armed") {
		t.Fatalf("expected live_loop_armed failed check, got %#v", domainlive.LiveDeploymentCheckFailedNames(report.Checks))
	}
	if !sameStringSliceSet(artifact.FailedChecks, []string{"live_loop_armed"}) {
		t.Fatalf("failed checks mismatch: got %#v", artifact.FailedChecks)
	}
	if err := domainlive.ValidateLiveDeploymentCheckArtifact(artifact); err != nil {
		t.Fatalf("validate failed deployment check artifact: %v", err)
	}
}

func TestValidateLiveDeploymentCheckArtifactFreshnessTableDriven(t *testing.T) {
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	valid := validLiveDeploymentCheckArtifact(t, now.Add(-time.Minute))

	tests := []struct {
		name       string
		artifact   domainlive.LiveDeploymentCheckArtifact
		now        time.Time
		maxAge     time.Duration
		wantErrSub string
	}{
		{name: "fresh artifact", artifact: valid, now: now, maxAge: 10 * time.Minute},
		{name: "stale artifact", artifact: mutateLiveDeploymentCheckArtifact(valid, func(a *domainlive.LiveDeploymentCheckArtifact) {
			a.CreatedAt = now.Add(-11 * time.Minute)
		}), now: now, maxAge: 10 * time.Minute, wantErrSub: "stale"},
		{name: "future artifact", artifact: mutateLiveDeploymentCheckArtifact(valid, func(a *domainlive.LiveDeploymentCheckArtifact) {
			a.CreatedAt = now.Add(time.Second)
		}), now: now, maxAge: 10 * time.Minute, wantErrSub: "future"},
		{name: "zero max age", artifact: valid, now: now, wantErrSub: "max_age"},
		{name: "missing now", artifact: valid, maxAge: 10 * time.Minute, wantErrSub: "now"},
		{name: "invalid artifact first", artifact: mutateLiveDeploymentCheckArtifact(valid, func(a *domainlive.LiveDeploymentCheckArtifact) {
			a.Checks = nil
		}), now: now, maxAge: 10 * time.Minute, wantErrSub: "checks"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := domainlive.ValidateLiveDeploymentCheckArtifactFreshness(tt.artifact, tt.now, tt.maxAge)
			if tt.wantErrSub != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("validate deployment check artifact freshness: %v", err)
			}
		})
	}
}

func TestValidateLiveDeploymentCheckArtifactHandoffTableDriven(t *testing.T) {
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	req := validLiveDeploymentCheckArtifactRequest(t, now)
	valid := validLiveDeploymentCheckArtifact(t, now)
	validExecution := validLiveDeploymentCheckArtifactHandoffExecution(req)

	tests := []struct {
		name            string
		mutateArtifact  func(*domainlive.LiveDeploymentCheckArtifact)
		mutateExecution func(*domainlive.LiveDeploymentCheckArtifactHandoffExecution)
		wantErrSub      string
	}{
		{name: "valid explicit deployment handoff"},
		{name: "not ready deployment artifact", mutateArtifact: func(a *domainlive.LiveDeploymentCheckArtifact) {
			a.Checks[0].Status = domainlive.ReadinessCheckStatusFail
			a.Summary.Passed--
			a.Summary.Failed++
			a.FailedChecks = []string{a.Checks[0].Name}
			a.Ready = false
			a.Execution.Execute = false
		}, wantErrSub: "ready"},
		{name: "warnings are rejected for final deployment handoff", mutateArtifact: func(a *domainlive.LiveDeploymentCheckArtifact) {
			a.Checks[0].Status = domainlive.ReadinessCheckStatusWarn
			a.Summary.Passed--
			a.Summary.Warned++
		}, wantErrSub: "warnings"},
		{name: "config mismatch", mutateExecution: func(e *domainlive.LiveDeploymentCheckArtifactHandoffExecution) {
			e.ConfigPath = "configs/other-live.yaml"
		}, wantErrSub: "config_path"},
		{name: "plan path mismatch", mutateExecution: func(e *domainlive.LiveDeploymentCheckArtifactHandoffExecution) {
			e.PlanPath = "artifacts/other-plan.json"
		}, wantErrSub: "plan_file.path"},
		{name: "plan sha mismatch", mutateExecution: func(e *domainlive.LiveDeploymentCheckArtifactHandoffExecution) {
			e.PlanFileSHA256 = strings.Repeat("d", 64)
		}, wantErrSub: "plan_file.sha256"},
		{name: "plan metadata mismatch", mutateExecution: func(e *domainlive.LiveDeploymentCheckArtifactHandoffExecution) {
			e.PlanArtifact.DecisionID = "risk_decision_live_other_0001"
		}, wantErrSub: "plan_file.decision_id"},
		{name: "readiness sha mismatch", mutateExecution: func(e *domainlive.LiveDeploymentCheckArtifactHandoffExecution) {
			e.ReadinessFileSHA256 = strings.Repeat("e", 64)
		}, wantErrSub: "readiness_file.sha256"},
		{name: "audit review mismatch", mutateArtifact: func(a *domainlive.LiveDeploymentCheckArtifact) {
			a.AuditFile.ReviewStatus = domainlive.LiveLoopAuditReviewStatusReview
		}, wantErrSub: "audit_file.review_status"},
		{name: "source mode mismatch", mutateExecution: func(e *domainlive.LiveDeploymentCheckArtifactHandoffExecution) {
			e.SelectPending = true
			e.PendingQuery = domainlive.PendingLiveDecisionQuery{Symbol: "BTCUSDT", Limit: 1}
		}, wantErrSub: "execution.select_pending"},
		{name: "selected decision mismatch", mutateExecution: func(e *domainlive.LiveDeploymentCheckArtifactHandoffExecution) {
			e.SelectedDecisionID = "risk_decision_live_other_0001"
		}, wantErrSub: "execution.selected_decision_id"},
		{name: "capital mismatch", mutateExecution: func(e *domainlive.LiveDeploymentCheckArtifactHandoffExecution) {
			e.MaxInitialLiveCapitalUSDT = decimal.NewFromInt(90)
		}, wantErrSub: "max_initial_live_capital_usdt"},
		{name: "max iterations mismatch", mutateExecution: func(e *domainlive.LiveDeploymentCheckArtifactHandoffExecution) {
			e.MaxIterations = 2
		}, wantErrSub: "execution.max_iterations"},
		{name: "runtime mismatch", mutateExecution: func(e *domainlive.LiveDeploymentCheckArtifactHandoffExecution) {
			e.MaxRuntime = 20 * time.Second
		}, wantErrSub: "execution.max_runtime"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			artifact := mutateLiveDeploymentCheckArtifact(valid, tt.mutateArtifact)
			execution := validExecution
			if tt.mutateExecution != nil {
				tt.mutateExecution(&execution)
			}

			err := domainlive.ValidateLiveDeploymentCheckArtifactHandoff(artifact, execution)
			if tt.wantErrSub != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("validate deployment check handoff: %v", err)
			}
		})
	}
}

func validLiveDeploymentCheckArtifact(
	t *testing.T,
	now time.Time,
) domainlive.LiveDeploymentCheckArtifact {
	t.Helper()

	req := validLiveDeploymentCheckArtifactRequest(t, now)
	artifact, err := domainlive.BuildLiveDeploymentCheckArtifact(req)
	if err != nil {
		t.Fatalf("build deployment check artifact: %v", err)
	}
	return artifact
}

func validLiveDeploymentCheckArtifactRequest(
	t *testing.T,
	now time.Time,
) domainlive.BuildLiveDeploymentCheckArtifactRequest {
	t.Helper()

	plan := validLiveOrderPlanArtifact(now.Add(-time.Minute))
	plan.Quantity = "0.001"
	plan.Notional = "100"
	planPath := "artifacts/live-order-plan.json"
	planSHA256 := strings.Repeat("a", 64)
	readinessPath := "artifacts/live-readiness.json"
	readinessSHA256 := strings.Repeat("b", 64)
	auditPath := "artifacts/live-loop-audit.json"
	auditSHA256 := strings.Repeat("c", 64)
	audit := validLiveReadinessHandoffAuditArtifact(now.Add(-20*time.Second), "configs/live.local.yaml")
	readiness := validLiveReadinessHandoffArtifact(now.Add(-30*time.Second), planPath, planSHA256, plan)
	readiness.Audit = liveReadinessHandoffAuditFromArtifact(audit)
	deployment := validLiveDeploymentCheckRequest(now, planPath, planSHA256, plan, readiness, audit)

	report, err := domainlive.BuildLiveDeploymentCheckReport(deployment)
	if err != nil {
		t.Fatalf("build deployment report: %v", err)
	}
	return domainlive.BuildLiveDeploymentCheckArtifactRequest{
		Report:              report,
		Deployment:          deployment,
		CreatedAt:           now,
		ConfigPath:          deployment.ConfigPath,
		PlanFilePath:        planPath,
		PlanFileSHA256:      planSHA256,
		ReadinessFilePath:   readinessPath,
		ReadinessFileSHA256: readinessSHA256,
		AuditFilePath:       auditPath,
		AuditFileSHA256:     auditSHA256,
	}
}

func validLiveDeploymentCheckArtifactHandoffExecution(
	req domainlive.BuildLiveDeploymentCheckArtifactRequest,
) domainlive.LiveDeploymentCheckArtifactHandoffExecution {
	return domainlive.LiveDeploymentCheckArtifactHandoffExecution{
		ConfigPath:                req.ConfigPath,
		PlanPath:                  req.PlanFilePath,
		PlanFileSHA256:            req.PlanFileSHA256,
		PlanArtifact:              req.Deployment.PlanArtifact,
		ReadinessPath:             req.ReadinessFilePath,
		ReadinessFileSHA256:       req.ReadinessFileSHA256,
		ReadinessArtifact:         req.Deployment.ReadinessArtifact,
		AuditPath:                 req.AuditFilePath,
		AuditFileSHA256:           req.AuditFileSHA256,
		AuditArtifact:             req.Deployment.AuditArtifact,
		Execute:                   req.Deployment.Execute,
		SubaccountConfirmed:       req.Deployment.SubaccountConfirmed,
		SelectPending:             req.Deployment.SelectPending,
		PendingQuery:              req.Report.PendingQuery,
		DecisionID:                req.Deployment.DecisionID,
		SelectedDecisionID:        req.Report.SelectedDecisionID,
		MaxInitialLiveCapitalUSDT: req.Deployment.MaxInitialLiveCapitalUSDT,
		MaxIterations:             req.Deployment.MaxIterations,
		MaxRuntime:                req.Deployment.MaxRuntime,
		IterationTimeout:          req.Deployment.IterationTimeout,
	}
}

func mutateLiveDeploymentCheckArtifact(
	artifact domainlive.LiveDeploymentCheckArtifact,
	mutate func(*domainlive.LiveDeploymentCheckArtifact),
) domainlive.LiveDeploymentCheckArtifact {
	artifact.FailedChecks = append([]string(nil), artifact.FailedChecks...)
	artifact.Checks = append([]domainlive.LiveDeploymentCheckArtifactCheck(nil), artifact.Checks...)
	if mutate != nil {
		mutate(&artifact)
	}
	return artifact
}

func sameStringSliceSet(left []string, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	seen := make(map[string]int, len(left))
	for _, value := range left {
		seen[value]++
	}
	for _, value := range right {
		if seen[value] == 0 {
			return false
		}
		seen[value]--
	}
	return true
}
