package live_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	applive "github.com/VersoIt/Inquisitor/internal/app/live"
	"github.com/VersoIt/Inquisitor/internal/clock"
	domainlive "github.com/VersoIt/Inquisitor/internal/live"
)

func TestServiceBuildLivePositionDriftReportTableDriven(t *testing.T) {
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	btcQuery := domainlive.PositionSnapshotQuery{Exchange: "bybit", Category: "linear", Symbol: "BTCUSDT"}
	ethQuery := domainlive.PositionSnapshotQuery{Exchange: "bybit", Category: "linear", Symbol: "ETHUSDT"}
	btcBaseline := validLiveStartupFlatPositionSnapshot(t, btcQuery, now.Add(-time.Minute))
	btcCurrent := validLiveStartupFlatPositionSnapshot(t, btcQuery, now.Add(-time.Second))
	ethBaseline := validLiveStartupFlatPositionSnapshot(t, ethQuery, now.Add(-time.Minute))
	ethCurrent := validLiveStartupOpenPositionSnapshot(t, ethQuery, now.Add(-time.Second))

	tests := []struct {
		name       string
		queries    []domainlive.PositionSnapshotQuery
		current    map[string]domainlive.PositionSnapshot
		baselines  map[string]domainlive.PositionSnapshot
		missing    map[string]bool
		req        applive.LivePositionDriftReportRequest
		wantStatus domainlive.LiveOpsStatus
		wantCheck  string
	}{
		{
			name:       "clear when current snapshot matches fresh DB baseline",
			queries:    []domainlive.PositionSnapshotQuery{btcQuery},
			current:    map[string]domainlive.PositionSnapshot{appPositionDriftKey(btcQuery): btcCurrent},
			baselines:  map[string]domainlive.PositionSnapshot{appPositionDriftKey(btcQuery): btcBaseline},
			wantStatus: domainlive.LiveOpsStatusClear,
		},
		{
			name:       "missing DB baseline needs attention",
			queries:    []domainlive.PositionSnapshotQuery{btcQuery},
			current:    map[string]domainlive.PositionSnapshot{appPositionDriftKey(btcQuery): btcCurrent},
			missing:    map[string]bool{appPositionDriftKey(btcQuery): true},
			wantStatus: domainlive.LiveOpsStatusAttention,
			wantCheck:  "db_position_baseline",
		},
		{
			name:    "stale DB baseline needs attention",
			queries: []domainlive.PositionSnapshotQuery{btcQuery},
			current: map[string]domainlive.PositionSnapshot{appPositionDriftKey(btcQuery): btcCurrent},
			baselines: map[string]domainlive.PositionSnapshot{appPositionDriftKey(btcQuery): mutateAppPositionDriftSnapshot(btcBaseline, func(s *domainlive.PositionSnapshot) {
				s.ObservedAt = now.Add(-time.Hour)
			})},
			req: applive.LivePositionDriftReportRequest{
				BaselineMaxAge: 10 * time.Minute,
			},
			wantStatus: domainlive.LiveOpsStatusAttention,
			wantCheck:  "db_position_baseline",
		},
		{
			name:       "unexpected open position blocks",
			queries:    []domainlive.PositionSnapshotQuery{btcQuery},
			current:    map[string]domainlive.PositionSnapshot{appPositionDriftKey(btcQuery): validLiveStartupOpenPositionSnapshot(t, btcQuery, now.Add(-time.Second))},
			baselines:  map[string]domainlive.PositionSnapshot{appPositionDriftKey(btcQuery): btcBaseline},
			wantStatus: domainlive.LiveOpsStatusBlocked,
			wantCheck:  "position_exposure_drift",
		},
		{
			name:    "current stale snapshot blocks",
			queries: []domainlive.PositionSnapshotQuery{btcQuery},
			current: map[string]domainlive.PositionSnapshot{appPositionDriftKey(btcQuery): mutateAppPositionDriftSnapshot(btcCurrent, func(s *domainlive.PositionSnapshot) {
				s.ObservedAt = now.Add(-10 * time.Second)
			})},
			baselines: map[string]domainlive.PositionSnapshot{appPositionDriftKey(btcQuery): btcBaseline},
			req: applive.LivePositionDriftReportRequest{
				CurrentMaxAge: 5 * time.Second,
			},
			wantStatus: domainlive.LiveOpsStatusBlocked,
			wantCheck:  "current_position_snapshot",
		},
		{
			name: "aggregates multiple symbols and blocks on any drift",
			queries: []domainlive.PositionSnapshotQuery{
				btcQuery,
				ethQuery,
			},
			current: map[string]domainlive.PositionSnapshot{
				appPositionDriftKey(btcQuery): btcCurrent,
				appPositionDriftKey(ethQuery): ethCurrent,
			},
			baselines: map[string]domainlive.PositionSnapshot{
				appPositionDriftKey(btcQuery): btcBaseline,
				appPositionDriftKey(ethQuery): ethBaseline,
			},
			wantStatus: domainlive.LiveOpsStatusBlocked,
			wantCheck:  "position_exposure_drift",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reader := &fakeLivePositionDriftSnapshotReader{snapshots: tt.current}
			history := &fakeLivePositionDriftHistoryReader{snapshots: tt.baselines, missing: tt.missing}
			service := applive.NewService(
				applive.WithClock(clock.FixedClock{Time: now}),
				applive.WithPositionSnapshotReader(reader),
				applive.WithPositionSnapshotHistoryReader(history),
			)
			req := tt.req
			req.Queries = tt.queries

			got, err := service.BuildLivePositionDriftReport(context.Background(), req)
			if err != nil {
				t.Fatalf("build drift report: %v", err)
			}
			if got.Status != tt.wantStatus {
				t.Fatalf("status mismatch: got %s want %s checks=%#v", got.Status, tt.wantStatus, got.Checks)
			}
			if len(got.Comparisons) != len(tt.queries) || reader.calls != len(tt.queries) || history.calls != len(tt.queries) {
				t.Fatalf("query count mismatch: comparisons=%d reader=%d history=%d queries=%d", len(got.Comparisons), reader.calls, history.calls, len(tt.queries))
			}
			if got.Summary.Total != len(got.Checks) {
				t.Fatalf("summary mismatch: %#v checks=%#v", got.Summary, got.Checks)
			}
			if tt.wantCheck != "" && !appLiveOpsHasCheck(got.Checks, tt.wantCheck) {
				t.Fatalf("expected check %q, got %#v", tt.wantCheck, got.Checks)
			}
		})
	}
}

