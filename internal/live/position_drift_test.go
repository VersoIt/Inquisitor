package live_test

import (
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	domainlive "github.com/VersoIt/Inquisitor/internal/live"
)

func TestValidatePositionSnapshotFreshnessTableDriven(t *testing.T) {
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	valid := validFlatPositionSnapshot()
	valid.ObservedAt = now.Add(-time.Second)

	tests := []struct {
		name       string
		snapshot   domainlive.PositionSnapshot
		now        time.Time
		maxAge     time.Duration
		wantErrSub string
	}{
		{name: "fresh", snapshot: valid, now: now, maxAge: 5 * time.Second},
		{name: "stale", snapshot: mutatePositionDriftSnapshot(valid, func(s *domainlive.PositionSnapshot) {
			s.ObservedAt = now.Add(-6 * time.Second)
		}), now: now, maxAge: 5 * time.Second, wantErrSub: "stale"},
		{name: "future", snapshot: mutatePositionDriftSnapshot(valid, func(s *domainlive.PositionSnapshot) {
			s.ObservedAt = now.Add(time.Second)
		}), now: now, maxAge: 5 * time.Second, wantErrSub: "future"},
		{name: "zero now", snapshot: valid, maxAge: 5 * time.Second, wantErrSub: "now"},
		{name: "zero max age", snapshot: valid, now: now, wantErrSub: "max_age"},
		{name: "invalid snapshot first", snapshot: mutatePositionDriftSnapshot(valid, func(s *domainlive.PositionSnapshot) {
			s.Symbol = "btcusdt"
		}), now: now, maxAge: 5 * time.Second, wantErrSub: "symbol"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := domainlive.ValidatePositionSnapshotFreshness(tt.snapshot, tt.now, tt.maxAge)
			if tt.wantErrSub != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("validate freshness: %v", err)
			}
		})
	}
}

func TestEnsurePositionSnapshotMatchesQueryTableDriven(t *testing.T) {
	query := domainlive.PositionSnapshotQuery{Exchange: "bybit", Category: "linear", Symbol: "BTCUSDT"}
	snapshot := validFlatPositionSnapshot()

	tests := []struct {
		name       string
		snapshot   domainlive.PositionSnapshot
		query      domainlive.PositionSnapshotQuery
		wantErrSub string
	}{
		{name: "matches", snapshot: snapshot, query: query},
		{name: "symbol mismatch", snapshot: mutatePositionDriftSnapshot(snapshot, func(s *domainlive.PositionSnapshot) {
			s.Symbol = "ETHUSDT"
		}), query: query, wantErrSub: "symbol"},
		{name: "invalid query", snapshot: snapshot, query: domainlive.PositionSnapshotQuery{Exchange: "BYBIT", Category: "linear", Symbol: "BTCUSDT"}, wantErrSub: "exchange"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := domainlive.EnsurePositionSnapshotMatchesQuery(tt.snapshot, tt.query)
			if tt.wantErrSub != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ensure snapshot matches query: %v", err)
			}
		})
	}
}

func TestPositionExposureDriftProblemsTableDriven(t *testing.T) {
	open := validOpenPositionSnapshot()

	tests := []struct {
		name         string
		current      domainlive.PositionSnapshot
		wantProblems bool
		wantSub      string
	}{
		{
			name: "same exposure ignores market noise",
			current: mutatePositionDriftSnapshot(open, func(s *domainlive.PositionSnapshot) {
				s.MarkPrice = decimal.RequireFromString("100250")
				s.PositionValue = decimal.RequireFromString("25062.5")
				s.UnrealisedPnL = decimal.RequireFromString("61")
				s.CurrentRealisedPnL = decimal.RequireFromString("-13")
				s.CumulativeRealisedPnL = decimal.RequireFromString("12")
				s.ExchangeUpdatedAt = s.ExchangeUpdatedAt.Add(time.Second)
				s.ObservedAt = s.ObservedAt.Add(time.Second)
			}),
		},
		{
			name: "size drift",
			current: mutatePositionDriftSnapshot(open, func(s *domainlive.PositionSnapshot) {
				s.Size = decimal.RequireFromString("0.30")
			}),
			wantProblems: true,
			wantSub:      "size",
		},
		{
			name: "side drift",
			current: mutatePositionDriftSnapshot(open, func(s *domainlive.PositionSnapshot) {
				s.Side = domainlive.OrderSideShort
			}),
			wantProblems: true,
			wantSub:      "side",
		},
		{
			name: "open flag drift",
			current: mutatePositionDriftSnapshot(open, func(s *domainlive.PositionSnapshot) {
				*s = validFlatPositionSnapshot()
			}),
			wantProblems: true,
			wantSub:      "open",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			problems := domainlive.PositionExposureDriftProblems(open, tt.current)
			if tt.wantProblems {
				if len(problems) == 0 || !strings.Contains(strings.Join(problems, "; "), tt.wantSub) {
					t.Fatalf("expected problems containing %q, got %#v", tt.wantSub, problems)
				}
				return
			}
			if len(problems) != 0 {
				t.Fatalf("expected no exposure drift, got %#v", problems)
			}
		})
	}
}

