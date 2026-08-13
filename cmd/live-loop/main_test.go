package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/shopspring/decimal"

	"github.com/VersoIt/Inquisitor/internal/config"
	domainlive "github.com/VersoIt/Inquisitor/internal/live"
	domainrisk "github.com/VersoIt/Inquisitor/internal/risk"
)

func TestDeterministicLiveLoopIdentityIsStableAndBybitSafe(t *testing.T) {
	first, err := deterministicLiveLoopIdentity(" risk_decision_live_cli_0001 ", "")
	if err != nil {
		t.Fatalf("identity: %v", err)
	}
	second, err := deterministicLiveLoopIdentity("risk_decision_live_cli_0001", "")
	if err != nil {
		t.Fatalf("identity: %v", err)
	}

	if first != second {
		t.Fatalf("identity must be deterministic after trimming: first=%#v second=%#v", first, second)
	}
	if len(first.ClientOrderID) > 36 || len(first.SubmissionID) > 36 {
		t.Fatalf("identity must stay within Bybit orderLinkId length: %#v", first)
	}
	for _, value := range []string{first.RunID, first.SubmissionID, first.ClientOrderID} {
		for _, r := range value {
			if !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') && r != '_' {
				t.Fatalf("identity contains unsupported character %q in %q", r, value)
			}
		}
	}

	custom, err := deterministicLiveLoopIdentity("risk_decision_live_cli_0001", "live_loop_operator_0001")
	if err != nil {
		t.Fatalf("custom run identity: %v", err)
	}
	if custom.RunID != "live_loop_operator_0001" {
		t.Fatalf("custom run id mismatch: %#v", custom)
	}
	if custom.SubmissionID != first.SubmissionID || custom.ClientOrderID != first.ClientOrderID {
		t.Fatalf("custom run id must not change idempotency keys: got %#v want submission=%s client=%s", custom, first.SubmissionID, first.ClientOrderID)
	}

	_, err = deterministicLiveLoopIdentity("risk_decision_live_cli_0001", " live_loop_operator_0001 ")
	if err == nil || !strings.Contains(err.Error(), "run-id") || !strings.Contains(err.Error(), "trimmed") {
		t.Fatalf("expected trimmed run-id error, got %v", err)
	}
}

func TestRunLiveLoopRequiresExecuteBeforeSideEffects(t *testing.T) {
	var opened bool

	err := runLiveLoop(context.Background(), []string{
		"-decision-id", "risk_decision_live_cli_0001",
	}, liveLoopDependencies{
		openDB: func(context.Context, config.DatabaseConfig) (*sql.DB, error) {
			opened = true
			return nil, nil
		},
		output: &bytes.Buffer{},
	})
	if err == nil || !strings.Contains(err.Error(), "execute") {
		t.Fatalf("expected execute gate error, got %v", err)
	}
	if opened {
		t.Fatal("database must not be opened when execute gate is not armed")
	}
}

func TestRunLiveLoopProcessesPersistedDecisionThroughBoundedLoop(t *testing.T) {
	t.Setenv("TRADING_LIVE_CONFIRM", "true")
	t.Setenv("BYBIT_API_KEY", "actual-live-api-key-value")
	t.Setenv("BYBIT_API_SECRET", "actual-live-api-secret-value")

	now := time.Now().UTC()
	decisionTime := now.Add(-2 * time.Second)
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	mock.ExpectQuery("SELECT decision_id, intent_id, mode").
		WillReturnRows(liveLoopRiskDecisionRows(decisionTime))
	mock.ExpectExec("INSERT INTO live_loop_runs").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectQuery("SELECT active, reason, source, created_at").
		WillReturnRows(sqlmock.NewRows([]string{"active", "reason", "source", "created_at"}))
	mock.ExpectExec("INSERT INTO live_account_snapshots").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("INSERT INTO live_position_snapshots").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectQuery("SELECT active, reason, source, created_at").
		WillReturnRows(sqlmock.NewRows([]string{"active", "reason", "source", "created_at"}))
	mock.ExpectQuery("SELECT decision_id, intent_id, mode").
		WillReturnRows(liveLoopRiskDecisionRows(decisionTime))
	mock.ExpectQuery("SELECT active, reason, source, created_at").
		WillReturnRows(sqlmock.NewRows([]string{"active", "reason", "source", "created_at"}))
	mock.ExpectExec("INSERT INTO live_order_submissions").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("INSERT INTO live_order_acknowledgements").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("INSERT INTO live_order_status_snapshots").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("INSERT INTO live_position_snapshots").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("INSERT INTO live_loop_iterations").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("UPDATE live_loop_runs").
		WillReturnResult(sqlmock.NewResult(0, 1))

	identity, err := deterministicLiveLoopIdentity("risk_decision_live_cli_0001", "live_loop_cli_0001")
	if err != nil {
		t.Fatalf("identity: %v", err)
	}
	configPath := writeLiveLoopConfig(t)
	planArtifact := liveLoopPlanArtifact(t, "risk_decision_live_cli_0001", "live_loop_cli_0001", decisionTime)
	planFile := writeLiveLoopPlanArtifact(t, planArtifact)
	auditArtifact := liveLoopAuditArtifact(now, configPath)
	auditFile := writeLiveLoopAuditArtifact(t, auditArtifact)
	readinessArtifact := liveLoopReadinessArtifact(t, configPath, planFile, planArtifact, now)
	readinessArtifact.Audit = liveLoopReadinessAuditFromAuditArtifact(auditArtifact)
	readinessFile := writeLiveLoopReadinessArtifact(t, readinessArtifact)
	killSwitchFile := writeLiveLoopKillSwitchArtifact(t, liveLoopKillSwitchArtifact(t, now.Add(-time.Second), configPath))
	deployCheckArtifact := liveLoopDeploymentCheckArtifact(
		t,
		now,
		configPath,
		planFile,
		readinessFile,
		auditFile,
		planArtifact,
		readinessArtifact,
		auditArtifact,
		decimal.NewFromInt(500),
	)
	deployCheckFile := writeLiveLoopDeploymentCheckArtifact(t, deployCheckArtifact)
	executor := &fakeLiveLoopExecutor{receivedAt: now}
	accountReader := &fakeLiveLoopAccountReader{
		snapshot: validLiveLoopAccountSnapshot(t),
	}

	var output bytes.Buffer
	err = runLiveLoop(context.Background(), []string{
		"-config", configPath,
		"-plan-file", planFile,
		"-readiness-file", readinessFile,
		"-kill-switch-file", killSwitchFile,
		"-audit-file", auditFile,
		"-deploy-check-file", deployCheckFile,
		"-subaccount-confirmed",
		"-max-initial-live-capital-usdt", "100",
		"-max-iterations", "1",
		"-max-runtime", "15s",
		"-iteration-timeout", "10s",
		"-execute",
	}, liveLoopDependencies{
		openDB: func(context.Context, config.DatabaseConfig) (*sql.DB, error) {
			return db, nil
		},
		newExecutor: func(_ *config.Config, apiKey string, apiSecret string) (domainlive.OrderExecutor, error) {
			if apiKey != "actual-live-api-key-value" || apiSecret != "actual-live-api-secret-value" {
				t.Fatalf("executor credentials mismatch")
			}
			return executor, nil
		},
		newAccountReader: func(*config.Config) (domainlive.AccountSnapshotReader, error) {
			return accountReader, nil
		},
		output: &output,
	})
	if err != nil {
		t.Fatalf("run live loop: %v\nlogs:\n%s", err, output.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sql expectations: %v", err)
	}
	if accountReader.calls != 1 || accountReader.query.AccountType != domainlive.AccountTypeUnified {
		t.Fatalf("account reader mismatch: calls=%d query=%#v", accountReader.calls, accountReader.query)
	}
	if executor.calls != 1 || executor.statusCalls != 1 || executor.positionCalls != 2 {
		t.Fatalf("executor calls mismatch: submit=%d status=%d position=%d", executor.calls, executor.statusCalls, executor.positionCalls)
	}
	if len(executor.positionQueries) != 2 {
		t.Fatalf("position queries mismatch: %#v", executor.positionQueries)
	}
	if executor.positionQueries[0].Symbol != "BTCUSDT" || executor.positionQueries[0].Exchange != "bybit" || executor.positionQueries[0].Category != "linear" {
		t.Fatalf("preflight position query mismatch: %#v", executor.positionQueries[0])
	}
	if executor.positionQueries[1].Symbol != "BTCUSDT" || executor.positionQueries[1].Exchange != "bybit" || executor.positionQueries[1].Category != "linear" {
		t.Fatalf("reconciliation position query mismatch: %#v", executor.positionQueries[1])
	}
	if executor.statusQuery.ClientOrderID != identity.ClientOrderID ||
		executor.statusQuery.Symbol != "BTCUSDT" ||
		executor.statusQuery.Exchange != "bybit" {
		t.Fatalf("status query mismatch: %#v", executor.statusQuery)
	}
	if executor.submission.DecisionID != "risk_decision_live_cli_0001" ||
		executor.submission.SubmissionID != identity.SubmissionID ||
		executor.submission.ClientOrderID != identity.ClientOrderID ||
		executor.submission.Type != domainlive.OrderTypeMarket ||
		executor.submission.TimeInForce != domainlive.TimeInForceIOC {
		t.Fatalf("executor submission mismatch: %#v", executor.submission)
	}

	logs := output.String()
	for _, want := range []string{
		`"msg":"live loop checked"`,
		`"completed":true`,
		`"run_id":"live_loop_cli_0001"`,
		`"preflight_checked":true`,
		`"preflight_ready":true`,
		`"account_snapshot_inserted":1`,
		`"position_snapshot_inserted":1`,
		`"iterations_attempted":1`,
		`"iterations_succeeded":1`,
		`"stop_reason":"ITERATION_REQUESTED"`,
		`"iteration_action":"SUBMITTED"`,
		`"iteration_reason":"live_order_submitted"`,
		`"decision_id":"risk_decision_live_cli_0001"`,
		`"exchange_submitted":true`,
		`"msg":"live loop completed"`,
	} {
		if !strings.Contains(logs, want) {
			t.Fatalf("expected logs to contain %s, got\n%s", want, logs)
		}
	}
	if strings.Contains(logs, "actual-live-api-key-value") || strings.Contains(logs, "actual-live-api-secret-value") {
		t.Fatalf("logs must not contain credential values, got\n%s", logs)
	}
}

func TestRunLiveLoopExpectedIdentityMismatchStopsBeforeSideEffects(t *testing.T) {
	var opened bool

	err := runLiveLoop(context.Background(), []string{
		"-decision-id", "risk_decision_live_cli_0001",
		"-expected-submission-id", "live_sub_wrong",
		"-expected-client-order-id", "inq_live_wrong",
		"-execute",
	}, liveLoopDependencies{
		openDB: func(context.Context, config.DatabaseConfig) (*sql.DB, error) {
			opened = true
			return nil, nil
		},
		output: &bytes.Buffer{},
	})
	if err == nil || !strings.Contains(err.Error(), "identity expectation") || !strings.Contains(err.Error(), "planned submission_id") {
		t.Fatalf("expected identity mismatch error, got %v", err)
	}
	if opened {
		t.Fatal("database must not be opened when explicit decision expected identity mismatches")
	}
}

func TestRunLiveLoopPlanFileDecisionMismatchStopsBeforeSideEffects(t *testing.T) {
	var opened bool
	decisionTime := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	planFile := writeLiveLoopPlanArtifact(t, liveLoopPlanArtifact(t, "risk_decision_live_cli_0001", "live_loop_cli_0001", decisionTime))

	err := runLiveLoop(context.Background(), []string{
		"-plan-file", planFile,
		"-decision-id", "risk_decision_live_cli_0002",
		"-execute",
	}, liveLoopDependencies{
		openDB: func(context.Context, config.DatabaseConfig) (*sql.DB, error) {
			opened = true
			return nil, nil
		},
		output: &bytes.Buffer{},
	})
	if err == nil || !strings.Contains(err.Error(), "plan-file decision-id") {
		t.Fatalf("expected plan-file decision mismatch error, got %v", err)
	}
	if opened {
		t.Fatal("database must not be opened when plan-file decision mismatches explicit decision")
	}
}

func TestRunLiveLoopPlanFileSourceMismatchStopsBeforeSideEffects(t *testing.T) {
	decisionTime := time.Now().UTC().Add(-2 * time.Second)
	tests := []struct {
		name             string
		source           string
		pendingSymbol    string
		selectPending    bool
		wantErrSubstring string
	}{
		{
			name:             "select pending artifact requires selector execution",
			source:           domainlive.LiveOrderPlanArtifactSourceSelectPending,
			pendingSymbol:    "BTCUSDT",
			wantErrSubstring: "explicit decision execution",
		},
		{
			name:             "decision id artifact cannot drive selector execution",
			source:           domainlive.LiveOrderPlanArtifactSourceDecisionID,
			selectPending:    true,
			wantErrSubstring: "-select-pending execution",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var opened bool
			artifact := liveLoopPlanArtifact(t, "risk_decision_live_cli_0001", "live_loop_cli_0001", decisionTime)
			artifact.Source = tt.source
			artifact.PendingSymbol = tt.pendingSymbol
			planFile := writeLiveLoopPlanArtifact(t, artifact)
			args := []string{
				"-plan-file", planFile,
				"-execute",
			}
			if tt.selectPending {
				args = append(args, "-select-pending")
			}

			err := runLiveLoop(context.Background(), args, liveLoopDependencies{
				openDB: func(context.Context, config.DatabaseConfig) (*sql.DB, error) {
					opened = true
					return nil, nil
				},
				output: &bytes.Buffer{},
			})
			if err == nil || !strings.Contains(err.Error(), "plan-file source") || !strings.Contains(err.Error(), tt.wantErrSubstring) {
				t.Fatalf("expected plan-file source mismatch error containing %q, got %v", tt.wantErrSubstring, err)
			}
			if opened {
				t.Fatal("database must not be opened when plan-file source mismatches execution mode")
			}
		})
	}
}

