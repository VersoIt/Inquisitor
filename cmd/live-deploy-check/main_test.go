package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	domainlive "github.com/VersoIt/Inquisitor/internal/live"
)

func TestRunLiveDeployCheckTableDriven(t *testing.T) {
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	plan := validLiveDeployPlanArtifact(now.Add(-time.Minute), domainlive.LiveOrderPlanArtifactSourceDecisionID)

	tests := []struct {
		name            string
		plan            domainlive.LiveOrderPlanArtifact
		mutateReadiness func(*domainlive.LiveReadinessArtifact)
		mutateAudit     func(*domainlive.LiveLoopAuditArtifact)
		args            func(planFile string, readinessFile string, auditFile string, plan domainlive.LiveOrderPlanArtifact) []string
		wantErrSub      string
		wantLogSub      string
	}{
		{
			name: "valid explicit deployment check",
			args: func(planFile string, readinessFile string, auditFile string, plan domainlive.LiveOrderPlanArtifact) []string {
				return []string{"-config", "configs/live.local.yaml", "-plan-file", planFile, "-readiness-file", readinessFile, "-audit-file", auditFile, "-decision-id", plan.DecisionID, "-subaccount-confirmed", "-execute"}
			},
			wantLogSub: `"msg":"live deploy check passed"`,
		},
		{
			name: "valid select pending deployment check",
			plan: func() domainlive.LiveOrderPlanArtifact {
				artifact := validLiveDeployPlanArtifact(now.Add(-time.Minute), domainlive.LiveOrderPlanArtifactSourceSelectPending)
				artifact.PendingSymbol = artifact.Symbol
				return artifact
			}(),
			args: func(planFile string, readinessFile string, auditFile string, plan domainlive.LiveOrderPlanArtifact) []string {
				return []string{"-config", "configs/live.local.yaml", "-plan-file", planFile, "-readiness-file", readinessFile, "-audit-file", auditFile, "-select-pending", "-pending-symbol", strings.ToLower(plan.Symbol), "-subaccount-confirmed", "-execute"}
			},
			wantLogSub: `"select_pending":true`,
		},
		{
			name: "missing execute fails checklist",
			args: func(planFile string, readinessFile string, auditFile string, plan domainlive.LiveOrderPlanArtifact) []string {
				return []string{"-config", "configs/live.local.yaml", "-plan-file", planFile, "-readiness-file", readinessFile, "-audit-file", auditFile, "-decision-id", plan.DecisionID, "-subaccount-confirmed"}
			},
			wantErrSub: "live_loop_armed",
		},
		{
			name: "two iterations fail first order checklist",
			args: func(planFile string, readinessFile string, auditFile string, plan domainlive.LiveOrderPlanArtifact) []string {
				return []string{"-config", "configs/live.local.yaml", "-plan-file", planFile, "-readiness-file", readinessFile, "-audit-file", auditFile, "-decision-id", plan.DecisionID, "-subaccount-confirmed", "-execute", "-max-iterations", "2"}
			},
			wantErrSub: "live_loop_bounds",
		},
		{
			name: "capital above micro limit fails checklist",
			args: func(planFile string, readinessFile string, auditFile string, plan domainlive.LiveOrderPlanArtifact) []string {
				return []string{"-config", "configs/live.local.yaml", "-plan-file", planFile, "-readiness-file", readinessFile, "-audit-file", auditFile, "-decision-id", plan.DecisionID, "-subaccount-confirmed", "-execute", "-max-initial-live-capital-usdt", "250"}
			},
			wantErrSub: "live_micro_capital",
		},
		{
			name: "stale audit fails checklist",
			mutateAudit: func(a *domainlive.LiveLoopAuditArtifact) {
				a.CreatedAt = now.Add(-time.Hour)
			},
			args: func(planFile string, readinessFile string, auditFile string, plan domainlive.LiveOrderPlanArtifact) []string {
				return []string{"-config", "configs/live.local.yaml", "-plan-file", planFile, "-readiness-file", readinessFile, "-audit-file", auditFile, "-decision-id", plan.DecisionID, "-subaccount-confirmed", "-execute", "-max-audit-age", "10m"}
			},
			wantErrSub: "deployment_artifact_freshness",
		},
		{
			name: "readiness warning fails checklist",
			mutateReadiness: func(a *domainlive.LiveReadinessArtifact) {
				a.Checks = append(a.Checks, domainlive.LiveReadinessArtifactCheck{
					Name:    "recent_live_loop_audit",
					Status:  domainlive.ReadinessCheckStatusWarn,
					Details: "operator review required before first order",
				})
				a.Summary = domainlive.LiveReadinessArtifactSummary{Total: 2, Passed: 1, Warned: 1}
			},
			args: func(planFile string, readinessFile string, auditFile string, plan domainlive.LiveOrderPlanArtifact) []string {
				return []string{"-config", "configs/live.local.yaml", "-plan-file", planFile, "-readiness-file", readinessFile, "-audit-file", auditFile, "-decision-id", plan.DecisionID, "-subaccount-confirmed", "-execute"}
			},
			wantErrSub: "readiness_operator_review",
		},
		{
			name: "audit config mismatch fails handoff",
			mutateAudit: func(a *domainlive.LiveLoopAuditArtifact) {
				a.ConfigPath = "configs/other-live.yaml"
			},
			args: func(planFile string, readinessFile string, auditFile string, plan domainlive.LiveOrderPlanArtifact) []string {
				return []string{"-config", "configs/live.local.yaml", "-plan-file", planFile, "-readiness-file", readinessFile, "-audit-file", auditFile, "-decision-id", plan.DecisionID, "-subaccount-confirmed", "-execute"}
			},
			wantErrSub: "deployment_artifact_handoff",
		},
		{
			name: "source mismatch fails checklist",
			plan: func() domainlive.LiveOrderPlanArtifact {
				artifact := validLiveDeployPlanArtifact(now.Add(-time.Minute), domainlive.LiveOrderPlanArtifactSourceSelectPending)
				artifact.PendingSymbol = artifact.Symbol
				return artifact
			}(),
			args: func(planFile string, readinessFile string, auditFile string, plan domainlive.LiveOrderPlanArtifact) []string {
				return []string{"-config", "configs/live.local.yaml", "-plan-file", planFile, "-readiness-file", readinessFile, "-audit-file", auditFile, "-decision-id", plan.DecisionID, "-subaccount-confirmed", "-execute"}
			},
			wantErrSub: "live_loop_source",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testPlan := tt.plan
			if testPlan.SchemaVersion == "" {
				testPlan = plan
			}
			planFile := writeLiveDeployPlanArtifact(t, testPlan)
			audit := validLiveDeployAuditArtifact(now.Add(-20*time.Second), "configs/live.local.yaml")
			if tt.mutateAudit != nil {
				tt.mutateAudit(&audit)
			}
			readiness := validLiveDeployReadinessArtifact(t, now.Add(-30*time.Second), "configs/live.local.yaml", planFile, testPlan)
			readiness.Audit = liveDeployReadinessAuditFromAuditArtifact(audit)
			if tt.mutateReadiness != nil {
				tt.mutateReadiness(&readiness)
			}
			readinessFile := writeLiveDeployReadinessArtifact(t, readiness)
			auditFile := writeLiveDeployAuditArtifact(t, audit)

			var output bytes.Buffer
			err := runLiveDeployCheck(context.Background(), tt.args(planFile, readinessFile, auditFile, testPlan), liveDeployCheckDependencies{
				now:    func() time.Time { return now },
				output: &output,
			})
			if tt.wantErrSub != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
					t.Fatalf("expected error containing %q, got %v\nlogs:\n%s", tt.wantErrSub, err, output.String())
				}
				return
			}
			if err != nil {
				t.Fatalf("run deploy check: %v\nlogs:\n%s", err, output.String())
			}
			if !strings.Contains(output.String(), tt.wantLogSub) {
				t.Fatalf("expected logs to contain %s, got\n%s", tt.wantLogSub, output.String())
			}
		})
	}
}

