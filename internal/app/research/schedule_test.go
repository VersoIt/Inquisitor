package research_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	appresearch "github.com/VersoIt/Inquisitor/internal/app/research"
	"github.com/VersoIt/Inquisitor/internal/clock"
	domainhypothesis "github.com/VersoIt/Inquisitor/internal/hypothesis"
	domainresearch "github.com/VersoIt/Inquisitor/internal/research"
)

func TestServiceScheduleCreatesPlannedRunFromDraftHypothesis(t *testing.T) {
	plannedAt := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)
	hypothesis := testHypothesisRecord(t, plannedAt.Add(-time.Hour))
	hypotheses := &fakeHypothesisRepository{records: []domainhypothesis.Record{hypothesis}}
	runs := &fakeRunRepository{stats: domainresearch.WriteStats{Inserted: 1}}
	service := appresearch.NewService(
		hypotheses,
		runs,
		appresearch.WithClock(clock.FixedClock{Time: plannedAt}),
		appresearch.WithIDGenerator(fakeIDGenerator{id: "research_fixed_0001"}),
	)

	got, err := service.Schedule(context.Background(), appresearch.ScheduleRequest{
		HypothesisName:    " trend_momentum_draft ",
		HypothesisVersion: "0.1.0",
		WindowStart:       plannedAt.Add(-24 * time.Hour),
		WindowEnd:         plannedAt.Add(-time.Hour),
		Notes:             []string{" table-driven smoke "},
	})
	if err != nil {
		t.Fatalf("schedule research run: %v", err)
	}

	if got.Stats.Inserted != 1 || got.Stats.Updated != 0 {
		t.Fatalf("stats mismatch: %#v", got.Stats)
	}
	if got.Run.RunID != "research_fixed_0001" || got.Run.Status != domainresearch.StatusPlanned {
		t.Fatalf("run identity mismatch: %#v", got.Run)
	}
	if got.Run.HypothesisContentSHA256 != hypothesis.ContentSHA256 {
		t.Fatalf("hypothesis hash mismatch: got %s want %s", got.Run.HypothesisContentSHA256, hypothesis.ContentSHA256)
	}
	if len(got.Run.Symbols) != 2 || got.Run.Symbols[1] != "ETHUSDT" {
		t.Fatalf("symbols were not copied from hypothesis: %#v", got.Run.Symbols)
	}
	if len(runs.runs) != 1 || runs.runs[0].RunID != got.Run.RunID {
		t.Fatalf("stored runs mismatch: %#v", runs.runs)
	}
	if len(hypotheses.queries) != 1 || hypotheses.queries[0].Version != "0.1.0" {
		t.Fatalf("hypothesis query mismatch: %#v", hypotheses.queries)
	}
}

func TestServiceScheduleRejectsInvalidInputsTableDriven(t *testing.T) {
	plannedAt := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)
	validReq := appresearch.ScheduleRequest{
		HypothesisName:    "trend_momentum_draft",
		HypothesisVersion: "0.1.0",
		WindowStart:       plannedAt.Add(-24 * time.Hour),
		WindowEnd:         plannedAt.Add(-time.Hour),
	}
	validHypothesis := testHypothesisRecord(t, plannedAt.Add(-time.Hour))
	repositoryErr := errors.New("research table unavailable")
	idErr := errors.New("entropy machine tired")

	tests := []struct {
		name       string
		service    *appresearch.Service
		req        appresearch.ScheduleRequest
		wantErrSub string
	}{
		{
			name: "missing hypothesis name",
			service: testScheduleService(
				plannedAt,
				&fakeHypothesisRepository{records: []domainhypothesis.Record{validHypothesis}},
				&fakeRunRepository{},
				fakeIDGenerator{id: "research_fixed_0001"},
			),
			req: func() appresearch.ScheduleRequest {
				req := validReq
				req.HypothesisName = ""
				return req
			}(),
			wantErrSub: "hypothesis_name",
		},
		{
			name: "missing hypothesis",
			service: testScheduleService(
				plannedAt,
				&fakeHypothesisRepository{},
				&fakeRunRepository{},
				fakeIDGenerator{id: "research_fixed_0001"},
			),
			req:        validReq,
			wantErrSub: "not found",
		},
		{
			name: "future research window",
			service: testScheduleService(
				plannedAt,
				&fakeHypothesisRepository{records: []domainhypothesis.Record{validHypothesis}},
				&fakeRunRepository{},
				fakeIDGenerator{id: "research_fixed_0001"},
			),
			req: func() appresearch.ScheduleRequest {
				req := validReq
				req.WindowEnd = plannedAt.Add(time.Minute)
				return req
			}(),
			wantErrSub: "planned_at",
		},
		{
			name: "id generator error",
			service: testScheduleService(
				plannedAt,
				&fakeHypothesisRepository{records: []domainhypothesis.Record{validHypothesis}},
				&fakeRunRepository{},
				fakeIDGenerator{err: idErr},
			),
			req:        validReq,
			wantErrSub: idErr.Error(),
		},
		{
			name: "run repository error",
			service: testScheduleService(
				plannedAt,
				&fakeHypothesisRepository{records: []domainhypothesis.Record{validHypothesis}},
				&fakeRunRepository{err: repositoryErr},
				fakeIDGenerator{id: "research_fixed_0001"},
			),
			req:        validReq,
			wantErrSub: repositoryErr.Error(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.service.Schedule(context.Background(), tt.req)
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
		})
	}
}

