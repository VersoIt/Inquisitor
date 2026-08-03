package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"

	"github.com/VersoIt/Inquisitor/internal/config"
	domainrisk "github.com/VersoIt/Inquisitor/internal/risk"
)

func TestRiskKillSwitchRequestFromFlagsTableDriven(t *testing.T) {
	now := time.Date(2026, 8, 3, 12, 30, 45, 987654321, time.UTC)

	tests := []struct {
		name       string
		action     string
		eventID    string
		reason     string
		source     string
		active     string
		limit      int
		want       riskKillSwitchRequest
		wantActive *bool
		wantErrSub string
	}{
		{
			name:   "default state",
			action: "state",
			want:   riskKillSwitchRequest{Action: riskKillSwitchActionState, Limit: defaultRiskKillSwitchLimit},
		},
		{
			name:       "normalizes action and write source",
			action:     "ACTIVATE",
			reason:     "operator emergency stop",
			source:     "OPERATOR",
			want:       riskKillSwitchRequest{Action: riskKillSwitchActionActivate, EventID: "risk_kill_switch_activate_20260803T123045.987654321Z", Reason: "operator emergency stop", Source: "operator", Limit: defaultRiskKillSwitchLimit},
			wantActive: nil,
		},
		{
			name:    "release with explicit event id",
			action:  "release",
			eventID: "risk_kill_switch_release_0001",
			reason:  "operator verified recovery",
			want:    riskKillSwitchRequest{Action: riskKillSwitchActionRelease, EventID: "risk_kill_switch_release_0001", Reason: "operator verified recovery", Source: "operator", Limit: defaultRiskKillSwitchLimit},
		},
		{
			name:    "list with active false filter",
			action:  "list",
			eventID: "risk_kill_switch_release_0001",
			source:  "live_position_drift",
			active:  "false",
			limit:   5,
			want:    riskKillSwitchRequest{Action: riskKillSwitchActionList, EventID: "risk_kill_switch_release_0001", Source: "live_position_drift", Limit: 5},
		},
		{name: "unknown action", action: "delete", wantErrSub: "action"},
		{name: "untrimmed action", action: " state ", wantErrSub: "action"},
		{name: "untrimmed event id", action: "list", eventID: " risk_kill_switch_0001 ", wantErrSub: "event-id"},
		{name: "untrimmed reason", action: "activate", reason: " stop ", wantErrSub: "reason"},
		{name: "missing write reason", action: "activate", wantErrSub: "reason"},
		{name: "bad active", action: "list", active: "yes", wantErrSub: "active"},
		{name: "negative limit", action: "list", limit: -1, wantErrSub: "limit"},
		{name: "limit above max", action: "list", limit: 1001, wantErrSub: "limit"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := riskKillSwitchRequestFromFlags(tt.action, tt.eventID, tt.reason, tt.source, tt.active, tt.limit, now)
			if tt.wantErrSub != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("request from flags: %v", err)
			}
			if tt.active == "false" {
				if got.Active == nil || *got.Active {
					t.Fatalf("active filter mismatch: %#v", got.Active)
				}
				got.Active = nil
			}
			if got != tt.want {
				t.Fatalf("request mismatch: got %#v want %#v", got, tt.want)
			}
		})
	}
}

func TestRiskKillSwitchGeneratedEventIDIsUTC(t *testing.T) {
	now := time.Date(2026, 8, 3, 15, 30, 45, 123456789, time.FixedZone("MSK", 3*60*60))
	got := riskKillSwitchGeneratedEventID("activate", now)
	want := "risk_kill_switch_activate_20260803T123045.123456789Z"
	if got != want {
		t.Fatalf("generated id mismatch: got %q want %q", got, want)
	}
}