func TestRunLiveLoopReadinessFileStopsBeforeSideEffects(t *testing.T) {
	now := time.Now().UTC()
	decisionTime := now.Add(-2 * time.Second)
	configPath := "configs/live.local.yaml"
	planArtifact := liveLoopPlanArtifact(t, "risk_decision_live_cli_0001", "live_loop_cli_0001", decisionTime)
	planFile := writeLiveLoopPlanArtifact(t, planArtifact)
	validReadiness := liveLoopReadinessArtifact(t, configPath, planFile, planArtifact, now)
	readinessWithoutPlan := cloneLiveLoopReadinessArtifact(validReadiness)
	readinessWithoutPlan.PlanFile = nil
	readinessWithWrongPlan := cloneLiveLoopReadinessArtifact(validReadiness)
	readinessWithWrongPlan.PlanFile = cloneLiveLoopReadinessPlanFile(validReadiness.PlanFile)
	readinessWithWrongPlan.PlanFile.DecisionID = "risk_decision_live_cli_0002"
	readinessWithWrongPlanSHA := cloneLiveLoopReadinessArtifact(validReadiness)
	readinessWithWrongPlanSHA.PlanFile = cloneLiveLoopReadinessPlanFile(validReadiness.PlanFile)
	readinessWithWrongPlanSHA.PlanFile.SHA256 = strings.Repeat("b", 64)
	notReady := cloneLiveLoopReadinessArtifact(validReadiness)
	notReady.Ready = false
	notReady.Summary.Passed = 0
	notReady.Summary.Failed = 1
	notReady.FailedChecks = []string{"live_order_plan_artifact"}
	notReady.Checks[0].Status = domainlive.ReadinessCheckStatusFail
	notReady.Checks[0].Details = "no pending approved LIVE decisions without submissions"
	configMismatch := cloneLiveLoopReadinessArtifact(validReadiness)
	configMismatch.ConfigPath = "configs/other-live.yaml"
	stale := cloneLiveLoopReadinessArtifact(validReadiness)
	stale.CreatedAt = now.Add(-time.Hour)

	tests := []struct {
		name       string
		args       func(string) []string
		artifact   domainlive.LiveReadinessArtifact
		wantErrSub string
	}{
		{
			name: "stale readiness",
			args: func(readinessFile string) []string {
				return []string{"-config", configPath, "-decision-id", "risk_decision_live_cli_0001", "-readiness-file", readinessFile, "-max-readiness-age", "10m", "-execute"}
			},
			artifact:   stale,
			wantErrSub: "stale",
		},
		{
			name: "not ready",
			args: func(readinessFile string) []string {
				return []string{"-config", configPath, "-decision-id", "risk_decision_live_cli_0001", "-readiness-file", readinessFile, "-execute"}
			},
			artifact:   notReady,
			wantErrSub: "ready",
		},
		{
			name: "config mismatch",
			args: func(readinessFile string) []string {
				return []string{"-config", configPath, "-decision-id", "risk_decision_live_cli_0001", "-readiness-file", readinessFile, "-execute"}
			},
			artifact:   configMismatch,
			wantErrSub: "config_path",
		},
		{
			name: "readiness plan requires plan file",
			args: func(readinessFile string) []string {
				return []string{"-config", configPath, "-decision-id", "risk_decision_live_cli_0001", "-readiness-file", readinessFile, "-execute"}
			},
			artifact:   validReadiness,
			wantErrSub: "requires -plan-file",
		},
		{
			name: "plan file requires readiness plan",
			args: func(readinessFile string) []string {
				return []string{"-config", configPath, "-plan-file", planFile, "-readiness-file", readinessFile, "-execute"}
			},
			artifact:   readinessWithoutPlan,
			wantErrSub: "plan_file is required",
		},
		{
			name: "readiness plan metadata mismatch",
			args: func(readinessFile string) []string {
				return []string{"-config", configPath, "-plan-file", planFile, "-readiness-file", readinessFile, "-execute"}
			},
			artifact:   readinessWithWrongPlan,
			wantErrSub: "plan_file.decision_id",
		},
		{
			name: "readiness plan sha mismatch",
			args: func(readinessFile string) []string {
				return []string{"-config", configPath, "-plan-file", planFile, "-readiness-file", readinessFile, "-execute"}
			},
			artifact:   readinessWithWrongPlanSHA,
			wantErrSub: "plan_file.sha256",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var opened bool
			readinessFile := writeLiveLoopReadinessArtifact(t, tt.artifact)
			err := runLiveLoop(context.Background(), tt.args(readinessFile), liveLoopDependencies{
				openDB: func(context.Context, config.DatabaseConfig) (*sql.DB, error) {
					opened = true
					return nil, nil
				},
				output: &bytes.Buffer{},
			})
			if err == nil || !strings.Contains(err.Error(), "readiness") || !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected readiness error containing %q, got %v", tt.wantErrSub, err)
			}
			if opened {
				t.Fatal("database must not be opened when readiness artifact fails before execution")
			}
		})
	}
}

func TestRunLiveLoopAuditFileStopsBeforeSideEffects(t *testing.T) {
	configPath := writeLiveLoopConfig(t)
	now := time.Now().UTC()
	decisionTime := now.Add(-2 * time.Second)
	planArtifact := liveLoopPlanArtifact(t, "risk_decision_live_cli_0001", "live_loop_cli_0001", decisionTime)
	planFile := writeLiveLoopPlanArtifact(t, planArtifact)
	validAudit := liveLoopAuditArtifact(now.Add(-time.Second), configPath)
	validReadiness := liveLoopReadinessArtifact(t, configPath, planFile, planArtifact, now)
	validReadiness.Audit = liveLoopReadinessAuditFromAuditArtifact(validAudit)

	tests := []struct {
		name            string
		args            func(readinessFile string, auditFile string) []string
		audit           domainlive.LiveLoopAuditArtifact
		mutateReadiness func(*domainlive.LiveReadinessArtifact)
		wantErrSub      string
	}{
		{
			name: "audit file requires readiness file",
			args: func(_ string, auditFile string) []string {
				return []string{"-config", configPath, "-plan-file", planFile, "-audit-file", auditFile, "-execute"}
			},
			audit:      validAudit,
			wantErrSub: "requires -readiness-file",
		},
		{
			name: "stale audit artifact",
			args: func(readinessFile string, auditFile string) []string {
				return []string{"-config", configPath, "-plan-file", planFile, "-readiness-file", readinessFile, "-audit-file", auditFile, "-max-audit-age", "10m", "-execute"}
			},
			audit: func() domainlive.LiveLoopAuditArtifact {
				artifact := validAudit
				artifact.CreatedAt = now.Add(-time.Hour)
				return artifact
			}(),
			wantErrSub: "stale",
		},
		{
			name: "readiness audit mismatch",
			args: func(readinessFile string, auditFile string) []string {
				return []string{"-config", configPath, "-plan-file", planFile, "-readiness-file", readinessFile, "-audit-file", auditFile, "-execute"}
			},
			audit: validAudit,
			mutateReadiness: func(a *domainlive.LiveReadinessArtifact) {
				a.Audit.Completed = 1
			},
			wantErrSub: "audit.completed",
		},
		{
			name: "audit config mismatch",
			args: func(readinessFile string, auditFile string) []string {
				return []string{"-config", configPath, "-plan-file", planFile, "-readiness-file", readinessFile, "-audit-file", auditFile, "-execute"}
			},
			audit: func() domainlive.LiveLoopAuditArtifact {
				artifact := validAudit
				artifact.ConfigPath = "configs/other-live.yaml"
				return artifact
			}(),
			wantErrSub: "audit config_path",
		},
		{
			name: "nonpositive max audit age",
			args: func(readinessFile string, auditFile string) []string {
				return []string{"-config", configPath, "-plan-file", planFile, "-readiness-file", readinessFile, "-audit-file", auditFile, "-max-audit-age", "0s", "-execute"}
			},
			audit:      validAudit,
			wantErrSub: "max-audit-age",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var opened bool
			readiness := cloneLiveLoopReadinessArtifact(validReadiness)
			readiness.Audit = liveLoopReadinessAuditFromAuditArtifact(tt.audit)
			if tt.mutateReadiness != nil {
				tt.mutateReadiness(&readiness)
			}
			readinessFile := writeLiveLoopReadinessArtifact(t, readiness)
			auditFile := writeLiveLoopAuditArtifact(t, tt.audit)
			err := runLiveLoop(context.Background(), tt.args(readinessFile, auditFile), liveLoopDependencies{
				openDB: func(context.Context, config.DatabaseConfig) (*sql.DB, error) {
					opened = true
					return nil, nil
				},
				output: &bytes.Buffer{},
			})
			if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected audit error containing %q, got %v", tt.wantErrSub, err)
			}
			if opened {
				t.Fatal("database must not be opened when audit artifact fails before execution")
			}
		})
	}
}

