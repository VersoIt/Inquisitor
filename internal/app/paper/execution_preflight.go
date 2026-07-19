package paper

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	domainpaper "github.com/VersoIt/Inquisitor/internal/paper"
)

type PreflightPaperExecutionCycleRequest struct {
	ValidationID      string
	Exchange          string
	Category          string
	Symbol            string
	Interval          string
	AsOf              time.Time
	MaxStaleness      time.Duration
	MaxSpreadBPS      decimal.Decimal
	PendingScanLimit  int
	PositionScanLimit int
	QuoteScanLimit    int
}

type PreflightPaperExecutionCycleResult struct {
	Record                     domainpaper.ValidationRecord
	Quote                      SourceOrderbookQuoteResult
	QuoteSourced               bool
	KillSwitchActive           bool
	KillSwitchReason           string
	KillSwitchSource           string
	EntryBlockedByKillSwitch   bool
	PendingTickets             int
	ScannedTickets             int
	FilledTickets              int
	ScannedPositions           int
	ActivePositions            int
	ClosedPositions            int
	AccountedClosedPositions   int
	UnaccountedClosedPositions int
}

func (s *Service) PreflightPaperExecutionCycle(ctx context.Context, req PreflightPaperExecutionCycleRequest) (PreflightPaperExecutionCycleResult, error) {
	if err := ctx.Err(); err != nil {
		return PreflightPaperExecutionCycleResult{}, err
	}
	if s == nil || s.records == nil {
		return PreflightPaperExecutionCycleResult{}, fmt.Errorf("paper execution cycle preflight requires validation record repository")
	}
	if s.tickets == nil {
		return PreflightPaperExecutionCycleResult{}, fmt.Errorf("paper execution cycle preflight requires order ticket repository")
	}
	if s.fills == nil {
		return PreflightPaperExecutionCycleResult{}, fmt.Errorf("paper execution cycle preflight requires order fill repository")
	}
	if s.positions == nil {
		return PreflightPaperExecutionCycleResult{}, fmt.Errorf("paper execution cycle preflight requires open position repository")
	}
	if s.closes == nil {
		return PreflightPaperExecutionCycleResult{}, fmt.Errorf("paper execution cycle preflight requires position close repository")
	}
	if s.equity == nil {
		return PreflightPaperExecutionCycleResult{}, fmt.Errorf("paper execution cycle preflight requires equity event repository")
	}
	if s.killSwitch == nil {
		return PreflightPaperExecutionCycleResult{}, fmt.Errorf("paper execution cycle preflight requires kill switch repository")
	}
	if s.orderbooks == nil {
		return PreflightPaperExecutionCycleResult{}, fmt.Errorf("paper execution cycle preflight requires orderbook snapshot repository")
	}

	exchange := strings.ToLower(strings.TrimSpace(req.Exchange))
	category := strings.ToLower(strings.TrimSpace(req.Category))
	if exchange == "" {
		return PreflightPaperExecutionCycleResult{}, fmt.Errorf("exchange is required")
	}
	if category == "" {
		return PreflightPaperExecutionCycleResult{}, fmt.Errorf("category is required")
	}
	scope, err := requirePaperExecutionCycleScope(req.ValidationID, req.Symbol, req.Interval)
	if err != nil {
		return PreflightPaperExecutionCycleResult{}, err
	}
	if err := validatePaperExecutionCycleScanLimits(req.PendingScanLimit, req.PositionScanLimit, req.QuoteScanLimit); err != nil {
		return PreflightPaperExecutionCycleResult{}, err
	}

	record, err := s.loadValidationRecord(ctx, scope.ValidationID)
	if err != nil {
		return PreflightPaperExecutionCycleResult{}, err
	}
	if record.Status != domainpaper.ValidationStatusRunning {
		return PreflightPaperExecutionCycleResult{}, fmt.Errorf("paper execution cycle preflight requires RUNNING validation status")
	}
	killSwitch, err := s.killSwitch.CurrentKillSwitchState(ctx)
	if err != nil {
		return PreflightPaperExecutionCycleResult{}, fmt.Errorf("load kill switch before execution preflight: %w", err)
	}

	quote, err := s.SourceOrderbookQuote(ctx, SourceOrderbookQuoteRequest{
		Exchange:     exchange,
		Category:     category,
		Symbol:       scope.Symbol,
		AsOf:         req.AsOf,
		MaxStaleness: req.MaxStaleness,
		MaxSpreadBPS: req.MaxSpreadBPS,
		ScanLimit:    req.QuoteScanLimit,
	})
	if err != nil {
		return PreflightPaperExecutionCycleResult{}, err
	}

	pending, err := s.ListPendingOrderTickets(ctx, ListPendingOrderTicketsRequest{
		ValidationID: scope.ValidationID,
		Symbol:       scope.Symbol,
		Interval:     scope.Interval,
		Limit:        preflightResultLimit(req.PendingScanLimit),
		ScanLimit:    req.PendingScanLimit,
	})
	if err != nil {
		return PreflightPaperExecutionCycleResult{}, err
	}

	positionScanLimit := req.PositionScanLimit
	if positionScanLimit == 0 {
		positionScanLimit = defaultExitPositionScanLimit
	}
	positions, err := s.positions.ListOpenPositions(ctx, domainpaper.OpenPositionQuery{
		ValidationID: scope.ValidationID,
		Symbol:       scope.Symbol,
		Interval:     scope.Interval,
		Limit:        positionScanLimit,
	})
	if err != nil {
		return PreflightPaperExecutionCycleResult{}, fmt.Errorf("list paper open positions for execution preflight: %w", err)
	}

	result := PreflightPaperExecutionCycleResult{
		Record:           record,
		Quote:            quote,
		QuoteSourced:     true,
		KillSwitchActive: killSwitch.Active,
		KillSwitchReason: killSwitch.Reason,
		KillSwitchSource: killSwitch.Source,
		PendingTickets:   len(pending.Tickets),
		ScannedTickets:   pending.ScannedTickets,
		FilledTickets:    pending.FilledTickets,
		ScannedPositions: len(positions),
	}
	for _, position := range positions {
		closes, err := s.closes.ListPositionCloses(ctx, domainpaper.PositionCloseQuery{
			ValidationID: record.ValidationID,
			PositionID:   position.PositionID,
			Limit:        2,
		})
		if err != nil {
			return PreflightPaperExecutionCycleResult{}, fmt.Errorf("check paper position %q close status for execution preflight: %w", position.PositionID, err)
		}
		if len(closes) > 1 {
			return PreflightPaperExecutionCycleResult{}, fmt.Errorf("paper position %q has an inconsistent close journal", position.PositionID)
		}
		if len(closes) == 1 {
			result.ClosedPositions++
			accounted, err := s.paperExitCloseHasEquityEvent(ctx, record.ValidationID, closes[0].CloseID)
			if err != nil {
				return PreflightPaperExecutionCycleResult{}, fmt.Errorf("check paper close %q equity status for execution preflight: %w", closes[0].CloseID, err)
			}
			if accounted {
				result.AccountedClosedPositions++
				continue
			}
			result.UnaccountedClosedPositions++
			continue
		}
		result.ActivePositions++
	}
	if result.KillSwitchActive && result.PendingTickets > 0 &&
		result.ActivePositions == 0 && result.UnaccountedClosedPositions == 0 {
		result.EntryBlockedByKillSwitch = true
		return result, fmt.Errorf("paper execution cycle preflight requires inactive kill switch before pending entry: reason=%q source=%q", result.KillSwitchReason, result.KillSwitchSource)
	}
	return result, nil
}

func preflightResultLimit(scanLimit int) int {
	if scanLimit > 0 {
		return scanLimit
	}
	return defaultPendingOrderTicketScanLimit
}