func TestServiceBuildLivePositionDriftReportRejectsUnsafeInputsTableDriven(t *testing.T) {
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	query := domainlive.PositionSnapshotQuery{Exchange: "bybit", Category: "linear", Symbol: "BTCUSDT"}
	current := validLiveStartupFlatPositionSnapshot(t, query, now.Add(-time.Second))
	baseline := validLiveStartupFlatPositionSnapshot(t, query, now.Add(-time.Minute))
	readerErr := errors.New("bybit offline")
	historyErr := errors.New("postgres offline")

	tests := []struct {
		name       string
		service    *applive.Service
		req        applive.LivePositionDriftReportRequest
		wantErrSub string
	}{
		{
			name:       "nil service",
			req:        applive.LivePositionDriftReportRequest{Queries: []domainlive.PositionSnapshotQuery{query}},
			wantErrSub: "service",
		},
		{
			name: "missing reader",
			service: applive.NewService(
				applive.WithPositionSnapshotHistoryReader(&fakeLivePositionDriftHistoryReader{}),
			),
			req:        applive.LivePositionDriftReportRequest{Queries: []domainlive.PositionSnapshotQuery{query}},
			wantErrSub: "position snapshot reader",
		},
		{
			name: "missing history reader",
			service: applive.NewService(
				applive.WithPositionSnapshotReader(&fakeLivePositionDriftSnapshotReader{}),
			),
			req:        applive.LivePositionDriftReportRequest{Queries: []domainlive.PositionSnapshotQuery{query}},
			wantErrSub: "position snapshot history reader",
		},
		{
			name: "missing queries",
			service: applive.NewService(
				applive.WithPositionSnapshotReader(&fakeLivePositionDriftSnapshotReader{}),
				applive.WithPositionSnapshotHistoryReader(&fakeLivePositionDriftHistoryReader{}),
			),
			wantErrSub: "at least one",
		},
		{
			name: "invalid query",
			service: applive.NewService(
				applive.WithPositionSnapshotReader(&fakeLivePositionDriftSnapshotReader{}),
				applive.WithPositionSnapshotHistoryReader(&fakeLivePositionDriftHistoryReader{}),
			),
			req: applive.LivePositionDriftReportRequest{Queries: []domainlive.PositionSnapshotQuery{{
				Exchange: "BYBIT",
				Category: "linear",
				Symbol:   "BTCUSDT",
			}}},
			wantErrSub: "exchange",
		},
		{
			name: "reader error",
			service: applive.NewService(
				applive.WithPositionSnapshotReader(&fakeLivePositionDriftSnapshotReader{err: readerErr}),
				applive.WithPositionSnapshotHistoryReader(&fakeLivePositionDriftHistoryReader{}),
			),
			req:        applive.LivePositionDriftReportRequest{Queries: []domainlive.PositionSnapshotQuery{query}},
			wantErrSub: "bybit offline",
		},
		{
			name: "history error",
			service: applive.NewService(
				applive.WithPositionSnapshotReader(&fakeLivePositionDriftSnapshotReader{snapshots: map[string]domainlive.PositionSnapshot{appPositionDriftKey(query): current}}),
				applive.WithPositionSnapshotHistoryReader(&fakeLivePositionDriftHistoryReader{err: historyErr}),
			),
			req:        applive.LivePositionDriftReportRequest{Queries: []domainlive.PositionSnapshotQuery{query}},
			wantErrSub: "postgres offline",
		},
		{
			name: "current query mismatch",
			service: applive.NewService(
				applive.WithClock(clock.FixedClock{Time: now}),
				applive.WithPositionSnapshotReader(&fakeLivePositionDriftSnapshotReader{snapshots: map[string]domainlive.PositionSnapshot{appPositionDriftKey(query): mutateAppPositionDriftSnapshot(current, func(s *domainlive.PositionSnapshot) {
					s.Symbol = "ETHUSDT"
				})}}),
				applive.WithPositionSnapshotHistoryReader(&fakeLivePositionDriftHistoryReader{snapshots: map[string]domainlive.PositionSnapshot{appPositionDriftKey(query): baseline}}),
			),
			req:        applive.LivePositionDriftReportRequest{Queries: []domainlive.PositionSnapshotQuery{query}},
			wantErrSub: "current snapshot",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := tt.service
			if tt.name == "nil service" {
				service = nil
			}
			_, err := service.BuildLivePositionDriftReport(context.Background(), tt.req)
			if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
		})
	}
}

