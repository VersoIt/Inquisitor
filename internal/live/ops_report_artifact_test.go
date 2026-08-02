package live_test

import (
	"strings"
	"testing"
	"time"

	domainlive "github.com/VersoIt/Inquisitor/internal/live"
)

func TestValidateLiveOpsReportArtifactTableDriven(t *testing.T) {
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	valid := validLiveOpsReportArtifact(now)

	tests := []struct {
		name       string
		mutate     func(*domainlive.LiveOpsReportArtifact)
		wantErrSub string
	}{
		{name: "valid clear artifact"},
		{name: "bad schema", mutate: func(a *domainlive.LiveOpsReportArtifact) {
			a.SchemaVersion = "old"
		}, wantErrSub: "schema_version"},
		{name: "missing config path", mutate: func(a *domainlive.LiveOpsReportArtifact) {
			a.ConfigPath = ""
		}, wantErrSub: "config_path"},
		{name: "summary mismatch", mutate: func(a *domainlive.LiveOpsReportArtifact) {
			a.Summary.Passed--
		}, wantErrSub: "summary"},
		{name: "status mismatch", mutate: func(a *domainlive.LiveOpsReportArtifact) {
			a.Status = domainlive.LiveOpsStatusBlocked
		}, wantErrSub: "status"},
		{name: "failed checks mismatch", mutate: func(a *domainlive.LiveOpsReportArtifact) {
			a.Checks[0].Status = domainlive.ReadinessCheckStatusFail
			a.Checks[0].Details = "kill switch active"
			a.Status = domainlive.LiveOpsStatusBlocked
			a.Summary.Passed--
			a.Summary.Failed++
		}, wantErrSub: "failed_checks"},
		{name: "pending limit out of range", mutate: func(a *domainlive.LiveOpsReportArtifact) {
			a.Pending.Limit = 101
		}, wantErrSub: "pending.limit"},
		{name: "pending total zero keeps next metadata", mutate: func(a *domainlive.LiveOpsReportArtifact) {
			a.Pending.Total = 0
		}, wantErrSub: "pending next"},
		{name: "pending next timestamp order", mutate: func(a *domainlive.LiveOpsReportArtifact) {
			oldest := now
			newest := now.Add(-time.Minute)
			a.Pending.OldestAt = &oldest
			a.Pending.NewestAt = &newest
		}, wantErrSub: "pending.newest_at"},
		{name: "audit counts mismatch", mutate: func(a *domainlive.LiveOpsReportArtifact) {
			a.Audit.Total = 2
		}, wantErrSub: "audit.total"},
		{name: "audit blocked requires run id", mutate: func(a *domainlive.LiveOpsReportArtifact) {
			a.Audit.ReviewStatus = domainlive.LiveLoopAuditReviewStatusBlocked
			a.Audit.ReviewRunID = ""
			a.Audit.ReviewReason = "live-loop run is still RUNNING"
			a.Audit.OperatorActionRequired = true
		}, wantErrSub: "audit.review_run_id"},
		{name: "kill switch active requires timestamp", mutate: func(a *domainlive.LiveOpsReportArtifact) {
			a.KillSwitch.Active = true
			a.KillSwitch.Reason = "operator stop"
			a.KillSwitch.Source = "operator"
		}, wantErrSub: "kill_switch"},
		{name: "first order review invalid sha", mutate: func(a *domainlive.LiveOpsReportArtifact) {
			a.FirstOrderReview.SHA256 = "ABC"
		}, wantErrSub: "first_order_review.sha256"},
		{name: "first order review requires check", mutate: func(a *domainlive.LiveOpsReportArtifact) {
			a.Checks = a.Checks[:3]
			a.Summary.Total = 3
			a.Summary.Passed = 3
		}, wantErrSub: "first_order_review check"},
		{name: "ready first order review requires exchange order", mutate: func(a *domainlive.LiveOpsReportArtifact) {
			a.FirstOrderReview.ExchangeOrderID = ""
		}, wantErrSub: "exchange_order_id"},
		{name: "failed first order review requires failed checks", mutate: func(a *domainlive.LiveOpsReportArtifact) {
			a.FirstOrderReview.Ready = false
			a.FirstOrderReview.Summary.Passed = 3
			a.FirstOrderReview.Summary.Failed = 1
		}, wantErrSub: "first_order_review.failed_checks"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			artifact := mutateLiveOpsReportArtifact(valid, tt.mutate)
			err := domainlive.ValidateLiveOpsReportArtifact(artifact)
			if tt.wantErrSub != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("validate live ops report artifact: %v", err)
			}
		})
	}
}