func TestRunRiskKillSwitchRejectsUnsafeFlagsBeforeSideEffects(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantErrSub string
	}{
		{name: "bad action", args: []string{"-action", "delete"}, wantErrSub: "action"},
		{name: "bad timeout", args: []string{"-timeout", "0s"}, wantErrSub: "timeout"},
		{name: "missing activate reason", args: []string{"-action", "activate"}, wantErrSub: "reason"},
		{name: "untrimmed event id", args: []string{"-action", "list", "-event-id", " risk_kill_switch_0001 "}, wantErrSub: "event-id"},
		{name: "bad active filter", args: []string{"-action", "list", "-active", "yes"}, wantErrSub: "active"},
		{name: "limit above max", args: []string{"-action", "list", "-limit", "1001"}, wantErrSub: "limit"},
		{name: "untrimmed artifact path", args: []string{"-artifact-path", " artifacts/risk-kill-switch.json "}, wantErrSub: "artifact-path"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var loaded bool
			var opened bool
			err := runRiskKillSwitch(context.Background(), tt.args, riskKillSwitchDependencies{
				loadConfig: func(string) (*config.Config, error) {
					loaded = true
					return validRiskKillSwitchConfig(), nil
				},
				openDB: func(context.Context, config.DatabaseConfig) (*sql.DB, error) {
					opened = true
					return nil, nil
				},
				output: &bytes.Buffer{},
			})
			if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
			if loaded || opened {
				t.Fatalf("unsafe flags must stop before side effects: loaded=%t opened=%t", loaded, opened)
			}
		})
	}
}

func TestRunRiskKillSwitchLogsCurrentState(t *testing.T) {
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	repo := &fakeRiskKillSwitchRepository{state: domainrisk.KillSwitchState{
		Active:    true,
		Reason:    "operator emergency stop",
		Source:    "operator",
		UpdatedAt: now.Add(-time.Minute),
	}}
	var output bytes.Buffer
	artifactPath := filepath.Join(t.TempDir(), "artifacts", "risk-kill-switch-state.json")
	err = runRiskKillSwitch(context.Background(), []string{
		"-action", "state",
		"-artifact-path", artifactPath,
	}, riskKillSwitchDependencies{
		loadConfig: func(string) (*config.Config, error) {
			return validRiskKillSwitchConfig(), nil
		},
		openDB: func(context.Context, config.DatabaseConfig) (*sql.DB, error) {
			return db, nil
		},
		newKillSwitch: func(*sql.DB) domainrisk.KillSwitchRepository {
			return repo
		},
		now: func() time.Time {
			return now
		},
		output: &output,
	})
	if err != nil {
		t.Fatalf("run risk kill switch state: %v\nlogs:\n%s", err, output.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sql expectations: %v", err)
	}
	if repo.currentCalls != 1 {
		t.Fatalf("current calls mismatch: %d", repo.currentCalls)
	}
	artifact := readRiskKillSwitchArtifact(t, artifactPath)
	if artifact.Action != domainrisk.KillSwitchArtifactActionState ||
		artifact.State == nil ||
		!artifact.State.Active ||
		artifact.State.Reason != "operator emergency stop" ||
		artifact.Query != nil ||
		artifact.Event != nil ||
		len(artifact.Events) != 0 {
		t.Fatalf("state artifact mismatch: %#v", artifact)
	}
	for _, want := range []string{
		`"msg":"risk kill switch state"`,
		`"msg":"risk kill switch artifact written"`,
		`"active":true`,
		`"reason":"operator emergency stop"`,
		`"source":"operator"`,
	} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("expected logs to contain %s, got\n%s", want, output.String())
		}
	}
}

