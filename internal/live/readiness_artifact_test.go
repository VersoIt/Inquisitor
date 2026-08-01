package live_test

import (
	"strings"
	"testing"
	"time"

	domainlive "github.com/VersoIt/Inquisitor/internal/live"
)

func TestValidateLiveReadinessArtifactTableDriven(t *testing.T) {
	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	valid := validLiveReadinessArtifact(now)

	tests := []struct {
		name       string
		mutate     func(*domainlive.LiveReadinessArtifact)
		wantErrSub string
	}{
		{name: "valid ready artifact"},
		{name: "bad schema", mutate: func(a *domainlive.LiveReadinessArtifact) {
			a.SchemaVersion = "old"
		}, wantErrSub: "schema_version"},
		{name: "missing config path", mutate: func(a *domainlive.LiveReadinessArtifact) {
			a.ConfigPath = ""
		}, wantErrSub: "config_path"},
		{name: "summary mismatch", mutate: func(a *domainlive.LiveReadinessArtifact) {
			a.Summary.Failed = 1
		}, wantErrSub: "summary"},
		{name: "ready mismatch", mutate: func(a *domainlive.LiveReadinessArtifact) {
			a.Ready = false
		}, wantErrSub: "ready"},
		{name: "failed checks mismatch", mutate: func(a *domainlive.LiveReadinessArtifact) {
			a.Checks[0].Status = domainlive.ReadinessCheckStatusFail
			a.Summary.Passed = 0
			a.Summary.Failed = 1
			a.Ready = false
		}, wantErrSub: "failed_checks"},
		{name: "untrimmed pending symbol", mutate: func(a *domainlive.LiveReadinessArtifact) {
			a.Pending.Symbol = " BTCUSDT "
		}, wantErrSub: "pending.symbol"},
		{name: "invalid plan file schema", mutate: func(a *domainlive.LiveReadinessArtifact) {
			a.PlanFile.SchemaVersion = "old"
		}, wantErrSub: "plan_file.schema_version"},
		{name: "missing plan decision id", mutate: func(a *domainlive.LiveReadinessArtifact) {
			a.PlanFile.DecisionID = ""
		}, wantErrSub: "plan_file.decision_id"},
		{name: "bad plan max age", mutate: func(a *domainlive.LiveReadinessArtifact) {
			a.PlanFile.MaxAge = "soon"
		}, wantErrSub: "plan_file.max_age"},
		{name: "decision plan rejects pending symbol", mutate: func(a *domainlive.LiveReadinessArtifact) {
			a.PlanFile.PendingSymbol = "BTCUSDT"
		}, wantErrSub: "plan_file.pending_symbol"},
		{name: "select pending plan rejects mismatched pending symbol", mutate: func(a *domainlive.LiveReadinessArtifact) {
			a.PlanFile.Source = domainlive.LiveOrderPlanArtifactSourceSelectPending
			a.PlanFile.PendingSymbol = "ETHUSDT"
		}, wantErrSub: "plan_file.pending_symbol"},
		{name: "active kill switch without timestamp", mutate: func(a *domainlive.LiveReadinessArtifact) {
			a.KillSwitch.Active = true
		}, wantErrSub: "kill_switch"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			artifact := valid
			if artifact.PlanFile != nil {
				plan := *artifact.PlanFile
				artifact.PlanFile = &plan
			}
			if tt.mutate != nil {
				tt.mutate(&artifact)
			}
			err := domainlive.ValidateLiveReadinessArtifact(artifact)
			if tt.wantErrSub != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("validate readiness artifact: %v", err)
			}
		})
	}
}

func TestValidateLiveReadinessArtifactFreshnessTableDriven(t *testing.T) {
	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	valid := validLiveReadinessArtifact(now.Add(-time.Minute))

	tests := []struct {
		name       string
		artifact   domainlive.LiveReadinessArtifact
		now        time.Time
		maxAge     time.Duration
		wantErrSub string
	}{
		{name: "fresh artifact", artifact: valid, now: now, maxAge: 10 * time.Minute},
		{name: "stale artifact", artifact: mutateLiveReadinessArtifact(valid, func(a *domainlive.LiveReadinessArtifact) {
			a.CreatedAt = now.Add(-11 * time.Minute)
		}), now: now, maxAge: 10 * time.Minute, wantErrSub: "stale"},
		{name: "future artifact", artifact: mutateLiveReadinessArtifact(valid, func(a *domainlive.LiveReadinessArtifact) {
			a.CreatedAt = now.Add(time.Second)
		}), now: now, maxAge: 10 * time.Minute, wantErrSub: "future"},
		{name: "zero max age", artifact: valid, now: now, wantErrSub: "max_age"},
		{name: "missing now", artifact: valid, maxAge: 10 * time.Minute, wantErrSub: "now"},
		{name: "invalid artifact first", artifact: mutateLiveReadinessArtifact(valid, func(a *domainlive.LiveReadinessArtifact) {
			a.Checks = nil
		}), now: now, maxAge: 10 * time.Minute, wantErrSub: "checks"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := domainlive.ValidateLiveReadinessArtifactFreshness(tt.artifact, tt.now, tt.maxAge)
			if tt.wantErrSub != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("validate readiness freshness: %v", err)
			}
		})
	}
}

func validLiveReadinessArtifact(createdAt time.Time) domainlive.LiveReadinessArtifact {
	oldestAt := createdAt.Add(-2 * time.Minute)
	newestAt := createdAt.Add(-time.Minute)
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
			Name:    "live_config",
			Status:  domainlive.ReadinessCheckStatusPass,
			Details: "live trading config is explicitly enabled",
		}},
		Pending: domainlive.LiveReadinessArtifactPending{
			Symbol:         "BTCUSDT",
			Limit:          1,
			Required:       true,
			Total:          1,
			NextDecisionID: "risk_decision_live_ready_0001",
			NextSymbol:     "BTCUSDT",
			OldestAt:       &oldestAt,
			NewestAt:       &newestAt,
		},
		Audit: domainlive.LiveReadinessArtifactAudit{
			Limit:     10,
			Total:     1,
			Completed: 1,
		},
		KillSwitch: domainlive.LiveReadinessArtifactKillSwitch{},
		PlanFile: &domainlive.LiveReadinessArtifactPlanFile{
			Path:          "artifacts/live-order-plan.json",
			SchemaVersion: domainlive.LiveOrderPlanArtifactSchemaVersion,
			Source:        domainlive.LiveOrderPlanArtifactSourceDecisionID,
			DecisionID:    "risk_decision_live_ready_0001",
			SubmissionID:  "live_sub_ready_0001",
			ClientOrderID: "inq_live_ready_0001",
			Symbol:        "BTCUSDT",
			MaxAge:        "10m0s",
		},
	}
}

func mutateLiveReadinessArtifact(
	artifact domainlive.LiveReadinessArtifact,
	mutate func(*domainlive.LiveReadinessArtifact),
) domainlive.LiveReadinessArtifact {
	if mutate != nil {
		mutate(&artifact)
	}
	return artifact
}
