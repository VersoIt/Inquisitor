package live_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	applive "github.com/VersoIt/Inquisitor/internal/app/live"
	domainlive "github.com/VersoIt/Inquisitor/internal/live"
	domainrisk "github.com/VersoIt/Inquisitor/internal/risk"
)

func TestServiceBuildPendingLiveDecisionReportSummarizesCandidates(t *testing.T) {
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	reader := &fakePendingLiveDecisionReader{candidates: []domainlive.PendingLiveDecision{
		pendingLiveDecision("risk_decision_live_pending_0001", "BTCUSDT", now.Add(-2*time.Minute)),
		pendingLiveDecision("risk_decision_live_pending_0002", "BTCUSDT", now.Add(-time.Minute)),
	}}
	service := applive.NewService(applive.WithPendingLiveDecisionReader(reader))

	got, err := service.BuildPendingLiveDecisionReport(context.Background(), applive.PendingLiveDecisionReportRequest{
		Symbol: "btcusdt",
	})
	if err != nil {
		t.Fatalf("build pending live decision report: %v", err)
	}
	if reader.calls != 1 || reader.query.Symbol != "BTCUSDT" || reader.query.Limit != 10 {
		t.Fatalf("reader query mismatch: calls=%d query=%#v", reader.calls, reader.query)
	}
	if got.Summary.Total != 2 ||
		got.Summary.NextID != "risk_decision_live_pending_0001" ||
		got.Summary.NextSymbol != "BTCUSDT" ||
		!got.Summary.OldestAt.Equal(now.Add(-2*time.Minute)) ||
		!got.Summary.NewestAt.Equal(now.Add(-time.Minute)) {
		t.Fatalf("summary mismatch: %#v", got.Summary)
	}
	if len(got.Candidates) != 2 || got.Candidates[0].Decision.DecisionID != "risk_decision_live_pending_0001" {
		t.Fatalf("candidates mismatch: %#v", got.Candidates)
	}
}

func TestServiceBuildPendingLiveDecisionReportRejectsUnsafeInputsTableDriven(t *testing.T) {
	tests := []struct {
		name       string
		service    *applive.Service
		req        applive.PendingLiveDecisionReportRequest
		wantErrSub string
	}{
		{
			name:       "missing reader",
			service:    applive.NewService(),
			req:        applive.PendingLiveDecisionReportRequest{},
			wantErrSub: "pending decision reader",
		},
		{
			name:       "untrimmed symbol",
			service:    applive.NewService(applive.WithPendingLiveDecisionReader(&fakePendingLiveDecisionReader{})),
			req:        applive.PendingLiveDecisionReportRequest{Symbol: " BTCUSDT "},
			wantErrSub: "symbol",
		},
		{
			name:       "limit above max",
			service:    applive.NewService(applive.WithPendingLiveDecisionReader(&fakePendingLiveDecisionReader{})),
			req:        applive.PendingLiveDecisionReportRequest{Limit: 101},
			wantErrSub: "limit",
		},
		{
			name:       "reader error",
			service:    applive.NewService(applive.WithPendingLiveDecisionReader(&fakePendingLiveDecisionReader{err: errors.New("db unavailable")})),
			req:        applive.PendingLiveDecisionReportRequest{},
			wantErrSub: "db unavailable",
		},
		{
			name: "invalid candidate",
			service: applive.NewService(applive.WithPendingLiveDecisionReader(&fakePendingLiveDecisionReader{
				candidates: []domainlive.PendingLiveDecision{func() domainlive.PendingLiveDecision {
					candidate := pendingLiveDecision("risk_decision_live_pending_0001", "BTCUSDT", time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC))
					candidate.Decision.Mode = domainrisk.ModePaper
					return candidate
				}()},
			})),
			req:        applive.PendingLiveDecisionReportRequest{},
			wantErrSub: "LIVE",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.service.BuildPendingLiveDecisionReport(context.Background(), tt.req)
			if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
		})
	}
}

type fakePendingLiveDecisionReader struct {
	query      domainlive.PendingLiveDecisionQuery
	candidates []domainlive.PendingLiveDecision
	calls      int
	err        error
}

func (r *fakePendingLiveDecisionReader) ListPendingLiveDecisions(
	_ context.Context,
	query domainlive.PendingLiveDecisionQuery,
) ([]domainlive.PendingLiveDecision, error) {
	r.calls++
	r.query = query
	if r.err != nil {
		return nil, r.err
	}
	return append([]domainlive.PendingLiveDecision(nil), r.candidates...), nil
}

func pendingLiveDecision(decisionID string, symbol string, createdAt time.Time) domainlive.PendingLiveDecision {
	return domainlive.PendingLiveDecision{
		Decision: domainrisk.DecisionAuditRecord{
			DecisionID: decisionID,
			Decision: domainrisk.Decision{
				IntentID:      "risk_intent_" + strings.TrimPrefix(decisionID, "risk_decision_"),
				Approved:      true,
				FinalQuantity: decimal.RequireFromString("0.005"),
				MaxLoss:       decimal.RequireFromString("5"),
				StopLoss:      decimal.RequireFromString("99000"),
				TakeProfit:    decimal.RequireFromString("102000"),
				Reason:        "risk_checks_passed",
				Checks: []domainrisk.Check{{
					Name:   "trading_enabled",
					Passed: true,
				}},
				CreatedAt: createdAt,
			},
			Mode:            domainrisk.ModeLive,
			HypothesisID:    "hypothesis_live_pending_0001",
			StrategyName:    "trend-momentum",
			Symbol:          symbol,
			Side:            domainrisk.SideLong,
			EntryPrice:      decimal.RequireFromString("100000"),
			Leverage:        decimal.RequireFromString("1"),
			Confidence:      80,
			IntentReason:    "signal confirmed",
			IntentCreatedAt: createdAt.Add(-time.Minute),
			RecordedAt:      createdAt.Add(time.Second),
		},
	}
}