func TestRunLiveLoopKillSwitchFileGate(t *testing.T) {
	configPath := writeLiveLoopConfig(t)
	now := time.Now().UTC()
	decisionTime := now.Add(-2 * time.Second)
	validArtifact := liveLoopKillSwitchArtifact(t, now.Add(-time.Second), configPath)
	activeArtifact := liveLoopKillSwitchArtifactWithState(t, now.Add(-time.Second), configPath, domainrisk.KillSwitchState{
		Active:    true,
		Reason:    "operator emergency stop",
		Source:    "operator",
		UpdatedAt: now.Add(-time.Second),
	})
	staleArtifact := liveLoopKillSwitchArtifact(t, now.Add(-time.Hour), configPath)
	configMismatch := liveLoopKillSwitchArtifact(t, now.Add(-time.Second), "configs/other-live.yaml")
	planArtifact := liveLoopPlanArtifact(t, "risk_decision_live_cli_0001", "live_loop_cli_0001", decisionTime)
	planFile := writeLiveLoopPlanArtifact(t, planArtifact)
	readinessMismatch := liveLoopReadinessArtifact(t, configPath, planFile, planArtifact, now)
	releasedAt := now.Add(-time.Minute)
	readinessMismatch.KillSwitch = domainlive.LiveReadinessArtifactKillSwitch{
		Reason:    "operator verified recovery",
		Source:    "operator",
		UpdatedAt: &releasedAt,
	}
	readinessMismatchFile := writeLiveLoopReadinessArtifact(t, readinessMismatch)

	tests := []struct {
		name       string
		args       func(string) []string
		artifact   domainrisk.KillSwitchArtifact
		wantErrSub string
		wantOpened bool
	}{
		{
			name: "inactive snapshot passes artifact gate",
			args: func(killSwitchFile string) []string {
				return []string{"-config", configPath, "-decision-id", "risk_decision_live_cli_0001", "-kill-switch-file", killSwitchFile, "-execute"}
			},
			artifact:   validArtifact,
			wantErrSub: "stop after kill switch artifact validation",
			wantOpened: true,
		},
		{
			name: "active snapshot stops before db",
			args: func(killSwitchFile string) []string {
				return []string{"-config", configPath, "-decision-id", "risk_decision_live_cli_0001", "-kill-switch-file", killSwitchFile, "-execute"}
			},
			artifact:   activeArtifact,
			wantErrSub: "inactive",
		},
		{
			name: "stale snapshot stops before db",
			args: func(killSwitchFile string) []string {
				return []string{"-config", configPath, "-decision-id", "risk_decision_live_cli_0001", "-kill-switch-file", killSwitchFile, "-max-kill-switch-age", "10m", "-execute"}
			},
			artifact:   staleArtifact,
			wantErrSub: "stale",
		},
		{
			name: "config mismatch stops before db",
			args: func(killSwitchFile string) []string {
				return []string{"-config", configPath, "-decision-id", "risk_decision_live_cli_0001", "-kill-switch-file", killSwitchFile, "-execute"}
			},
			artifact:   configMismatch,
			wantErrSub: "config_path",
		},
		{
			name: "readiness mismatch stops before db",
			args: func(killSwitchFile string) []string {
				return []string{
					"-config", configPath,
					"-plan-file", planFile,
					"-readiness-file", readinessMismatchFile,
					"-kill-switch-file", killSwitchFile,
					"-execute",
				}
			},
			artifact:   validArtifact,
			wantErrSub: "kill switch readiness",
		},
		{
			name: "nonpositive max kill switch age stops before db",
			args: func(killSwitchFile string) []string {
				return []string{"-config", configPath, "-decision-id", "risk_decision_live_cli_0001", "-kill-switch-file", killSwitchFile, "-max-kill-switch-age", "0s", "-execute"}
			},
			artifact:   validArtifact,
			wantErrSub: "max-kill-switch-age",
		},
		{
			name: "untrimmed kill switch path stops before db",
			args: func(killSwitchFile string) []string {
				return []string{"-config", configPath, "-decision-id", "risk_decision_live_cli_0001", "-kill-switch-file", " " + killSwitchFile + " ", "-execute"}
			},
			artifact:   validArtifact,
			wantErrSub: "kill-switch-file",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var opened bool
			killSwitchFile := writeLiveLoopKillSwitchArtifact(t, tt.artifact)
			err := runLiveLoop(context.Background(), tt.args(killSwitchFile), liveLoopDependencies{
				openDB: func(context.Context, config.DatabaseConfig) (*sql.DB, error) {
					opened = true
					return nil, errors.New("stop after kill switch artifact validation")
				},
				output: &bytes.Buffer{},
			})
			if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected kill-switch error containing %q, got %v", tt.wantErrSub, err)
			}
			if opened != tt.wantOpened {
				t.Fatalf("database side effect mismatch: opened=%t want=%t", opened, tt.wantOpened)
			}
		})
	}
}

func TestRunLiveLoopOpsReportFileGate(t *testing.T) {
	configPath := writeLiveLoopConfig(t)
	now := time.Now().UTC()
	clearArtifact := liveLoopOpsReportArtifact(now.Add(-time.Second), configPath, domainlive.LiveOpsStatusClear)
	staleArtifact := liveLoopOpsReportArtifact(now.Add(-time.Hour), configPath, domainlive.LiveOpsStatusClear)
	attentionArtifact := liveLoopOpsReportArtifact(now.Add(-time.Second), configPath, domainlive.LiveOpsStatusAttention)
	configMismatch := liveLoopOpsReportArtifact(now.Add(-time.Second), "configs/other-live.yaml", domainlive.LiveOpsStatusClear)

	tests := []struct {
		name       string
		args       func(string) []string
		artifact   domainlive.LiveOpsReportArtifact
		wantErrSub string
		wantOpened bool
	}{
		{
			name: "clear ops report passes artifact gate",
			args: func(opsFile string) []string {
				return []string{"-config", configPath, "-decision-id", "risk_decision_live_cli_0001", "-ops-report-file", opsFile, "-execute"}
			},
			artifact:   clearArtifact,
			wantErrSub: "stop after ops artifact validation",
			wantOpened: true,
		},
		{
			name: "stale ops report stops before db",
			args: func(opsFile string) []string {
				return []string{"-config", configPath, "-decision-id", "risk_decision_live_cli_0001", "-ops-report-file", opsFile, "-max-ops-report-age", "10m", "-execute"}
			},
			artifact:   staleArtifact,
			wantErrSub: "stale",
		},
		{
			name: "attention ops report stops before db",
			args: func(opsFile string) []string {
				return []string{"-config", configPath, "-decision-id", "risk_decision_live_cli_0001", "-ops-report-file", opsFile, "-execute"}
			},
			artifact:   attentionArtifact,
			wantErrSub: "CLEAR",
		},
		{
			name: "ops report config mismatch stops before db",
			args: func(opsFile string) []string {
				return []string{"-config", configPath, "-decision-id", "risk_decision_live_cli_0001", "-ops-report-file", opsFile, "-execute"}
			},
			artifact:   configMismatch,
			wantErrSub: "config_path",
		},
		{
			name: "nonpositive max ops report age stops before db",
			args: func(opsFile string) []string {
				return []string{"-config", configPath, "-decision-id", "risk_decision_live_cli_0001", "-ops-report-file", opsFile, "-max-ops-report-age", "0s", "-execute"}
			},
			artifact:   clearArtifact,
			wantErrSub: "max-ops-report-age",
		},
		{
			name: "untrimmed ops report path stops before db",
			args: func(opsFile string) []string {
				return []string{"-config", configPath, "-decision-id", "risk_decision_live_cli_0001", "-ops-report-file", " " + opsFile + " ", "-execute"}
			},
			artifact:   clearArtifact,
			wantErrSub: "ops-report-file",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var opened bool
			opsFile := writeLiveLoopOpsReportArtifact(t, tt.artifact)
			err := runLiveLoop(context.Background(), tt.args(opsFile), liveLoopDependencies{
				openDB: func(context.Context, config.DatabaseConfig) (*sql.DB, error) {
					opened = true
					return nil, errors.New("stop after ops artifact validation")
				},
				output: &bytes.Buffer{},
			})
			if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected ops-report error containing %q, got %v", tt.wantErrSub, err)
			}
			if opened != tt.wantOpened {
				t.Fatalf("database side effect mismatch: opened=%t want=%t", opened, tt.wantOpened)
			}
		})
	}
}

func TestRunLiveLoopDeployCheckFileStopsBeforeSideEffects(t *testing.T) {
	configPath := "configs/live.local.yaml"
	now := time.Now().UTC()
	decisionTime := now.Add(-2 * time.Second)
	planArtifact := liveLoopPlanArtifact(t, "risk_decision_live_cli_0001", "live_loop_cli_0001", decisionTime)
	planFile := writeLiveLoopPlanArtifact(t, planArtifact)
	auditArtifact := liveLoopAuditArtifact(now.Add(-time.Second), configPath)
	auditFile := writeLiveLoopAuditArtifact(t, auditArtifact)
	readinessArtifact := liveLoopReadinessArtifact(t, configPath, planFile, planArtifact, now)
	readinessArtifact.Audit = liveLoopReadinessAuditFromAuditArtifact(auditArtifact)
	readinessFile := writeLiveLoopReadinessArtifact(t, readinessArtifact)
	validDeployCheck := liveLoopDeploymentCheckArtifact(
		t,
		now,
		configPath,
		planFile,
		readinessFile,
		auditFile,
		planArtifact,
		readinessArtifact,
		auditArtifact,
		decimal.NewFromInt(500),
	)

	validArgs := func(deployCheckFile string) []string {
		return []string{
			"-config", configPath,
			"-plan-file", planFile,
			"-readiness-file", readinessFile,
			"-audit-file", auditFile,
			"-deploy-check-file", deployCheckFile,
			"-subaccount-confirmed",
			"-max-initial-live-capital-usdt", "100",
			"-max-iterations", "1",
			"-max-runtime", "15s",
			"-iteration-timeout", "10s",
			"-execute",
		}
	}

	tests := []struct {
		name       string
		artifact   domainlive.LiveDeploymentCheckArtifact
		args       func(string) []string
		wantErrSub string
	}{
		{
			name: "requires complete artifact chain",
			args: func(deployCheckFile string) []string {
				return []string{"-config", configPath, "-decision-id", planArtifact.DecisionID, "-deploy-check-file", deployCheckFile, "-subaccount-confirmed", "-execute"}
			},
			artifact:   validDeployCheck,
			wantErrSub: "requires -plan-file",
		},
		{
			name: "stale deploy check",
			artifact: cloneLiveLoopDeploymentCheckArtifact(validDeployCheck, func(a *domainlive.LiveDeploymentCheckArtifact) {
				a.CreatedAt = now.Add(-time.Hour)
			}),
			args: func(deployCheckFile string) []string {
				args := validArgs(deployCheckFile)
				return append(args, "-max-deploy-check-age", "10m")
			},
			wantErrSub: "stale",
		},
		{
			name: "failed deploy check",
			artifact: cloneLiveLoopDeploymentCheckArtifact(validDeployCheck, func(a *domainlive.LiveDeploymentCheckArtifact) {
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
			artifact: cloneLiveLoopDeploymentCheckArtifact(validDeployCheck, func(a *domainlive.LiveDeploymentCheckArtifact) {
				a.ReadinessFile.SHA256 = strings.Repeat("d", 64)
			}),
			args:       validArgs,
			wantErrSub: "readiness_file.sha256",
		},
		{
			name: "runtime mismatch",
			args: func(deployCheckFile string) []string {
				args := validArgs(deployCheckFile)
				return append(args, "-max-runtime", "20s")
			},
			artifact:   validDeployCheck,
			wantErrSub: "execution.max_runtime",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var opened bool
			deployCheckFile := writeLiveLoopDeploymentCheckArtifact(t, tt.artifact)
			err := runLiveLoop(context.Background(), tt.args(deployCheckFile), liveLoopDependencies{
				openDB: func(context.Context, config.DatabaseConfig) (*sql.DB, error) {
					opened = true
					return nil, nil
				},
				output: &bytes.Buffer{},
			})
			if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected deploy-check error containing %q, got %v", tt.wantErrSub, err)
			}
			if opened {
				t.Fatal("database must not be opened when deploy-check artifact fails before execution")
			}
		})
	}
}

