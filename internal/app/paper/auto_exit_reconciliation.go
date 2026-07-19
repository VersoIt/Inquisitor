package paper

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/VersoIt/Inquisitor/internal/backtest"
	domainpaper "github.com/VersoIt/Inquisitor/internal/paper"
)

const defaultExitPositionScanLimit = 1000

type ReconcilePaperExitWithQuoteRequest struct {
	ValidationID      string
	PositionID        string
	CloseID           string
	EventID           string
	Symbol            string
	Interval          string
	Liquidity         backtest.LiquidityRole
	Costs             backtest.CostModel
	AsOf              time.Time
	MaxStaleness      time.Duration
	MaxSpreadBPS      decimal.Decimal
	PositionScanLimit int
	QuoteScanLimit    int
}

type ReconcilePaperExitWithQuoteResult struct {
	Record            domainpaper.ValidationRecord
	Position          domainpaper.OpenPosition
	Quote             SourceOrderbookQuoteResult
	Close             domainpaper.PositionClose
	Event             domainpaper.EquityEvent
	CloseReason       domainpaper.PositionCloseReason
	CloseStats        domainpaper.PositionCloseStats
	EquityStats       domainpaper.EquityEventStats
	ScannedPositions  int
	ClosedPositions   int
	CheckedPositions  int
	PositionFound     bool
	QuoteSourced      bool
	ExitTriggered     bool
	UsedExistingClose bool
}

func (s *Service) ReconcilePaperExitWithQuote(ctx context.Context, req ReconcilePaperExitWithQuoteRequest) (ReconcilePaperExitWithQuoteResult, error) {
	if err := ctx.Err(); err != nil {
		return ReconcilePaperExitWithQuoteResult{}, err
	}
	if s == nil || s.records == nil {
		return ReconcilePaperExitWithQuoteResult{}, fmt.Errorf("paper auto exit reconciliation requires validation record repository")
	}
	if s.positions == nil {
		return ReconcilePaperExitWithQuoteResult{}, fmt.Errorf("paper auto exit reconciliation requires open position repository")
	}
	if s.closes == nil {
		return ReconcilePaperExitWithQuoteResult{}, fmt.Errorf("paper auto exit reconciliation requires position close repository")
	}
	if s.equity == nil {
		return ReconcilePaperExitWithQuoteResult{}, fmt.Errorf("paper auto exit reconciliation requires equity event repository")
	}
	validationID := strings.TrimSpace(req.ValidationID)
	if validationID == "" {
		return ReconcilePaperExitWithQuoteResult{}, fmt.Errorf("validation_id is required")
	}
	positionScanLimit := req.PositionScanLimit
	if positionScanLimit == 0 {
		positionScanLimit = defaultExitPositionScanLimit
	}
	if positionScanLimit < 0 {
		return ReconcilePaperExitWithQuoteResult{}, fmt.Errorf("position_scan_limit must be greater than or equal to zero")
	}

	record, err := s.loadValidationRecord(ctx, validationID)
	if err != nil {
		return ReconcilePaperExitWithQuoteResult{}, err
	}
	if record.Status != domainpaper.ValidationStatusRunning {
		return ReconcilePaperExitWithQuoteResult{}, fmt.Errorf("paper auto exit reconciliation requires RUNNING validation status")
	}

	if positionID := strings.TrimSpace(req.PositionID); positionID != "" {
		return s.reconcileExplicitPaperExit(ctx, record, req, positionID)
	}
	return s.reconcileScannedPaperExit(ctx, record, req, positionScanLimit)
}

func (s *Service) reconcileExplicitPaperExit(
	ctx context.Context,
	record domainpaper.ValidationRecord,
	req ReconcilePaperExitWithQuoteRequest,
	positionID string,
) (ReconcilePaperExitWithQuoteResult, error) {
	position, err := s.loadOpenPosition(ctx, positionID)
	if err != nil {
		return ReconcilePaperExitWithQuoteResult{}, err
	}
	if position.ValidationID != record.ValidationID {
		return ReconcilePaperExitWithQuoteResult{}, fmt.Errorf("paper open position %q belongs to validation %q, not %q", position.PositionID, position.ValidationID, record.ValidationID)
	}
	result := ReconcilePaperExitWithQuoteResult{
		Record:           record,
		Position:         position,
		ScannedPositions: 1,
		PositionFound:    true,
	}
	existingClose, ok, err := s.findExistingCloseForPaperExit(ctx, record.ValidationID, position.PositionID)
	if err != nil {
		return ReconcilePaperExitWithQuoteResult{}, err
	}
	if ok {
		return s.accountExistingPaperExit(ctx, result, req, existingClose)
	}
	return s.evaluateAndSettlePaperExit(ctx, result, req)
}

