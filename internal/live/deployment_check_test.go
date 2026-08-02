package live_test

import (
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	domainlive "github.com/VersoIt/Inquisitor/internal/live"
)

func TestBuildLiveDeploymentCheckReportTableDriven(t *testing.T) {
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	plan := validLiveOrderPlanArtifact(now.Add(-time.Minute))
	plan.Quantity = "0.001"
	plan.Notional = "100"
	planPath := "artifacts/live-order-plan.json"
	planSHA256 := strings.Repeat("c", 64)
	audit := validLiveReadinessHandoffAuditArtifact(now.Add(-20*time.Second), "configs/live.local.yaml")
	readiness := validLiveReadinessHandoffArtifact(now.Add(-30*time.Second), planPath, planSHA256, plan)
	readiness.Audit = liveReadinessHandoffAuditFromArtifact(audit)

	tests := []struct {
		name            string
		mutateRequest   func(*domainlive.LiveDeploymentCheckRequest)
		wantReady       bool
		wantFailedCheck string
		wantErrSub      string
	}{
		{name: "valid explicit first order deployment check", wantReady: true},
		{name: "valid select pending first order deployment check", mutateRequest: func(req *domainlive.LiveDeploymentCheckRequest) {
			req.PlanArtifact.Source = domainlive.LiveOrderPlanArtifactSourceSelectPending
			req.PlanArtifact.PendingSymbol = req.PlanArtifact.Symbol
			req.ReadinessArtifact.PlanFile.Source = domainlive.LiveOrderPlanArtifactSourceSelectPending
			req.ReadinessArtifact.PlanFile.PendingSymbol = req.PlanArtifact.Symbol
			req.SelectPending = true
			req.PendingSymbol = strings.ToLower(req.PlanArtifact.Symbol)
		}, wantReady: true},
		{name: "execute flag is required", mutateRequest: func(req *domainlive.LiveDeploymentCheckRequest) {
			req.Execute = false
		}, wantFailedCheck: "live_loop_armed"},
		{name: "subaccount confirmation is required", mutateRequest: func(req *domainlive.LiveDeploymentCheckRequest) {
			req.SubaccountConfirmed = false
		}, wantFailedCheck: "live_loop_armed"},
		{name: "first order is bounded to one iteration", mutateRequest: func(req *domainlive.LiveDeploymentCheckRequest) {
			req.MaxIterations = 2
		}, wantFailedCheck: "live_loop_bounds"},
		{name: "iteration timeout cannot exceed runtime", mutateRequest: func(req *domainlive.LiveDeploymentCheckRequest) {
			req.MaxRuntime = 5 * time.Second
			req.IterationTimeout = 10 * time.Second
		}, wantFailedCheck: "live_loop_bounds"},
		{name: "capital cap cannot exceed live micro limit", mutateRequest: func(req *domainlive.LiveDeploymentCheckRequest) {
			req.MaxInitialLiveCapitalUSDT = decimal.NewFromInt(250)
		}, wantFailedCheck: "live_micro_capital"},
		{name: "planned notional cannot exceed live micro limit", mutateRequest: func(req *domainlive.LiveDeploymentCheckRequest) {
			req.PlanArtifact.Notional = "150"
		}, wantFailedCheck: "live_micro_capital"},
		{name: "planned leverage cannot exceed one", mutateRequest: func(req *domainlive.LiveDeploymentCheckRequest) {
			req.PlanArtifact.Leverage = "2"
		}, wantFailedCheck: "live_micro_capital"},
		{name: "stale plan artifact blocks deployment", mutateRequest: func(req *domainlive.LiveDeploymentCheckRequest) {
			req.PlanArtifact.SubmissionCreatedAt = now.Add(-time.Hour)
		}, wantFailedCheck: "deployment_artifact_freshness"},
		{name: "readiness warnings block first order deployment", mutateRequest: func(req *domainlive.LiveDeploymentCheckRequest) {
			req.ReadinessArtifact.Checks = append(req.ReadinessArtifact.Checks, domainlive.LiveReadinessArtifactCheck{
				Name:    "recent_live_loop_audit",
				Status:  domainlive.ReadinessCheckStatusWarn,
				Details: "operator review required before first order",
			})
			req.ReadinessArtifact.Summary = domainlive.LiveReadinessArtifactSummary{Total: 2, Passed: 1, Warned: 1}
		}, wantFailedCheck: "readiness_operator_review"},
		{name: "audit mismatch blocks handoff", mutateRequest: func(req *domainlive.LiveDeploymentCheckRequest) {
			req.AuditArtifact.ConfigPath = "configs/other-live.yaml"
		}, wantFailedCheck: "deployment_artifact_handoff"},
		{name: "source mismatch blocks deployment", mutateRequest: func(req *domainlive.LiveDeploymentCheckRequest) {
			req.PlanArtifact.Source = domainlive.LiveOrderPlanArtifactSourceSelectPending
			req.PlanArtifact.PendingSymbol = req.PlanArtifact.Symbol
			req.ReadinessArtifact.PlanFile.Source = domainlive.LiveOrderPlanArtifactSourceSelectPending
			req.ReadinessArtifact.PlanFile.PendingSymbol = req.PlanArtifact.Symbol
		}, wantFailedCheck: "live_loop_source"},
		{name: "invalid report validation catches bad selected id", mutateRequest: func(req *domainlive.LiveDeploymentCheckRequest) {
			req.PlanArtifact.DecisionID = ""
		}, wantFailedCheck: "live_loop_source"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := validLiveDeploymentCheckRequest(now, planPath, planSHA256, plan, readiness, audit)
			if tt.mutateRequest != nil {
				tt.mutateRequest(&req)
			}

			report, err := domainlive.BuildLiveDeploymentCheckReport(req)
			if tt.wantErrSub != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("build deployment check report: %v", err)
			}
			if report.Ready != tt.wantReady {
				t.Fatalf("ready mismatch: got %t want %t report=%#v", report.Ready, tt.wantReady, report)
			}
			if tt.wantFailedCheck != "" && !containsLiveDeploymentFailedCheck(report.Checks, tt.wantFailedCheck) {
				t.Fatalf("expected failed check %q, got %#v", tt.wantFailedCheck, domainlive.LiveDeploymentCheckFailedNames(report.Checks))
			}
			if report.Ready {
				if err := domainlive.ValidateLiveDeploymentCheckReport(report); err != nil {
					t.Fatalf("validate deployment check report: %v", err)
				}
			}
		})
	}
}