func TestRunLiveLoopPlanFileStaleArtifactStopsBeforeSideEffects(t *testing.T) {
	var opened bool
	var executorCreated bool
	var accountReaderCreated bool
	decisionTime := time.Now().UTC().Add(-2 * time.Second)
	artifact := liveLoopPlanArtifact(t, "risk_decision_live_cli_0001", "live_loop_cli_0001", decisionTime)
	artifact.SubmissionCreatedAt = time.Now().UTC().Add(-time.Hour)
	planFile := writeLiveLoopPlanArtifact(t, artifact)

	err := runLiveLoop(context.Background(), []string{
		"-plan-file", planFile,
		"-execute",
	}, liveLoopDependencies{
		openDB: func(context.Context, config.DatabaseConfig) (*sql.DB, error) {
			opened = true
			return nil, nil
		},
		newExecutor: func(_ *config.Config, _ string, _ string) (domainlive.OrderExecutor, error) {
			executorCreated = true
			return &fakeLiveLoopExecutor{}, nil
		},
		newAccountReader: func(*config.Config) (domainlive.AccountSnapshotReader, error) {
			accountReaderCreated = true
			return &fakeLiveLoopAccountReader{}, nil
		},
		output: &bytes.Buffer{},
	})
	if err == nil || !strings.Contains(err.Error(), "freshness") || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("expected stale plan artifact freshness error, got %v", err)
	}
	if opened || executorCreated || accountReaderCreated {
		t.Fatalf("stale plan artifact must stop before side effects: db=%t executor=%t account_reader=%t", opened, executorCreated, accountReaderCreated)
	}
}

func TestRunLiveLoopPlanFileStaleRiskSnapshotStopsBeforePreflightSideEffects(t *testing.T) {
	decisionTime := time.Now().UTC().Add(-2 * time.Second)
	artifact := liveLoopPlanArtifact(t, "risk_decision_live_cli_0001", "live_loop_cli_0001", decisionTime)
	artifact.Quantity = "0.010"
	artifact.Notional = "1000"
	planFile := writeLiveLoopPlanArtifact(t, artifact)

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	mock.ExpectQuery("SELECT decision_id, intent_id, mode").
		WillReturnRows(liveLoopRiskDecisionRows(decisionTime))

	var executorCreated bool
	var accountReaderCreated bool
	err = runLiveLoop(context.Background(), []string{
		"-config", writeLiveLoopConfig(t),
		"-plan-file", planFile,
		"-subaccount-confirmed",
		"-max-initial-live-capital-usdt", "100",
		"-max-iterations", "1",
		"-max-runtime", "15s",
		"-iteration-timeout", "10s",
		"-execute",
	}, liveLoopDependencies{
		openDB: func(context.Context, config.DatabaseConfig) (*sql.DB, error) {
			return db, nil
		},
		newExecutor: func(_ *config.Config, _ string, _ string) (domainlive.OrderExecutor, error) {
			executorCreated = true
			return &fakeLiveLoopExecutor{}, nil
		},
		newAccountReader: func(*config.Config) (domainlive.AccountSnapshotReader, error) {
			accountReaderCreated = true
			return &fakeLiveLoopAccountReader{}, nil
		},
		output: &bytes.Buffer{},
	})
	if err == nil || !strings.Contains(err.Error(), "artifact snapshot") || !strings.Contains(err.Error(), "quantity") {
		t.Fatalf("expected stale plan artifact snapshot error, got %v", err)
	}
	if executorCreated || accountReaderCreated {
		t.Fatalf("stale plan artifact must stop before exchange readers: executor=%t account_reader=%t", executorCreated, accountReaderCreated)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sql expectations: %v", err)
	}
}

