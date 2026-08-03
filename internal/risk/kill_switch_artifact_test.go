package risk_test

import (
	"strings"
	"testing"
	"time"

	"github.com/VersoIt/Inquisitor/internal/risk"
)

func TestBuildKillSwitchArtifactTableDriven(t *testing.T) {
	createdAt := time.Date(2026, 8, 3, 15, 0, 0, 123, time.FixedZone("UTC+3", 3*60*60))
	eventAt := time.Date(2026, 8, 3, 15, 1, 0, 456, time.FixedZone("UTC+3", 3*60*60))
	falseActive := false
	activeEvent := validKillSwitchArtifactDomainEvent(eventAt, true)
	releaseEvent := validKillSwitchArtifactDomainEvent(eventAt.Add(time.Minute), false)

	tests := []struct {
		name   string
		req    risk.BuildKillSwitchArtifactRequest
		assert func(*testing.T, risk.KillSwitchArtifact)
	}{
		{
			name: "state normalizes metadata and keeps optional timestamp",
			req: risk.BuildKillSwitchArtifactRequest{
				CreatedAt:  createdAt,
				ConfigPath: " configs/live.local.yaml ",
				Action:     "STATE",
				State: &risk.KillSwitchState{
					Active:    true,
					Reason:    "operator emergency stop",
					Source:    "operator",
					UpdatedAt: eventAt,
				},
			},
			assert: func(t *testing.T, artifact risk.KillSwitchArtifact) {
				t.Helper()
				if artifact.SchemaVersion != risk.KillSwitchArtifactSchemaVersion ||
					artifact.Action != risk.KillSwitchArtifactActionState ||
					artifact.ConfigPath != "configs/live.local.yaml" ||
					!artifact.CreatedAt.Equal(createdAt.UTC()) {
					t.Fatalf("metadata mismatch: %#v", artifact)
				}
				if artifact.State == nil || !artifact.State.Active || artifact.State.UpdatedAt == nil || !artifact.State.UpdatedAt.Equal(eventAt.UTC()) {
					t.Fatalf("state mismatch: %#v", artifact.State)
				}
				if artifact.Query != nil || artifact.Event != nil || len(artifact.Events) != 0 {
					t.Fatalf("state artifact should not include query/event/events: %#v", artifact)
				}
			},
		},
		{
			name: "state supports never activated zero state",
			req: risk.BuildKillSwitchArtifactRequest{
				CreatedAt:  createdAt,
				ConfigPath: "configs/live.local.yaml",
				Action:     risk.KillSwitchArtifactActionState,
				State:      &risk.KillSwitchState{},
			},
			assert: func(t *testing.T, artifact risk.KillSwitchArtifact) {
				t.Helper()
				if artifact.State == nil || artifact.State.Active || artifact.State.UpdatedAt != nil {
					t.Fatalf("zero state mismatch: %#v", artifact.State)
				}
			},
		},
		{
			name: "list preserves false active filter, utc window, and events",
			req: risk.BuildKillSwitchArtifactRequest{
				CreatedAt:  createdAt,
				ConfigPath: "configs/live.local.yaml",
				Action:     risk.KillSwitchArtifactActionList,
				Query: &risk.KillSwitchEventQuery{
					EventID: "risk_kill_switch_0001",
					Active:  &falseActive,
					Source:  "operator",
					Start:   eventAt.Add(-time.Hour),
					End:     eventAt,
					Limit:   2,
				},
				Events: []risk.KillSwitchEvent{activeEvent},
			},
			assert: func(t *testing.T, artifact risk.KillSwitchArtifact) {
				t.Helper()
				if artifact.Query == nil || artifact.Query.Active == nil || *artifact.Query.Active {
					t.Fatalf("active=false query filter was not preserved: %#v", artifact.Query)
				}
				if artifact.Query.Start == nil || !artifact.Query.Start.Equal(eventAt.Add(-time.Hour).UTC()) ||
					artifact.Query.End == nil || !artifact.Query.End.Equal(eventAt.UTC()) ||
					artifact.Query.Limit != 2 {
					t.Fatalf("query window mismatch: %#v", artifact.Query)
				}
				if len(artifact.Events) != 1 || artifact.Events[0].EventID != activeEvent.EventID {
					t.Fatalf("events mismatch: %#v", artifact.Events)
				}
				if artifact.State != nil || artifact.Event != nil {
					t.Fatalf("list artifact should not include state/event: %#v", artifact)
				}
			},
		},
		{
			name: "activate mirrors event into state",
			req: risk.BuildKillSwitchArtifactRequest{
				CreatedAt:  createdAt,
				ConfigPath: "configs/live.local.yaml",
				Action:     risk.KillSwitchArtifactActionActivate,
				State:      ptr(risk.KillSwitchStateFromEvent(activeEvent)),
				Event:      &activeEvent,
			},
			assert: func(t *testing.T, artifact risk.KillSwitchArtifact) {
				t.Helper()
				assertKillSwitchArtifactWriteMirror(t, artifact, true, activeEvent.EventID)
			},
		},
		{
			name: "release mirrors inactive event into state",
			req: risk.BuildKillSwitchArtifactRequest{
				CreatedAt:  createdAt,
				ConfigPath: "configs/live.local.yaml",
				Action:     risk.KillSwitchArtifactActionRelease,
				State:      ptr(risk.KillSwitchStateFromEvent(releaseEvent)),
				Event:      &releaseEvent,
			},
			assert: func(t *testing.T, artifact risk.KillSwitchArtifact) {
				t.Helper()
				assertKillSwitchArtifactWriteMirror(t, artifact, false, releaseEvent.EventID)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			artifact, err := risk.BuildKillSwitchArtifact(tt.req)
			if err != nil {
				t.Fatalf("build kill switch artifact: %v", err)
			}
			if err := risk.ValidateKillSwitchArtifact(artifact); err != nil {
				t.Fatalf("validate built artifact: %v", err)
			}
			tt.assert(t, artifact)
		})
	}
}

