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

func TestRunLiveHandoffVerifyTableDriven(t *testing.T) {
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	plan := validLiveHandoffPlanArtifact(now.Add(-time.Minute), domainlive.LiveOrderPlanArtifactSourceDecisionID)
	selectPlan := validLiveHandoffPlanArtifact(now.Add(-time.Minute), domainlive.LiveOrderPlanArtifactSourceSelectPending)
	selectPlan.PendingSymbol = selectPlan.Symbol

	tests := []struct {
		name            string
		args            func(planFile string, readinessFile string, artifact domainlive.LiveOrderPlanArtifact) []string
		plan            domainlive.LiveOrderPlanArtifact
		mutateReadiness func(*domainlive.LiveReadinessArtifact)
		wantErrSub      string
		wantLogSub      string
	}{
		{
			name: "valid explicit handoff",
			args: func(planFile string, readinessFile string, artifact domainlive.LiveOrderPlanArtifact) []string {
				return []string{"-config", "configs/live.local.yaml", "-plan-file", planFile, "-readiness-file", readinessFile, "-decision-id", artifact.DecisionID}
			},
			plan:       plan,
			wantLogSub: `"msg":"live handoff verified"`,
		},
		{
			name: "valid explicit handoff defaults decision id from plan",
			args: func(planFile string, readinessFile string, _ domainlive.LiveOrderPlanArtifact) []string {
				return []string{"-config", "configs/live.local.yaml", "-plan-file", planFile, "-readiness-file", readinessFile}
			},
			plan:       plan,
			wantLogSub: `"selected_decision_id":"risk_decision_live_verify_0001"`,
		},
		{
			name: "valid selector handoff",
			args: func(planFile string, readinessFile string, _ domainlive.LiveOrderPlanArtifact) []string {
				return []string{"-config", "configs/live.local.yaml", "-plan-file", planFile, "-readiness-file", readinessFile, "-select-pending"}
			},
			plan:       selectPlan,
			wantLogSub: `"select_pending":true`,
		},
		{
			name: "valid selector handoff with explicit pending symbol",
			args: func(planFile string, readinessFile string, artifact domainlive.LiveOrderPlanArtifact) []string {
				return []string{"-config", "configs/live.local.yaml", "-plan-file", planFile, "-readiness-file", readinessFile, "-select-pending", "-pending-symbol", strings.ToLower(artifact.Symbol)}
			},
			plan:       selectPlan,
			wantLogSub: `"pending_symbol":"BTCUSDT"`,
		},
		{
			name: "stale plan",
			args: func(planFile string, readinessFile string, _ domainlive.LiveOrderPlanArtifact) []string {
				return []string{"-config", "configs/live.local.yaml", "-plan-file", planFile, "-readiness-file", readinessFile, "-max-plan-age", "10m"}
			},
			plan: mutateLiveHandoffPlanArtifact(plan, func(a *domainlive.LiveOrderPlanArtifact) {
				a.SubmissionCreatedAt = now.Add(-time.Hour)
			}),
			wantErrSub: "stale",
		},
		{
			name: "stale readiness",
			args: func(planFile string, readinessFile string, _ domainlive.LiveOrderPlanArtifact) []string {
				return []string{"-config", "configs/live.local.yaml", "-plan-file", planFile, "-readiness-file", readinessFile, "-max-readiness-age", "10m"}
			},
			plan: plan,
			mutateReadiness: func(a *domainlive.LiveReadinessArtifact) {
				a.CreatedAt = now.Add(-time.Hour)
			},
			wantErrSub: "stale",
		},
		{
			name: "readiness sha mismatch",
			args: func(planFile string, readinessFile string, _ domainlive.LiveOrderPlanArtifact) []string {
				return []string{"-config", "configs/live.local.yaml", "-plan-file", planFile, "-readiness-file", readinessFile}
			},
			plan: plan,
			mutateReadiness: func(a *domainlive.LiveReadinessArtifact) {
				a.PlanFile.SHA256 = strings.Repeat("f", 64)
			},
			wantErrSub: "plan_file.sha256",
		},
		{
			name: "config mismatch",
			args: func(planFile string, readinessFile string, _ domainlive.LiveOrderPlanArtifact) []string {
				return []string{"-config", "configs/other-live.yaml", "-plan-file", planFile, "-readiness-file", readinessFile}
			},
			plan:       plan,
			wantErrSub: "config_path",
		},
		{
			name: "source mode mismatch",
			args: func(planFile string, readinessFile string, _ domainlive.LiveOrderPlanArtifact) []string {
				return []string{"-config", "configs/live.local.yaml", "-plan-file", planFile, "-readiness-file", readinessFile}
			},
			plan:       selectPlan,
			wantErrSub: "explicit decision execution",
		},
		{
			name: "decision id mismatch",
			args: func(planFile string, readinessFile string, _ domainlive.LiveOrderPlanArtifact) []string {
				return []string{"-config", "configs/live.local.yaml", "-plan-file", planFile, "-readiness-file", readinessFile, "-decision-id", "risk_decision_live_verify_0002"}
			},
			plan:       plan,
			wantErrSub: "decision-id",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testPlanFile := writeLiveHandoffPlanArtifact(t, tt.plan)
			readiness := validLiveHandoffReadinessArtifact(t, now.Add(-30*time.Second), "configs/live.local.yaml", testPlanFile, tt.plan)
			if tt.mutateReadiness != nil {
				tt.mutateReadiness(&readiness)
			}
			readinessFile := writeLiveHandoffReadinessArtifact(t, readiness)
			args := tt.args(testPlanFile, readinessFile, tt.plan)

			var output bytes.Buffer
			err := runLiveHandoffVerify(context.Background(), args, liveHandoffVerifyDependencies{
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
				t.Fatalf("verify handoff: %v\nlogs:\n%s", err, output.String())
			}
			if !strings.Contains(output.String(), tt.wantLogSub) {
				t.Fatalf("expected logs to contain %s, got\n%s", tt.wantLogSub, output.String())
			}
		})
	}
}

