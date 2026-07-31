package live_test

import (
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	domainlive "github.com/VersoIt/Inquisitor/internal/live"
)

func TestValidateLiveOrderPlanArtifactTableDriven(t *testing.T) {
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	valid := validLiveOrderPlanArtifact(now)

	tests := []struct {
		name       string
		mutate     func(*domainlive.LiveOrderPlanArtifact)
		wantErrSub string
	}{
		{name: "valid read-only market artifact"},
		{name: "bad schema", mutate: func(a *domainlive.LiveOrderPlanArtifact) { a.SchemaVersion = "old" }, wantErrSub: "schema_version"},
		{name: "bad source", mutate: func(a *domainlive.LiveOrderPlanArtifact) { a.Source = "auto" }, wantErrSub: "source"},
		{name: "missing decision id", mutate: func(a *domainlive.LiveOrderPlanArtifact) { a.DecisionID = "" }, wantErrSub: "decision_id"},
		{name: "untrimmed run id", mutate: func(a *domainlive.LiveOrderPlanArtifact) { a.RunID = " " + a.RunID }, wantErrSub: "run_id"},
		{name: "bad exchange normalization", mutate: func(a *domainlive.LiveOrderPlanArtifact) { a.Exchange = "BYBIT" }, wantErrSub: "exchange"},
		{name: "bad symbol normalization", mutate: func(a *domainlive.LiveOrderPlanArtifact) { a.Symbol = "btcusdt" }, wantErrSub: "symbol"},
		{name: "invalid order type", mutate: func(a *domainlive.LiveOrderPlanArtifact) { a.OrderType = "STOP" }, wantErrSub: "order_type"},
		{name: "market requires zero limit", mutate: func(a *domainlive.LiveOrderPlanArtifact) { a.LimitPrice = "100000" }, wantErrSub: "market artifact"},
		{name: "market rejects post only", mutate: func(a *domainlive.LiveOrderPlanArtifact) { a.TimeInForce = domainlive.TimeInForcePostOnly }, wantErrSub: "market artifact time_in_force"},
		{name: "limit requires positive limit", mutate: func(a *domainlive.LiveOrderPlanArtifact) { a.OrderType = domainlive.OrderTypeLimit }, wantErrSub: "limit artifact"},
		{name: "non decimal quantity", mutate: func(a *domainlive.LiveOrderPlanArtifact) { a.Quantity = "abc" }, wantErrSub: "quantity"},
		{name: "reserved side effect rejected", mutate: func(a *domainlive.LiveOrderPlanArtifact) { a.Reserved = true }, wantErrSub: "reserved"},
		{name: "exchange contacted side effect rejected", mutate: func(a *domainlive.LiveOrderPlanArtifact) { a.ExchangeContacted = true }, wantErrSub: "exchange_contacted"},
		{name: "order submitted side effect rejected", mutate: func(a *domainlive.LiveOrderPlanArtifact) { a.OrderSubmitted = true }, wantErrSub: "order_submitted"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			artifact := valid
			if tt.mutate != nil {
				tt.mutate(&artifact)
			}
			err := domainlive.ValidateLiveOrderPlanArtifact(artifact)
			if tt.wantErrSub != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("validate artifact: %v", err)
			}
		})
	}
}

func TestLiveOrderPlanArtifactIdentityExpectation(t *testing.T) {
	artifact := validLiveOrderPlanArtifact(time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC))
	got := artifact.IdentityExpectation()
	if got.SubmissionID != artifact.SubmissionID || got.ClientOrderID != artifact.ClientOrderID {
		t.Fatalf("identity expectation mismatch: got %#v artifact=%#v", got, artifact)
	}
}

func TestValidateLiveOrderPlanArtifactSnapshotTableDriven(t *testing.T) {
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	artifact := validLiveOrderPlanArtifact(now)
	snapshot := validLiveOrderPlanArtifactSnapshot(t, artifact)

	tests := []struct {
		name       string
		mutate     func(*domainlive.LiveOrderPlanArtifact, *domainlive.LiveOrderPlanArtifactSnapshot)
		wantErrSub string
	}{
		{name: "matches current rebuilt plan"},
		{name: "decision mismatch", mutate: func(_ *domainlive.LiveOrderPlanArtifact, s *domainlive.LiveOrderPlanArtifactSnapshot) {
			s.Submission.DecisionID = "risk_decision_live_artifact_0002"
		}, wantErrSub: "decision_id"},
		{name: "quantity mismatch", mutate: func(a *domainlive.LiveOrderPlanArtifact, _ *domainlive.LiveOrderPlanArtifactSnapshot) {
			a.Quantity = "0.010"
		}, wantErrSub: "quantity"},
		{name: "max loss mismatch", mutate: func(a *domainlive.LiveOrderPlanArtifact, _ *domainlive.LiveOrderPlanArtifactSnapshot) {
			a.MaxLoss = "10"
		}, wantErrSub: "max_loss"},
		{name: "decision created at mismatch", mutate: func(_ *domainlive.LiveOrderPlanArtifact, s *domainlive.LiveOrderPlanArtifactSnapshot) {
			s.DecisionCreatedAt = s.DecisionCreatedAt.Add(time.Second)
		}, wantErrSub: "decision_created_at"},
		{name: "recorded at mismatch", mutate: func(_ *domainlive.LiveOrderPlanArtifact, s *domainlive.LiveOrderPlanArtifactSnapshot) {
			s.RecordedAt = s.RecordedAt.Add(time.Second)
		}, wantErrSub: "recorded_at"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			candidate := artifact
			current := snapshot
			if tt.mutate != nil {
				tt.mutate(&candidate, &current)
			}
			err := domainlive.ValidateLiveOrderPlanArtifactSnapshot(candidate, current)
			if tt.wantErrSub != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("validate artifact snapshot: %v", err)
			}
		})
	}
}

