package paper

import (
	"context"
	"time"

	"github.com/shopspring/decimal"

	"github.com/VersoIt/Inquisitor/internal/backtest"
	domainpaper "github.com/VersoIt/Inquisitor/internal/paper"
)

type PaperExecutionCycleAction string

const (
	PaperExecutionCycleActionNone  PaperExecutionCycleAction = "NONE"
	PaperExecutionCycleActionExit  PaperExecutionCycleAction = "EXIT"
	PaperExecutionCycleActionEntry PaperExecutionCycleAction = "ENTRY"
)

type PaperExecutionCycleSkipReason string

const (
	PaperExecutionCycleSkipNone            PaperExecutionCycleSkipReason = ""
	PaperExecutionCycleSkipActivePosition  PaperExecutionCycleSkipReason = "ACTIVE_POSITION"
	PaperExecutionCycleSkipNoPendingTicket PaperExecutionCycleSkipReason = "NO_PENDING_TICKET"
	PaperExecutionCycleSkipNoExitTrigger   PaperExecutionCycleSkipReason = "NO_EXIT_TRIGGER"
	PaperExecutionCycleSkipNoOpenOrPending PaperExecutionCycleSkipReason = "NO_OPEN_OR_PENDING"
	PaperExecutionCycleSkipAlreadyClosed   PaperExecutionCycleSkipReason = "ALREADY_CLOSED"
)

type RunPaperExecutionCycleRequest struct {
	ValidationID      string
	Symbol            string
	Interval          string
	Liquidity         backtest.LiquidityRole
	Costs             backtest.CostModel
	AsOf              time.Time
	MaxStaleness      time.Duration
	MaxSpreadBPS      decimal.Decimal
	PendingScanLimit  int
	PositionScanLimit int
	QuoteScanLimit    int
}

type RunPaperExecutionCycleResult struct {
	Record          domainpaper.ValidationRecord
	Action          PaperExecutionCycleAction
	SkipReason      PaperExecutionCycleSkipReason
	Exit            ReconcilePaperExitWithQuoteResult
	Entry           ReconcilePaperEntryWithQuoteResult
	PendingTickets  int
	ScannedTickets  int
	FilledTickets   int
	PositionChecked bool
	EntryChecked    bool
}

func (s *Service) RunPaperExecutionCycle(ctx context.Context, req RunPaperExecutionCycleRequest) (RunPaperExecutionCycleResult, error) {
	if err := ctx.Err(); err != nil {
		return RunPaperExecutionCycleResult{}, err
	}
	scope, err := requirePaperExecutionCycleScope(req.ValidationID, req.Symbol, req.Interval)
	if err != nil {
		return RunPaperExecutionCycleResult{}, err
	}
	if err := validatePaperExecutionCycleScanLimits(req.PendingScanLimit, req.PositionScanLimit, req.QuoteScanLimit); err != nil {
		return RunPaperExecutionCycleResult{}, err
	}

	exit, err := s.ReconcilePaperExitWithQuote(ctx, ReconcilePaperExitWithQuoteRequest{
		ValidationID:      scope.ValidationID,
		Symbol:            scope.Symbol,
		Interval:          scope.Interval,
		Liquidity:         req.Liquidity,
		Costs:             req.Costs,
		AsOf:              req.AsOf,
		MaxStaleness:      req.MaxStaleness,
		MaxSpreadBPS:      req.MaxSpreadBPS,
		PositionScanLimit: req.PositionScanLimit,
		QuoteScanLimit:    req.QuoteScanLimit,
	})
	if err != nil {
		return RunPaperExecutionCycleResult{}, err
	}
	result := RunPaperExecutionCycleResult{
		Record:          exit.Record,
		Exit:            exit,
		PositionChecked: true,
	}
	if exit.ExitTriggered {
		result.Action = PaperExecutionCycleActionExit
		return result, nil
	}
	if exit.PositionFound {
		result.Action = PaperExecutionCycleActionNone
		result.SkipReason = PaperExecutionCycleSkipActivePosition
		if exit.CheckedPositions > 0 {
			result.SkipReason = PaperExecutionCycleSkipNoExitTrigger
		}
		return result, nil
	}

	pending, err := s.ListPendingOrderTickets(ctx, ListPendingOrderTicketsRequest{
		ValidationID: scope.ValidationID,
		Symbol:       scope.Symbol,
		Interval:     scope.Interval,
		Limit:        1,
		ScanLimit:    req.PendingScanLimit,
	})
	if err != nil {
		return RunPaperExecutionCycleResult{}, err
	}
	result.Record = pending.Record
	result.PendingTickets = len(pending.Tickets)
	result.ScannedTickets = pending.ScannedTickets
	result.FilledTickets = pending.FilledTickets
	result.EntryChecked = true
	if len(pending.Tickets) == 0 {
		result.Action = PaperExecutionCycleActionNone
		result.SkipReason = PaperExecutionCycleSkipNoPendingTicket
		if exit.ScannedPositions == 0 {
			result.SkipReason = PaperExecutionCycleSkipNoOpenOrPending
		}
		if exit.ClosedPositions > 0 {
			result.SkipReason = PaperExecutionCycleSkipAlreadyClosed
		}
		return result, nil
	}

	entry, err := s.ReconcilePaperEntryWithQuote(ctx, ReconcilePaperEntryWithQuoteRequest{
		ValidationID:     scope.ValidationID,
		TicketID:         pending.Tickets[0].TicketID,
		Symbol:           scope.Symbol,
		Interval:         scope.Interval,
		Liquidity:        req.Liquidity,
		Costs:            req.Costs,
		AsOf:             req.AsOf,
		MaxStaleness:     req.MaxStaleness,
		MaxSpreadBPS:     req.MaxSpreadBPS,
		PendingScanLimit: req.PendingScanLimit,
		QuoteScanLimit:   req.QuoteScanLimit,
	})
	if err != nil {
		return RunPaperExecutionCycleResult{}, err
	}
	result.Record = entry.Record
	result.Action = PaperExecutionCycleActionEntry
	result.Entry = entry
	return result, nil
}
