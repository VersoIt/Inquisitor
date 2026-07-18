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
	Record           domainpaper.ValidationRecord
	Quote            SourceOrderbookQuoteResult
	QuoteSourced     bool
	PendingTickets   int
	ScannedTickets   int
	FilledTickets    int
	ScannedPositions int
	ActivePositions  int
	ClosedPositions  int
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
	if s.orderbooks == nil {
		return PreflightPaperExecutionCycleResult{}, fmt.Errorf("paper execution cycle preflight requires orderbook snapshot repository")
	}

	validationID := strings.TrimSpace(req.ValidationID)
	exchange := strings.ToLower(strings.TrimSpace(req.Exchange))
	category := strings.ToLower(strings.TrimSpace(req.Category))
	symbol := strings.ToUpper(strings.TrimSpace(req.Symbol))
	interval := strings.TrimSpace(req.Interval)
	if validationID == "" {
		return PreflightPaperExecutionCycleResult{}, fmt.Errorf("validation_id is required")
	}
	if exchange == "" {
		return PreflightPaperExecutionCycleResult{}, fmt.Errorf("exchange is required")
	}
	if category == "" {
		return PreflightPaperExecutionCycleResult{}, fmt.Errorf("category is required")
	}
	if symbol == "" {
		return PreflightPaperExecutionCycleResult{}, fmt.Errorf("symbol is required")
	}
	if interval == "" {
		return PreflightPaperExecutionCycleResult{}, fmt.Errorf("interval is required")
	}
	if req.PendingScanLimit < 0 {
		return PreflightPaperExecutionCycleResult{}, fmt.Errorf("pending_scan_limit must be greater than or equal to zero")
	}
	if req.PositionScanLimit < 0 {
		return PreflightPaperExecutionCycleResult{}, fmt.Errorf("position_scan_limit must be greater than or equal to zero")
	}
	if req.QuoteScanLimit < 0 {
		return PreflightPaperExecutionCycleResult{}, fmt.Errorf("quote_scan_limit must be greater than or equal to zero")
	}

	record, err := s.loadValidationRecord(ctx, validationID)
	if err != nil {
		return PreflightPaperExecutionCycleResult{}, err
	}
	if record.Status != domainpaper.ValidationStatusRunning {
		return PreflightPaperExecutionCycleResult{}, fmt.Errorf("paper execution cycle preflight requires RUNNING validation status")
	}

	quote, err := s.SourceOrderbookQuote(ctx, SourceOrderbookQuoteRequest{
		Exchange:     exchange,
		Category:     category,
		Symbol:       symbol,
		AsOf:         req.AsOf,
		MaxStaleness: req.MaxStaleness,
		MaxSpreadBPS: req.MaxSpreadBPS,
		ScanLimit:    req.QuoteScanLimit,
	})
	if err != nil {
		return PreflightPaperExecutionCycleResult{}, err
	}

	pending, err := s.ListPendingOrderTickets(ctx, ListPendingOrderTicketsRequest{
		ValidationID: validationID,
		Symbol:       symbol,
		Interval:     interval,
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
		ValidationID: validationID,
		Symbol:       symbol,
		Interval:     interval,
		Limit:        positionScanLimit,
	})
	if err != nil {
		return PreflightPaperExecutionCycleResult{}, fmt.Errorf("list paper open positions for execution preflight: %w", err)
	}

	result := PreflightPaperExecutionCycleResult{
		Record:           record,
		Quote:            quote,
		QuoteSourced:     true,
		PendingTickets:   len(pending.Tickets),
		ScannedTickets:   pending.ScannedTickets,
		FilledTickets:    pending.FilledTickets,
		ScannedPositions: len(positions),
	}
	for _, position := range positions {
		closes, err := s.closes.ListPositionCloses(ctx, domainpaper.PositionCloseQuery{
			PositionID: position.PositionID,
			Limit:      2,
		})
		if err != nil {
			return PreflightPaperExecutionCycleResult{}, fmt.Errorf("check paper position %q close status for execution preflight: %w", position.PositionID, err)
		}
		if len(closes) > 1 {
			return PreflightPaperExecutionCycleResult{}, fmt.Errorf("paper position %q has an inconsistent close journal", position.PositionID)
		}
		if len(closes) == 1 {
			result.ClosedPositions++
			continue
		}
		result.ActivePositions++
	}
	return result, nil
}

func preflightResultLimit(scanLimit int) int {
	if scanLimit > 0 {
		return scanLimit
	}
	return defaultPendingOrderTicketScanLimit
}
