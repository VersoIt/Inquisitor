package risk_test

import (
	"strings"
	"testing"
	"time"

	"github.com/VersoIt/Inquisitor/internal/risk"
)

func TestNewKillSwitchEventNormalizesOperatorInput(t *testing.T) {
	now := time.Date(2026, 6, 21, 13, 0, 0, 0, time.FixedZone("UTC+3", 3*60*60))
	event, err := risk.NewKillSwitchEvent(risk.KillSwitchEventInput{
		EventID:   " kill_switch_0001 ",
		Active:    true,
		Reason:    " operator emergency stop ",
		Source:    " OPERATOR ",
		CreatedAt: now,
	})
	if err != nil {
		t.Fatalf("new kill switch event: %v", err)
	}
	if event.EventID != "kill_switch_0001" || event.Reason != "operator emergency stop" || event.Source != "operator" {
		t.Fatalf("event was not normalized: %#v", event)
	}
	if event.CreatedAt.Location() != time.UTC {
		t.Fatalf("created_at must be UTC, got %s", event.CreatedAt)
	}
}

func TestKillSwitchEventValidationRejectsInvalidEventsTableDriven(t *testing.T) {
	now := time.Date(2026, 6, 21, 13, 0, 0, 0, time.UTC)
	valid := risk.KillSwitchEvent{
		EventID:   "kill_switch_0001",
		Active:    true,
		Reason:    "operator emergency stop",
		Source:    "operator",
		CreatedAt: now,
	}

	tests := []struct {
		name       string
		mutate     func(*risk.KillSwitchEvent)
		wantErrSub string
	}{
		{"missing event id", func(e *risk.KillSwitchEvent) { e.EventID = "" }, "event_id"},
		{"untrimmed event id", func(e *risk.KillSwitchEvent) { e.EventID = " kill_switch_0001 " }, "event_id"},
		{"missing reason", func(e *risk.KillSwitchEvent) { e.Reason = " " }, "reason"},
		{"missing source", func(e *risk.KillSwitchEvent) { e.Source = "" }, "source"},
		{"source not normalized", func(e *risk.KillSwitchEvent) { e.Source = "Operator" }, "source"},
		{"missing created at", func(e *risk.KillSwitchEvent) { e.CreatedAt = time.Time{} }, "created_at"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			event := valid
			tt.mutate(&event)

			err := risk.ValidateKillSwitchEvent(event)
			if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
		})
	}
}

func TestKillSwitchStateValidationTableDriven(t *testing.T) {
	now := time.Date(2026, 6, 21, 13, 0, 0, 0, time.UTC)
	tests := []struct {
		name       string
		state      risk.KillSwitchState
		wantErrSub string
	}{
		{"never activated zero state is valid", risk.KillSwitchState{}, ""},
		{"active state is valid", risk.KillSwitchState{Active: true, Reason: "operator stop", Source: "operator", UpdatedAt: now}, ""},
		{"inactive released state is valid", risk.KillSwitchState{Active: false, Reason: "operator release", Source: "operator", UpdatedAt: now}, ""},
		{"active without timestamp", risk.KillSwitchState{Active: true, Reason: "operator stop", Source: "operator"}, "updated_at"},
		{"timestamp without reason", risk.KillSwitchState{UpdatedAt: now, Source: "operator"}, "reason"},
		{"timestamp without source", risk.KillSwitchState{UpdatedAt: now, Reason: "operator stop"}, "source"},
		{"source not normalized", risk.KillSwitchState{UpdatedAt: now, Reason: "operator stop", Source: "Operator"}, "source"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := risk.ValidateKillSwitchState(tt.state)
			if tt.wantErrSub == "" {
				if err != nil {
					t.Fatalf("validate state: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
		})
	}
}

func TestKillSwitchEventQueryValidationTableDriven(t *testing.T) {
	now := time.Date(2026, 6, 21, 13, 0, 0, 0, time.UTC)
	tests := []struct {
		name       string
		query      risk.KillSwitchEventQuery
		wantErrSub string
	}{
		{"valid empty query", risk.KillSwitchEventQuery{}, ""},
		{"valid filtered query", risk.KillSwitchEventQuery{Source: "operator", Start: now, End: now.Add(time.Hour), Limit: 10}, ""},
		{"rejects uppercase source", risk.KillSwitchEventQuery{Source: "Operator"}, "source"},
		{"rejects invalid window", risk.KillSwitchEventQuery{Start: now, End: now}, "end"},
		{"rejects negative limit", risk.KillSwitchEventQuery{Limit: -1}, "limit"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := risk.ValidateKillSwitchEventQuery(tt.query)
			if tt.wantErrSub == "" {
				if err != nil {
					t.Fatalf("validate query: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
		})
	}
}
