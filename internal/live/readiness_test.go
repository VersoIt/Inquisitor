package live_test

import (
	"strings"
	"testing"

	"github.com/VersoIt/Inquisitor/internal/live"
)

func TestReadinessCheckSummaryAndReadyState(t *testing.T) {
	checks := []live.ReadinessCheck{
		live.NewReadinessCheck("live_config", live.ReadinessCheckStatusPass, "live config is safe"),
		live.NewReadinessCheck("recent_audit", live.ReadinessCheckStatusWarn, "recent failed runs require review"),
		live.NewReadinessCheck("pending_decision", live.ReadinessCheckStatusFail, "no pending decision"),
	}

	summary := live.SummarizeReadinessChecks(checks)
	if summary.Total != 3 || summary.Passed != 1 || summary.Warned != 1 || summary.Failed != 1 {
		t.Fatalf("summary mismatch: %#v", summary)
	}
	if live.ReadinessChecksReady(checks) {
		t.Fatalf("checks with FAIL must not be ready")
	}

	checks[2] = live.NewReadinessCheck("pending_decision", live.ReadinessCheckStatusPass, "pending decision found")
	if !live.ReadinessChecksReady(checks) {
		t.Fatalf("checks without FAIL must be ready")
	}
}

func TestValidateReadinessChecksTableDriven(t *testing.T) {
	valid := live.NewReadinessCheck("live_config", live.ReadinessCheckStatusPass, "live config is safe")
	tests := []struct {
		name       string
		checks     []live.ReadinessCheck
		wantErrSub string
	}{
		{name: "valid", checks: []live.ReadinessCheck{valid}},
		{name: "empty list", wantErrSub: "required"},
		{name: "missing name", checks: []live.ReadinessCheck{{Status: live.ReadinessCheckStatusPass, Details: "ok"}}, wantErrSub: "name"},
		{name: "untrimmed name", checks: []live.ReadinessCheck{{Name: " live_config ", Status: live.ReadinessCheckStatusPass, Details: "ok"}}, wantErrSub: "trimmed"},
		{name: "invalid status", checks: []live.ReadinessCheck{{Name: "live_config", Status: "BROKEN", Details: "ok"}}, wantErrSub: "status"},
		{name: "missing details", checks: []live.ReadinessCheck{{Name: "live_config", Status: live.ReadinessCheckStatusPass}}, wantErrSub: "details"},
		{name: "untrimmed details", checks: []live.ReadinessCheck{{Name: "live_config", Status: live.ReadinessCheckStatusPass, Details: " ok "}}, wantErrSub: "details"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := live.ValidateReadinessChecks(tt.checks)
			if tt.wantErrSub != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("validate readiness checks: %v", err)
			}
		})
	}
}