func (s *Service) reconcileScannedPaperExit(
	ctx context.Context,
	record domainpaper.ValidationRecord,
	req ReconcilePaperExitWithQuoteRequest,
	positionScanLimit int,
) (ReconcilePaperExitWithQuoteResult, error) {
	positions, err := s.positions.ListOpenPositions(ctx, domainpaper.OpenPositionQuery{
		ValidationID: record.ValidationID,
		Symbol:       strings.ToUpper(strings.TrimSpace(req.Symbol)),
		Interval:     strings.TrimSpace(req.Interval),
		Limit:        positionScanLimit,
	})
	if err != nil {
		return ReconcilePaperExitWithQuoteResult{}, fmt.Errorf("list paper open positions for exit reconciliation: %w", err)
	}

	result := ReconcilePaperExitWithQuoteResult{
		Record:           record,
		ScannedPositions: len(positions),
	}
	for _, position := range positions {
		existingClose, ok, err := s.findExistingCloseForPaperExit(ctx, record.ValidationID, position.PositionID)
		if err != nil {
			return ReconcilePaperExitWithQuoteResult{}, err
		}
		if ok {
			result.ClosedPositions++
			accounted, accountErr := s.paperExitCloseHasEquityEvent(ctx, existingClose.ValidationID, existingClose.CloseID)
			if accountErr != nil {
				return ReconcilePaperExitWithQuoteResult{}, accountErr
			}
			if !accounted {
				result.Position = position
				result.PositionFound = true
				return s.accountExistingPaperExit(ctx, result, req, existingClose)
			}
			continue
		}
		result.Position = position
		result.PositionFound = true
		checked, err := s.evaluateAndSettlePaperExit(ctx, result, req)
		if err != nil {
			return ReconcilePaperExitWithQuoteResult{}, err
		}
		result = checked
		if checked.ExitTriggered {
			return checked, nil
		}
	}
	return result, nil
}

func (s *Service) evaluateAndSettlePaperExit(
	ctx context.Context,
	result ReconcilePaperExitWithQuoteResult,
	req ReconcilePaperExitWithQuoteRequest,
) (ReconcilePaperExitWithQuoteResult, error) {
	if s.orderbooks == nil {
		return ReconcilePaperExitWithQuoteResult{}, fmt.Errorf("paper auto exit reconciliation requires orderbook snapshot repository")
	}

	quote, err := s.SourceOrderbookQuote(ctx, SourceOrderbookQuoteRequest{
		Exchange:     result.Position.Exchange,
		Category:     result.Position.Category,
		Symbol:       result.Position.Symbol,
		AsOf:         req.AsOf,
		MaxStaleness: req.MaxStaleness,
		MaxSpreadBPS: req.MaxSpreadBPS,
		ScanLimit:    req.QuoteScanLimit,
	})
	if err != nil {
		return ReconcilePaperExitWithQuoteResult{}, err
	}
	result.Quote = quote
	result.QuoteSourced = true
	result.CheckedPositions++

	reason, triggered := paperExitTriggerReason(result.Position, quote.MidPrice)
	if !triggered {
		return result, nil
	}
	closeID := defaultPaperExitCloseID(result.Position.PositionID, req.CloseID)
	eventID := defaultPaperExitEventID(closeID, req.EventID)
	settled, err := s.SettlePositionAtMarket(ctx, SettlePositionAtMarketRequest{
		EventID:      eventID,
		CloseID:      closeID,
		PositionID:   result.Position.PositionID,
		Liquidity:    req.Liquidity,
		ExitMidPrice: quote.MidPrice,
		Costs:        req.Costs,
		CloseReason:  reason,
		ClosedAt:     quote.Snapshot.ExchangeTime,
	})
	if err != nil {
		return ReconcilePaperExitWithQuoteResult{}, err
	}
	result.Record = settled.Record
	result.Position = settled.Position
	result.Close = settled.Close
	result.Event = settled.Event
	result.CloseReason = reason
	result.CloseStats = settled.CloseStats
	result.EquityStats = settled.EquityStats
	result.ExitTriggered = true
	return result, nil
}

