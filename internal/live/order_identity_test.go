package live_test

import (
	"strings"
	"testing"

	domainlive "github.com/VersoIt/Inquisitor/internal/live"
)

func TestDeterministicLiveLoopOrderIdentityIsStableAndExchangeSafe(t *testing.T) {
	first, err := domainlive.NewDeterministicLiveLoopOrderIdentity(" risk_decision_live_domain_0001 ", "")
	if err != nil {
		t.Fatalf("identity: %v", err)
	}
	second, err := domainlive.NewDeterministicLiveLoopOrderIdentity("risk_decision_live_domain_0001", "")
	if err != nil {
		t.Fatalf("identity again: %v", err)
	}

	if first != second {
		t.Fatalf("identity must be deterministic after decision-id trimming: first=%#v second=%#v", first, second)
	}
	if len(first.SubmissionID) > 36 || len(first.ClientOrderID) > 36 {
		t.Fatalf("identity must fit Bybit orderLinkId limit: %#v", first)
	}
	for _, value := range []string{first.RunID, first.SubmissionID, first.ClientOrderID} {
		for _, r := range value {
			if !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') && r != '_' {
				t.Fatalf("identity contains unsupported character %q in %q", r, value)
			}
		}
	}

	custom, err := domainlive.NewDeterministicLiveLoopOrderIdentity("risk_decision_live_domain_0001", "live_loop_operator_0001")
	if err != nil {
		t.Fatalf("custom identity: %v", err)
	}
	if custom.RunID != "live_loop_operator_0001" {
		t.Fatalf("custom run id mismatch: %#v", custom)
	}
	if custom.SubmissionID != first.SubmissionID || custom.ClientOrderID != first.ClientOrderID {
		t.Fatalf("custom run id must not alter idempotency keys: got %#v want submission=%s client=%s", custom, first.SubmissionID, first.ClientOrderID)
	}
}

func TestDeterministicLiveLoopOrderIdentityRejectsUnsafeInputsTableDriven(t *testing.T) {
	tests := []struct {
		name       string
		decisionID string
		runID      string
		wantErrSub string
	}{
		{name: "missing decision id", wantErrSub: "decision-id"},
		{name: "untrimmed run id", decisionID: "risk_decision_live_domain_0001", runID: " live_loop_operator_0001 ", wantErrSub: "run-id"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := domainlive.NewDeterministicLiveLoopOrderIdentity(tt.decisionID, tt.runID)
			if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
		})
	}
}
