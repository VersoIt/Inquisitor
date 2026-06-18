package paper_test

import (
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/VersoIt/Inquisitor/internal/paper"
)

func TestValidationLifecycleTransitionsAtExactBoundaries(t *testing.T) {
	plannedAt := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	planned := lifecycleRecord(plannedAt)

	running, err := paper.StartValidation(planned, plannedAt.Add(time.Hour))
	if err != nil {
		t.Fatalf("start validation: %v", err)
	}
	if running.Status != paper.ValidationStatusRunning || !running.StartedAt.Equal(plannedAt.Add(time.Hour)) {
		t.Fatalf("running state mismatch: %#v", running)
	}

	completedAt := running.StartedAt.AddDate(0, 0, running.MinimumDays)
	completed, err := paper.CompleteValidation(running, completedAt)
	if err != nil {
		t.Fatalf("complete validation: %v", err)
	}
	if completed.Status != paper.ValidationStatusCompleted || !completed.CompletedAt.Equal(completedAt) {
		t.Fatalf("completed state mismatch: %#v", completed)
	}
	if completed.StatusReason != "minimum_validation_period_completed" {
		t.Fatalf("completion reason mismatch: %q", completed.StatusReason)
	}
}

func TestValidationLifecycleRejectsInvalidTransitionsTableDriven(t *testing.T) {
	plannedAt := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	planned := lifecycleRecord(plannedAt)
	running, err := paper.StartValidation(planned, plannedAt.Add(time.Hour))
	if err != nil {
		t.Fatalf("start fixture: %v", err)
	}
	completed, err := paper.CompleteValidation(running, running.StartedAt.AddDate(0, 0, running.MinimumDays))
	if err != nil {
		t.Fatalf("complete fixture: %v", err)
	}

	tests := []struct {
		name       string
		run        func() error
		wantErrSub string
	}{
		{"start running", func() error { _, err := paper.StartValidation(running, plannedAt.Add(2*time.Hour)); return err }, "PLANNED"},
		{"start before planned", func() error { _, err := paper.StartValidation(planned, plannedAt.Add(-time.Second)); return err }, "before planned"},
		{"complete planned", func() error { _, err := paper.CompleteValidation(planned, plannedAt.AddDate(0, 0, 30)); return err }, "RUNNING"},
		{"complete one nanosecond early", func() error {
			_, err := paper.CompleteValidation(running, running.StartedAt.AddDate(0, 0, running.MinimumDays).Add(-time.Nanosecond))
			return err
		}, "minimum validation period"},
		{"cancel completed", func() error {
			_, err := paper.CancelValidation(completed, completed.CompletedAt.Add(time.Hour), "late")
			return err
		}, "PLANNED or RUNNING"},
		{"cancel without reason", func() error { _, err := paper.CancelValidation(planned, plannedAt.Add(time.Hour), " "); return err }, "reason"},
		{"cancel before start", func() error {
			_, err := paper.CancelValidation(running, running.StartedAt.Add(-time.Second), "operator stop")
			return err
		}, "precedes"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.run()
			if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
		})
	}
}

func TestCancelValidationSupportsPlannedAndRunningTableDriven(t *testing.T) {
	plannedAt := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	planned := lifecycleRecord(plannedAt)
	running, err := paper.StartValidation(planned, plannedAt.Add(time.Hour))
	if err != nil {
		t.Fatalf("start fixture: %v", err)
	}
	for _, record := range []paper.ValidationRecord{planned, running} {
		t.Run(string(record.Status), func(t *testing.T) {
			got, err := paper.CancelValidation(record, plannedAt.Add(2*time.Hour), " operator requested stop ")
			if err != nil {
				t.Fatalf("cancel validation: %v", err)
			}
			if got.Status != paper.ValidationStatusCancelled || got.StatusReason != "operator requested stop" {
				t.Fatalf("cancelled state mismatch: %#v", got)
			}
		})
	}
}

func lifecycleRecord(plannedAt time.Time) paper.ValidationRecord {
	return paper.ValidationRecord{
		ValidationID:   "paper_validation_lifecycle_0001",
		RunID:          "research_lifecycle_0001",
		Status:         paper.ValidationStatusPlanned,
		Mode:           "paper",
		InitialBalance: decimal.RequireFromString("1000"),
		MinimumDays:    30,
		Reasons:        []string{"paper_validation_allowed"},
		PlannedAt:      plannedAt,
	}
}
