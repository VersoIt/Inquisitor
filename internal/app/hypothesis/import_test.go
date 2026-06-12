package hypothesis_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	apphypothesis "github.com/VersoIt/Inquisitor/internal/app/hypothesis"
	"github.com/VersoIt/Inquisitor/internal/clock"
	domainhypothesis "github.com/VersoIt/Inquisitor/internal/hypothesis"
)

func TestServiceValidateImportReadsAndValidatesDraft(t *testing.T) {
	reader := &fakeFileReader{files: map[string][]byte{
		"hypotheses/test.yaml": []byte(validImportYAML()),
	}}
	service := apphypothesis.NewService(apphypothesis.WithFileReader(reader))

	got, err := service.ValidateImport(context.Background(), apphypothesis.ImportRequest{Path: " hypotheses/test.yaml "})
	if err != nil {
		t.Fatalf("validate import: %v", err)
	}

	if got.Path != "hypotheses/test.yaml" {
		t.Fatalf("path mismatch: got %q", got.Path)
	}
	if got.Hypothesis.Name != "range_reversion_draft" {
		t.Fatalf("name mismatch: got %q", got.Hypothesis.Name)
	}
	if got.ContentSHA256 == "" {
		t.Fatal("content hash is required")
	}
	if len(reader.paths) != 1 || reader.paths[0] != "hypotheses/test.yaml" {
		t.Fatalf("reader paths mismatch: %#v", reader.paths)
	}
}

func TestServiceImportAndStorePersistsValidatedRecord(t *testing.T) {
	importedAt := time.Date(2026, 6, 12, 10, 0, 0, 0, time.UTC)
	reader := &fakeFileReader{files: map[string][]byte{
		"hypotheses/test.yaml": []byte(validImportYAML()),
	}}
	repository := &fakeRepository{stats: domainhypothesis.WriteStats{Inserted: 1}}
	service := apphypothesis.NewService(
		apphypothesis.WithFileReader(reader),
		apphypothesis.WithRepository(repository),
		apphypothesis.WithClock(clock.FixedClock{Time: importedAt}),
	)

	got, err := service.ImportAndStore(context.Background(), apphypothesis.ImportRequest{Path: "hypotheses/test.yaml"})
	if err != nil {
		t.Fatalf("import and store: %v", err)
	}

	if got.Stats.Inserted != 1 || got.Stats.Updated != 0 {
		t.Fatalf("stats mismatch: %#v", got.Stats)
	}
	if len(repository.records) != 1 {
		t.Fatalf("stored records mismatch: got %d want 1", len(repository.records))
	}
	stored := repository.records[0]
	if stored.Name != "range_reversion_draft" || stored.Version != "0.1.0" {
		t.Fatalf("stored identity mismatch: %#v", stored)
	}
	if !stored.ImportedAt.Equal(importedAt) {
		t.Fatalf("imported_at mismatch: got %s want %s", stored.ImportedAt, importedAt)
	}
	if got.ContentSHA256 != stored.ContentSHA256 {
		t.Fatalf("result hash mismatch: got %s want %s", got.ContentSHA256, stored.ContentSHA256)
	}
}

func TestServiceValidateImportRejectsInvalidInputsTableDriven(t *testing.T) {
	readErr := errors.New("disk is taking a dramatic nap")
	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()

	tests := []struct {
		name       string
		service    *apphypothesis.Service
		ctx        context.Context
		req        apphypothesis.ImportRequest
		wantErrSub string
	}{
		{
			name:       "missing path",
			service:    apphypothesis.NewService(apphypothesis.WithFileReader(&fakeFileReader{})),
			ctx:        context.Background(),
			req:        apphypothesis.ImportRequest{Path: " "},
			wantErrSub: "path is required",
		},
		{
			name:       "missing reader",
			service:    apphypothesis.NewService(apphypothesis.WithFileReader(nil)),
			ctx:        context.Background(),
			req:        apphypothesis.ImportRequest{Path: "hypothesis.yaml"},
			wantErrSub: "file reader",
		},
		{
			name:       "context canceled",
			service:    apphypothesis.NewService(apphypothesis.WithFileReader(&fakeFileReader{})),
			ctx:        canceledCtx,
			req:        apphypothesis.ImportRequest{Path: "hypothesis.yaml"},
			wantErrSub: context.Canceled.Error(),
		},
		{
			name:       "reader error",
			service:    apphypothesis.NewService(apphypothesis.WithFileReader(&fakeFileReader{err: readErr})),
			ctx:        context.Background(),
			req:        apphypothesis.ImportRequest{Path: "missing.yaml"},
			wantErrSub: "disk is taking a dramatic nap",
		},
		{
			name: "domain validation error",
			service: apphypothesis.NewService(apphypothesis.WithFileReader(&fakeFileReader{files: map[string][]byte{
				"bad.yaml": []byte(strings.Replace(validImportYAML(), "status: DRAFT", "status: LIVE_ENABLED", 1)),
			}})),
			ctx:        context.Background(),
			req:        apphypothesis.ImportRequest{Path: "bad.yaml"},
			wantErrSub: "status must be DRAFT",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.service.ValidateImport(tt.ctx, tt.req)
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
		})
	}
}

