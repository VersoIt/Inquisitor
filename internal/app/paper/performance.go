package paper

import (
	"context"
	"fmt"
	"strings"

	domainbacktest "github.com/VersoIt/Inquisitor/internal/backtest"
	domainpaper "github.com/VersoIt/Inquisitor/internal/paper"
)

const maxPerformanceReportTrades = 100_000

type PerformanceReportRequest struct {
	ValidationID string
	RecordDaily  bool
}

type PerformanceReportResult struct {
	Record     domainpaper.ValidationRecord
	Trades     []domainpaper.ValidationTrade
	Summary    domainbacktest.Summary
	Daily      []domainpaper.DailyPerformance
	DailyStats domainpaper.DailyPerformanceStats
}

func (s *Service) BuildPerformanceReport(ctx context.Context, req PerformanceReportRequest) (PerformanceReportResult, error) {
	if err := ctx.Err(); err != nil {
		return PerformanceReportResult{}, err
	}
	if s == nil || s.records == nil {
		return PerformanceReportResult{}, fmt.Errorf("paper performance report requires validation record repository")
	}
	if s.trades == nil {
		return PerformanceReportResult{}, fmt.Errorf("paper performance report requires validation trade repository")
	}
	if s.clock == nil {
		return PerformanceReportResult{}, fmt.Errorf("paper performance report requires clock")
	}
	validationID := strings.TrimSpace(req.ValidationID)
	if validationID == "" {
		return PerformanceReportResult{}, fmt.Errorf("validation_id is required")
	}
	record, err := s.loadValidationRecord(ctx, validationID)
	if err != nil {
		return PerformanceReportResult{}, err
	}
	trades, err := s.trades.ListValidationTrades(ctx, domainpaper.ValidationTradeQuery{
		ValidationID: validationID,
		Limit:        maxPerformanceReportTrades + 1,
	})
	if err != nil {
		return PerformanceReportResult{}, fmt.Errorf("load paper validation trades %q: %w", validationID, err)
	}
	if len(trades) > maxPerformanceReportTrades {
		return PerformanceReportResult{}, fmt.Errorf("paper performance report exceeds %d trade safety limit", maxPerformanceReportTrades)
	}
	roundTrips := make([]domainbacktest.RoundTrip, 0, len(trades))
	for _, trade := range trades {
		roundTrips = append(roundTrips, trade.RoundTrip)
	}
	summary, err := domainbacktest.SummarizeRoundTrips(record.InitialBalance, roundTrips)
	if err != nil {
		return PerformanceReportResult{}, fmt.Errorf("summarize paper validation %q: %w", validationID, err)
	}
	daily, err := domainpaper.BuildDailyPerformance(validationID, record.InitialBalance, trades, s.clock.Now())
	if err != nil {
		return PerformanceReportResult{}, err
	}
	out := PerformanceReportResult{Record: record, Trades: trades, Summary: summary, Daily: daily}
	if !req.RecordDaily || len(daily) == 0 {
		return out, nil
	}
	if s.performance == nil {
		return PerformanceReportResult{}, fmt.Errorf("paper performance report requires daily performance repository")
	}
	stats, err := s.performance.RecordDailyPerformance(ctx, daily)
	if err != nil {
		return PerformanceReportResult{}, fmt.Errorf("record paper daily performance %q: %w", validationID, err)
	}
	out.DailyStats = stats
	return out, nil
}
