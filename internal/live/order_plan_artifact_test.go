package live_test

import (
	"strings"
	"testing"
	"time"

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
