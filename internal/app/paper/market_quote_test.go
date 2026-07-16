package paper_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	apppaper "github.com/VersoIt/Inquisitor/internal/app/paper"
	"github.com/VersoIt/Inquisitor/internal/marketdata"
)

func TestServiceSourceOrderbookQuoteReturnsFreshMidPrice(t *testing.T) {
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	old := appOrderbookSnapshot(now.Add(-20*time.Second), "99", "101")
	latest := appOrderbookSnapshot(now.Add(-2*time.Second), "100", "102")
	repo := &fakeOrderbookSnapshotRepository{snapshots: []marketdata.OrderbookSnapshot{latest, old}}
	service := quoteService(repo)

	got, err := service.SourceOrderbookQuote(context.Background(), apppaper.SourceOrderbookQuoteRequest{
		Exchange:     " BYBIT ",
		Category:     " LINEAR ",
		Symbol:       " btcusdt ",
		AsOf:         now,
		MaxStaleness: 30 * time.Second,
		MaxSpreadBPS: decimal.RequireFromString("250"),
		ScanLimit:    10,
	})
	if err != nil {
		t.Fatalf("source quote: %v", err)
	}

	if got.Snapshot.ExchangeTime != latest.ExchangeTime || !got.Bid.Equal(decimal.RequireFromString("100")) ||
		!got.Ask.Equal(decimal.RequireFromString("102")) || !got.MidPrice.Equal(decimal.RequireFromString("101")) ||
		got.Age != 2*time.Second {
		t.Fatalf("quote mismatch: %#v", got)
	}
	if len(repo.queries) != 1 || repo.queries[0].Exchange != "bybit" || repo.queries[0].Category != "linear" ||
		repo.queries[0].Symbol != "BTCUSDT" || repo.queries[0].Limit != 10 ||
		!repo.queries[0].Start.Equal(now.Add(-30*time.Second)) || !repo.queries[0].End.Equal(now.Add(time.Nanosecond)) {
		t.Fatalf("query mismatch: %#v", repo.queries)
	}
}

func TestServiceSourceOrderbookQuoteRejectsUnsafeInputsTableDriven(t *testing.T) {
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	repositoryErr := errors.New("postgres unavailable")
	validReq := apppaper.SourceOrderbookQuoteRequest{
		Exchange:     "bybit",
		Category:     "linear",
		Symbol:       "BTCUSDT",
		AsOf:         now,
		MaxStaleness: 30 * time.Second,
		MaxSpreadBPS: decimal.RequireFromString("250"),
		ScanLimit:    10,
	}

	tests := []struct {
		name       string
		service    *apppaper.Service
		req        apppaper.SourceOrderbookQuoteRequest
		wantErrSub string
	}{
		{
			name:       "missing repository",
			service:    quoteService(nil),
			req:        validReq,
			wantErrSub: "orderbook snapshot repository",
		},
		{
			name:       "missing exchange",
			service:    quoteService(&fakeOrderbookSnapshotRepository{}),
			req:        func() apppaper.SourceOrderbookQuoteRequest { req := validReq; req.Exchange = ""; return req }(),
			wantErrSub: "exchange",
		},
		{
			name:       "missing category",
			service:    quoteService(&fakeOrderbookSnapshotRepository{}),
			req:        func() apppaper.SourceOrderbookQuoteRequest { req := validReq; req.Category = ""; return req }(),
			wantErrSub: "category",
		},
		{
			name:       "missing symbol",
			service:    quoteService(&fakeOrderbookSnapshotRepository{}),
			req:        func() apppaper.SourceOrderbookQuoteRequest { req := validReq; req.Symbol = ""; return req }(),
			wantErrSub: "symbol",
		},
		{
			name:       "missing as of",
			service:    quoteService(&fakeOrderbookSnapshotRepository{}),
			req:        func() apppaper.SourceOrderbookQuoteRequest { req := validReq; req.AsOf = time.Time{}; return req }(),
			wantErrSub: "as_of",
		},
		{
			name:       "invalid staleness",
			service:    quoteService(&fakeOrderbookSnapshotRepository{}),
			req:        func() apppaper.SourceOrderbookQuoteRequest { req := validReq; req.MaxStaleness = 0; return req }(),
			wantErrSub: "max_staleness",
		},
		{
			name:    "negative spread limit",
			service: quoteService(&fakeOrderbookSnapshotRepository{}),
			req: func() apppaper.SourceOrderbookQuoteRequest {
				req := validReq
				req.MaxSpreadBPS = decimal.RequireFromString("-1")
				return req
			}(),
			wantErrSub: "max_spread_bps",
		},
		{
			name:       "negative scan limit",
			service:    quoteService(&fakeOrderbookSnapshotRepository{}),
			req:        func() apppaper.SourceOrderbookQuoteRequest { req := validReq; req.ScanLimit = -1; return req }(),
			wantErrSub: "scan_limit",
		},
		{
			name:       "repository error",
			service:    quoteService(&fakeOrderbookSnapshotRepository{err: repositoryErr}),
			req:        validReq,
			wantErrSub: repositoryErr.Error(),
		},
		{
			name:       "no fresh snapshot",
			service:    quoteService(&fakeOrderbookSnapshotRepository{}),
			req:        validReq,
			wantErrSub: "no fresh",
		},
		{
			name:       "future snapshot ignored",
			service:    quoteService(&fakeOrderbookSnapshotRepository{snapshots: []marketdata.OrderbookSnapshot{appOrderbookSnapshot(now.Add(time.Second), "100", "101")}}),
			req:        validReq,
			wantErrSub: "no fresh",
		},
		{
			name:       "stale snapshot rejected",
			service:    quoteService(&fakeOrderbookSnapshotRepository{snapshots: []marketdata.OrderbookSnapshot{appOrderbookSnapshot(now.Add(-time.Minute), "100", "101")}}),
			req:        validReq,
			wantErrSub: "no fresh",
		},
		{
			name:    "wide spread rejected",
			service: quoteService(&fakeOrderbookSnapshotRepository{snapshots: []marketdata.OrderbookSnapshot{appOrderbookSnapshot(now.Add(-time.Second), "99", "102")}}),
			req: func() apppaper.SourceOrderbookQuoteRequest {
				req := validReq
				req.MaxSpreadBPS = decimal.RequireFromString("100")
				return req
			}(),
			wantErrSub: "spread",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.service.SourceOrderbookQuote(context.Background(), tt.req)
			if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
		})
	}
}