func TestRunLiveLoopSelectsPendingDecisionThroughBoundedLoop(t *testing.T) {
	t.Setenv("TRADING_LIVE_CONFIRM", "true")
	t.Setenv("BYBIT_API_KEY", "actual-live-api-key-value")
	t.Setenv("BYBIT_API_SECRET", "actual-live-api-secret-value")

	now := time.Now().UTC()
	decisionTime := now.Add(-2 * time.Second)
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	lockKey := livePendingDecisionSelectionLockKey()
	mock.ExpectQuery("SELECT pg_try_advisory_lock").WithArgs(lockKey).
		WillReturnRows(sqlmock.NewRows([]string{"pg_try_advisory_lock"}).AddRow(true))
	mock.ExpectQuery("SELECT rd.decision_id, rd.intent_id, rd.mode").
		WithArgs("BTCUSDT", 1).
		WillReturnRows(liveLoopRiskDecisionRows(decisionTime))
	mock.ExpectExec("INSERT INTO live_loop_runs").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectQuery("SELECT active, reason, source, created_at").
		WillReturnRows(sqlmock.NewRows([]string{"active", "reason", "source", "created_at"}))
	mock.ExpectExec("INSERT INTO live_account_snapshots").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("INSERT INTO live_position_snapshots").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectQuery("SELECT active, reason, source, created_at").
		WillReturnRows(sqlmock.NewRows([]string{"active", "reason", "source", "created_at"}))
	mock.ExpectQuery("SELECT decision_id, intent_id, mode").
		WillReturnRows(liveLoopRiskDecisionRows(decisionTime))
	mock.ExpectQuery("SELECT active, reason, source, created_at").
		WillReturnRows(sqlmock.NewRows([]string{"active", "reason", "source", "created_at"}))
	mock.ExpectExec("INSERT INTO live_order_submissions").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("INSERT INTO live_order_acknowledgements").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("INSERT INTO live_order_status_snapshots").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("INSERT INTO live_position_snapshots").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("INSERT INTO live_loop_iterations").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("UPDATE live_loop_runs").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT pg_advisory_unlock").WithArgs(lockKey).
		WillReturnRows(sqlmock.NewRows([]string{"pg_advisory_unlock"}).AddRow(true))

	identity, err := deterministicLiveLoopIdentity("risk_decision_live_cli_0001", "live_loop_cli_0001")
	if err != nil {
		t.Fatalf("identity: %v", err)
	}
	executor := &fakeLiveLoopExecutor{receivedAt: now}
	accountReader := &fakeLiveLoopAccountReader{
		snapshot: validLiveLoopAccountSnapshot(t),
	}

	var output bytes.Buffer
	err = runLiveLoop(context.Background(), []string{
		"-config", writeLiveLoopConfigWithMaxOpenConns(t, 2),
		"-select-pending",
		"-pending-symbol", "BTCUSDT",
		"-subaccount-confirmed",
		"-max-initial-live-capital-usdt", "100",
		"-run-id", "live_loop_cli_0001",
		"-max-iterations", "1",
		"-max-runtime", "15s",
		"-iteration-timeout", "10s",
		"-execute",
	}, liveLoopDependencies{
		openDB: func(context.Context, config.DatabaseConfig) (*sql.DB, error) {
			return db, nil
		},
		newExecutor: func(_ *config.Config, apiKey string, apiSecret string) (domainlive.OrderExecutor, error) {
			if apiKey != "actual-live-api-key-value" || apiSecret != "actual-live-api-secret-value" {
				t.Fatalf("executor credentials mismatch")
			}
			return executor, nil
		},
		newAccountReader: func(*config.Config) (domainlive.AccountSnapshotReader, error) {
			return accountReader, nil
		},
		output: &output,
	})
	if err != nil {
		t.Fatalf("run live loop: %v\nlogs:\n%s", err, output.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sql expectations: %v", err)
	}
	if accountReader.calls != 1 {
		t.Fatalf("account reader mismatch: calls=%d query=%#v", accountReader.calls, accountReader.query)
	}
	if executor.calls != 1 || executor.statusCalls != 1 || executor.positionCalls != 2 {
		t.Fatalf("executor calls mismatch: submit=%d status=%d position=%d", executor.calls, executor.statusCalls, executor.positionCalls)
	}
	if executor.submission.DecisionID != "risk_decision_live_cli_0001" ||
		executor.submission.SubmissionID != identity.SubmissionID ||
		executor.submission.ClientOrderID != identity.ClientOrderID {
		t.Fatalf("executor submission mismatch: %#v", executor.submission)
	}

	logs := output.String()
	for _, want := range []string{
		`"msg":"pending live decision selection lock acquired"`,
		`"msg":"pending live decision selected"`,
		`"decision_id":"risk_decision_live_cli_0001"`,
		`"symbol":"BTCUSDT"`,
		`"candidates_checked":1`,
		`"msg":"live loop checked"`,
		`"completed":true`,
		`"run_id":"live_loop_cli_0001"`,
		`"iteration_action":"SUBMITTED"`,
		`"exchange_submitted":true`,
		`"msg":"live loop completed"`,
	} {
		if !strings.Contains(logs, want) {
			t.Fatalf("expected logs to contain %s, got\n%s", want, logs)
		}
	}
	if strings.Contains(logs, "actual-live-api-key-value") || strings.Contains(logs, "actual-live-api-secret-value") {
		t.Fatalf("logs must not contain credential values, got\n%s", logs)
	}
}

func TestRunLiveLoopSelectPendingDeployCheckSelectedDecisionMismatchStopsBeforePreflightSideEffects(t *testing.T) {
	configPath := writeLiveLoopConfigWithMaxOpenConns(t, 2)
	now := time.Now().UTC()
	decisionTime := now.Add(-2 * time.Second)
	planArtifact := liveLoopPlanArtifact(t, "risk_decision_live_cli_0001", "live_loop_cli_0001", decisionTime)
	planArtifact.Source = domainlive.LiveOrderPlanArtifactSourceSelectPending
	planArtifact.PendingSymbol = planArtifact.Symbol
	planFile := writeLiveLoopPlanArtifact(t, planArtifact)
	auditArtifact := liveLoopAuditArtifact(now.Add(-time.Second), configPath)
	auditFile := writeLiveLoopAuditArtifact(t, auditArtifact)
	readinessArtifact := liveLoopReadinessArtifact(t, configPath, planFile, planArtifact, now)
	readinessArtifact.Audit = liveLoopReadinessAuditFromAuditArtifact(auditArtifact)
	readinessFile := writeLiveLoopReadinessArtifact(t, readinessArtifact)
	deployCheckArtifact := liveLoopDeploymentCheckArtifact(
		t,
		now,
		configPath,
		planFile,
		readinessFile,
		auditFile,
		planArtifact,
		readinessArtifact,
		auditArtifact,
		decimal.NewFromInt(500),
	)
	deployCheckArtifact.Execution.SelectedDecisionID = "risk_decision_live_other_0001"
	deployCheckFile := writeLiveLoopDeploymentCheckArtifact(t, deployCheckArtifact)

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	lockKey := livePendingDecisionSelectionLockKey()
	mock.ExpectQuery("SELECT pg_try_advisory_lock").WithArgs(lockKey).
		WillReturnRows(sqlmock.NewRows([]string{"pg_try_advisory_lock"}).AddRow(true))
	mock.ExpectQuery("SELECT rd.decision_id, rd.intent_id, rd.mode").
		WithArgs("BTCUSDT", 1).
		WillReturnRows(liveLoopRiskDecisionRows(decisionTime))
	mock.ExpectQuery("SELECT pg_advisory_unlock").WithArgs(lockKey).
		WillReturnRows(sqlmock.NewRows([]string{"pg_advisory_unlock"}).AddRow(true))

	var executorCreated bool
	var accountReaderCreated bool
	err = runLiveLoop(context.Background(), []string{
		"-config", configPath,
		"-plan-file", planFile,
		"-readiness-file", readinessFile,
		"-audit-file", auditFile,
		"-deploy-check-file", deployCheckFile,
		"-select-pending",
		"-pending-symbol", "BTCUSDT",
		"-subaccount-confirmed",
		"-max-initial-live-capital-usdt", "100",
		"-max-iterations", "1",
		"-max-runtime", "15s",
		"-iteration-timeout", "10s",
		"-execute",
	}, liveLoopDependencies{
		openDB: func(context.Context, config.DatabaseConfig) (*sql.DB, error) {
			return db, nil
		},
		newExecutor: func(_ *config.Config, _ string, _ string) (domainlive.OrderExecutor, error) {
			executorCreated = true
			return &fakeLiveLoopExecutor{}, nil
		},
		newAccountReader: func(*config.Config) (domainlive.AccountSnapshotReader, error) {
			accountReaderCreated = true
			return &fakeLiveLoopAccountReader{}, nil
		},
		output: &bytes.Buffer{},
	})
	if err == nil || !strings.Contains(err.Error(), "execution.selected_decision_id") {
		t.Fatalf("expected deploy-check selected decision mismatch, got %v", err)
	}
	if executorCreated || accountReaderCreated {
		t.Fatalf("deploy-check mismatch must stop before exchange readers: executor=%t account_reader=%t", executorCreated, accountReaderCreated)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sql expectations: %v", err)
	}
}

func TestRunLiveLoopSelectPendingExpectedIdentityMismatchStopsBeforePreflightSideEffects(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	lockKey := livePendingDecisionSelectionLockKey()
	mock.ExpectQuery("SELECT pg_try_advisory_lock").WithArgs(lockKey).
		WillReturnRows(sqlmock.NewRows([]string{"pg_try_advisory_lock"}).AddRow(true))
	mock.ExpectQuery("SELECT rd.decision_id, rd.intent_id, rd.mode").
		WithArgs("BTCUSDT", 1).
		WillReturnRows(liveLoopRiskDecisionRows(time.Now().UTC().Add(-time.Minute)))
	mock.ExpectQuery("SELECT pg_advisory_unlock").WithArgs(lockKey).
		WillReturnRows(sqlmock.NewRows([]string{"pg_advisory_unlock"}).AddRow(true))

	var executorCreated bool
	var accountReaderCreated bool
	err = runLiveLoop(context.Background(), []string{
		"-config", writeLiveLoopConfigWithMaxOpenConns(t, 2),
		"-select-pending",
		"-pending-symbol", "BTCUSDT",
		"-expected-submission-id", "live_sub_wrong",
		"-subaccount-confirmed",
		"-max-initial-live-capital-usdt", "100",
		"-run-id", "live_loop_cli_0001",
		"-max-iterations", "1",
		"-max-runtime", "15s",
		"-iteration-timeout", "10s",
		"-execute",
	}, liveLoopDependencies{
		openDB: func(context.Context, config.DatabaseConfig) (*sql.DB, error) {
			return db, nil
		},
		newExecutor: func(_ *config.Config, _ string, _ string) (domainlive.OrderExecutor, error) {
			executorCreated = true
			return &fakeLiveLoopExecutor{}, nil
		},
		newAccountReader: func(*config.Config) (domainlive.AccountSnapshotReader, error) {
			accountReaderCreated = true
			return &fakeLiveLoopAccountReader{}, nil
		},
		output: &bytes.Buffer{},
	})
	if err == nil || !strings.Contains(err.Error(), "identity expectation") || !strings.Contains(err.Error(), "planned submission_id") {
		t.Fatalf("expected identity mismatch error, got %v", err)
	}
	if executorCreated || accountReaderCreated {
		t.Fatalf("identity mismatch must stop before exchange readers: executor=%t account_reader=%t", executorCreated, accountReaderCreated)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sql expectations: %v", err)
	}
}

func TestRunLiveLoopSelectPendingRequiresCandidateBeforePreflightSideEffects(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	lockKey := livePendingDecisionSelectionLockKey()
	mock.ExpectQuery("SELECT pg_try_advisory_lock").WithArgs(lockKey).
		WillReturnRows(sqlmock.NewRows([]string{"pg_try_advisory_lock"}).AddRow(true))
	mock.ExpectQuery("SELECT rd.decision_id, rd.intent_id, rd.mode").
		WithArgs("", 1).
		WillReturnRows(emptyLiveLoopRiskDecisionRows())
	mock.ExpectQuery("SELECT pg_advisory_unlock").WithArgs(lockKey).
		WillReturnRows(sqlmock.NewRows([]string{"pg_advisory_unlock"}).AddRow(true))

	var executorCreated bool
	var accountReaderCreated bool
	err = runLiveLoop(context.Background(), []string{
		"-config", writeLiveLoopConfigWithMaxOpenConns(t, 2),
		"-select-pending",
		"-subaccount-confirmed",
		"-max-initial-live-capital-usdt", "100",
		"-run-id", "live_loop_cli_0001",
		"-max-iterations", "1",
		"-max-runtime", "15s",
		"-iteration-timeout", "10s",
		"-execute",
	}, liveLoopDependencies{
		openDB: func(context.Context, config.DatabaseConfig) (*sql.DB, error) {
			return db, nil
		},
		newExecutor: func(_ *config.Config, _ string, _ string) (domainlive.OrderExecutor, error) {
			executorCreated = true
			return &fakeLiveLoopExecutor{}, nil
		},
		newAccountReader: func(*config.Config) (domainlive.AccountSnapshotReader, error) {
			accountReaderCreated = true
			return &fakeLiveLoopAccountReader{}, nil
		},
		output: &bytes.Buffer{},
	})
	if err == nil || !strings.Contains(err.Error(), "no pending LIVE risk decisions") {
		t.Fatalf("expected no pending decision error, got %v", err)
	}
	if executorCreated || accountReaderCreated {
		t.Fatalf("missing pending candidate must stop before exchange readers: executor=%t account_reader=%t", executorCreated, accountReaderCreated)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sql expectations: %v", err)
	}
}

func TestRunLiveLoopSelectPendingRequiresUnlockedSelectorBeforeSelection(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	lockKey := livePendingDecisionSelectionLockKey()
	mock.ExpectQuery("SELECT pg_try_advisory_lock").WithArgs(lockKey).
		WillReturnRows(sqlmock.NewRows([]string{"pg_try_advisory_lock"}).AddRow(false))

	var executorCreated bool
	var accountReaderCreated bool
	err = runLiveLoop(context.Background(), []string{
		"-config", writeLiveLoopConfigWithMaxOpenConns(t, 2),
		"-select-pending",
		"-pending-symbol", "BTCUSDT",
		"-subaccount-confirmed",
		"-max-initial-live-capital-usdt", "100",
		"-run-id", "live_loop_cli_0001",
		"-max-iterations", "1",
		"-max-runtime", "15s",
		"-iteration-timeout", "10s",
		"-execute",
	}, liveLoopDependencies{
		openDB: func(context.Context, config.DatabaseConfig) (*sql.DB, error) {
			return db, nil
		},
		newExecutor: func(_ *config.Config, _ string, _ string) (domainlive.OrderExecutor, error) {
			executorCreated = true
			return &fakeLiveLoopExecutor{}, nil
		},
		newAccountReader: func(*config.Config) (domainlive.AccountSnapshotReader, error) {
			accountReaderCreated = true
			return &fakeLiveLoopAccountReader{}, nil
		},
		output: &bytes.Buffer{},
	})
	if err == nil || !strings.Contains(err.Error(), "selector already running") {
		t.Fatalf("expected selector lock error, got %v", err)
	}
	if executorCreated || accountReaderCreated {
		t.Fatalf("locked pending selector must stop before exchange readers: executor=%t account_reader=%t", executorCreated, accountReaderCreated)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sql expectations: %v", err)
	}
}

func TestRunLiveLoopSelectPendingRequiresSpareDBConnectionBeforeOpen(t *testing.T) {
	var opened bool

	err := runLiveLoop(context.Background(), []string{
		"-config", writeLiveLoopConfigWithMaxOpenConns(t, 1),
		"-select-pending",
		"-pending-symbol", "BTCUSDT",
		"-subaccount-confirmed",
		"-max-initial-live-capital-usdt", "100",
		"-run-id", "live_loop_cli_0001",
		"-max-iterations", "1",
		"-max-runtime", "15s",
		"-iteration-timeout", "10s",
		"-execute",
	}, liveLoopDependencies{
		openDB: func(context.Context, config.DatabaseConfig) (*sql.DB, error) {
			opened = true
			return nil, nil
		},
		output: &bytes.Buffer{},
	})
	if err == nil || !strings.Contains(err.Error(), "database.max_open_conns") {
		t.Fatalf("expected max_open_conns error, got %v", err)
	}
	if opened {
		t.Fatal("database must not be opened when selector lock cannot be held safely")
	}
}

func TestRunLiveLoopRequiresExecutorStatusReaderBeforeLoopSideEffects(t *testing.T) {
	t.Setenv("TRADING_LIVE_CONFIRM", "true")
	t.Setenv("BYBIT_API_KEY", "actual-live-api-key-value")
	t.Setenv("BYBIT_API_SECRET", "actual-live-api-secret-value")

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	executor := &fakeLiveLoopSubmitOnlyExecutor{}
	var accountReaderCreated bool

	err = runLiveLoop(context.Background(), []string{
		"-config", writeLiveLoopConfig(t),
		"-decision-id", "risk_decision_live_cli_0001",
		"-subaccount-confirmed",
		"-max-initial-live-capital-usdt", "100",
		"-run-id", "live_loop_cli_0001",
		"-max-iterations", "1",
		"-max-runtime", "15s",
		"-iteration-timeout", "10s",
		"-execute",
	}, liveLoopDependencies{
		openDB: func(context.Context, config.DatabaseConfig) (*sql.DB, error) {
			return db, nil
		},
		newExecutor: func(_ *config.Config, _ string, _ string) (domainlive.OrderExecutor, error) {
			return executor, nil
		},
		newAccountReader: func(*config.Config) (domainlive.AccountSnapshotReader, error) {
			accountReaderCreated = true
			return &fakeLiveLoopAccountReader{}, nil
		},
		output: &bytes.Buffer{},
	})
	if err == nil || !strings.Contains(err.Error(), "order status reconciliation") {
		t.Fatalf("expected status reader requirement error, got %v", err)
	}
	if executor.calls != 0 {
		t.Fatalf("executor must not submit before capability check, calls=%d", executor.calls)
	}
	if accountReaderCreated {
		t.Fatal("account reader must not be created before executor capability check")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sql expectations: %v", err)
	}
}

func TestRunLiveLoopRejectsUnsafePreflightBeforeOrderIteration(t *testing.T) {
	t.Setenv("TRADING_LIVE_CONFIRM", "true")
	t.Setenv("BYBIT_API_KEY", "actual-live-api-key-value")
	t.Setenv("BYBIT_API_SECRET", "actual-live-api-secret-value")

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	mock.ExpectExec("INSERT INTO live_loop_runs").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectQuery("SELECT active, reason, source, created_at").
		WillReturnRows(sqlmock.NewRows([]string{"active", "reason", "source", "created_at"}))
	mock.ExpectExec("UPDATE live_loop_runs").
		WillReturnResult(sqlmock.NewResult(0, 1))
	executor := &fakeLiveLoopExecutor{receivedAt: time.Now().UTC()}
	accountReader := &fakeLiveLoopAccountReader{snapshot: validLiveLoopAccountSnapshot(t)}

	var output bytes.Buffer
	err = runLiveLoop(context.Background(), []string{
		"-config", writeLiveLoopConfig(t),
		"-decision-id", "risk_decision_live_cli_0001",
		"-max-initial-live-capital-usdt", "100",
		"-run-id", "live_loop_cli_0001",
		"-max-iterations", "1",
		"-max-runtime", "15s",
		"-iteration-timeout", "10s",
		"-execute",
	}, liveLoopDependencies{
		openDB: func(context.Context, config.DatabaseConfig) (*sql.DB, error) {
			return db, nil
		},
		newExecutor: func(_ *config.Config, _ string, _ string) (domainlive.OrderExecutor, error) {
			return executor, nil
		},
		newAccountReader: func(*config.Config) (domainlive.AccountSnapshotReader, error) {
			return accountReader, nil
		},
		output: &output,
	})
	if err == nil || !strings.Contains(err.Error(), "dedicated live subaccount") {
		t.Fatalf("expected subaccount preflight error, got %v", err)
	}
	if executor.calls != 0 || executor.statusCalls != 0 || executor.positionCalls != 0 {
		t.Fatalf("unsafe preflight must block order iteration, executor=%#v", executor)
	}
	if accountReader.calls != 0 {
		t.Fatalf("unsafe policy preflight must not read account, calls=%d", accountReader.calls)
	}
	logs := output.String()
	for _, want := range []string{
		`"msg":"live loop checked"`,
		`"completed":false`,
		`"preflight_checked":true`,
		`"preflight_ready":false`,
		`"stop_reason":"PREFLIGHT_FAILED"`,
	} {
		if !strings.Contains(logs, want) {
			t.Fatalf("expected logs to contain %s, got\n%s", want, logs)
		}
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sql expectations: %v", err)
	}
}

func TestValidateLiveLoopFlagsTableDriven(t *testing.T) {
	tests := []struct {
		name       string
		runID      string
		iterations int
		runtime    time.Duration
		timeout    time.Duration
		wantErrSub string
	}{
		{name: "valid", runID: "live_loop_operator", iterations: 1, runtime: time.Second, timeout: time.Second},
		{name: "missing run id", runID: "", iterations: 1, runtime: time.Second, timeout: time.Second, wantErrSub: "run-id"},
		{name: "untrimmed run id", runID: " live_loop_operator ", iterations: 1, runtime: time.Second, timeout: time.Second, wantErrSub: "trimmed"},
		{name: "zero iterations", runID: "live_loop_operator", iterations: 0, runtime: time.Second, timeout: time.Second, wantErrSub: "max-iterations"},
		{name: "iterations above ceiling", runID: "live_loop_operator", iterations: 101, runtime: time.Second, timeout: time.Second, wantErrSub: "max-iterations"},
		{name: "zero runtime", runID: "live_loop_operator", iterations: 1, timeout: time.Second, wantErrSub: "max-runtime"},
		{name: "runtime above ceiling", runID: "live_loop_operator", iterations: 1, runtime: 25 * time.Hour, timeout: time.Second, wantErrSub: "max-runtime"},
		{name: "zero timeout", runID: "live_loop_operator", iterations: 1, runtime: time.Second, wantErrSub: "iteration-timeout"},
		{name: "timeout exceeds runtime", runID: "live_loop_operator", iterations: 1, runtime: time.Second, timeout: 2 * time.Second, wantErrSub: "must not exceed"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateLiveLoopFlags(tt.runID, tt.iterations, tt.runtime, tt.timeout)
			if tt.wantErrSub != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("validate live loop flags: %v", err)
			}
		})
	}
}