func TestRunRiskKillSwitchListsEvents(t *testing.T) {
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	repo := &fakeRiskKillSwitchRepository{events: []domainrisk.KillSwitchEvent{{
		EventID:   "risk_kill_switch_0001",
		Active:    true,
		Reason:    "position drift BLOCKED",
		Source:    "live_position_drift",
		CreatedAt: now.Add(-time.Minute),
	}}}
	var output bytes.Buffer
	artifactPath := filepath.Join(t.TempDir(), "artifacts", "risk-kill-switch-list.json")
	err = runRiskKillSwitch(context.Background(), []string{
		"-action", "list",
		"-active", "true",
		"-source", "live_position_drift",
		"-limit", "5",
		"-artifact-path", artifactPath,
	}, riskKillSwitchDependencies{
		loadConfig: func(string) (*config.Config, error) {
			return validRiskKillSwitchConfig(), nil
		},
		openDB: func(context.Context, config.DatabaseConfig) (*sql.DB, error) {
			return db, nil
		},
		newKillSwitch: func(*sql.DB) domainrisk.KillSwitchRepository {
			return repo
		},
		now: func() time.Time {
			return now
		},
		output: &output,
	})
	if err != nil {
		t.Fatalf("run risk kill switch list: %v\nlogs:\n%s", err, output.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sql expectations: %v", err)
	}
	if repo.listCalls != 1 || repo.query.Limit != 5 || repo.query.Source != "live_position_drift" || repo.query.Active == nil || !*repo.query.Active {
		t.Fatalf("list query mismatch: calls=%d query=%#v", repo.listCalls, repo.query)
	}
	artifact := readRiskKillSwitchArtifact(t, artifactPath)
	if artifact.Action != domainrisk.KillSwitchArtifactActionList ||
		artifact.Query == nil ||
		artifact.Query.Active == nil ||
		!*artifact.Query.Active ||
		artifact.Query.Source != "live_position_drift" ||
		artifact.Query.Limit != 5 ||
		len(artifact.Events) != 1 ||
		artifact.Events[0].EventID != "risk_kill_switch_0001" ||
		artifact.State != nil ||
		artifact.Event != nil {
		t.Fatalf("list artifact mismatch: %#v", artifact)
	}
	for _, want := range []string{
		`"msg":"risk kill switch events"`,
		`"msg":"risk kill switch artifact written"`,
		`"events":1`,
		`"active_filter":true`,
		`"msg":"risk kill switch event"`,
		`"event_id":"risk_kill_switch_0001"`,
		`"source":"live_position_drift"`,
	} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("expected logs to contain %s, got\n%s", want, output.String())
		}
	}
}