func TestValidateLiveOpsReportArtifactFreshnessTableDriven(t *testing.T) {
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	valid := validLiveOpsReportArtifact(now.Add(-time.Minute))

	tests := []struct {
		name       string
		artifact   domainlive.LiveOpsReportArtifact
		now        time.Time
		maxAge     time.Duration
		wantErrSub string
	}{
		{name: "fresh artifact", artifact: valid, now: now, maxAge: 10 * time.Minute},
		{name: "stale artifact", artifact: mutateLiveOpsReportArtifact(valid, func(a *domainlive.LiveOpsReportArtifact) {
			a.CreatedAt = now.Add(-11 * time.Minute)
		}), now: now, maxAge: 10 * time.Minute, wantErrSub: "stale"},
		{name: "future artifact", artifact: mutateLiveOpsReportArtifact(valid, func(a *domainlive.LiveOpsReportArtifact) {
			a.CreatedAt = now.Add(time.Second)
		}), now: now, maxAge: 10 * time.Minute, wantErrSub: "future"},
		{name: "zero max age", artifact: valid, now: now, wantErrSub: "max_age"},
		{name: "missing now", artifact: valid, maxAge: 10 * time.Minute, wantErrSub: "now"},
		{name: "invalid artifact first", artifact: mutateLiveOpsReportArtifact(valid, func(a *domainlive.LiveOpsReportArtifact) {
			a.Checks = nil
		}), now: now, maxAge: 10 * time.Minute, wantErrSub: "checks"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := domainlive.ValidateLiveOpsReportArtifactFreshness(tt.artifact, tt.now, tt.maxAge)
			if tt.wantErrSub != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("validate live ops report freshness: %v", err)
			}
		})
	}
}

func TestValidateLiveOpsReportArtifactPositionDriftTableDriven(t *testing.T) {
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	valid := validLiveOpsReportArtifactWithPositionDrift(now)

	tests := []struct {
		name       string
		mutate     func(*domainlive.LiveOpsReportArtifact)
		wantErrSub string
	}{
		{name: "valid position drift metadata"},
		{name: "position drift summary mismatch", mutate: func(a *domainlive.LiveOpsReportArtifact) {
			a.PositionDrift.Summary.Passed--
		}, wantErrSub: "position_drift.summary"},
		{name: "position drift status mismatch", mutate: func(a *domainlive.LiveOpsReportArtifact) {
			a.PositionDrift.Status = domainlive.LiveOpsStatusBlocked
		}, wantErrSub: "position_drift.status"},
		{name: "position drift failed checks mismatch", mutate: func(a *domainlive.LiveOpsReportArtifact) {
			a.PositionDrift.Checks[0].Status = domainlive.ReadinessCheckStatusFail
			a.PositionDrift.Checks[0].Details = "current snapshot stale"
			a.PositionDrift.Status = domainlive.LiveOpsStatusBlocked
			a.PositionDrift.Summary.Passed--
			a.PositionDrift.Summary.Failed++
		}, wantErrSub: "position_drift.failed_checks"},
		{name: "position drift check missing from top level", mutate: func(a *domainlive.LiveOpsReportArtifact) {
			a.Checks = a.Checks[:len(a.Checks)-1]
			a.Summary.Total--
			a.Summary.Passed--
		}, wantErrSub: "position_drift checks"},
		{name: "position drift baseline required", mutate: func(a *domainlive.LiveOpsReportArtifact) {
			a.PositionDrift.Comparisons[0].Baseline = nil
		}, wantErrSub: "baseline is required"},
		{name: "position drift baseline omitted when absent", mutate: func(a *domainlive.LiveOpsReportArtifact) {
			a.PositionDrift.Comparisons[0].HasBaseline = false
		}, wantErrSub: "baseline must be omitted"},
		{name: "position drift current size decimal", mutate: func(a *domainlive.LiveOpsReportArtifact) {
			a.PositionDrift.Comparisons[0].Current.Size = "not-a-decimal"
		}, wantErrSub: "size must be a decimal"},
		{name: "position drift open snapshot requires side", mutate: func(a *domainlive.LiveOpsReportArtifact) {
			a.PositionDrift.Comparisons[0].Current.Open = true
			a.PositionDrift.Comparisons[0].Current.Size = "0.005"
			a.PositionDrift.Comparisons[0].Current.Side = ""
			a.PositionDrift.Comparisons[0].Current.ExchangeStatus = domainlive.ExchangePositionStatusNormal
			createdAt := now.Add(-time.Hour)
			a.PositionDrift.Comparisons[0].Current.ExchangeCreatedAt = &createdAt
		}, wantErrSub: "side"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			artifact := mutateLiveOpsReportArtifact(valid, tt.mutate)
			err := domainlive.ValidateLiveOpsReportArtifact(artifact)
			if tt.wantErrSub != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("validate live ops artifact with drift: %v", err)
			}
		})
	}
}