func TestValidateLiveLoopDecisionSourceFlagsTableDriven(t *testing.T) {
	tests := []struct {
		name          string
		decisionID    string
		selectPending bool
		runID         string
		wantErrSub    string
	}{
		{name: "explicit decision id", decisionID: "risk_decision_live_cli_0001"},
		{name: "pending selector", selectPending: true},
		{name: "both sources", decisionID: "risk_decision_live_cli_0001", selectPending: true, wantErrSub: "decision-id"},
		{name: "missing source", wantErrSub: "decision-id is required"},
		{name: "untrimmed run id", decisionID: "risk_decision_live_cli_0001", runID: " live_loop_cli_0001 ", wantErrSub: "run-id"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateLiveLoopDecisionSourceFlags(tt.decisionID, tt.selectPending, tt.runID)
			if tt.wantErrSub != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("validate live loop decision source flags: %v", err)
			}
		})
	}
}

func TestLiveLoopPendingDecisionQueryFromFlagsTableDriven(t *testing.T) {
	tests := []struct {
		name       string
		symbol     string
		enabled    bool
		want       domainlive.PendingLiveDecisionQuery
		wantErrSub string
	}{
		{name: "disabled", want: domainlive.PendingLiveDecisionQuery{}},
		{name: "lowercase symbol normalizes", symbol: "btcusdt", enabled: true, want: domainlive.PendingLiveDecisionQuery{Symbol: "BTCUSDT", Limit: 1}},
		{name: "empty symbol selects across all pending decisions", enabled: true, want: domainlive.PendingLiveDecisionQuery{Limit: 1}},
		{name: "symbol without selector rejected", symbol: "BTCUSDT", wantErrSub: "select-pending"},
		{name: "untrimmed symbol rejected", symbol: " BTCUSDT ", enabled: true, wantErrSub: "trimmed"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := liveLoopPendingDecisionQueryFromFlags(tt.symbol, tt.enabled)
			if tt.wantErrSub != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("build pending decision query: %v", err)
			}
			if got != tt.want {
				t.Fatalf("query mismatch: got %#v want %#v", got, tt.want)
			}
		})
	}
}

func TestLivePendingDecisionSelectionLockKeyIsStable(t *testing.T) {
	if got, again := livePendingDecisionSelectionLockKey(), livePendingDecisionSelectionLockKey(); got != again {
		t.Fatalf("lock key must be stable: got %d then %d", got, again)
	}
}

func TestRequireLivePendingDecisionSelectionLockCapacityTableDriven(t *testing.T) {
	tests := []struct {
		name       string
		maxOpen    int
		wantErrSub string
	}{
		{name: "unlimited", maxOpen: 0},
		{name: "two connections", maxOpen: 2},
		{name: "one connection rejected", maxOpen: 1, wantErrSub: "max_open_conns"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := requireLivePendingDecisionSelectionLockCapacity(config.DatabaseConfig{MaxOpenConns: tt.maxOpen})
			if tt.wantErrSub != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("require pending selector lock capacity: %v", err)
			}
		})
	}
}

func TestAcquireLivePendingDecisionSelectionLockTableDriven(t *testing.T) {
	ctx := context.Background()
	key := livePendingDecisionSelectionLockKey()

	tests := []struct {
		name       string
		mock       func(sqlmock.Sqlmock)
		wantErrSub string
		unlock     bool
	}{
		{
			name: "acquires and releases lock",
			mock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery("SELECT pg_try_advisory_lock").WithArgs(key).
					WillReturnRows(sqlmock.NewRows([]string{"pg_try_advisory_lock"}).AddRow(true))
				mock.ExpectQuery("SELECT pg_advisory_unlock").WithArgs(key).
					WillReturnRows(sqlmock.NewRows([]string{"pg_advisory_unlock"}).AddRow(true))
			},
			unlock: true,
		},
		{
			name: "rejects when lock is already held",
			mock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery("SELECT pg_try_advisory_lock").WithArgs(key).
					WillReturnRows(sqlmock.NewRows([]string{"pg_try_advisory_lock"}).AddRow(false))
			},
			wantErrSub: "already running",
		},
		{
			name: "reports unlock mismatch",
			mock: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery("SELECT pg_try_advisory_lock").WithArgs(key).
					WillReturnRows(sqlmock.NewRows([]string{"pg_try_advisory_lock"}).AddRow(true))
				mock.ExpectQuery("SELECT pg_advisory_unlock").WithArgs(key).
					WillReturnRows(sqlmock.NewRows([]string{"pg_advisory_unlock"}).AddRow(false))
			},
			wantErrSub: "not held",
			unlock:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatalf("sqlmock: %v", err)
			}
			defer db.Close()
			tt.mock(mock)

			unlock, err := acquireLivePendingDecisionSelectionLock(ctx, db)
			if tt.wantErrSub != "" && !tt.unlock {
				if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
				}
				if unlock != nil {
					t.Fatal("unlock must be nil when lock acquisition fails")
				}
			} else {
				if err != nil {
					t.Fatalf("acquire lock: %v", err)
				}
				if unlock == nil {
					t.Fatal("unlock is required after successful acquisition")
				}
				err = unlock(ctx)
				if tt.wantErrSub != "" {
					if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
						t.Fatalf("expected unlock error containing %q, got %v", tt.wantErrSub, err)
					}
				} else if err != nil {
					t.Fatalf("unlock: %v", err)
				}
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatalf("sql expectations: %v", err)
			}
		})
	}
}

