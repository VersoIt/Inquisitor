package live_test

import (
	"strings"
	"testing"

	domainlive "github.com/VersoIt/Inquisitor/internal/live"
)

func TestSummarizeLiveOpsStatusTableDriven(t *testing.T) {
	tests := []struct {
		name       string
		checks     []domainlive.ReadinessCheck
		want       domainlive.LiveOpsStatus
		wantErrSub string
	}{
		{
			name: "clear when every check passes",
			checks: []domainlive.ReadinessCheck{
				domainlive.NewReadinessCheck("kill_switch", domainlive.ReadinessCheckStatusPass, "inactive"),
				domainlive.NewReadinessCheck("recent_live_loop_audit", domainlive.ReadinessCheckStatusPass, "clear"),
			},
			want: domainlive.LiveOpsStatusClear,
		},
		{
			name: "attention when any check warns",
			checks: []domainlive.ReadinessCheck{
				domainlive.NewReadinessCheck("kill_switch", domainlive.ReadinessCheckStatusPass, "inactive"),
				domainlive.NewReadinessCheck("pending_live_decision", domainlive.ReadinessCheckStatusWarn, "none pending"),
			},
			want: domainlive.LiveOpsStatusAttention,
		},
		{
			name: "blocked when any check fails",
			checks: []domainlive.ReadinessCheck{
				domainlive.NewReadinessCheck("kill_switch", domainlive.ReadinessCheckStatusFail, "active"),
				domainlive.NewReadinessCheck("pending_live_decision", domainlive.ReadinessCheckStatusWarn, "none pending"),
			},
			want: domainlive.LiveOpsStatusBlocked,
		},
		{
			name:       "invalid checks fail closed",
			checks:     []domainlive.ReadinessCheck{{Name: "", Status: domainlive.ReadinessCheckStatusPass, Details: "bad"}},
			wantErrSub: "name",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := domainlive.SummarizeLiveOpsStatus(tt.checks)
			if tt.wantErrSub != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("summarize status: %v", err)
			}
			if got != tt.want {
				t.Fatalf("status mismatch: got %s want %s", got, tt.want)
			}
		})
	}
}

func TestValidateLiveOpsStatusTableDriven(t *testing.T) {
	tests := []struct {
		name       string
		status     domainlive.LiveOpsStatus
		wantErrSub string
	}{
		{name: "clear", status: domainlive.LiveOpsStatusClear},
		{name: "attention", status: domainlive.LiveOpsStatusAttention},
		{name: "blocked", status: domainlive.LiveOpsStatusBlocked},
		{name: "lowercase rejected", status: "clear", wantErrSub: "status"},
		{name: "unknown rejected", status: "BROKEN", wantErrSub: "status"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := domainlive.ValidateLiveOpsStatus(tt.status)
			if tt.wantErrSub == "" {
				if err != nil {
					t.Fatalf("validate status: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
		})
	}
}
