package live_test

import (
	"strings"
	"testing"
	"time"

	domainlive "github.com/VersoIt/Inquisitor/internal/live"
	domainrisk "github.com/VersoIt/Inquisitor/internal/risk"
)

func TestValidateKillSwitchReadinessArtifactHandoffTableDriven(t *testing.T) {
	createdAt := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	releasedAt := createdAt.Add(-time.Minute)
	validReadiness := validLiveReadinessArtifact(createdAt)
	validKillSwitch := validKillSwitchReadinessHandoffArtifact(t, createdAt, domainrisk.KillSwitchState{})
	releasedKillSwitch := validKillSwitchReadinessHandoffArtifact(t, createdAt, domainrisk.KillSwitchState{
		Reason:    "operator verified recovery",
		Source:    "operator",
		UpdatedAt: releasedAt,
	})

	tests := []struct {
		name       string
		killSwitch domainrisk.KillSwitchArtifact
		readiness  domainlive.LiveReadinessArtifact
		wantErrSub string
	}{
		{name: "valid inactive snapshot", killSwitch: validKillSwitch, readiness: validReadiness},
		{
			name:       "valid released inactive snapshot",
			killSwitch: releasedKillSwitch,
			readiness: mutateLiveReadinessArtifact(validReadiness, func(a *domainlive.LiveReadinessArtifact) {
				a.KillSwitch.Reason = "operator verified recovery"
				a.KillSwitch.Source = "operator"
				a.KillSwitch.UpdatedAt = &releasedAt
			}),
		},
		{
			name: "active mismatch",
			killSwitch: validKillSwitchReadinessHandoffArtifact(t, createdAt, domainrisk.KillSwitchState{
				Active:    true,
				Reason:    "operator emergency stop",
				Source:    "operator",
				UpdatedAt: releasedAt,
			}),
			readiness:  validReadiness,
			wantErrSub: "active",
		},
		{
			name:       "reason mismatch",
			killSwitch: releasedKillSwitch,
			readiness: mutateLiveReadinessArtifact(validReadiness, func(a *domainlive.LiveReadinessArtifact) {
				a.KillSwitch.Reason = "different release"
				a.KillSwitch.Source = "operator"
				a.KillSwitch.UpdatedAt = &releasedAt
			}),
			wantErrSub: "reason",
		},
		{
			name:       "source mismatch",
			killSwitch: releasedKillSwitch,
			readiness: mutateLiveReadinessArtifact(validReadiness, func(a *domainlive.LiveReadinessArtifact) {
				a.KillSwitch.Reason = "operator verified recovery"
				a.KillSwitch.Source = "automation"
				a.KillSwitch.UpdatedAt = &releasedAt
			}),
			wantErrSub: "source",
		},
		{
			name:       "updated at mismatch",
			killSwitch: releasedKillSwitch,
			readiness: mutateLiveReadinessArtifact(validReadiness, func(a *domainlive.LiveReadinessArtifact) {
				otherUpdatedAt := releasedAt.Add(-time.Second)
				a.KillSwitch.Reason = "operator verified recovery"
				a.KillSwitch.Source = "operator"
				a.KillSwitch.UpdatedAt = &otherUpdatedAt
			}),
			wantErrSub: "updated_at",
		},
		{
			name:       "invalid readiness fails first",
			killSwitch: validKillSwitch,
			readiness: mutateLiveReadinessArtifact(validReadiness, func(a *domainlive.LiveReadinessArtifact) {
				a.Checks = nil
			}),
			wantErrSub: "checks",
		},
		{
			name: "missing kill switch state",
			killSwitch: domainrisk.KillSwitchArtifact{
				SchemaVersion: domainrisk.KillSwitchArtifactSchemaVersion,
				CreatedAt:     createdAt,
				ConfigPath:    "configs/live.local.yaml",
				Action:        domainrisk.KillSwitchArtifactActionState,
			},
			readiness:  validReadiness,
			wantErrSub: "state",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := domainlive.ValidateKillSwitchReadinessArtifactHandoff(tt.killSwitch, tt.readiness)
			if tt.wantErrSub != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("validate kill switch readiness handoff: %v", err)
			}
		})
	}
}

func validKillSwitchReadinessHandoffArtifact(
	t *testing.T,
	createdAt time.Time,
	state domainrisk.KillSwitchState,
) domainrisk.KillSwitchArtifact {
	t.Helper()

	artifact, err := domainrisk.BuildKillSwitchArtifact(domainrisk.BuildKillSwitchArtifactRequest{
		CreatedAt:  createdAt,
		ConfigPath: "configs/live.local.yaml",
		Action:     domainrisk.KillSwitchArtifactActionState,
		State:      &state,
	})
	if err != nil {
		t.Fatalf("build kill switch artifact: %v", err)
	}
	return artifact
}