func TestValidateKillSwitchArtifactRejectsInvalidArtifactsTableDriven(t *testing.T) {
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name       string
		artifact   risk.KillSwitchArtifact
		wantErrSub string
	}{
		{
			name:       "bad schema",
			artifact:   withKillSwitchArtifactMutation(validKillSwitchWriteArtifact(now, true), func(a *risk.KillSwitchArtifact) { a.SchemaVersion = "old" }),
			wantErrSub: "schema_version",
		},
		{
			name:       "missing created at",
			artifact:   withKillSwitchArtifactMutation(validKillSwitchWriteArtifact(now, true), func(a *risk.KillSwitchArtifact) { a.CreatedAt = time.Time{} }),
			wantErrSub: "created_at",
		},
		{
			name:       "untrimmed config path",
			artifact:   withKillSwitchArtifactMutation(validKillSwitchWriteArtifact(now, true), func(a *risk.KillSwitchArtifact) { a.ConfigPath = " configs/live.local.yaml " }),
			wantErrSub: "config_path",
		},
		{
			name:       "unknown action",
			artifact:   withKillSwitchArtifactMutation(validKillSwitchWriteArtifact(now, true), func(a *risk.KillSwitchArtifact) { a.Action = "delete" }),
			wantErrSub: "action",
		},
		{
			name:       "state action missing state",
			artifact:   validKillSwitchStateArtifact(now, nil),
			wantErrSub: "state action requires state",
		},
		{
			name: "state action rejects query",
			artifact: withKillSwitchArtifactMutation(validKillSwitchStateArtifact(now, &risk.KillSwitchArtifactState{}), func(a *risk.KillSwitchArtifact) {
				a.Query = &risk.KillSwitchArtifactQuery{Limit: 1}
			}),
			wantErrSub: "state action must not include query",
		},
		{
			name:       "list action missing query",
			artifact:   validKillSwitchListArtifact(now, nil, nil),
			wantErrSub: "list action requires query",
		},
		{
			name: "list action requires positive limit",
			artifact: validKillSwitchListArtifact(now, &risk.KillSwitchArtifactQuery{
				Limit: 0,
			}, nil),
			wantErrSub: "query.limit",
		},
		{
			name: "list action caps events by limit",
			artifact: validKillSwitchListArtifact(now, &risk.KillSwitchArtifactQuery{Limit: 1}, []risk.KillSwitchArtifactEvent{
				validKillSwitchArtifactEvent(now, true),
				validKillSwitchArtifactEvent(now.Add(time.Second), false),
			}),
			wantErrSub: "events length",
		},
		{
			name:       "activate action requires active event",
			artifact:   withKillSwitchArtifactMutation(validKillSwitchWriteArtifact(now, false), func(a *risk.KillSwitchArtifact) { a.Action = risk.KillSwitchArtifactActionActivate }),
			wantErrSub: "active",
		},
		{
			name: "write action state must mirror event",
			artifact: withKillSwitchArtifactMutation(validKillSwitchWriteArtifact(now, true), func(a *risk.KillSwitchArtifact) {
				a.State.Source = "live_position_drift"
			}),
			wantErrSub: "mirror",
		},
		{
			name: "write action rejects invalid event",
			artifact: withKillSwitchArtifactMutation(validKillSwitchWriteArtifact(now, true), func(a *risk.KillSwitchArtifact) {
				a.Event.EventID = ""
			}),
			wantErrSub: "event_id",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := risk.ValidateKillSwitchArtifact(tt.artifact)
			if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
		})
	}
}