func TestValidateLiveOrderPlanArtifactFreshnessTableDriven(t *testing.T) {
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	valid := validLiveOrderPlanArtifact(now.Add(-time.Minute))

	tests := []struct {
		name       string
		artifact   domainlive.LiveOrderPlanArtifact
		now        time.Time
		maxAge     time.Duration
		wantErrSub string
	}{
		{name: "fresh artifact", artifact: valid, now: now, maxAge: 10 * time.Minute},
		{name: "stale artifact", artifact: mutateLiveOrderPlanArtifact(valid, func(a *domainlive.LiveOrderPlanArtifact) {
			a.SubmissionCreatedAt = now.Add(-11 * time.Minute)
		}), now: now, maxAge: 10 * time.Minute, wantErrSub: "stale"},
		{name: "future artifact", artifact: mutateLiveOrderPlanArtifact(valid, func(a *domainlive.LiveOrderPlanArtifact) {
			a.SubmissionCreatedAt = now.Add(time.Second)
		}), now: now, maxAge: 10 * time.Minute, wantErrSub: "future"},
		{name: "zero max age", artifact: valid, now: now, wantErrSub: "max_age"},
		{name: "missing now", artifact: valid, maxAge: 10 * time.Minute, wantErrSub: "now"},
		{name: "invalid artifact first", artifact: mutateLiveOrderPlanArtifact(valid, func(a *domainlive.LiveOrderPlanArtifact) {
			a.OrderSubmitted = true
		}), now: now, maxAge: 10 * time.Minute, wantErrSub: "order_submitted"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := domainlive.ValidateLiveOrderPlanArtifactFreshness(tt.artifact, tt.now, tt.maxAge)
			if tt.wantErrSub != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("validate artifact freshness: %v", err)
			}
		})
	}
}

func validLiveOrderPlanArtifact(now time.Time) domainlive.LiveOrderPlanArtifact {
	return domainlive.LiveOrderPlanArtifact{
		SchemaVersion:       domainlive.LiveOrderPlanArtifactSchemaVersion,
		Source:              "decision-id",
		RunID:               "live_loop_artifact_0001",
		DecisionID:          "risk_decision_live_artifact_0001",
		SubmissionID:        "live_sub_artifact_0001",
		ClientOrderID:       "inq_live_artifact_0001",
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
		DecisionCreatedAt:   now.Add(-2 * time.Minute),
		RecordedAt:          now.Add(-time.Minute),
		SubmissionCreatedAt: now,
	}
}

func mutateLiveOrderPlanArtifact(
	artifact domainlive.LiveOrderPlanArtifact,
	mutate func(*domainlive.LiveOrderPlanArtifact),
) domainlive.LiveOrderPlanArtifact {
	if mutate != nil {
		mutate(&artifact)
	}
	return artifact
}

func validLiveOrderPlanArtifactSnapshot(
	t *testing.T,
	artifact domainlive.LiveOrderPlanArtifact,
) domainlive.LiveOrderPlanArtifactSnapshot {
	t.Helper()

	submission, err := domainlive.NewOrderSubmission(domainlive.OrderSubmissionInput{
		SubmissionID:     artifact.SubmissionID,
		ClientOrderID:    artifact.ClientOrderID,
		DecisionID:       artifact.DecisionID,
		DecisionApproved: true,
		IntentID:         "risk_intent_live_artifact_0001",
		RiskMode:         domainlive.RiskModeLive,
		Exchange:         artifact.Exchange,
		Category:         artifact.Category,
		Symbol:           artifact.Symbol,
		Side:             artifact.Side,
		Type:             artifact.OrderType,
		TimeInForce:      artifact.TimeInForce,
		Quantity:         decimal.RequireFromString(artifact.Quantity),
		ReferencePrice:   decimal.RequireFromString(artifact.EntryPrice),
		LimitPrice:       decimal.RequireFromString(artifact.LimitPrice),
		StopLoss:         decimal.RequireFromString(artifact.StopLoss),
		TakeProfit:       decimal.RequireFromString(artifact.TakeProfit),
		Leverage:         decimal.RequireFromString(artifact.Leverage),
		MaxLoss:          decimal.RequireFromString(artifact.MaxLoss),
		Confidence:       artifact.Confidence,
		Reason:           "risk_checks_passed",
		CreatedAt:        artifact.SubmissionCreatedAt,
	})
	if err != nil {
		t.Fatalf("new order submission: %v", err)
	}
	return domainlive.LiveOrderPlanArtifactSnapshot{
		RunID:             artifact.RunID,
		Submission:        submission,
		DecisionCreatedAt: artifact.DecisionCreatedAt,
		RecordedAt:        artifact.RecordedAt,
	}
}