func TestServiceValidateImportPreservesValidationErrorType(t *testing.T) {
	service := apphypothesis.NewService(apphypothesis.WithFileReader(&fakeFileReader{files: map[string][]byte{
		"bad.yaml": []byte(strings.Replace(validImportYAML(), "status: DRAFT", "status: LIVE_ENABLED", 1)),
	}}))

	_, err := service.ValidateImport(context.Background(), apphypothesis.ImportRequest{Path: "bad.yaml"})
	if err == nil {
		t.Fatal("expected error")
	}

	var validationErr domainhypothesis.ValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("expected wrapped ValidationError, got %T: %v", err, err)
	}
}

func TestServiceImportAndStoreRejectsInvalidDependenciesTableDriven(t *testing.T) {
	tests := []struct {
		name       string
		service    *apphypothesis.Service
		wantErrSub string
	}{
		{
			name: "missing repository",
			service: apphypothesis.NewService(
				apphypothesis.WithFileReader(&fakeFileReader{files: map[string][]byte{"hypotheses/test.yaml": []byte(validImportYAML())}}),
				apphypothesis.WithClock(clock.FixedClock{Time: time.Date(2026, 6, 12, 10, 0, 0, 0, time.UTC)}),
			),
			wantErrSub: "repository",
		},
		{
			name: "missing clock",
			service: apphypothesis.NewService(
				apphypothesis.WithFileReader(&fakeFileReader{files: map[string][]byte{"hypotheses/test.yaml": []byte(validImportYAML())}}),
				apphypothesis.WithRepository(&fakeRepository{}),
				apphypothesis.WithClock(nil),
			),
			wantErrSub: "clock",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.service.ImportAndStore(context.Background(), apphypothesis.ImportRequest{Path: "hypotheses/test.yaml"})
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
		})
	}
}

func TestServiceImportAndStoreWrapsRepositoryError(t *testing.T) {
	repositoryErr := errors.New("postgres went for coffee")
	service := apphypothesis.NewService(
		apphypothesis.WithFileReader(&fakeFileReader{files: map[string][]byte{"hypotheses/test.yaml": []byte(validImportYAML())}}),
		apphypothesis.WithRepository(&fakeRepository{err: repositoryErr}),
		apphypothesis.WithClock(clock.FixedClock{Time: time.Date(2026, 6, 12, 10, 0, 0, 0, time.UTC)}),
	)

	_, err := service.ImportAndStore(context.Background(), apphypothesis.ImportRequest{Path: "hypotheses/test.yaml"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "range_reversion_draft") || !strings.Contains(err.Error(), repositoryErr.Error()) {
		t.Fatalf("expected contextual repository error, got %v", err)
	}
}

type fakeFileReader struct {
	files map[string][]byte
	err   error
	paths []string
}

func (r *fakeFileReader) ReadFile(path string) ([]byte, error) {
	r.paths = append(r.paths, path)
	if r.err != nil {
		return nil, r.err
	}
	raw, exists := r.files[path]
	if !exists {
		return nil, fmt.Errorf("file %q not found", path)
	}
	return append([]byte(nil), raw...), nil
}

type fakeRepository struct {
	records []domainhypothesis.Record
	stats   domainhypothesis.WriteStats
	err     error
}

func (r *fakeRepository) UpsertHypotheses(_ context.Context, records []domainhypothesis.Record) (domainhypothesis.WriteStats, error) {
	r.records = append(r.records, records...)
	if r.err != nil {
		return domainhypothesis.WriteStats{}, r.err
	}
	return r.stats, nil
}

func (r *fakeRepository) ListHypotheses(context.Context, domainhypothesis.Query) ([]domainhypothesis.Record, error) {
	return append([]domainhypothesis.Record(nil), r.records...), nil
}

func validImportYAML() string {
	return `name: range_reversion_draft
version: "0.1.0"
status: DRAFT
description: Draft research hypothesis for bounded range behaviour.
thesis: Range regimes may revert after volatility and spread filters confirm clean data.
market:
  exchange: bybit
  category: linear
  symbols:
    - BTCUSDT
  intervals:
    - "5"
regime:
  allowed:
    - RANGE
  blocked:
    - NO_TRADE
    - CHAOS
direction: BOTH
signals:
  - name: compression_filter
    description: Volatility compression should be present before range research.
    feature: volatility.compression
    operator: "<="
    value: 1
risk:
  max_risk_per_trade_pct: 0.25
  min_confidence: 70
  require_stop_loss: true
validation:
  min_trades: 200
  require_out_of_sample: true
  require_walk_forward: true
  require_regime_analysis: true
costs:
  include_fees: true
  include_spread: true
  include_slippage: true
tags:
  - phase4
`
}