func assertKillSwitchArtifactWriteMirror(t *testing.T, artifact risk.KillSwitchArtifact, active bool, eventID string) {
	t.Helper()
	if artifact.Event == nil || artifact.State == nil {
		t.Fatalf("write artifact must include event and state: %#v", artifact)
	}
	if artifact.Event.Active != active || artifact.State.Active != active || artifact.Event.EventID != eventID {
		t.Fatalf("write active/event mismatch: event=%#v state=%#v", artifact.Event, artifact.State)
	}
	if artifact.Event.Reason != artifact.State.Reason ||
		artifact.Event.Source != artifact.State.Source ||
		artifact.State.UpdatedAt == nil ||
		!artifact.State.UpdatedAt.Equal(artifact.Event.CreatedAt.UTC()) {
		t.Fatalf("write state does not mirror event: event=%#v state=%#v", artifact.Event, artifact.State)
	}
	if artifact.Query != nil || len(artifact.Events) != 0 {
		t.Fatalf("write artifact should not include query/events: %#v", artifact)
	}
}

func validKillSwitchArtifactDomainEvent(createdAt time.Time, active bool) risk.KillSwitchEvent {
	eventID := "risk_kill_switch_activate_0001"
	reason := "operator emergency stop"
	if !active {
		eventID = "risk_kill_switch_release_0001"
		reason = "operator verified recovery"
	}
	return risk.KillSwitchEvent{
		EventID:   eventID,
		Active:    active,
		Reason:    reason,
		Source:    "operator",
		CreatedAt: createdAt,
	}
}

func validKillSwitchArtifactEvent(createdAt time.Time, active bool) risk.KillSwitchArtifactEvent {
	event := validKillSwitchArtifactDomainEvent(createdAt, active)
	return risk.KillSwitchArtifactEvent{
		EventID:   event.EventID,
		Active:    event.Active,
		Reason:    event.Reason,
		Source:    event.Source,
		CreatedAt: event.CreatedAt.UTC(),
	}
}

func validKillSwitchStateArtifact(now time.Time, state *risk.KillSwitchArtifactState) risk.KillSwitchArtifact {
	return risk.KillSwitchArtifact{
		SchemaVersion: risk.KillSwitchArtifactSchemaVersion,
		CreatedAt:     now,
		ConfigPath:    "configs/live.local.yaml",
		Action:        risk.KillSwitchArtifactActionState,
		State:         state,
	}
}

func validKillSwitchListArtifact(now time.Time, query *risk.KillSwitchArtifactQuery, events []risk.KillSwitchArtifactEvent) risk.KillSwitchArtifact {
	return risk.KillSwitchArtifact{
		SchemaVersion: risk.KillSwitchArtifactSchemaVersion,
		CreatedAt:     now,
		ConfigPath:    "configs/live.local.yaml",
		Action:        risk.KillSwitchArtifactActionList,
		Query:         query,
		Events:        events,
	}
}

func validKillSwitchWriteArtifact(now time.Time, active bool) risk.KillSwitchArtifact {
	event := validKillSwitchArtifactEvent(now, active)
	updatedAt := event.CreatedAt
	action := risk.KillSwitchArtifactActionRelease
	if active {
		action = risk.KillSwitchArtifactActionActivate
	}
	return risk.KillSwitchArtifact{
		SchemaVersion: risk.KillSwitchArtifactSchemaVersion,
		CreatedAt:     now,
		ConfigPath:    "configs/live.local.yaml",
		Action:        action,
		Event:         &event,
		State: &risk.KillSwitchArtifactState{
			Active:    event.Active,
			Reason:    event.Reason,
			Source:    event.Source,
			UpdatedAt: &updatedAt,
		},
	}
}

func withKillSwitchArtifactMutation(artifact risk.KillSwitchArtifact, mutate func(*risk.KillSwitchArtifact)) risk.KillSwitchArtifact {
	mutate(&artifact)
	return artifact
}

func ptr[T any](value T) *T {
	return &value
}