func TestRunRiskKillSwitchWritesEventsTableDriven(t *testing.T) {
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name       string
		args       []string
		wantActive bool
		wantID     string
		wantLog    string
	}{
		{
			name:       "activate",
			args:       []string{"-action", "activate", "-event-id", "risk_kill_switch_activate_0001", "-reason", "operator emergency stop"},
			wantActive: true,
			wantID:     "risk_kill_switch_activate_0001",
			wantLog:    `"msg":"risk kill switch activated"`,
		},
		{
			name:       "release auto id",
			args:       []string{"-action", "release", "-reason", "operator verified recovery"},
			wantActive: false,
			wantID:     "risk_kill_switch_release_20260803T120000.000000000Z",
			wantLog:    `"msg":"risk kill switch released"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatalf("sqlmock: %v", err)
			}
			defer db.Close()

			repo := &fakeRiskKillSwitchRepository{}
			var output bytes.Buffer
			artifactPath := filepath.Join(t.TempDir(), "artifacts", "risk-kill-switch-write.json")
			args := append(append([]string{}, tt.args...), "-artifact-path", artifactPath)
			err = runRiskKillSwitch(context.Background(), args, riskKillSwitchDependencies{
				loadConfig: func(string) (*config.Config, error) {
					return validRiskKillSwitchConfig(), nil
				},
				openDB: func(context.Context, config.DatabaseConfig) (*sql.DB, error) {
					return db, nil
				},
				newKillSwitch: func(*sql.DB) domainrisk.KillSwitchRepository {
					return repo
				},
				now: func() time.Time {
					return now
				},
				output: &output,
			})
			if err != nil {
				t.Fatalf("run risk kill switch write: %v\nlogs:\n%s", err, output.String())
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatalf("sql expectations: %v", err)
			}
			if repo.appendCalls != 1 || len(repo.appended) != 1 {
				t.Fatalf("append mismatch: calls=%d events=%#v", repo.appendCalls, repo.appended)
			}
			event := repo.appended[0]
			if event.Active != tt.wantActive ||
				event.EventID != tt.wantID ||
				event.Source != "operator" ||
				!event.CreatedAt.Equal(now) {
				t.Fatalf("event mismatch: %#v", event)
			}
			if !strings.Contains(output.String(), tt.wantLog) || !strings.Contains(output.String(), `"event_id":"`+tt.wantID+`"`) {
				t.Fatalf("expected write log, got\n%s", output.String())
			}
			artifact := readRiskKillSwitchArtifact(t, artifactPath)
			wantAction := domainrisk.KillSwitchArtifactActionRelease
			if tt.wantActive {
				wantAction = domainrisk.KillSwitchArtifactActionActivate
			}
			if artifact.Action != wantAction ||
				artifact.Event == nil ||
				artifact.State == nil ||
				artifact.Event.Active != tt.wantActive ||
				artifact.State.Active != tt.wantActive ||
				artifact.Event.EventID != tt.wantID ||
				artifact.State.Reason != event.Reason ||
				artifact.State.Source != event.Source ||
				artifact.State.UpdatedAt == nil ||
				!artifact.State.UpdatedAt.Equal(event.CreatedAt.UTC()) {
				t.Fatalf("write artifact mismatch: %#v", artifact)
			}
		})
	}
}

func TestRunRiskKillSwitchPropagatesRepositoryError(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	repo := &fakeRiskKillSwitchRepository{err: errors.New("postgres unavailable")}
	err = runRiskKillSwitch(context.Background(), []string{"-action", "state"}, riskKillSwitchDependencies{
		loadConfig: func(string) (*config.Config, error) {
			return validRiskKillSwitchConfig(), nil
		},
		openDB: func(context.Context, config.DatabaseConfig) (*sql.DB, error) {
			return db, nil
		},
		newKillSwitch: func(*sql.DB) domainrisk.KillSwitchRepository {
			return repo
		},
		output: &bytes.Buffer{},
	})
	if err == nil || !strings.Contains(err.Error(), "postgres unavailable") {
		t.Fatalf("expected repository error, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sql expectations: %v", err)
	}
}

type fakeRiskKillSwitchRepository struct {
	state        domainrisk.KillSwitchState
	events       []domainrisk.KillSwitchEvent
	query        domainrisk.KillSwitchEventQuery
	appended     []domainrisk.KillSwitchEvent
	currentCalls int
	listCalls    int
	appendCalls  int
	err          error
}

func (r *fakeRiskKillSwitchRepository) AppendKillSwitchEvent(_ context.Context, event domainrisk.KillSwitchEvent) (domainrisk.KillSwitchStats, error) {
	r.appendCalls++
	if r.err != nil {
		return domainrisk.KillSwitchStats{}, r.err
	}
	r.appended = append(r.appended, event)
	return domainrisk.KillSwitchStats{Inserted: 1}, nil
}

func (r *fakeRiskKillSwitchRepository) CurrentKillSwitchState(context.Context) (domainrisk.KillSwitchState, error) {
	r.currentCalls++
	if r.err != nil {
		return domainrisk.KillSwitchState{}, r.err
	}
	return r.state, nil
}

func (r *fakeRiskKillSwitchRepository) ListKillSwitchEvents(_ context.Context, query domainrisk.KillSwitchEventQuery) ([]domainrisk.KillSwitchEvent, error) {
	r.listCalls++
	r.query = query
	if r.err != nil {
		return nil, r.err
	}
	return append([]domainrisk.KillSwitchEvent(nil), r.events...), nil
}

func validRiskKillSwitchConfig() *config.Config {
	return &config.Config{App: config.AppConfig{LogLevel: "info"}}
}

func readRiskKillSwitchArtifact(t *testing.T, path string) domainrisk.KillSwitchArtifact {
	t.Helper()
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read risk kill switch artifact: %v", err)
	}
	if len(payload) == 0 || payload[len(payload)-1] != '\n' {
		t.Fatalf("artifact must end with newline, got %q", string(payload))
	}
	var artifact domainrisk.KillSwitchArtifact
	if err := json.Unmarshal(payload, &artifact); err != nil {
		t.Fatalf("decode risk kill switch artifact: %v\npayload:\n%s", err, string(payload))
	}
	if err := domainrisk.ValidateKillSwitchArtifact(artifact); err != nil {
		t.Fatalf("validate risk kill switch artifact: %v\npayload:\n%s", err, string(payload))
	}
	return artifact
}