type fakeLiveLoopAccountReader struct {
	query    domainlive.AccountSnapshotQuery
	snapshot domainlive.AccountSnapshot
	calls    int
	err      error
}

func (r *fakeLiveLoopAccountReader) GetAccountSnapshot(_ context.Context, query domainlive.AccountSnapshotQuery) (domainlive.AccountSnapshot, error) {
	r.calls++
	r.query = query
	if r.err != nil {
		return domainlive.AccountSnapshot{}, r.err
	}
	return r.snapshot, nil
}

type fakeLiveLoopExecutor struct {
	submission      domainlive.OrderSubmission
	statusQuery     domainlive.OrderStatusQuery
	positionQueries []domainlive.PositionSnapshotQuery
	receivedAt      time.Time
	calls           int
	statusCalls     int
	positionCalls   int
}

func (e *fakeLiveLoopExecutor) SubmitOrder(_ context.Context, submission domainlive.OrderSubmission) (domainlive.OrderAcknowledgement, error) {
	e.calls++
	e.submission = submission
	return domainlive.NewOrderAcknowledgement(domainlive.OrderAcknowledgementInput{
		SubmissionID:    submission.SubmissionID,
		ClientOrderID:   submission.ClientOrderID,
		Exchange:        submission.Exchange,
		ExchangeOrderID: "bybit_order_loop_0001",
		Status:          domainlive.OrderStatusAccepted,
		ReceivedAt:      e.receivedAt,
	})
}

func (e *fakeLiveLoopExecutor) GetOrderStatus(_ context.Context, query domainlive.OrderStatusQuery) (domainlive.OrderStatusSnapshot, error) {
	e.statusCalls++
	e.statusQuery = query
	return domainlive.NewOrderStatusSnapshot(domainlive.OrderStatusSnapshotInput{
		ClientOrderID:              e.submission.ClientOrderID,
		ExchangeOrderID:            "bybit_order_loop_0001",
		Exchange:                   e.submission.Exchange,
		Category:                   e.submission.Category,
		Symbol:                     e.submission.Symbol,
		Side:                       e.submission.Side,
		Type:                       e.submission.Type,
		TimeInForce:                e.submission.TimeInForce,
		ExchangeStatus:             domainlive.ExchangeOrderStatusFilled,
		RejectReason:               "EC_NoError",
		Quantity:                   e.submission.Quantity,
		Price:                      decimal.Zero,
		AveragePrice:               e.submission.ReferencePrice,
		LeavesQuantity:             decimal.Zero,
		CumulativeExecutedQuantity: e.submission.Quantity,
		CumulativeExecutedValue:    e.submission.Notional,
		CumulativeFee:              decimal.RequireFromString("1"),
		ReduceOnly:                 e.submission.ReduceOnly,
		ExchangeCreatedAt:          e.receivedAt.Add(-time.Second),
		ExchangeUpdatedAt:          e.receivedAt,
		ObservedAt:                 e.receivedAt,
	})
}

func (e *fakeLiveLoopExecutor) GetPositionSnapshot(_ context.Context, query domainlive.PositionSnapshotQuery) (domainlive.PositionSnapshot, error) {
	e.positionCalls++
	e.positionQueries = append(e.positionQueries, query)
	if e.positionCalls == 1 {
		return validLiveLoopFlatPositionSnapshot(query, e.receivedAt.Add(-500*time.Millisecond))
	}
	return domainlive.NewPositionSnapshot(domainlive.PositionSnapshotInput{
		Exchange:              e.submission.Exchange,
		Category:              e.submission.Category,
		Symbol:                e.submission.Symbol,
		Side:                  e.submission.Side,
		Size:                  e.submission.Quantity,
		AveragePrice:          e.submission.ReferencePrice,
		PositionValue:         e.submission.Notional,
		MarkPrice:             e.submission.ReferencePrice,
		LiquidationPrice:      e.submission.StopLoss,
		Leverage:              e.submission.Leverage,
		UnrealisedPnL:         decimal.Zero,
		CurrentRealisedPnL:    decimal.Zero,
		CumulativeRealisedPnL: decimal.Zero,
		ExchangeStatus:        domainlive.ExchangePositionStatusNormal,
		PositionIndex:         0,
		Sequence:              123,
		ExchangeCreatedAt:     e.receivedAt.Add(-time.Second),
		ExchangeUpdatedAt:     e.receivedAt,
		ObservedAt:            e.receivedAt,
	})
}

type fakeLiveLoopSubmitOnlyExecutor struct {
	calls int
}

func (e *fakeLiveLoopSubmitOnlyExecutor) SubmitOrder(context.Context, domainlive.OrderSubmission) (domainlive.OrderAcknowledgement, error) {
	e.calls++
	return domainlive.OrderAcknowledgement{}, nil
}

