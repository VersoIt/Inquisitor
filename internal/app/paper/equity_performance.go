package paper

import (
	"context"
	"fmt"
	"strings"

	domainbacktest "github.com/VersoIt/Inquisitor/internal/backtest"
	domainpaper "github.com/VersoIt/Inquisitor/internal/paper"
)

type EquityPerformanceReportRequest struct {
	ValidationID string
	RecordDaily  bool
}

type EquityPerformanceReportResult struct {
	Record     domainpaper.ValidationRecord
	Events     []domainpaper.EquityEvent
	Summary    domainbacktest.Summary
	Daily      []domainpaper.DailyPerformance
	DailyStats domainpaper.DailyPerformanceStats
}

func (s *Service) BuildEquityPerformanceReport(ctx context.Context, req EquityPerformanceReportRequest) (EquityPerformanceReportResult, error) {
	if err := ctx.Err(); err != nil {
		return EquityPerformanceReportResult{}, err
	}
	if s == nil || s.records == nil {
		return EquityPerformanceReportResult{}, fmt.Errorf("paper equity performance report requires validation record repository")
	}
	if s.equity == nil {
		return EquityPerformanceReportResult{}, fmt.Errorf("paper equity performance report requires equity event repository")
	}
	if s.clock == nil {
		return EquityPerformanceReportResult{}, fmt.Errorf("paper equity performance report requires clock")
	}
	validationID := strings.TrimSpace(req.ValidationID)
	if validationID == "" {
		return EquityPerformanceReportResult{}, fmt.Errorf("validation_id is required")
	}
	record, err := s.loadValidationRecord(ctx, validationID)
	if err != nil {
		return EquityPerformanceReportResult{}, err
	}
	events, err := s.equity.ListEquityEvents(ctx, domainpaper.EquityEventQuery{
		ValidationID: validationID,
		Limit:        maxEquityLedgerEvents + 1,
	})
	if err != nil {
		return EquityPerformanceReportResult{}, fmt.Errorf("load paper equity ledger %q: %w", validationID, err)
	}
	if len(events) > maxEquityLedgerEvents {
		return EquityPerformanceReportResult{}, fmt.Errorf("paper equity performance report exceeds %d event safety limit", maxEquityLedgerEvents)
	}
	summary, err := domainpaper.SummarizeEquityEvents(validationID, record.InitialBalance, events)
	if err != nil {
		return EquityPerformanceReportResult{}, err
	}
	daily, err := domainpaper.BuildDailyEquityPerformance(validationID, record.InitialBalance, events, s.clock.Now())
	if err != nil {
		return EquityPerformanceReportResult{}, err
	}
	out := EquityPerformanceReportResult{Record: record, Events: events, Summary: summary, Daily: daily}
	if !req.RecordDaily || len(daily) == 0 {
		return out, nil
	}
	if s.performance == nil {
		return EquityPerformanceReportResult{}, fmt.Errorf("paper equity performance report requires daily performance repository")
	}
	stats, err := s.performance.RecordDailyPerformance(ctx, daily)
	if err != nil {
		return EquityPerformanceReportResult{}, fmt.Errorf("record paper equity daily performance %q: %w", validationID, err)
	}
	out.DailyStats = stats
	return out, nil
}