func quoteService(orderbooks marketdata.OrderbookSnapshotRepository) *apppaper.Service {
	return apppaper.NewService(
		&fakeRunRepository{},
		&fakeResultRepository{},
		apppaper.WithOrderbookSnapshotRepository(orderbooks),
	)
}

type fakeOrderbookSnapshotRepository struct {
	snapshots []marketdata.OrderbookSnapshot
	queries   []marketdata.OrderbookSnapshotQuery
	err       error
}

func (r *fakeOrderbookSnapshotRepository) CreateOrderbookSnapshots(context.Context, []marketdata.OrderbookSnapshot) (marketdata.WriteStats, error) {
	panic("not implemented")
}

func (r *fakeOrderbookSnapshotRepository) ListOrderbookSnapshots(_ context.Context, query marketdata.OrderbookSnapshotQuery) ([]marketdata.OrderbookSnapshot, error) {
	r.queries = append(r.queries, query)
	if r.err != nil {
		return nil, r.err
	}
	return append([]marketdata.OrderbookSnapshot(nil), r.snapshots...), nil
}

func appOrderbookSnapshot(exchangeTime time.Time, bestBid string, bestAsk string) marketdata.OrderbookSnapshot {
	bid := decimal.RequireFromString(bestBid)
	ask := decimal.RequireFromString(bestAsk)
	spread := ask.Sub(bid)
	mid := ask.Add(bid).Div(decimal.NewFromInt(2))
	return marketdata.OrderbookSnapshot{
		Exchange:           "bybit",
		Category:           "linear",
		Symbol:             "BTCUSDT",
		Depth:              50,
		Bids:               []marketdata.OrderbookLevel{{Price: bid, Quantity: decimal.RequireFromString("2")}},
		Asks:               []marketdata.OrderbookLevel{{Price: ask, Quantity: decimal.RequireFromString("3")}},
		BestBid:            bid,
		BestAsk:            ask,
		Spread:             spread,
		SpreadBPS:          spread.Div(mid).Mul(decimal.NewFromInt(10000)),
		UpdateID:           100,
		Sequence:           200,
		ExchangeTime:       exchangeTime.UTC(),
		MatchingEngineTime: exchangeTime.UTC(),
		CreatedAt:          exchangeTime.Add(time.Second).UTC(),
	}
}