func validLiveOpsReportArtifact(createdAt time.Time) domainlive.LiveOpsReportArtifact {
	oldestAt := createdAt.Add(-2 * time.Minute)
	newestAt := createdAt.Add(-time.Minute)
	return domainlive.LiveOpsReportArtifact{
		SchemaVersion: domainlive.LiveOpsReportArtifactSchemaVersion,
		CreatedAt:     createdAt,
		ConfigPath:    "configs/live.local.yaml",
		Status:        domainlive.LiveOpsStatusClear,
		Summary: domainlive.LiveOpsReportArtifactSummary{
			Total:  4,
			Passed: 4,
		},
		Checks: []domainlive.LiveOpsReportArtifactCheck{
			{Name: "kill_switch", Status: domainlive.ReadinessCheckStatusPass, Details: "kill switch is inactive"},
			{Name: "pending_live_decision", Status: domainlive.ReadinessCheckStatusPass, Details: "next decision risk_decision_live_ops_0001 for BTCUSDT is pending"},
			{Name: "recent_live_loop_audit", Status: domainlive.ReadinessCheckStatusPass, Details: "recent live-loop audit has no running or failed runs"},
			{Name: "first_order_review", Status: domainlive.ReadinessCheckStatusPass, Details: "first-order review passed for client_order_id inq_live_ops_0001"},
		},
		Pending: domainlive.LiveOpsReportArtifactPending{
			Symbol:         "BTCUSDT",
			Limit:          10,
			Total:          1,
			NextDecisionID: "risk_decision_live_ops_0001",
			NextSymbol:     "BTCUSDT",
			OldestAt:       &oldestAt,
			NewestAt:       &newestAt,
		},
		Audit: domainlive.LiveOpsReportArtifactAudit{
			Limit:                  10,
			Total:                  1,
			Completed:              1,
			ReviewStatus:           domainlive.LiveLoopAuditReviewStatusClear,
			ReviewReason:           "recent live-loop audit has no running or failed runs",
			OperatorActionRequired: false,
		},
		KillSwitch: domainlive.LiveOpsReportArtifactKillSwitch{},
		FirstOrderReview: &domainlive.LiveOpsReportArtifactFirstOrderReview{
			Path:               "artifacts/live-first-order/live-first-order-review.json",
			SHA256:             strings.Repeat("b", 64),
			SchemaVersion:      domainlive.LiveFirstOrderReviewArtifactSchemaVersion,
			CreatedAt:          createdAt.Add(-time.Minute),
			Ready:              true,
			Summary:            domainlive.LiveFirstOrderReviewArtifactSummary{Total: 4, Passed: 4},
			RunID:              "live_loop_ops_0001",
			DecisionID:         "risk_decision_live_ops_0001",
			SubmissionID:       "live_submission_ops_0001",
			ClientOrderID:      "inq_live_ops_0001",
			ExchangeOrderID:    "bybit_order_ops_0001",
			LatestOrderStatus:  domainlive.ExchangeOrderStatusFilled,
			LatestPositionOpen: true,
			LatestPositionSize: "0.005",
		},
	}
}

func validLiveOpsReportArtifactWithPositionDrift(createdAt time.Time) domainlive.LiveOpsReportArtifact {
	artifact := validLiveOpsReportArtifact(createdAt)
	driftChecks := []domainlive.LiveOpsReportArtifactCheck{
		{Name: "current_position_snapshot", Status: domainlive.ReadinessCheckStatusPass, Details: "BTCUSDT current exchange position snapshot is fresh"},
		{Name: "db_position_baseline", Status: domainlive.ReadinessCheckStatusPass, Details: "BTCUSDT DB position baseline is fresh"},
		{Name: "position_exchange_status", Status: domainlive.ReadinessCheckStatusPass, Details: "BTCUSDT exchange position status is normal"},
		{Name: "position_exposure_drift", Status: domainlive.ReadinessCheckStatusPass, Details: "BTCUSDT current exchange exposure matches DB baseline"},
	}
	artifact.Checks = append(artifact.Checks, driftChecks...)
	artifact.Summary.Total += len(driftChecks)
	artifact.Summary.Passed += len(driftChecks)
	artifact.PositionDrift = &domainlive.LiveOpsReportArtifactPositionDrift{
		Status:  domainlive.LiveOpsStatusClear,
		Summary: domainlive.LiveOpsReportArtifactSummary{Total: 4, Passed: 4},
		Checks:  append([]domainlive.LiveOpsReportArtifactCheck(nil), driftChecks...),
		Comparisons: []domainlive.LiveOpsReportArtifactPositionDriftItem{{
			Exchange:    "bybit",
			Category:    "linear",
			Symbol:      "BTCUSDT",
			Status:      domainlive.LiveOpsStatusClear,
			HasBaseline: true,
			Current: domainlive.LiveOpsReportArtifactPositionSnapshot{
				Open:          false,
				Size:          "0",
				AveragePrice:  "0",
				Leverage:      "0",
				PositionIndex: 0,
				ObservedAt:    createdAt.Add(-time.Second),
			},
			Baseline: &domainlive.LiveOpsReportArtifactPositionSnapshot{
				Open:          false,
				Size:          "0",
				AveragePrice:  "0",
				Leverage:      "0",
				PositionIndex: 0,
				ObservedAt:    createdAt.Add(-time.Minute),
			},
		}},
	}
	return artifact
}

func mutateLiveOpsReportArtifact(
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
		for index := range drift.Comparisons {
			if drift.Comparisons[index].Baseline != nil {
				baseline := *drift.Comparisons[index].Baseline
				drift.Comparisons[index].Baseline = &baseline
			}
		}
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
