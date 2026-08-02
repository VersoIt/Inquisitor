package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"

	"github.com/VersoIt/Inquisitor/internal/config"
	domainlive "github.com/VersoIt/Inquisitor/internal/live"
)

func TestParseLiveLoopAuditStatusTableDriven(t *testing.T) {
	tests := []struct {
		name       string
		value      string
		want       domainlive.LiveLoopRunStatus
		wantErrSub string
	}{
		{name: "empty", value: "", want: ""},
		{name: "completed lower", value: " completed ", want: domainlive.LiveLoopRunStatusCompleted},
		{name: "running", value: "RUNNING", want: domainlive.LiveLoopRunStatusRunning},
		{name: "failed", value: "FAILED", want: domainlive.LiveLoopRunStatusFailed},
		{name: "unknown", value: "BROKEN", wantErrSub: "status"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseLiveLoopAuditStatus(tt.value)
			if tt.wantErrSub != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("parse status: %v", err)
			}
			if got != tt.want {
				t.Fatalf("status mismatch: got %s want %s", got, tt.want)
			}
		})
	}
}

func TestRunLiveLoopAuditRejectsUnsafeFlagsBeforeSideEffects(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantErrSub string
	}{
		{name: "bad status", args: []string{"-status", "BROKEN"}, wantErrSub: "status"},
		{name: "limit above max", args: []string{"-limit", "101"}, wantErrSub: "limit"},
		{name: "untrimmed run id", args: []string{"-run-id", " live_loop_audit_cli_0001 "}, wantErrSub: "run_id"},
		{name: "untrimmed artifact path", args: []string{"-artifact-path", " artifacts/live-loop-audit.json "}, wantErrSub: "artifact-path"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var loaded bool
			var opened bool
			err := runLiveLoopAudit(context.Background(), tt.args, liveLoopAuditDependencies{
				loadConfig: func(string) (*config.Config, error) {
					loaded = true
					return &config.Config{}, nil
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

func TestRunLiveLoopAuditLogsReport(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	reader := &fakeLiveLoopAuditCommandReader{
		runs: []domainlive.LiveLoopRunAudit{validLiveLoopAuditCommandRun()},
	}
	artifactPath := filepath.Join(t.TempDir(), "artifacts", "live-loop-audit.json")
	var output bytes.Buffer
	err = runLiveLoopAudit(context.Background(), []string{
		"-status", "completed",
		"-limit", "5",
		"-artifact-path", artifactPath,
	}, liveLoopAuditDependencies{
		loadConfig: func(string) (*config.Config, error) {
			return &config.Config{App: config.AppConfig{LogLevel: "info"}}, nil
		},
		openDB: func(context.Context, config.DatabaseConfig) (*sql.DB, error) {
			return db, nil
		},
		newAuditReader: func(*sql.DB) domainlive.LiveLoopAuditReader {
			return reader
		},
		now: func() time.Time {
			return time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
		},
		output: &output,
	})
	if err != nil {
		t.Fatalf("run live-loop audit: %v\nlogs:\n%s", err, output.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sql expectations: %v", err)
	}
	if reader.calls != 1 || reader.query.Status != domainlive.LiveLoopRunStatusCompleted || reader.query.Limit != 5 || !reader.query.IncludeIterations {
		t.Fatalf("reader query mismatch: calls=%d query=%#v", reader.calls, reader.query)
	}
	logs := output.String()
	for _, want := range []string{
		`"msg":"live-loop audit report"`,
		`"runs":1`,
		`"completed":1`,
		`"review_status":"CLEAR"`,
		`"operator_action_required":false`,
		`"status_filter":"COMPLETED"`,
		`"msg":"live-loop audit run"`,
		`"run_id":"live_loop_audit_cli_0001"`,
		`"stop_reason":"ITERATION_REQUESTED"`,
		`"msg":"live-loop audit iteration"`,
		`"decision_id":"risk_decision_live_audit_cli_0001"`,
		`"exchange_submitted":true`,
		`"msg":"live-loop audit artifact written"`,
		`"review_status":"CLEAR"`,
	} {
		if !strings.Contains(logs, want) {
			t.Fatalf("expected logs to contain %s, got\n%s", want, logs)
		}
	}

	artifact := readLiveLoopAuditArtifact(t, artifactPath)
	if artifact.SchemaVersion != domainlive.LiveLoopAuditArtifactSchemaVersion ||
		artifact.ConfigPath != "configs/config.example.yaml" ||
		artifact.Query.Status != domainlive.LiveLoopRunStatusCompleted ||
		artifact.Query.Limit != 5 ||
		!artifact.Query.IncludeIterations ||
		artifact.Summary.ReviewStatus != domainlive.LiveLoopAuditReviewStatusClear ||
		artifact.Summary.OperatorActionRequired ||
		len(artifact.Runs) != 1 ||
		len(artifact.Runs[0].Iterations) != 1 {
		t.Fatalf("audit artifact mismatch: %#v", artifact)
	}
}

type fakeLiveLoopAuditCommandReader struct {
	query domainlive.LiveLoopAuditQuery
	runs  []domainlive.LiveLoopRunAudit
	calls int
}

func (r *fakeLiveLoopAuditCommandReader) ListLiveLoopRunAudits(_ context.Context, query domainlive.LiveLoopAuditQuery) ([]domainlive.LiveLoopRunAudit, error) {
	r.calls++
	r.query = query
	return append([]domainlive.LiveLoopRunAudit(nil), r.runs...), nil
}

func validLiveLoopAuditCommandRun() domainlive.LiveLoopRunAudit {
	startedAt := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	return domainlive.LiveLoopRunAudit{
		RunID:                 "live_loop_audit_cli_0001",
		StartedAt:             startedAt,
		MaxIterations:         1,
		MaxRuntime:            15 * time.Second,
		IterationTimeout:      10 * time.Second,
		Status:                domainlive.LiveLoopRunStatusCompleted,
		FinishedAt:            startedAt.Add(2 * time.Second),
		PreflightChecked:      true,
		PreflightReady:        true,
		IterationsAttempted:   1,
		IterationsSucceeded:   1,
		StopReason:            "ITERATION_REQUESTED",
		StopDetails:           "live_order_submitted",
		CompletedWithinBounds: true,
		Iterations: []domainlive.LiveLoopIterationAudit{{
			RunID:             "live_loop_audit_cli_0001",
			RunStartedAt:      startedAt,
			Iteration:         1,
			Action:            domainlive.LiveLoopAuditIterationActionSubmitted,
			RequestStop:       true,
			Reason:            "live_order_submitted",
			DecisionID:        "risk_decision_live_audit_cli_0001",
			SubmissionID:      "live_submission_audit_cli_0001",
			ClientOrderID:     "live_client_audit_cli_0001",
			ExchangeSubmitted: true,
			StartedAt:         startedAt.Add(time.Second),
			FinishedAt:        startedAt.Add(2 * time.Second),
		}},
	}
}

func readLiveLoopAuditArtifact(t *testing.T, path string) domainlive.LiveLoopAuditArtifact {
	t.Helper()

	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read audit artifact: %v", err)
	}
	var artifact domainlive.LiveLoopAuditArtifact
	if err := json.Unmarshal(payload, &artifact); err != nil {
		t.Fatalf("decode audit artifact: %v", err)
	}
	if err := domainlive.ValidateLiveLoopAuditArtifact(artifact); err != nil {
		t.Fatalf("validate audit artifact: %v", err)
	}
	return artifact
}