func (s *Service) accountExistingPaperExit(
	ctx context.Context,
	result ReconcilePaperExitWithQuoteResult,
	req ReconcilePaperExitWithQuoteRequest,
	existingClose domainpaper.PositionClose,
) (ReconcilePaperExitWithQuoteResult, error) {
	if requestedCloseID := strings.TrimSpace(req.CloseID); requestedCloseID != "" && requestedCloseID != existingClose.CloseID {
		return ReconcilePaperExitWithQuoteResult{}, fmt.Errorf("paper open position %q already has close %q, not requested close %q", result.Position.PositionID, existingClose.CloseID, requestedCloseID)
	}
	eventID, err := s.paperExitEventIDForExistingClose(ctx, existingClose.ValidationID, existingClose.CloseID, req.EventID)
	if err != nil {
		return ReconcilePaperExitWithQuoteResult{}, err
	}
	accounted, err := s.AccountPositionClose(ctx, AccountPositionCloseRequest{
		EventID: eventID,
		CloseID: existingClose.CloseID,
	})
	if err != nil {
		return ReconcilePaperExitWithQuoteResult{}, err
	}
	result.Record = accounted.Record
	result.Close = accounted.Close
	result.Event = accounted.Event
	result.CloseReason = accounted.Close.CloseReason
	result.EquityStats = accounted.Stats
	result.ExitTriggered = true
	result.UsedExistingClose = true
	return result, nil
}

func (s *Service) paperExitCloseHasEquityEvent(ctx context.Context, validationID string, closeID string) (bool, error) {
	events, err := s.equity.ListEquityEvents(ctx, domainpaper.EquityEventQuery{
		ValidationID: validationID,
		CloseID:      closeID,
		Limit:        2,
	})
	if err != nil {
		return false, fmt.Errorf("check paper close %q equity ledger: %w", closeID, err)
	}
	if len(events) > 1 {
		return false, fmt.Errorf("paper close %q has an inconsistent equity ledger", closeID)
	}
	return len(events) == 1, nil
}

func (s *Service) paperExitEventIDForExistingClose(ctx context.Context, validationID string, closeID string, requested string) (string, error) {
	if trimmed := strings.TrimSpace(requested); trimmed != "" {
		return trimmed, nil
	}
	events, err := s.equity.ListEquityEvents(ctx, domainpaper.EquityEventQuery{
		ValidationID: validationID,
		CloseID:      closeID,
		Limit:        2,
	})
	if err != nil {
		return "", fmt.Errorf("check paper close %q equity ledger: %w", closeID, err)
	}
	if len(events) > 1 {
		return "", fmt.Errorf("paper close %q has an inconsistent equity ledger", closeID)
	}
	if len(events) == 1 {
		return events[0].EventID, nil
	}
	return defaultPaperExitEventID(closeID, ""), nil
}

func (s *Service) findExistingCloseForPaperExit(ctx context.Context, validationID string, positionID string) (domainpaper.PositionClose, bool, error) {
	closes, err := s.closes.ListPositionCloses(ctx, domainpaper.PositionCloseQuery{
		ValidationID: validationID,
		PositionID:   positionID,
		Limit:        2,
	})
	if err != nil {
		return domainpaper.PositionClose{}, false, fmt.Errorf("check paper position %q close journal: %w", positionID, err)
	}
	if len(closes) > 1 {
		return domainpaper.PositionClose{}, false, fmt.Errorf("paper position %q has an inconsistent close journal", positionID)
	}
	if len(closes) == 0 {
		return domainpaper.PositionClose{}, false, nil
	}
	return closes[0], true, nil
}

func paperExitTriggerReason(position domainpaper.OpenPosition, midPrice decimal.Decimal) (domainpaper.PositionCloseReason, bool) {
	switch position.Side {
	case domainpaper.OrderSideLong:
		if midPrice.LessThanOrEqual(position.StopLoss) {
			return domainpaper.PositionCloseReasonStopLoss, true
		}
		if position.TakeProfit.IsPositive() && midPrice.GreaterThanOrEqual(position.TakeProfit) {
			return domainpaper.PositionCloseReasonTakeProfit, true
		}
	case domainpaper.OrderSideShort:
		if midPrice.GreaterThanOrEqual(position.StopLoss) {
			return domainpaper.PositionCloseReasonStopLoss, true
		}
		if position.TakeProfit.IsPositive() && midPrice.LessThanOrEqual(position.TakeProfit) {
			return domainpaper.PositionCloseReasonTakeProfit, true
		}
	}
	return "", false
}

func defaultPaperExitCloseID(positionID string, requested string) string {
	if trimmed := strings.TrimSpace(requested); trimmed != "" {
		return trimmed
	}
	return strings.TrimSpace(positionID) + "_close"
}

func defaultPaperExitEventID(closeID string, requested string) string {
	if trimmed := strings.TrimSpace(requested); trimmed != "" {
		return trimmed
	}
	return strings.TrimSpace(closeID) + "_equity"
}