func TestServiceScheduleRequiresDependenciesTableDriven(t *testing.T) {
	plannedAt := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)
	req := appresearch.ScheduleRequest{
		HypothesisName:    "trend_momentum_draft",
		HypothesisVersion: "0.1.0",
		WindowStart:       plannedAt.Add(-24 * time.Hour),
		WindowEnd:         plannedAt.Add(-time.Hour),
	}

	tests := []struct {
		name       string
		service    *appresearch.Service
		wantErrSub string
	}{
		{
			name:       "missing hypothesis repository",
			service:    appresearch.NewService(nil, &fakeRunRepository{}),
			wantErrSub: "hypothesis repository",
		},
		{
			name:       "missing run repository",
			service:    appresearch.NewService(&fakeHypothesisRepository{}, nil),
			wantErrSub: "research run repository",
		},
		{
			name: "missing clock",
			service: appresearch.NewService(
				&fakeHypothesisRepository{},
				&fakeRunRepository{},
				appresearch.WithClock(nil),
			),
			wantErrSub: "clock",
		},
		{
			name: "missing id generator",
			service: appresearch.NewService(
				&fakeHypothesisRepository{},
				&fakeRunRepository{},
				appresearch.WithIDGenerator(nil),
			),
			wantErrSub: "id generator",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.service.Schedule(context.Background(), req)
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
		})
	}
}

func testScheduleService(plannedAt time.Time, hypotheses *fakeHypothesisRepository, runs *fakeRunRepository, generator fakeIDGenerator) *appresearch.Service {
	return appresearch.NewService(
		hypotheses,
		runs,
		appresearch.WithClock(clock.FixedClock{Time: plannedAt}),
		appresearch.WithIDGenerator(generator),
	)
}

type fakeHypothesisRepository struct {
	records []domainhypothesis.Record
	queries []domainhypothesis.Query
	err     error
}

func (r *fakeHypothesisRepository) UpsertHypotheses(context.Context, []domainhypothesis.Record) (domainhypothesis.WriteStats, error) {
	return domainhypothesis.WriteStats{}, nil
}

func (r *fakeHypothesisRepository) ListHypotheses(_ context.Context, query domainhypothesis.Query) ([]domainhypothesis.Record, error) {
	r.queries = append(r.queries, query)
	if r.err != nil {
		return nil, r.err
	}
	var records []domainhypothesis.Record
	for _, record := range r.records {
		if query.Name != "" && record.Name != query.Name {
			continue
		}
		if query.Version != "" && record.Version != query.Version {
			continue
		}
		if query.Status != "" && record.Status != query.Status {
			continue
		}
		records = append(records, record)
	}
	return records, nil
}

type fakeRunRepository struct {
	runs    []domainresearch.Run
	stats   domainresearch.WriteStats
	err     error
	queries []domainresearch.Query
}

func (r *fakeRunRepository) UpsertRuns(_ context.Context, runs []domainresearch.Run) (domainresearch.WriteStats, error) {
	r.runs = append(r.runs, runs...)
	if r.err != nil {
		return domainresearch.WriteStats{}, r.err
	}
	return r.stats, nil
}

func (r *fakeRunRepository) ListRuns(_ context.Context, query domainresearch.Query) ([]domainresearch.Run, error) {
	r.queries = append(r.queries, query)
	if r.err != nil {
		return nil, r.err
	}
	return append([]domainresearch.Run(nil), r.runs...), nil
}

type fakeResultRecorder struct {
	run     domainresearch.Run
	result  domainresearch.Result
	stats   domainresearch.RecordResultStats
	err     error
	results []domainresearch.Result
}

func (r *fakeResultRecorder) RecordResult(_ context.Context, run domainresearch.Run, result domainresearch.Result) (domainresearch.RecordResultStats, error) {
	r.run = run
	r.result = result
	if r.err != nil {
		return domainresearch.RecordResultStats{}, r.err
	}
	return r.stats, nil
}

func (r *fakeResultRecorder) ListResults(context.Context, domainresearch.ResultQuery) ([]domainresearch.Result, error) {
	return append([]domainresearch.Result(nil), r.results...), nil
}

type fakeIDGenerator struct {
	id  string
	err error
}

func (g fakeIDGenerator) NewID() (string, error) {
	if g.err != nil {
		return "", g.err
	}
	if g.id == "" {
		return "", fmt.Errorf("missing fake id")
	}
	return g.id, nil
}

func testHypothesisRecord(t *testing.T, importedAt time.Time) domainhypothesis.Record {
	t.Helper()

	raw := []byte(`name: trend_momentum_draft
version: "0.1.0"
status: DRAFT
description: Draft research hypothesis for directional momentum with regime gating.
thesis: Strong directional markets may persist after feature confirmation.
market:
  exchange: bybit
  category: linear
  symbols:
    - BTCUSDT
    - ETHUSDT
  intervals:
    - "1"
    - "5"
regime:
  allowed:
    - TREND_UP
  blocked:
    - NO_TRADE
direction: LONG
signals:
  - name: ma_alignment
    description: Fast trend average should stay above slow average.
    feature: trend.ma20
    operator: ">"
    value: trend.ma50
risk:
  max_risk_per_trade_pct: 0.25
  min_confidence: 70
  require_stop_loss: true
validation:
  min_trades: 150
  require_out_of_sample: true
  require_walk_forward: true
  require_regime_analysis: true
costs:
  include_fees: true
  include_spread: true
  include_slippage: true
tags:
  - research
`)
	spec, err := domainhypothesis.ParseYAML(raw)
	if err != nil {
		t.Fatalf("parse test hypothesis: %v", err)
	}
	record, err := domainhypothesis.NewRecord(spec, "hypotheses/test.yaml", raw, importedAt)
	if err != nil {
		t.Fatalf("new test hypothesis record: %v", err)
	}
	return record
}