func TestRunLiveDeployCheckRequiresArtifacts(t *testing.T) {
	var output bytes.Buffer
	err := runLiveDeployCheck(context.Background(), []string{"-subaccount-confirmed", "-execute"}, liveDeployCheckDependencies{
		now:    func() time.Time { return time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC) },
		output: &output,
	})
	if err == nil || !strings.Contains(err.Error(), "plan-file") {
		t.Fatalf("expected required plan-file error, got %v", err)
	}
}

func TestRunLiveDeployCheckWritesArtifactBeforeFailure(t *testing.T) {
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	plan := validLiveDeployPlanArtifact(now.Add(-time.Minute), domainlive.LiveOrderPlanArtifactSourceDecisionID)
	planFile := writeLiveDeployPlanArtifact(t, plan)
	audit := validLiveDeployAuditArtifact(now.Add(-20*time.Second), "configs/live.local.yaml")
	readiness := validLiveDeployReadinessArtifact(t, now.Add(-30*time.Second), "configs/live.local.yaml", planFile, plan)
	readiness.Audit = liveDeployReadinessAuditFromAuditArtifact(audit)
	readinessFile := writeLiveDeployReadinessArtifact(t, readiness)
	auditFile := writeLiveDeployAuditArtifact(t, audit)
	artifactPath := filepath.Join(t.TempDir(), "artifacts", "live-deploy-check.json")

	var output bytes.Buffer
	err := runLiveDeployCheck(context.Background(), []string{
		"-config", "configs/live.local.yaml",
		"-plan-file", planFile,
		"-readiness-file", readinessFile,
		"-audit-file", auditFile,
		"-artifact-path", artifactPath,
		"-decision-id", plan.DecisionID,
		"-subaccount-confirmed",
	}, liveDeployCheckDependencies{
		now:    func() time.Time { return now },
		output: &output,
	})
	if err == nil || !strings.Contains(err.Error(), "live_loop_armed") {
		t.Fatalf("expected live_loop_armed failure, got %v\nlogs:\n%s", err, output.String())
	}
	if !strings.Contains(output.String(), `"msg":"live deploy check artifact written"`) {
		t.Fatalf("expected artifact write log, got\n%s", output.String())
	}

	artifact := readLiveDeployCheckArtifact(t, artifactPath)
	if err := domainlive.ValidateLiveDeploymentCheckArtifact(artifact); err != nil {
		t.Fatalf("validate deployment check artifact: %v", err)
	}
	if artifact.Ready {
		t.Fatal("expected failed artifact")
	}
	if !sameLiveDeployStringSet(artifact.FailedChecks, []string{"live_loop_armed"}) {
		t.Fatalf("failed checks mismatch: got %#v", artifact.FailedChecks)
	}
	if artifact.PlanFile.SHA256 != liveDeployPlanFileSHA256(t, planFile) {
		t.Fatalf("plan sha mismatch: got %q", artifact.PlanFile.SHA256)
	}
	readinessLoaded, err := loadLiveDeployReadinessArtifactFile(readinessFile)
	if err != nil {
		t.Fatalf("load readiness artifact: %v", err)
	}
	if artifact.ReadinessFile.SHA256 != readinessLoaded.SHA256 {
		t.Fatalf("readiness sha mismatch: got %q want %q", artifact.ReadinessFile.SHA256, readinessLoaded.SHA256)
	}
	auditLoaded, err := loadLiveDeployAuditArtifactFile(auditFile)
	if err != nil {
		t.Fatalf("load audit artifact: %v", err)
	}
	if artifact.AuditFile.SHA256 != auditLoaded.SHA256 {
		t.Fatalf("audit sha mismatch: got %q want %q", artifact.AuditFile.SHA256, auditLoaded.SHA256)
	}
}

