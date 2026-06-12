package hypothesis_test

import (
	"strings"
	"testing"
	"time"

	"github.com/VersoIt/Inquisitor/internal/hypothesis"
)

func TestNewRecordBuildsValidatedPersistenceRecord(t *testing.T) {
	importedAt := time.Date(2026, 6, 12, 10, 0, 0, 0, time.UTC)

	spec, err := hypothesis.ParseYAML([]byte(validHypothesisYAML()))
	if err != nil {
		t.Fatalf("parse valid hypothesis: %v", err)
	}
	got, err := hypothesis.NewRecord(spec, "hypotheses/test.yaml", []byte(validHypothesisYAML()), importedAt)
	if err != nil {
		t.Fatalf("new record: %v", err)
	}

	if got.Name != spec.Name || got.Version != spec.Version || got.Status != spec.Status {
		t.Fatalf("record identity mismatch: %#v", got)
	}
	if got.ContentSHA256 == "" {
		t.Fatal("content sha256 is required")
	}
	if !got.ImportedAt.Equal(importedAt) {
		t.Fatalf("imported_at mismatch: got %s want %s", got.ImportedAt, importedAt)
	}
}

func TestValidateRecordRejectsInvalidRecordsTableDriven(t *testing.T) {
	importedAt := time.Date(2026, 6, 12, 10, 0, 0, 0, time.UTC)
	spec, err := hypothesis.ParseYAML([]byte(validHypothesisYAML()))
	if err != nil {
		t.Fatalf("parse valid hypothesis: %v", err)
	}
	valid, err := hypothesis.NewRecord(spec, "hypotheses/test.yaml", []byte(validHypothesisYAML()), importedAt)
	if err != nil {
		t.Fatalf("new valid record: %v", err)
	}

	tests := []struct {
		name       string
		mutate     func(*hypothesis.Record)
		wantErrSub string
	}{
		{
			name: "missing source path",
			mutate: func(record *hypothesis.Record) {
				record.SourcePath = ""
			},
			wantErrSub: "source_path",
		},
		{
			name: "status mismatch",
			mutate: func(record *hypothesis.Record) {
				record.Status = hypothesis.Status("LIVE_ENABLED")
			},
			wantErrSub: "status must be DRAFT",
		},
		{
			name: "name mismatch",
			mutate: func(record *hypothesis.Record) {
				record.Name = "other"
			},
			wantErrSub: "name must match spec.name",
		},
		{
			name: "hash malformed",
			mutate: func(record *hypothesis.Record) {
				record.ContentSHA256 = "not-a-hash"
			},
			wantErrSub: "lowercase sha256",
		},
		{
			name: "hash does not match raw yaml",
			mutate: func(record *hypothesis.Record) {
				record.RawYAML += "\n# changed\n"
			},
			wantErrSub: "must match raw_yaml",
		},
		{
			name: "missing imported_at",
			mutate: func(record *hypothesis.Record) {
				record.ImportedAt = time.Time{}
			},
			wantErrSub: "imported_at",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			record := valid
			tt.mutate(&record)

			err := hypothesis.ValidateRecord(record)
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
		})
	}
}

func TestValidateQueryRejectsInvalidInputsTableDriven(t *testing.T) {
	tests := []struct {
		name       string
		query      hypothesis.Query
		wantErrSub string
	}{
		{
			name:       "live status unsupported",
			query:      hypothesis.Query{Status: "LIVE_ENABLED"},
			wantErrSub: "status",
		},
		{
			name:       "negative limit",
			query:      hypothesis.Query{Limit: -1},
			wantErrSub: "limit",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := hypothesis.ValidateQuery(tt.query)
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
		})
	}
}
