package live_test

import (
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/VersoIt/Inquisitor/internal/live"
	domainrisk "github.com/VersoIt/Inquisitor/internal/risk"
)

func TestValidatePendingLiveDecisionRejectsUnsafeInputsTableDriven(t *testing.T) {
	tests := []struct {
		name       string
		mutate     func(*live.PendingLiveDecision)
		wantErrSub string
	}{
		{name: "valid", mutate: func(*live.PendingLiveDecision) {}},
		{name: "paper mode", mutate: func(c *live.PendingLiveDecision) { c.Decision.Mode = domainrisk.ModePaper }, wantErrSub: "LIVE"},
		{name: "not approved", mutate: func(c *live.PendingLiveDecision) {
			c.Decision.Decision.Approved = false
			c.Decision.Decision.FinalQuantity = decimal.Zero
			c.Decision.Decision.MaxLoss = decimal.Zero
		}, wantErrSub: "approved"},
		{name: "invalid decision audit", mutate: func(c *live.PendingLiveDecision) { c.Decision.DecisionID = "" }, wantErrSub: "decision_id"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			candidate := validPendingLiveDecision()
			tt.mutate(&candidate)

			err := live.ValidatePendingLiveDecision(candidate)
			if tt.wantErrSub != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("validate pending live decision: %v", err)
			}
		})
	}
}

func TestValidatePendingLiveDecisionQueryRejectsUnsafeInputsTableDriven(t *testing.T) {
	tests := []struct {
		name       string
		query      live.PendingLiveDecisionQuery
		wantErrSub string
	}{
		{name: "valid empty"},
		{name: "valid symbol limit", query: live.PendingLiveDecisionQuery{Symbol: "BTCUSDT", Limit: 10}},
		{name: "lowercase symbol", query: live.PendingLiveDecisionQuery{Symbol: "btcusdt"}, wantErrSub: "symbol"},
		{name: "untrimmed symbol", query: live.PendingLiveDecisionQuery{Symbol: " BTCUSDT "}, wantErrSub: "symbol"},
		{name: "negative limit", query: live.PendingLiveDecisionQuery{Limit: -1}, wantErrSub: "limit"},
		{name: "limit above max", query: live.PendingLiveDecisionQuery{Limit: 101}, wantErrSub: "limit"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := live.ValidatePendingLiveDecisionQuery(tt.query)
			if tt.wantErrSub != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("validate pending live decision query: %v", err)
			}
		})
	}
}

func validPendingLiveDecision() live.PendingLiveDecision {
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	return live.PendingLiveDecision{
		Decision: domainrisk.DecisionAuditRecord{
			DecisionID: "risk_decision_live_pending_0001",
			Decision: domainrisk.Decision{
				IntentID:      "risk_intent_live_pending_0001",
				Approved:      true,
				FinalQuantity: decimal.RequireFromString("0.005"),
				MaxLoss:       decimal.RequireFromString("5"),
				StopLoss:      decimal.RequireFromString("99000"),
				TakeProfit:    decimal.RequireFromString("102000"),
				Reason:        "risk_checks_passed",
				Checks: []domainrisk.Check{{
					Name:   "trading_enabled",
					Passed: true,
				}},
				CreatedAt: now,
			},
			Mode:            domainrisk.ModeLive,
			HypothesisID:    "hypothesis_live_pending_0001",
			StrategyName:    "trend-momentum",
			Symbol:          "BTCUSDT",
			Side:            domainrisk.SideLong,
			EntryPrice:      decimal.RequireFromString("100000"),
			Leverage:        decimal.RequireFromString("1"),
			Confidence:      80,
			IntentReason:    "signal confirmed",
			IntentCreatedAt: now.Add(-time.Minute),
			RecordedAt:      now.Add(time.Second),
		},
	}
}