func validLiveLoopAccountSnapshot(t *testing.T) domainlive.AccountSnapshot {
	t.Helper()

	snapshot, err := domainlive.NewAccountSnapshot(domainlive.AccountSnapshotInput{
		Exchange:               "bybit",
		AccountType:            domainlive.AccountTypeUnified,
		TotalEquity:            decimal.RequireFromString("50"),
		TotalWalletBalance:     decimal.RequireFromString("50"),
		TotalMarginBalance:     decimal.RequireFromString("50"),
		TotalAvailableBalance:  decimal.RequireFromString("50"),
		TotalPerpUPL:           decimal.Zero,
		TotalInitialMargin:     decimal.Zero,
		TotalMaintenanceMargin: decimal.Zero,
		Coins: []domainlive.AccountCoinSnapshot{{
			Coin:             "USDT",
			Equity:           decimal.RequireFromString("50"),
			USDValue:         decimal.RequireFromString("50"),
			WalletBalance:    decimal.RequireFromString("50"),
			MarginCollateral: true,
			CollateralSwitch: true,
		}},
		ObservedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("new live loop account snapshot: %v", err)
	}
	return snapshot
}

func validLiveLoopFlatPositionSnapshot(query domainlive.PositionSnapshotQuery, observedAt time.Time) (domainlive.PositionSnapshot, error) {
	return domainlive.NewPositionSnapshot(domainlive.PositionSnapshotInput{
		Exchange:       query.Exchange,
		Category:       query.Category,
		Symbol:         query.Symbol,
		Size:           decimal.Zero,
		MarkPrice:      decimal.RequireFromString("100000"),
		ExchangeStatus: domainlive.ExchangePositionStatusNormal,
		PositionIndex:  0,
		Sequence:       -1,
		ObservedAt:     observedAt,
	})
}

func emptyLiveLoopRiskDecisionRows() *sqlmock.Rows {
	return sqlmock.NewRows(liveLoopRiskDecisionColumns())
}

func liveLoopRiskDecisionRows(now time.Time) *sqlmock.Rows {
	createdAt := now.Add(-2 * time.Second)
	recordedAt := now.Add(-time.Second)
	intentCreatedAt := now.Add(-time.Minute)
	return sqlmock.NewRows(liveLoopRiskDecisionColumns()).AddRow(
		"risk_decision_live_cli_0001",
		"risk_intent_live_cli_0001",
		"LIVE",
		"hypothesis_live_cli_0001",
		"trend-momentum",
		"BTCUSDT",
		"LONG",
		"100000",
		"1",
		82,
		"signal confirmed",
		intentCreatedAt,
		true,
		"0.005",
		"5",
		"99000",
		"102000",
		"risk_checks_passed",
		`[{"name":"trading_enabled","passed":true}]`,
		createdAt,
		recordedAt,
	)
}

func liveLoopRiskDecisionColumns() []string {
	return []string{
		"decision_id", "intent_id", "mode", "hypothesis_id", "strategy_name", "symbol", "side",
		"entry_price", "leverage", "confidence", "intent_reason", "intent_created_at",
		"approved", "final_quantity", "max_loss", "stop_loss", "take_profit",
		"reason", "checks_json", "created_at", "recorded_at",
	}
}

func writeLiveLoopConfig(t *testing.T) string {
	t.Helper()

	return writeLiveLoopConfigWithMaxOpenConns(t, 1)
}

func writeLiveLoopConfigWithMaxOpenConns(t *testing.T, maxOpenConns int) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "config.yaml")
	raw := []byte(strings.Replace(`
app:
  name: crypto-quant-platform
  env: test
  mode: live-loop
  log_level: info
database:
  dsn: postgres://user:pass@localhost:5432/inquisitor?sslmode=disable
  max_open_conns: __MAX_OPEN_CONNS__
  max_idle_conns: 1
exchange:
  primary: bybit
  rest_base_url: https://api-testnet.bybit.com
  public_ws_url: wss://stream-testnet.bybit.com/v5/public/linear
  category: linear
  symbols: [BTCUSDT]
market_data:
  candle_intervals: ["1"]
  backfill_days: 1
  orderbook_depth: 50
  max_data_staleness_ms: 1000
  reconnect_backoff_ms: 1000
fees:
  maker_bps: 1
  taker_bps: 6
slippage:
  default_bps: 3
  conservative_multiplier: 1.5
trading:
  enabled: true
  mode: live
  allow_live: true
  max_open_positions: 1
  max_leverage: 1
  base_currency: USDT
risk:
  risk_per_trade_pct: 0.25
  max_daily_loss_pct: 1
  max_weekly_loss_pct: 3
  max_total_drawdown_pct: 8
  max_losing_streak: 5
  max_spread_bps: 5
  max_slippage_bps: 10
  min_confidence: 70
  min_liquidity_usdt: 100000
  portfolio_max_crypto_exposure_pct: 30
  portfolio_max_correlated_exposure_pct: 20
regime:
  min_confidence: 70
  adx_trend_threshold: 25
  adx_range_threshold: 18
  atr_spike_multiplier: 2.5
research:
  min_trades: 200
  min_profit_factor: 1.15
  min_expectancy_r: 0.05
  max_drawdown_pct: 15
  require_out_of_sample: true
  require_walk_forward: true
  require_regime_analysis: true
paper:
  initial_balance: 1000
  minimum_days: 30
  simulate_fees: true
  simulate_slippage: true
  simulate_spread: true
live:
  require_env_confirmation: true
  confirmation_env: TRADING_LIVE_CONFIRM
  api_key_env: BYBIT_API_KEY
  api_secret_env: BYBIT_API_SECRET
  require_subaccount: true
  withdrawal_permission_allowed: false
  initial_live_capital_usdt: 50.25
edge_decay:
  enabled: true
  rolling_window_days: 30
  min_recent_profit_factor: 1
  max_recent_drawdown_pct: 8
monitoring:
  health_port: 8080
`, "__MAX_OPEN_CONNS__", strconv.Itoa(maxOpenConns), 1))
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func writeLiveLoopPlanArtifact(t *testing.T, artifact domainlive.LiveOrderPlanArtifact) string {
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

func writeLiveLoopReadinessArtifact(t *testing.T, artifact domainlive.LiveReadinessArtifact) string {
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

func writeLiveLoopKillSwitchArtifact(t *testing.T, artifact domainrisk.KillSwitchArtifact) string {
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

func writeLiveLoopAuditArtifact(t *testing.T, artifact domainlive.LiveLoopAuditArtifact) string {
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

func writeLiveLoopDeploymentCheckArtifact(t *testing.T, artifact domainlive.LiveDeploymentCheckArtifact) string {
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

func writeLiveLoopOpsReportArtifact(t *testing.T, artifact domainlive.LiveOpsReportArtifact) string {
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

func liveLoopOpsReportArtifact(
	createdAt time.Time,
	configPath string,
	status domainlive.LiveOpsStatus,
) domainlive.LiveOpsReportArtifact {
	checkName := "kill_switch"
	checkStatus := domainlive.ReadinessCheckStatusPass
	checkDetails := "kill switch is inactive"
	summary := domainlive.LiveOpsReportArtifactSummary{Total: 1, Passed: 1}
	if status == domainlive.LiveOpsStatusAttention {
		checkName = "pending_live_decision"
		checkStatus = domainlive.ReadinessCheckStatusWarn
		checkDetails = "no pending approved LIVE decisions without submissions"
		summary = domainlive.LiveOpsReportArtifactSummary{Total: 1, Warned: 1}
	}
	if status == domainlive.LiveOpsStatusBlocked {
		checkStatus = domainlive.ReadinessCheckStatusFail
		checkDetails = "kill switch is active"
		summary = domainlive.LiveOpsReportArtifactSummary{Total: 1, Failed: 1}
	}
	artifact := domainlive.LiveOpsReportArtifact{
		SchemaVersion: domainlive.LiveOpsReportArtifactSchemaVersion,
		CreatedAt:     createdAt.UTC(),
		ConfigPath:    strings.TrimSpace(configPath),
		Status:        status,
		Summary:       summary,
		Checks: []domainlive.LiveOpsReportArtifactCheck{{
			Name:    checkName,
			Status:  checkStatus,
			Details: checkDetails,
		}},
		Pending: domainlive.LiveOpsReportArtifactPending{
			Limit: 10,
		},
		Audit: domainlive.LiveOpsReportArtifactAudit{
			Limit:                  10,
			ReviewStatus:           domainlive.LiveLoopAuditReviewStatusClear,
			ReviewReason:           "no recent live-loop audit runs found",
			OperatorActionRequired: false,
		},
		KillSwitch: domainlive.LiveOpsReportArtifactKillSwitch{},
	}
	if status == domainlive.LiveOpsStatusBlocked {
		artifact.FailedChecks = []string{"kill_switch"}
		updatedAt := createdAt.Add(-time.Second).UTC()
		artifact.KillSwitch = domainlive.LiveOpsReportArtifactKillSwitch{
			Active:    true,
			Reason:    "operator stop",
			Source:    "operator",
			UpdatedAt: &updatedAt,
		}
	}
	return artifact
}

func liveLoopKillSwitchArtifact(t *testing.T, createdAt time.Time, configPath string) domainrisk.KillSwitchArtifact {
	t.Helper()

	return liveLoopKillSwitchArtifactWithState(t, createdAt, configPath, domainrisk.KillSwitchState{})
}

func liveLoopKillSwitchArtifactWithState(
	t *testing.T,
	createdAt time.Time,
	configPath string,
	state domainrisk.KillSwitchState,
) domainrisk.KillSwitchArtifact {
	t.Helper()

	artifact, err := domainrisk.BuildKillSwitchArtifact(domainrisk.BuildKillSwitchArtifactRequest{
		CreatedAt:  createdAt.UTC(),
		ConfigPath: configPath,
		Action:     domainrisk.KillSwitchArtifactActionState,
		State:      &state,
	})
	if err != nil {
		t.Fatalf("build kill switch artifact: %v", err)
	}
	return artifact
}

func liveLoopDeploymentCheckArtifact(
	t *testing.T,
	createdAt time.Time,
	configPath string,
	planPath string,
	readinessPath string,
	auditPath string,
	plan domainlive.LiveOrderPlanArtifact,
	readiness domainlive.LiveReadinessArtifact,
	audit domainlive.LiveLoopAuditArtifact,
	microLimit decimal.Decimal,
) domainlive.LiveDeploymentCheckArtifact {
	t.Helper()

	_, _, planSHA256, err := loadLiveLoopPlanArtifact(planPath)
	if err != nil {
		t.Fatalf("load plan artifact: %v", err)
	}
	_, _, readinessSHA256, err := loadLiveLoopReadinessArtifact(readinessPath)
	if err != nil {
		t.Fatalf("load readiness artifact: %v", err)
	}
	_, _, auditSHA256, err := loadLiveLoopAuditArtifact(auditPath)
	if err != nil {
		t.Fatalf("load audit artifact: %v", err)
	}
	deployment := domainlive.LiveDeploymentCheckRequest{
		ConfigPath:                strings.TrimSpace(configPath),
		PlanFilePath:              strings.TrimSpace(planPath),
		PlanFileSHA256:            planSHA256,
		PlanArtifact:              plan,
		ReadinessArtifact:         readiness,
		AuditArtifact:             audit,
		Now:                       createdAt.UTC(),
		MaxPlanArtifactAge:        domainlive.DefaultLiveOrderPlanArtifactMaxAge,
		MaxReadinessArtifactAge:   domainlive.DefaultLiveReadinessArtifactMaxAge,
		MaxAuditArtifactAge:       domainlive.DefaultLiveLoopAuditArtifactMaxAge,
		Execute:                   true,
		SubaccountConfirmed:       true,
		MaxInitialLiveCapitalUSDT: decimal.NewFromInt(100),
		MicroCapitalLimitUSDT:     microLimit,
		MaxIterations:             1,
		MaxRuntime:                15 * time.Second,
		IterationTimeout:          10 * time.Second,
	}
	if plan.Source == domainlive.LiveOrderPlanArtifactSourceSelectPending {
		deployment.SelectPending = true
		deployment.PendingSymbol = plan.PendingSymbol
	} else {
		deployment.DecisionID = plan.DecisionID
	}
	report, err := domainlive.BuildLiveDeploymentCheckReport(deployment)
	if err != nil {
		t.Fatalf("build deployment check report: %v", err)
	}
	if !report.Ready {
		t.Fatalf("deployment check report must be ready for fixture: %#v", report)
	}
	artifact, err := domainlive.BuildLiveDeploymentCheckArtifact(domainlive.BuildLiveDeploymentCheckArtifactRequest{
		Report:              report,
		Deployment:          deployment,
		CreatedAt:           createdAt.UTC(),
		ConfigPath:          configPath,
		PlanFilePath:        planPath,
		PlanFileSHA256:      planSHA256,
		ReadinessFilePath:   readinessPath,
		ReadinessFileSHA256: readinessSHA256,
		AuditFilePath:       auditPath,
		AuditFileSHA256:     auditSHA256,
	})
	if err != nil {
		t.Fatalf("build deployment check artifact: %v", err)
	}
	return artifact
}

func liveLoopAuditArtifact(createdAt time.Time, configPath string) domainlive.LiveLoopAuditArtifact {
	return domainlive.LiveLoopAuditArtifact{
		SchemaVersion: domainlive.LiveLoopAuditArtifactSchemaVersion,
		CreatedAt:     createdAt.UTC(),
		ConfigPath:    strings.TrimSpace(configPath),
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

func liveLoopReadinessAuditFromAuditArtifact(artifact domainlive.LiveLoopAuditArtifact) domainlive.LiveReadinessArtifactAudit {
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

func liveLoopReadinessArtifact(
	t *testing.T,
	configPath string,
	planPath string,
	plan domainlive.LiveOrderPlanArtifact,
	createdAt time.Time,
) domainlive.LiveReadinessArtifact {
	t.Helper()

	pendingSymbol := plan.PendingSymbol
	if pendingSymbol == "" {
		pendingSymbol = plan.Symbol
	}
	oldestAt := plan.DecisionCreatedAt
	newestAt := plan.DecisionCreatedAt
	_, _, planFileSHA256, err := loadLiveLoopPlanArtifact(planPath)
	if err != nil {
		t.Fatalf("load plan artifact for readiness sha256: %v", err)
	}
	return domainlive.LiveReadinessArtifact{
		SchemaVersion: domainlive.LiveReadinessArtifactSchemaVersion,
		CreatedAt:     createdAt.UTC(),
		ConfigPath:    strings.TrimSpace(configPath),
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
			Symbol:         pendingSymbol,
			Limit:          1,
			Required:       true,
			Total:          1,
			NextDecisionID: plan.DecisionID,
			NextSymbol:     plan.Symbol,
			OldestAt:       &oldestAt,
			NewestAt:       &newestAt,
		},
		Audit: domainlive.LiveReadinessArtifactAudit{
			Limit: 10,
		},
		KillSwitch: domainlive.LiveReadinessArtifactKillSwitch{},
		PlanFile: &domainlive.LiveReadinessArtifactPlanFile{
			Path:          strings.TrimSpace(planPath),
			SHA256:        planFileSHA256,
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

func cloneLiveLoopReadinessPlanFile(plan *domainlive.LiveReadinessArtifactPlanFile) *domainlive.LiveReadinessArtifactPlanFile {
	if plan == nil {
		return nil
	}
	copy := *plan
	return &copy
}

func cloneLiveLoopReadinessArtifact(artifact domainlive.LiveReadinessArtifact) domainlive.LiveReadinessArtifact {
	artifact.FailedChecks = append([]string(nil), artifact.FailedChecks...)
	artifact.Checks = append([]domainlive.LiveReadinessArtifactCheck(nil), artifact.Checks...)
	artifact.PlanFile = cloneLiveLoopReadinessPlanFile(artifact.PlanFile)
	return artifact
}

func cloneLiveLoopDeploymentCheckArtifact(
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

func liveLoopPlanArtifact(t *testing.T, decisionID string, runID string, decisionObservedAt time.Time) domainlive.LiveOrderPlanArtifact {
	t.Helper()

	identity, err := domainlive.NewDeterministicLiveLoopOrderIdentity(decisionID, runID)
	if err != nil {
		t.Fatalf("identity: %v", err)
	}
	createdAt := decisionObservedAt.UTC().Add(-2 * time.Second)
	recordedAt := decisionObservedAt.UTC().Add(-time.Second)
	return domainlive.LiveOrderPlanArtifact{
		SchemaVersion:       domainlive.LiveOrderPlanArtifactSchemaVersion,
		Source:              domainlive.LiveOrderPlanArtifactSourceDecisionID,
		RunID:               identity.RunID,
		DecisionID:          strings.TrimSpace(decisionID),
		SubmissionID:        identity.SubmissionID,
		ClientOrderID:       identity.ClientOrderID,
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
		Confidence:          82,
		DecisionCreatedAt:   createdAt,
		RecordedAt:          recordedAt,
		SubmissionCreatedAt: recordedAt.Add(time.Second),
	}
}
