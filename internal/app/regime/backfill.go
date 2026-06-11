package regime

import (
	"context"
	"fmt"
	"time"

	appfeatures "github.com/VersoIt/Inquisitor/internal/app/features"
	"github.com/VersoIt/Inquisitor/internal/marketdata"
)

type BackfillRequest struct {
	Exchange  string
	Category  string
	Symbols   []string
	Intervals []string
	Start     time.Time
	End       time.Time

	FeatureLookback time.Duration
	TargetLimit     int
	CandleLimit     int
	TradeLimit      int
	SnapshotLimit   int
	Runtime         appfeatures.RuntimeState
}

type BackfillResult struct {
	Symbols       int
	Intervals     int
	Pairs         int
	TargetCandles int
	Attempts      int
	Classified    int
	Stored        int
	Inserted      int
	Updated       int
	NoTrade       int
}

func (s *Service) Backfill(ctx context.Context, req BackfillRequest) (BackfillResult, error) {
	normalized, err := normalizeBackfillRequest(req)
	if err != nil {
		return BackfillResult{}, err
	}
	if s.repository == nil {
		return BackfillResult{}, fmt.Errorf("regime repository is required")
	}
	if s.candleLister == nil {
		return BackfillResult{}, fmt.Errorf("candle lister is required")
	}

	result := BackfillResult{
		Symbols:   len(normalized.Symbols),
		Intervals: len(normalized.Intervals),
		Pairs:     len(normalized.Symbols) * len(normalized.Intervals),
	}
	for _, symbol := range normalized.Symbols {
		for _, interval := range normalized.Intervals {
			targets, err := s.targetCandles(ctx, normalized, symbol, interval)
			if err != nil {
				return result, fmt.Errorf("list target candles %s %s: %w", symbol, interval, err)
			}
			result.TargetCandles += len(targets)

			for _, target := range targets {
				result.Attempts++
				classification, err := s.ClassifyAndStore(ctx, appfeatures.ComputeRequest{
					Exchange:      normalized.Exchange,
					Category:      normalized.Category,
					Symbol:        symbol,
					Interval:      interval,
					Start:         target.CloseTime.Add(-normalized.FeatureLookback),
					End:           target.CloseTime,
					ObservedAt:    target.CloseTime,
					CandleLimit:   normalized.CandleLimit,
					TradeLimit:    normalized.TradeLimit,
					SnapshotLimit: normalized.SnapshotLimit,
					Runtime:       normalized.Runtime,
				})
				if err != nil {
					return result, fmt.Errorf("classify and store historical regime %s %s %s: %w", symbol, interval, target.CloseTime.UTC().Format(time.RFC3339), err)
				}

				result.Classified++
				result.Inserted += classification.Stored.Inserted
				result.Updated += classification.Stored.Updated
				result.Stored += classification.Stored.Total()
				if classification.Regime.NoTrade {
					result.NoTrade++
				}
			}
		}
	}
	return result, nil
}

func (s *Service) targetCandles(ctx context.Context, req BackfillRequest, symbol, interval string) ([]marketdata.Candle, error) {
	duration, err := marketdata.IntervalDuration(interval)
	if err != nil {
		return nil, err
	}
	limit := req.TargetLimit
	if limit <= 0 {
		limit = 1000
	}

	candles, err := s.candleLister.ListCandles(ctx, marketdata.CandleQuery{
		Exchange: req.Exchange,
		Category: req.Category,
		Symbol:   symbol,
		Interval: interval,
		Start:    req.Start.Add(-duration),
		End:      req.End,
		Limit:    limit,
	})
	if err != nil {
		return nil, err
	}

	targets := make([]marketdata.Candle, 0, len(candles))
	for _, candle := range candles {
		closeTime := candle.CloseTime.UTC()
		if closeTime.Before(req.Start) || !closeTime.Before(req.End) {
			continue
		}
		targets = append(targets, candle)
	}
	return targets, nil
}

func normalizeBackfillRequest(req BackfillRequest) (BackfillRequest, error) {
	runReq, err := normalizeRunRequest(RunRequest{
		Exchange:      req.Exchange,
		Category:      req.Category,
		Symbols:       req.Symbols,
		Intervals:     req.Intervals,
		Start:         req.Start,
		End:           req.End,
		CandleLimit:   req.CandleLimit,
		TradeLimit:    req.TradeLimit,
		SnapshotLimit: req.SnapshotLimit,
		Runtime:       req.Runtime,
	})
	if err != nil {
		return BackfillRequest{}, err
	}
	if req.FeatureLookback <= 0 {
		return BackfillRequest{}, fmt.Errorf("feature_lookback must be positive")
	}
	if req.TargetLimit < 0 {
		return BackfillRequest{}, fmt.Errorf("target_limit must be non-negative")
	}

	req.Exchange = runReq.Exchange
	req.Category = runReq.Category
	req.Symbols = runReq.Symbols
	req.Intervals = runReq.Intervals
	req.Start = runReq.Start
	req.End = runReq.End
	req.CandleLimit = runReq.CandleLimit
	req.TradeLimit = runReq.TradeLimit
	req.SnapshotLimit = runReq.SnapshotLimit
	req.Runtime = runReq.Runtime
	return req, nil
}