func TestBuildPositionDriftComparisonTableDriven(t *testing.T) {
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	query := domainlive.PositionSnapshotQuery{Exchange: "bybit", Category: "linear", Symbol: "BTCUSDT"}
	baseline := validFlatPositionSnapshot()
	baseline.ObservedAt = now.Add(-time.Minute)
	current := baseline
	current.ObservedAt = now.Add(-time.Second)

	tests := []struct {
		name       string
		req        domainlive.PositionDriftComparisonRequest
		wantStatus domainlive.LiveOpsStatus
		wantCheck  string
		wantErrSub string
	}{
		{
			name: "clear flat position matches fresh baseline",
			req: domainlive.PositionDriftComparisonRequest{
				Query:       query,
				Current:     current,
				HasBaseline: true,
				Baseline:    baseline,
				Now:         now,
			},
			wantStatus: domainlive.LiveOpsStatusClear,
		},
		{
			name: "missing baseline needs attention",
			req: domainlive.PositionDriftComparisonRequest{
				Query:   query,
				Current: current,
				Now:     now,
			},
			wantStatus: domainlive.LiveOpsStatusAttention,
			wantCheck:  "db_position_baseline",
		},
		{
			name: "stale baseline needs attention",
			req: domainlive.PositionDriftComparisonRequest{
				Query:          query,
				Current:        current,
				HasBaseline:    true,
				Baseline:       mutatePositionDriftSnapshot(baseline, func(s *domainlive.PositionSnapshot) { s.ObservedAt = now.Add(-time.Hour) }),
				Now:            now,
				BaselineMaxAge: 10 * time.Minute,
			},
			wantStatus: domainlive.LiveOpsStatusAttention,
			wantCheck:  "db_position_baseline",
		},
		{
			name: "current stale blocks",
			req: domainlive.PositionDriftComparisonRequest{
				Query:         query,
				Current:       mutatePositionDriftSnapshot(current, func(s *domainlive.PositionSnapshot) { s.ObservedAt = now.Add(-10 * time.Second) }),
				HasBaseline:   true,
				Baseline:      baseline,
				Now:           now,
				CurrentMaxAge: 5 * time.Second,
			},
			wantStatus: domainlive.LiveOpsStatusBlocked,
			wantCheck:  "current_position_snapshot",
		},
		{
			name: "unexpected open position blocks",
			req: domainlive.PositionDriftComparisonRequest{
				Query:       query,
				Current:     mutatePositionDriftSnapshot(validOpenPositionSnapshot(), func(s *domainlive.PositionSnapshot) { s.ObservedAt = now.Add(-time.Second) }),
				HasBaseline: true,
				Baseline:    baseline,
				Now:         now,
			},
			wantStatus: domainlive.LiveOpsStatusBlocked,
			wantCheck:  "position_exposure_drift",
		},
		{
			name: "liquidation status blocks",
			req: domainlive.PositionDriftComparisonRequest{
				Query: query,
				Current: mutatePositionDriftSnapshot(validOpenPositionSnapshot(), func(s *domainlive.PositionSnapshot) {
					s.ExchangeStatus = domainlive.ExchangePositionStatusLiq
					s.ObservedAt = now.Add(-time.Second)
				}),
				HasBaseline: true,
				Baseline: mutatePositionDriftSnapshot(validOpenPositionSnapshot(), func(s *domainlive.PositionSnapshot) {
					s.ObservedAt = now.Add(-time.Minute)
				}),
				Now: now,
			},
			wantStatus: domainlive.LiveOpsStatusBlocked,
			wantCheck:  "position_exchange_status",
		},
		{
			name: "current query mismatch is rejected",
			req: domainlive.PositionDriftComparisonRequest{
				Query: query,
				Current: mutatePositionDriftSnapshot(current, func(s *domainlive.PositionSnapshot) {
					s.Symbol = "ETHUSDT"
				}),
				Now: now,
			},
			wantErrSub: "current snapshot",
		},
		{
			name: "missing now is rejected",
			req: domainlive.PositionDriftComparisonRequest{
				Query:   query,
				Current: current,
			},
			wantErrSub: "now",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := domainlive.BuildPositionDriftComparison(tt.req)
			if tt.wantErrSub != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("build drift comparison: %v", err)
			}
			if got.Status != tt.wantStatus {
				t.Fatalf("status mismatch: got %s want %s checks=%#v", got.Status, tt.wantStatus, got.Checks)
			}
			if tt.wantCheck != "" && !positionDriftHasCheck(got.Checks, tt.wantCheck) {
				t.Fatalf("expected check %q, got %#v", tt.wantCheck, got.Checks)
			}
		})
	}
}

func mutatePositionDriftSnapshot(
	snapshot domainlive.PositionSnapshot,
	mutate func(*domainlive.PositionSnapshot),
) domainlive.PositionSnapshot {
	if mutate != nil {
		mutate(&snapshot)
	}
	return snapshot
}

func positionDriftHasCheck(checks []domainlive.ReadinessCheck, name string) bool {
	for _, check := range checks {
		if check.Name == name {
			return true
		}
	}
	return false
}