func TestRunLiveHandoffVerifyRequiresArtifacts(t *testing.T) {
	var output bytes.Buffer
	err := runLiveHandoffVerify(context.Background(), []string{}, liveHandoffVerifyDependencies{
		now:    func() time.Time { return time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC) },
		output: &output,
	})
	if err == nil || !strings.Contains(err.Error(), "plan-file") {
		t.Fatalf("expected required plan-file error, got %v", err)
	}
}

func validLiveHandoffPlanArtifact(createdAt time.Time, source string) domainlive.LiveOrderPlanArtifact {
	artifact := domainlive.LiveOrderPlanArtifact{
		SchemaVersion:       domainlive.LiveOrderPlanArtifactSchemaVersion,
		Source:              source,
		RunID:               "live_loop_verify_0001",
		DecisionID:          "risk_decision_live_verify_0001",
		SubmissionID:        "live_sub_verify_0001",
		ClientOrderID:       "inq_live_verify_0001",
		Exchange:            "bybit",
		Category:            "linear",
		Symbol:              "BTCUSDT",
		Side:                domainlive.OrderSideLong,
		OrderType:           domainlive.OrderTypeMarket,
		TimeInForce:         domainlive.TimeInForceIOC,
		LimitPrice:          "0",
		Quantity:            "0.005",
		EntryPrice:          "100000",
		Notional:            "500",
		MaxLoss:             "5",
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

func validLiveHandoffReadinessArtifact(
	t *testing.T,
	createdAt time.Time,
	configPath string,
	planPath string,
	plan domainlive.LiveOrderPlanArtifact,
) domainlive.LiveReadinessArtifact {
	t.Helper()

	planSHA256 := liveHandoffPlanFileSHA256(t, planPath)
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
			SHA256:        planSHA256,
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

func writeLiveHandoffPlanArtifact(t *testing.T, artifact domainlive.LiveOrderPlanArtifact) string {
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

func writeLiveHandoffReadinessArtifact(t *testing.T, artifact domainlive.LiveReadinessArtifact) string {
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

func liveHandoffPlanFileSHA256(t *testing.T, path string) string {
	t.Helper()

	loaded, err := loadLiveHandoffPlanArtifactFile(path)
	if err != nil {
		t.Fatalf("load plan artifact: %v", err)
	}
	return loaded.SHA256
}

func mutateLiveHandoffPlanArtifact(
	artifact domainlive.LiveOrderPlanArtifact,
	mutate func(*domainlive.LiveOrderPlanArtifact),
) domainlive.LiveOrderPlanArtifact {
	if mutate != nil {
		mutate(&artifact)
	}
	return artifact
}

func mutateLiveHandoffReadinessArtifact(
	artifact domainlive.LiveReadinessArtifact,
	mutate func(*domainlive.LiveReadinessArtifact),
) domainlive.LiveReadinessArtifact {
	artifact.Checks = append([]domainlive.LiveReadinessArtifactCheck(nil), artifact.Checks...)
	if artifact.PlanFile != nil {
		plan := *artifact.PlanFile
		artifact.PlanFile = &plan
	}
	if mutate != nil {
		mutate(&artifact)
	}
	return artifact
}
