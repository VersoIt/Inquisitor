package paper

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/VersoIt/Inquisitor/internal/marketdata"
)

const defaultQuoteSnapshotScanLimit = 1000

type SourceOrderbookQuoteRequest struct {
	Exchange     string
	Category     string
	Symbol       string
	AsOf         time.Time
	MaxStaleness time.Duration
	MaxSpreadBPS decimal.Decimal
	ScanLimit    int
}

type SourceOrderbookQuoteResult struct {
	Snapshot  marketdata.OrderbookSnapshot
	Bid       decimal.Decimal
	Ask       decimal.Decimal
	MidPrice  decimal.Decimal
	SpreadBPS decimal.Decimal
	Age       time.Duration
}

func (s *Service) SourceOrderbookQuote(ctx context.Context, req SourceOrderbookQuoteRequest) (SourceOrderbookQuoteResult, error) {
	if err := ctx.Err(); err != nil {
		return SourceOrderbookQuoteResult{}, err
	}
	if s == nil || s.orderbooks == nil {
		return SourceOrderbookQuoteResult{}, fmt.Errorf("paper quote sourcing requires orderbook snapshot repository")
	}
	exchange := strings.ToLower(strings.TrimSpace(req.Exchange))
	category := strings.ToLower(strings.TrimSpace(req.Category))
	symbol := strings.ToUpper(strings.TrimSpace(req.Symbol))
	if exchange == "" {
		return SourceOrderbookQuoteResult{}, fmt.Errorf("exchange is required")
	}
	if category == "" {
		return SourceOrderbookQuoteResult{}, fmt.Errorf("category is required")
	}
	if symbol == "" {
		return SourceOrderbookQuoteResult{}, fmt.Errorf("symbol is required")
	}
	asOf := req.AsOf.UTC()
	if asOf.IsZero() {
		return SourceOrderbookQuoteResult{}, fmt.Errorf("as_of is required")
	}
	if req.MaxStaleness <= 0 {
		return SourceOrderbookQuoteResult{}, fmt.Errorf("max_staleness must be positive")
	}
	if req.MaxSpreadBPS.IsNegative() {
		return SourceOrderbookQuoteResult{}, fmt.Errorf("max_spread_bps must be greater than or equal to zero")
	}
	scanLimit := req.ScanLimit
	if scanLimit == 0 {
		scanLimit = defaultQuoteSnapshotScanLimit
	}
	if scanLimit < 0 {
		return SourceOrderbookQuoteResult{}, fmt.Errorf("scan_limit must be greater than or equal to zero")
	}

	start := asOf.Add(-req.MaxStaleness)
	snapshots, err := s.orderbooks.ListOrderbookSnapshots(ctx, marketdata.OrderbookSnapshotQuery{
		Exchange: exchange,
		Category: category,
		Symbol:   symbol,
		Start:    start,
		End:      asOf.Add(time.Nanosecond),
		Limit:    scanLimit,
	})
	if err != nil {
		return SourceOrderbookQuoteResult{}, fmt.Errorf("list orderbook snapshots for paper quote: %w", err)
	}

	latest, ok := latestQuoteSnapshot(snapshots, start, asOf)
	if !ok {
		return SourceOrderbookQuoteResult{}, fmt.Errorf("no fresh orderbook snapshot for %s %s %s", exchange, category, symbol)
	}
	age := asOf.Sub(latest.ExchangeTime.UTC())
	if age > req.MaxStaleness {
		return SourceOrderbookQuoteResult{}, fmt.Errorf("latest orderbook snapshot is stale: age=%s max=%s", age, req.MaxStaleness)
	}
	if latest.SpreadBPS.GreaterThan(req.MaxSpreadBPS) {
		return SourceOrderbookQuoteResult{}, fmt.Errorf("latest orderbook spread is too wide: spread_bps=%s max=%s", latest.SpreadBPS, req.MaxSpreadBPS)
	}

	return SourceOrderbookQuoteResult{
		Snapshot:  latest,
		Bid:       latest.BestBid,
		Ask:       latest.BestAsk,
		MidPrice:  latest.BestBid.Add(latest.BestAsk).Div(decimal.NewFromInt(2)),
		SpreadBPS: latest.SpreadBPS,
		Age:       age,
	}, nil
}

func latestQuoteSnapshot(snapshots []marketdata.OrderbookSnapshot, start time.Time, asOf time.Time) (marketdata.OrderbookSnapshot, bool) {
	var latest marketdata.OrderbookSnapshot
	var ok bool
	for _, snapshot := range snapshots {
		exchangeTime := snapshot.ExchangeTime.UTC()
		if exchangeTime.Before(start) || exchangeTime.After(asOf) {
			continue
		}
		if !ok || exchangeTime.After(latest.ExchangeTime.UTC()) {
			latest = snapshot
			ok = true
		}
	}
	return latest, ok
}