func TestParseLiveDeployPositiveDecimalFlagTableDriven(t *testing.T) {
	tests := []struct {
		name       string
		value      string
		wantErrSub string
	}{
		{name: "valid", value: "100"},
		{name: "untrimmed", value: " 100 ", wantErrSub: "trimmed"},
		{name: "empty", value: "", wantErrSub: "required"},
		{name: "zero", value: "0", wantErrSub: "positive"},
		{name: "negative", value: "-1", wantErrSub: "positive"},
		{name: "bad decimal", value: "abc", wantErrSub: "decimal"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseLiveDeployPositiveDecimalFlag("capital", tt.value)
			if tt.wantErrSub != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("parse capital: %v", err)
			}
		})
	}
}

func validLiveDeployPlanArtifact(createdAt time.Time, source string) domainlive.LiveOrderPlanArtifact {
	artifact := domainlive.LiveOrderPlanArtifact{
		SchemaVersion:       domainlive.LiveOrderPlanArtifactSchemaVersion,
		Source:              source,
		RunID:               "live_loop_deploy_0001",
		DecisionID:          "risk_decision_live_deploy_0001",
		SubmissionID:        "live_sub_deploy_0001",
		ClientOrderID:       "inq_live_deploy_0001",
		Exchange:            "bybit",
		Category:            "linear",
		Symbol:              "BTCUSDT",
		Side:                domainlive.OrderSideLong,
		OrderType:           domainlive.OrderTypeMarket,
		TimeInForce:         domainlive.TimeInForceIOC,
		LimitPrice:          "0",
		Quantity:            "0.001",
		EntryPrice:          "100000",
		Notional:            "100",
		MaxLoss:             "1",
		StopLoss:            "99000",
		TakeProfit:          "102000",
		Leverage:            "1",
		Confidence:          80,
		DecisionCreatedAt:   createdAt.Add(-2 * time.Minute),
		RecordedAt:          createdAt.Add(-time.Minute),
		SubmissionCreatedAt: createdAt,
	}
	if source == domainlive.LiveOrderPlanArtifactSourceSelectPending {
		artifact.PendingSymbol = artifact.Symbol
	}
	return artifact
}

