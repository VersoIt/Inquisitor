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

	"github.com/shopspring/decimal"

	domainlive "github.com/VersoIt/Inquisitor/internal/live"
	domainrisk "github.com/VersoIt/Inquisitor/internal/risk"
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

func TestRunLiveHandoffVerifyAuditArtifactTableDriven(t *testing.T) {
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	plan := validLiveHandoffPlanArtifact(now.Add(-time.Minute), domainlive.LiveOrderPlanArtifactSourceDecisionID)

	tests := []struct {
		name            string
		mutateAudit     func(*domainlive.LiveLoopAuditArtifact)
		mutateReadiness func(*domainlive.LiveReadinessArtifact)
		args            func(planFile string, readinessFile string, auditFile string) []string
		wantErrSub      string
		wantLogSub      string
	}{
		{
			name: "valid audit artifact handoff",
			args: func(planFile string, readinessFile string, auditFile string) []string {
				return []string{"-config", "configs/live.local.yaml", "-plan-file", planFile, "-readiness-file", readinessFile, "-audit-file", auditFile}
			},
			wantLogSub: `"audit_review_status":"CLEAR"`,
		},
		{
			name: "stale audit artifact",
			mutateAudit: func(a *domainlive.LiveLoopAuditArtifact) {
				a.CreatedAt = now.Add(-time.Hour)
			},
			args: func(planFile string, readinessFile string, auditFile string) []string {
				return []string{"-config", "configs/live.local.yaml", "-plan-file", planFile, "-readiness-file", readinessFile, "-audit-file", auditFile, "-max-audit-age", "10m"}
			},
			wantErrSub: "stale",
		},
		{
			name: "readiness audit mismatch",
			mutateReadiness: func(a *domainlive.LiveReadinessArtifact) {
				a.Audit.Completed = 1
			},
			args: func(planFile string, readinessFile string, auditFile string) []string {
				return []string{"-config", "configs/live.local.yaml", "-plan-file", planFile, "-readiness-file", readinessFile, "-audit-file", auditFile}
			},
			wantErrSub: "audit.completed",
		},
		{
			name: "audit config mismatch",
			mutateAudit: func(a *domainlive.LiveLoopAuditArtifact) {
				a.ConfigPath = "configs/other-live.yaml"
			},
			args: func(planFile string, readinessFile string, auditFile string) []string {
				return []string{"-config", "configs/live.local.yaml", "-plan-file", planFile, "-readiness-file", readinessFile, "-audit-file", auditFile}
			},
			wantErrSub: "audit config_path",
		},
		{
			name: "nonpositive max audit age",
			args: func(planFile string, readinessFile string, auditFile string) []string {
				return []string{"-config", "configs/live.local.yaml", "-plan-file", planFile, "-readiness-file", readinessFile, "-audit-file", auditFile, "-max-audit-age", "0s"}
			},
			wantErrSub: "max-audit-age",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			planFile := writeLiveHandoffPlanArtifact(t, plan)
			audit := validLiveHandoffAuditArtifact(now.Add(-30*time.Second), "configs/live.local.yaml")
			if tt.mutateAudit != nil {
				tt.mutateAudit(&audit)
			}
			auditFile := writeLiveHandoffAuditArtifact(t, audit)
			readiness := validLiveHandoffReadinessArtifact(t, now.Add(-20*time.Second), "configs/live.local.yaml", planFile, plan)
			readiness.Audit = liveHandoffReadinessAuditFromAuditArtifact(audit)
			if tt.mutateReadiness != nil {
				tt.mutateReadiness(&readiness)
			}
			readinessFile := writeLiveHandoffReadinessArtifact(t, readiness)

			var output bytes.Buffer
			err := runLiveHandoffVerify(context.Background(), tt.args(planFile, readinessFile, auditFile), liveHandoffVerifyDependencies{
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

func TestRunLiveHandoffVerifyKillSwitchArtifactTableDriven(t *testing.T) {
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	plan := validLiveHandoffPlanArtifact(now.Add(-time.Minute), domainlive.LiveOrderPlanArtifactSourceDecisionID)

	tests := []struct {
		name             string
		mutateKillSwitch func(*domainrisk.KillSwitchArtifact)
		mutateReadiness  func(*domainlive.LiveReadinessArtifact)
		args             func(planFile string, readinessFile string, killSwitchFile string) []string
		wantErrSub       string
		wantLogSub       string
	}{
		{
			name: "valid inactive kill switch handoff",
			args: func(planFile string, readinessFile string, killSwitchFile string) []string {
				return []string{"-config", "configs/live.local.yaml", "-plan-file", planFile, "-readiness-file", readinessFile, "-kill-switch-file", killSwitchFile}
			},
			wantLogSub: `"kill_switch_active":false`,
		},
		{
			name: "stale kill switch artifact",
			mutateKillSwitch: func(a *domainrisk.KillSwitchArtifact) {
				a.CreatedAt = now.Add(-time.Hour)
			},
			args: func(planFile string, readinessFile string, killSwitchFile string) []string {
				return []string{"-config", "configs/live.local.yaml", "-plan-file", planFile, "-readiness-file", readinessFile, "-kill-switch-file", killSwitchFile, "-max-kill-switch-age", "10m"}
			},
			wantErrSub: "stale",
		},
		{
			name: "active kill switch artifact stops handoff",
			mutateKillSwitch: func(a *domainrisk.KillSwitchArtifact) {
				updatedAt := now.Add(-2 * time.Minute)
				a.State = &domainrisk.KillSwitchArtifactState{
					Active:    true,
					Reason:    "operator emergency stop",
					Source:    "operator",
					UpdatedAt: &updatedAt,
				}
			},
			mutateReadiness: func(a *domainlive.LiveReadinessArtifact) {
				updatedAt := now.Add(-2 * time.Minute)
				a.KillSwitch = domainlive.LiveReadinessArtifactKillSwitch{
					Active:    true,
					Reason:    "operator emergency stop",
					Source:    "operator",
					UpdatedAt: &updatedAt,
				}
			},
			args: func(planFile string, readinessFile string, killSwitchFile string) []string {
				return []string{"-config", "configs/live.local.yaml", "-plan-file", planFile, "-readiness-file", readinessFile, "-kill-switch-file", killSwitchFile}
			},
			wantErrSub: "inactive",
		},
		{
			name: "kill switch config mismatch",
			mutateKillSwitch: func(a *domainrisk.KillSwitchArtifact) {
				a.ConfigPath = "configs/other-live.yaml"
			},
			args: func(planFile string, readinessFile string, killSwitchFile string) []string {
				return []string{"-config", "configs/live.local.yaml", "-plan-file", planFile, "-readiness-file", readinessFile, "-kill-switch-file", killSwitchFile}
			},
			wantErrSub: "config_path",
		},
		{
			name: "kill switch readiness mismatch",
			mutateReadiness: func(a *domainlive.LiveReadinessArtifact) {
				updatedAt := now.Add(-2 * time.Minute)
				a.KillSwitch = domainlive.LiveReadinessArtifactKillSwitch{
					Reason:    "operator verified recovery",
					Source:    "operator",
					UpdatedAt: &updatedAt,
				}
			},
			args: func(planFile string, readinessFile string, killSwitchFile string) []string {
				return []string{"-config", "configs/live.local.yaml", "-plan-file", planFile, "-readiness-file", readinessFile, "-kill-switch-file", killSwitchFile}
			},
			wantErrSub: "readiness",
		},
		{
			name: "untrimmed kill switch path",
			args: func(planFile string, readinessFile string, killSwitchFile string) []string {
				return []string{"-config", "configs/live.local.yaml", "-plan-file", planFile, "-readiness-file", readinessFile, "-kill-switch-file", killSwitchFile + " "}
			},
			wantErrSub: "kill-switch-file",
		},
		{
			name: "nonpositive max kill switch age",
			args: func(planFile string, readinessFile string, killSwitchFile string) []string {
				return []string{"-config", "configs/live.local.yaml", "-plan-file", planFile, "-readiness-file", readinessFile, "-kill-switch-file", killSwitchFile, "-max-kill-switch-age", "0s"}
			},
			wantErrSub: "max-kill-switch-age",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			planFile := writeLiveHandoffPlanArtifact(t, plan)
			readiness := validLiveHandoffReadinessArtifact(t, now.Add(-20*time.Second), "configs/live.local.yaml", planFile, plan)
			if tt.mutateReadiness != nil {
				tt.mutateReadiness(&readiness)
			}
			readinessFile := writeLiveHandoffReadinessArtifact(t, readiness)
			killSwitch := validLiveHandoffKillSwitchArtifact(t, now.Add(-30*time.Second), "configs/live.local.yaml")
			if tt.mutateKillSwitch != nil {
				tt.mutateKillSwitch(&killSwitch)
			}
			killSwitchFile := writeLiveHandoffKillSwitchArtifact(t, killSwitch)

			var output bytes.Buffer
			err := runLiveHandoffVerify(context.Background(), tt.args(planFile, readinessFile, killSwitchFile), liveHandoffVerifyDependencies{
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
				t.Fatalf("verify kill switch handoff: %v\nlogs:\n%s", err, output.String())
			}
			if !strings.Contains(output.String(), tt.wantLogSub) {
				t.Fatalf("expected logs to contain %s, got\n%s", tt.wantLogSub, output.String())
			}
		})
	}
}

func TestRunLiveHandoffVerifyDeployCheckArtifactTableDriven(t *testing.T) {
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	plan := validLiveHandoffPlanArtifact(now.Add(-time.Minute), domainlive.LiveOrderPlanArtifactSourceDecisionID)
	plan.Quantity = "0.001"
	plan.Notional = "100"
	plan.MaxLoss = "1"
	planFile := writeLiveHandoffPlanArtifact(t, plan)
	audit := validLiveHandoffAuditArtifact(now.Add(-30*time.Second), "configs/live.local.yaml")
	auditFile := writeLiveHandoffAuditArtifact(t, audit)
	readiness := validLiveHandoffReadinessArtifact(t, now.Add(-20*time.Second), "configs/live.local.yaml", planFile, plan)
	readiness.Audit = liveHandoffReadinessAuditFromAuditArtifact(audit)
	readinessFile := writeLiveHandoffReadinessArtifact(t, readiness)
	validDeployCheck := validLiveHandoffDeploymentCheckArtifact(t, now.Add(-10*time.Second), "configs/live.local.yaml", planFile, readinessFile, auditFile)

	validArgs := func(deployCheckFile string) []string {
		return []string{
			"-config", "configs/live.local.yaml",
			"-plan-file", planFile,
			"-readiness-file", readinessFile,
			"-audit-file", auditFile,
			"-deploy-check-file", deployCheckFile,
			"-decision-id", plan.DecisionID,
			"-execute",
			"-subaccount-confirmed",
			"-max-initial-live-capital-usdt", "100",
			"-max-iterations", "1",
			"-max-runtime", "15s",
			"-iteration-timeout", "10s",
		}
	}

	tests := []struct {
		name       string
		artifact   domainlive.LiveDeploymentCheckArtifact
		args       func(string) []string
		wantErrSub string
		wantLogSub string
	}{
		{
			name:       "valid deploy check handoff",
			artifact:   validDeployCheck,
			args:       validArgs,
			wantLogSub: `"deploy_check_ready":true`,
		},
		{
			name:     "valid deploy check handoff defaults decision id from plan",
			artifact: validDeployCheck,
			args: func(deployCheckFile string) []string {
				return []string{
					"-config", "configs/live.local.yaml",
					"-plan-file", planFile,
					"-readiness-file", readinessFile,
					"-audit-file", auditFile,
					"-deploy-check-file", deployCheckFile,
					"-execute",
					"-subaccount-confirmed",
					"-max-initial-live-capital-usdt", "100",
					"-max-iterations", "1",
					"-max-runtime", "15s",
					"-iteration-timeout", "10s",
				}
			},
			wantLogSub: `"selected_decision_id":"risk_decision_live_verify_0001"`,
		},
		{
			name:     "deploy check requires audit file",
			artifact: validDeployCheck,
			args: func(deployCheckFile string) []string {
				return []string{
					"-config", "configs/live.local.yaml",
					"-plan-file", planFile,
					"-readiness-file", readinessFile,
					"-deploy-check-file", deployCheckFile,
					"-decision-id", plan.DecisionID,
					"-execute",
					"-subaccount-confirmed",
				}
			},
			wantErrSub: "requires -audit-file",
		},
		{
			name: "stale deploy check artifact",
			artifact: mutateLiveHandoffDeploymentCheckArtifact(validDeployCheck, func(a *domainlive.LiveDeploymentCheckArtifact) {
				a.CreatedAt = now.Add(-time.Hour)
			}),
			args: func(deployCheckFile string) []string {
				return append(validArgs(deployCheckFile), "-max-deploy-check-age", "10m")
			},
			wantErrSub: "stale",
		},
		{
			name: "failed deploy check artifact",
			artifact: mutateLiveHandoffDeploymentCheckArtifact(validDeployCheck, func(a *domainlive.LiveDeploymentCheckArtifact) {
				a.Checks[1].Status = domainlive.ReadinessCheckStatusFail
				a.Checks[1].Details = "-execute=true is required"
				a.Summary.Passed--
				a.Summary.Failed++
				a.FailedChecks = []string{a.Checks[1].Name}
				a.Ready = false
				a.Execution.Execute = false
			}),
			args:       validArgs,
			wantErrSub: "ready",
		},
		{
			name: "readiness sha mismatch",
			artifact: mutateLiveHandoffDeploymentCheckArtifact(validDeployCheck, func(a *domainlive.LiveDeploymentCheckArtifact) {
				a.ReadinessFile.SHA256 = strings.Repeat("d", 64)
			}),
			args:       validArgs,
			wantErrSub: "readiness_file.sha256",
		},
		{
			name:     "runtime mismatch",
			artifact: validDeployCheck,
			args: func(deployCheckFile string) []string {
				args := validArgs(deployCheckFile)
				return append(args, "-max-runtime", "20s")
			},
			wantErrSub: "execution.max_runtime",
		},
		{
			name:     "execute flag mismatch",
			artifact: validDeployCheck,
			args: func(deployCheckFile string) []string {
				return []string{
					"-config", "configs/live.local.yaml",
					"-plan-file", planFile,
					"-readiness-file", readinessFile,
					"-audit-file", auditFile,
					"-deploy-check-file", deployCheckFile,
					"-decision-id", plan.DecisionID,
					"-subaccount-confirmed",
					"-max-initial-live-capital-usdt", "100",
					"-max-iterations", "1",
					"-max-runtime", "15s",
					"-iteration-timeout", "10s",
				}
			},
			wantErrSub: "execution.execute",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			deployCheckFile := writeLiveHandoffDeploymentCheckArtifact(t, tt.artifact)

			var output bytes.Buffer
			err := runLiveHandoffVerify(context.Background(), tt.args(deployCheckFile), liveHandoffVerifyDependencies{
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
				t.Fatalf("verify deploy-check handoff: %v\nlogs:\n%s", err, output.String())
			}
			if !strings.Contains(output.String(), tt.wantLogSub) {
				t.Fatalf("expected logs to contain %s, got\n%s", tt.wantLogSub, output.String())
			}
		})
	}
}

func TestRunLiveHandoffVerifyOpsReportArtifactTableDriven(t *testing.T) {
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	plan := validLiveHandoffPlanArtifact(now.Add(-time.Minute), domainlive.LiveOrderPlanArtifactSourceDecisionID)
	planFile := writeLiveHandoffPlanArtifact(t, plan)
	readiness := validLiveHandoffReadinessArtifact(t, now.Add(-20*time.Second), "configs/live.local.yaml", planFile, plan)
	readinessFile := writeLiveHandoffReadinessArtifact(t, readiness)
	validOpsReport := validLiveHandoffOpsReportArtifact(now.Add(-10*time.Second), "configs/live.local.yaml", plan)

	validArgs := func(opsReportFile string) []string {
		return []string{
			"-config", "configs/live.local.yaml",
			"-plan-file", planFile,
			"-readiness-file", readinessFile,
			"-ops-report-file", opsReportFile,
			"-decision-id", plan.DecisionID,
		}
	}

	tests := []struct {
		name       string
		artifact   domainlive.LiveOpsReportArtifact
		args       func(string) []string
		wantErrSub string
		wantLogSub string
	}{
		{
			name:       "valid clear ops report handoff",
			artifact:   validOpsReport,
			args:       validArgs,
			wantLogSub: `"ops_report_status":"CLEAR"`,
		},
		{
			name: "stale ops report artifact",
			artifact: mutateLiveHandoffOpsReportArtifact(validOpsReport, func(a *domainlive.LiveOpsReportArtifact) {
				a.CreatedAt = now.Add(-time.Hour)
			}),
			args: func(opsReportFile string) []string {
				return append(validArgs(opsReportFile), "-max-ops-report-age", "10m")
			},
			wantErrSub: "stale",
		},
		{
			name: "attention ops report artifact",
			artifact: mutateLiveHandoffOpsReportArtifact(validOpsReport, func(a *domainlive.LiveOpsReportArtifact) {
				a.Checks[1].Status = domainlive.ReadinessCheckStatusWarn
				a.Checks[1].Details = "no pending approved LIVE decisions without submissions"
				a.Status = domainlive.LiveOpsStatusAttention
				a.Summary.Passed--
				a.Summary.Warned++
			}),
			args:       validArgs,
			wantErrSub: "CLEAR",
		},
		{
			name: "ops report config mismatch",
			artifact: mutateLiveHandoffOpsReportArtifact(validOpsReport, func(a *domainlive.LiveOpsReportArtifact) {
				a.ConfigPath = "configs/other-live.yaml"
			}),
			args:       validArgs,
			wantErrSub: "config_path",
		},
		{
			name:       "untrimmed ops report path",
			artifact:   validOpsReport,
			args:       func(opsReportFile string) []string { return validArgs(" " + opsReportFile + " ") },
			wantErrSub: "ops-report-file",
		},
		{
			name:     "nonpositive max ops report age",
			artifact: validOpsReport,
			args: func(opsReportFile string) []string {
				return append(validArgs(opsReportFile), "-max-ops-report-age", "0s")
			},
			wantErrSub: "max-ops-report-age",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opsReportFile := writeLiveHandoffOpsReportArtifact(t, tt.artifact)

			var output bytes.Buffer
			err := runLiveHandoffVerify(context.Background(), tt.args(opsReportFile), liveHandoffVerifyDependencies{
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
				t.Fatalf("verify ops report handoff: %v\nlogs:\n%s", err, output.String())
			}
			if !strings.Contains(output.String(), tt.wantLogSub) {
				t.Fatalf("expected logs to contain %s, got\n%s", tt.wantLogSub, output.String())
			}
		})
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

func validLiveHandoffKillSwitchArtifact(t *testing.T, createdAt time.Time, configPath string) domainrisk.KillSwitchArtifact {
	t.Helper()

	state := domainrisk.KillSwitchState{}
	artifact, err := domainrisk.BuildKillSwitchArtifact(domainrisk.BuildKillSwitchArtifactRequest{
		CreatedAt:  createdAt,
		ConfigPath: configPath,
		Action:     domainrisk.KillSwitchArtifactActionState,
		State:      &state,
	})
	if err != nil {
		t.Fatalf("build kill switch artifact: %v", err)
	}
	return artifact
}

func writeLiveHandoffKillSwitchArtifact(t *testing.T, artifact domainrisk.KillSwitchArtifact) string {
	t.Helper()

	if err := domainrisk.ValidateKillSwitchArtifact(artifact); err != nil {
		t.Fatalf("validate kill switch artifact: %v", err)
	}
	payload, err := json.MarshalIndent(artifact, "", "  ")
	if err != nil {
		t.Fatalf("marshal kill switch artifact: %v", err)
	}
	path := filepath.Join(t.TempDir(), "risk-kill-switch-state.json")
	if err := os.WriteFile(path, append(payload, '\n'), 0o600); err != nil {
		t.Fatalf("write kill switch artifact: %v", err)
	}
	return path
}

func writeLiveHandoffAuditArtifact(t *testing.T, artifact domainlive.LiveLoopAuditArtifact) string {
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

func writeLiveHandoffDeploymentCheckArtifact(t *testing.T, artifact domainlive.LiveDeploymentCheckArtifact) string {
	t.Helper()

	if err := domainlive.ValidateLiveDeploymentCheckArtifact(artifact); err != nil {
		t.Fatalf("validate deployment check artifact: %v", err)
	}
	payload, err := json.MarshalIndent(artifact, "", "  ")
	if err != nil {
		t.Fatalf("marshal deployment check artifact: %v", err)
	}
	path := filepath.Join(t.TempDir(), "live-deploy-check.json")
	if err := os.WriteFile(path, append(payload, '\n'), 0o600); err != nil {
		t.Fatalf("write deployment check artifact: %v", err)
	}
	return path
}

func validLiveHandoffOpsReportArtifact(
	createdAt time.Time,
	configPath string,
	plan domainlive.LiveOrderPlanArtifact,
) domainlive.LiveOpsReportArtifact {
	oldestAt := createdAt.Add(-2 * time.Minute)
	newestAt := createdAt.Add(-time.Minute)
	return domainlive.LiveOpsReportArtifact{
		SchemaVersion: domainlive.LiveOpsReportArtifactSchemaVersion,
		CreatedAt:     createdAt,
		ConfigPath:    configPath,
		Status:        domainlive.LiveOpsStatusClear,
		Summary: domainlive.LiveOpsReportArtifactSummary{
			Total:  3,
			Passed: 3,
		},
		Checks: []domainlive.LiveOpsReportArtifactCheck{
			{Name: "kill_switch", Status: domainlive.ReadinessCheckStatusPass, Details: "kill switch is inactive"},
			{Name: "pending_live_decision", Status: domainlive.ReadinessCheckStatusPass, Details: "next decision " + plan.DecisionID + " for " + plan.Symbol + " is pending"},
			{Name: "recent_live_loop_audit", Status: domainlive.ReadinessCheckStatusPass, Details: "recent live-loop audit has no running or failed runs"},
		},
		Pending: domainlive.LiveOpsReportArtifactPending{
			Symbol:         plan.Symbol,
			Limit:          10,
			Total:          1,
			NextDecisionID: plan.DecisionID,
			NextSymbol:     plan.Symbol,
			OldestAt:       &oldestAt,
			NewestAt:       &newestAt,
		},
		Audit: domainlive.LiveOpsReportArtifactAudit{
			Limit:                  10,
			ReviewStatus:           domainlive.LiveLoopAuditReviewStatusClear,
			ReviewReason:           "recent live-loop audit has no running or failed runs",
			OperatorActionRequired: false,
		},
		KillSwitch: domainlive.LiveOpsReportArtifactKillSwitch{},
	}
}

func writeLiveHandoffOpsReportArtifact(t *testing.T, artifact domainlive.LiveOpsReportArtifact) string {
	t.Helper()

	if err := domainlive.ValidateLiveOpsReportArtifact(artifact); err != nil {
		t.Fatalf("validate ops report artifact: %v", err)
	}
	payload, err := json.MarshalIndent(artifact, "", "  ")
	if err != nil {
		t.Fatalf("marshal ops report artifact: %v", err)
	}
	path := filepath.Join(t.TempDir(), "live-ops-report.json")
	if err := os.WriteFile(path, append(payload, '\n'), 0o600); err != nil {
		t.Fatalf("write ops report artifact: %v", err)
	}
	return path
}

func mutateLiveHandoffOpsReportArtifact(
	artifact domainlive.LiveOpsReportArtifact,
	mutate func(*domainlive.LiveOpsReportArtifact),
) domainlive.LiveOpsReportArtifact {
	artifact.Checks = append([]domainlive.LiveOpsReportArtifactCheck(nil), artifact.Checks...)
	artifact.FailedChecks = append([]string(nil), artifact.FailedChecks...)
	if artifact.PositionDrift != nil {
		drift := *artifact.PositionDrift
		drift.FailedChecks = append([]string(nil), drift.FailedChecks...)
		drift.Checks = append([]domainlive.LiveOpsReportArtifactCheck(nil), drift.Checks...)
		drift.Comparisons = append([]domainlive.LiveOpsReportArtifactPositionDriftItem(nil), drift.Comparisons...)
		artifact.PositionDrift = &drift
	}
	if artifact.FirstOrderReview != nil {
		review := *artifact.FirstOrderReview
		review.FailedChecks = append([]string(nil), review.FailedChecks...)
		artifact.FirstOrderReview = &review
	}
	if mutate != nil {
		mutate(&artifact)
	}
	return artifact
}

func validLiveHandoffDeploymentCheckArtifact(
	t *testing.T,
	createdAt time.Time,
	configPath string,
	planPath string,
	readinessPath string,
	auditPath string,
) domainlive.LiveDeploymentCheckArtifact {
	t.Helper()

	plan, err := loadLiveHandoffPlanArtifactFile(planPath)
	if err != nil {
		t.Fatalf("load plan artifact: %v", err)
	}
	readiness, err := loadLiveHandoffReadinessArtifactFile(readinessPath)
	if err != nil {
		t.Fatalf("load readiness artifact: %v", err)
	}
	audit, hasAudit, err := loadLiveHandoffAuditArtifactFile(auditPath)
	if err != nil {
		t.Fatalf("load audit artifact: %v", err)
	}
	if !hasAudit {
		t.Fatal("audit artifact is required")
	}
	deployment := domainlive.LiveDeploymentCheckRequest{
		ConfigPath:                strings.TrimSpace(configPath),
		PlanFilePath:              strings.TrimSpace(planPath),
		PlanFileSHA256:            plan.SHA256,
		PlanArtifact:              plan.Artifact,
		ReadinessArtifact:         readiness.Artifact,
		AuditArtifact:             audit.Artifact,
		Now:                       createdAt.UTC(),
		MaxPlanArtifactAge:        domainlive.DefaultLiveOrderPlanArtifactMaxAge,
		MaxReadinessArtifactAge:   domainlive.DefaultLiveReadinessArtifactMaxAge,
		MaxAuditArtifactAge:       domainlive.DefaultLiveLoopAuditArtifactMaxAge,
		Execute:                   true,
		SubaccountConfirmed:       true,
		DecisionID:                plan.Artifact.DecisionID,
		MaxInitialLiveCapitalUSDT: decimal.NewFromInt(100),
		MicroCapitalLimitUSDT:     domainlive.DefaultLiveDeploymentMicroCapitalLimitUSDT(),
		MaxIterations:             1,
		MaxRuntime:                15 * time.Second,
		IterationTimeout:          10 * time.Second,
	}
	if plan.Artifact.Source == domainlive.LiveOrderPlanArtifactSourceSelectPending {
		deployment.DecisionID = ""
		deployment.SelectPending = true
		deployment.PendingSymbol = plan.Artifact.PendingSymbol
	}
	report, err := domainlive.BuildLiveDeploymentCheckReport(deployment)
	if err != nil {
		t.Fatalf("build deployment check report: %v", err)
	}
	if !report.Ready {
		t.Fatalf("deployment check report must be ready: %#v", report)
	}
	artifact, err := domainlive.BuildLiveDeploymentCheckArtifact(domainlive.BuildLiveDeploymentCheckArtifactRequest{
		Report:              report,
		Deployment:          deployment,
		CreatedAt:           createdAt.UTC(),
		ConfigPath:          configPath,
		PlanFilePath:        planPath,
		PlanFileSHA256:      plan.SHA256,
		ReadinessFilePath:   readinessPath,
		ReadinessFileSHA256: readiness.SHA256,
		AuditFilePath:       auditPath,
		AuditFileSHA256:     audit.SHA256,
	})
	if err != nil {
		t.Fatalf("build deployment check artifact: %v", err)
	}
	return artifact
}

func validLiveHandoffAuditArtifact(createdAt time.Time, configPath string) domainlive.LiveLoopAuditArtifact {
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

func liveHandoffReadinessAuditFromAuditArtifact(artifact domainlive.LiveLoopAuditArtifact) domainlive.LiveReadinessArtifactAudit {
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

func mutateLiveHandoffDeploymentCheckArtifact(
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