func TestResolveLiveReadinessHandoffExecutionSelectionTableDriven(t *testing.T) {
	plan := validLiveOrderPlanArtifact(time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC))
	selectPlan := plan
	selectPlan.Source = domainlive.LiveOrderPlanArtifactSourceSelectPending
	selectPlan.PendingSymbol = selectPlan.Symbol

	tests := []struct {
		name              string
		plan              domainlive.LiveOrderPlanArtifact
		decisionID        string
		selectPending     bool
		pendingSymbol     string
		wantDecisionID    string
		wantPendingSymbol string
		wantErrSub        string
	}{
		{name: "explicit defaults decision from plan", plan: plan, wantDecisionID: plan.DecisionID},
		{name: "explicit accepts matching decision", plan: plan, decisionID: plan.DecisionID, wantDecisionID: plan.DecisionID},
		{name: "explicit rejects mismatch", plan: plan, decisionID: "risk_decision_live_artifact_0002", wantErrSub: "decision-id"},
		{name: "explicit rejects pending symbol", plan: plan, pendingSymbol: "BTCUSDT", wantErrSub: "pending-symbol"},
		{name: "selector defaults symbol from plan", plan: selectPlan, selectPending: true, wantDecisionID: selectPlan.DecisionID, wantPendingSymbol: "BTCUSDT"},
		{name: "selector uppercases explicit symbol", plan: selectPlan, selectPending: true, pendingSymbol: "btcusdt", wantDecisionID: selectPlan.DecisionID, wantPendingSymbol: "BTCUSDT"},
		{name: "selector rejects decision id", plan: selectPlan, selectPending: true, decisionID: selectPlan.DecisionID, wantErrSub: "decision-id"},
		{name: "selector rejects untrimmed symbol", plan: selectPlan, selectPending: true, pendingSymbol: " BTCUSDT ", wantErrSub: "pending-symbol"},
		{name: "source mismatch rejects selector", plan: plan, selectPending: true, wantErrSub: "-select-pending execution"},
		{name: "source mismatch rejects explicit", plan: selectPlan, wantErrSub: "explicit decision execution"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decisionID, query, err := domainlive.ResolveLiveReadinessHandoffExecutionSelection(
				tt.plan,
				tt.decisionID,
				tt.selectPending,
				tt.pendingSymbol,
			)
			if tt.wantErrSub != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolve selection: %v", err)
			}
			if decisionID != tt.wantDecisionID || query.Symbol != tt.wantPendingSymbol {
				t.Fatalf("selection mismatch: decision=%q query=%#v", decisionID, query)
			}
		})
	}
}

func validLiveDeploymentCheckRequest(
	now time.Time,
	planPath string,
	planSHA256 string,
	plan domainlive.LiveOrderPlanArtifact,
	readiness domainlive.LiveReadinessArtifact,
	audit domainlive.LiveLoopAuditArtifact,
) domainlive.LiveDeploymentCheckRequest {
	return domainlive.LiveDeploymentCheckRequest{
		ConfigPath:                "configs/live.local.yaml",
		PlanFilePath:              planPath,
		PlanFileSHA256:            planSHA256,
		PlanArtifact:              plan,
		ReadinessArtifact:         readiness,
		AuditArtifact:             audit,
		Now:                       now,
		MaxPlanArtifactAge:        domainlive.DefaultLiveOrderPlanArtifactMaxAge,
		MaxReadinessArtifactAge:   domainlive.DefaultLiveReadinessArtifactMaxAge,
		MaxAuditArtifactAge:       domainlive.DefaultLiveLoopAuditArtifactMaxAge,
		Execute:                   true,
		SubaccountConfirmed:       true,
		MaxInitialLiveCapitalUSDT: decimal.NewFromInt(100),
		MicroCapitalLimitUSDT:     domainlive.DefaultLiveDeploymentMicroCapitalLimitUSDT(),
		MaxIterations:             1,
		MaxRuntime:                15 * time.Second,
		IterationTimeout:          10 * time.Second,
	}
}

func containsLiveDeploymentFailedCheck(checks []domainlive.ReadinessCheck, name string) bool {
	for _, check := range checks {
		if check.Name == name && check.Status == domainlive.ReadinessCheckStatusFail {
			return true
		}
	}
	return false
}