func validLiveDeployReadinessArtifact(
	t *testing.T,
	createdAt time.Time,
	configPath string,
	planPath string,
	plan domainlive.LiveOrderPlanArtifact,
) domainlive.LiveReadinessArtifact {
	t.Helper()

	return domainlive.LiveReadinessArtifact{
		SchemaVersion: domainlive.LiveReadinessArtifactSchemaVersion,
		CreatedAt:     createdAt,
		ConfigPath:    configPath,
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
			Path:          planPath,
			SHA256:        liveDeployPlanFileSHA256(t, planPath),
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

func validLiveDeployAuditArtifact(createdAt time.Time, configPath string) domainlive.LiveLoopAuditArtifact {
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

func liveDeployReadinessAuditFromAuditArtifact(artifact domainlive.LiveLoopAuditArtifact) domainlive.LiveReadinessArtifactAudit {
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

func writeLiveDeployPlanArtifact(t *testing.T, artifact domainlive.LiveOrderPlanArtifact) string {
	t.Helper()

	if err := domainlive.ValidateLiveOrderPlanArtifact(artifact); err != nil {
		t.Fatalf("validate plan artifact: %v", err)
	}
	payload, err := json.MarshalIndent(artifact, "", "  ")
	if err != nil {
		t.Fatalf("marshal plan artifact: %v", err)
	}
	path := filepath.Join(t.TempDir(), "live-order-plan.json")
	if err := os.WriteFile(path, append(payload, '\n'), 0o600); err != nil {
		t.Fatalf("write plan artifact: %v", err)
	}
	return path
}

func writeLiveDeployReadinessArtifact(t *testing.T, artifact domainlive.LiveReadinessArtifact) string {
	t.Helper()

	if err := domainlive.ValidateLiveReadinessArtifact(artifact); err != nil {
		t.Fatalf("validate readiness artifact: %v", err)
	}
	payload, err := json.MarshalIndent(artifact, "", "  ")
	if err != nil {
		t.Fatalf("marshal readiness artifact: %v", err)
	}
	path := filepath.Join(t.TempDir(), "live-readiness.json")
	if err := os.WriteFile(path, append(payload, '\n'), 0o600); err != nil {
		t.Fatalf("write readiness artifact: %v", err)
	}
	return path
}

func writeLiveDeployAuditArtifact(t *testing.T, artifact domainlive.LiveLoopAuditArtifact) string {
	t.Helper()

	if err := domainlive.ValidateLiveLoopAuditArtifact(artifact); err != nil {
		t.Fatalf("validate audit artifact: %v", err)
	}
	payload, err := json.MarshalIndent(artifact, "", "  ")
	if err != nil {
		t.Fatalf("marshal audit artifact: %v", err)
	}
	path := filepath.Join(t.TempDir(), "live-loop-audit.json")
	if err := os.WriteFile(path, append(payload, '\n'), 0o600); err != nil {
		t.Fatalf("write audit artifact: %v", err)
	}
	return path
}

func readLiveDeployCheckArtifact(t *testing.T, path string) domainlive.LiveDeploymentCheckArtifact {
	t.Helper()

	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read deployment check artifact: %v", err)
	}
	var artifact domainlive.LiveDeploymentCheckArtifact
	if err := json.Unmarshal(payload, &artifact); err != nil {
		t.Fatalf("decode deployment check artifact: %v", err)
	}
	return artifact
}

func liveDeployPlanFileSHA256(t *testing.T, path string) string {
	t.Helper()

	loaded, err := loadLiveDeployPlanArtifactFile(path)
	if err != nil {
		t.Fatalf("load plan artifact: %v", err)
	}
	return loaded.SHA256
}

func sameLiveDeployStringSet(left []string, right []string) bool {
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