type fakeLivePositionDriftSnapshotReader struct {
	snapshots map[string]domainlive.PositionSnapshot
	queries   []domainlive.PositionSnapshotQuery
	calls     int
	err       error
}

func (r *fakeLivePositionDriftSnapshotReader) GetPositionSnapshot(
	_ context.Context,
	query domainlive.PositionSnapshotQuery,
) (domainlive.PositionSnapshot, error) {
	r.calls++
	r.queries = append(r.queries, query)
	if r.err != nil {
		return domainlive.PositionSnapshot{}, r.err
	}
	snapshot, ok := r.snapshots[appPositionDriftKey(query)]
	if !ok {
		return domainlive.PositionSnapshot{}, errors.New("missing current snapshot fixture")
	}
	return snapshot, nil
}

type fakeLivePositionDriftHistoryReader struct {
	snapshots map[string]domainlive.PositionSnapshot
	missing   map[string]bool
	queries   []domainlive.PositionSnapshotQuery
	calls     int
	err       error
}

func (r *fakeLivePositionDriftHistoryReader) GetLatestPositionSnapshot(
	_ context.Context,
	query domainlive.PositionSnapshotQuery,
) (domainlive.PositionSnapshot, bool, error) {
	r.calls++
	r.queries = append(r.queries, query)
	if r.err != nil {
		return domainlive.PositionSnapshot{}, false, r.err
	}
	key := appPositionDriftKey(query)
	if r.missing[key] {
		return domainlive.PositionSnapshot{}, false, nil
	}
	snapshot, ok := r.snapshots[key]
	return snapshot, ok, nil
}

func appPositionDriftKey(query domainlive.PositionSnapshotQuery) string {
	return query.Exchange + "/" + query.Category + "/" + query.Symbol
}

func mutateAppPositionDriftSnapshot(
	snapshot domainlive.PositionSnapshot,
	mutate func(*domainlive.PositionSnapshot),
) domainlive.PositionSnapshot {
	if mutate != nil {
		mutate(&snapshot)
	}
	return snapshot
}
