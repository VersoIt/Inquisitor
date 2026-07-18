package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"hash/fnv"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	apppaper "github.com/VersoIt/Inquisitor/internal/app/paper"
	domainbacktest "github.com/VersoIt/Inquisitor/internal/backtest"
	"github.com/VersoIt/Inquisitor/internal/config"
	"github.com/VersoIt/Inquisitor/internal/logger"
	domainpaper "github.com/VersoIt/Inquisitor/internal/paper"
	"github.com/VersoIt/Inquisitor/internal/storage/postgres"
)

const maxPaperExecutionCycleLimit = 1000

func main() {
	configPath := flag.String("config", "configs/config.example.yaml", "path to YAML config")
	action := flag.String("action", "", "action: quote, pending, auto-enter, auto-exit, cycle-preflight, auto-cycle, enter, fill, settle")
	validationID := flag.String("validation-id", "", "paper validation id for action=pending, action=auto-enter, action=auto-exit, action=cycle-preflight, or action=auto-cycle")
	fillID := flag.String("fill-id", "", "stable paper fill id for action=auto-enter, action=enter, or action=fill")
	ticketID := flag.String("ticket-id", "", "paper order ticket id for action=auto-enter, action=enter, or action=fill")
	eventID := flag.String("event-id", "", "stable paper equity event id for action=auto-exit or action=settle")
	closeID := flag.String("close-id", "", "stable paper position close id for action=auto-exit or action=settle")
	positionID := flag.String("position-id", "", "paper open position id for action=auto-enter, action=auto-exit, action=enter, or action=settle")
	symbol := flag.String("symbol", "", "optional symbol filter for action=pending, action=auto-enter, or action=auto-exit; required for action=cycle-preflight or action=auto-cycle")
	interval := flag.String("interval", "", "optional interval filter for action=pending, action=auto-enter, or action=auto-exit; required for action=cycle-preflight or action=auto-cycle")
	pendingLimit := flag.Int("pending-limit", 100, "maximum pending tickets returned by action=pending")
	pendingScanLimit := flag.Int("pending-scan-limit", 1000, "maximum tickets scanned by action=pending, action=auto-enter, action=cycle-preflight, or action=auto-cycle")
	positionScanLimit := flag.Int("position-scan-limit", 1000, "maximum open positions scanned by action=auto-exit, action=cycle-preflight, or action=auto-cycle")
	cycleLimit := flag.Int("cycle-limit", 1, "bounded execution cycles for action=auto-cycle")
	cycleDelay := flag.Duration("cycle-delay", 0, "optional delay between action=auto-cycle iterations")
	quoteAsOfValue := flag.String("quote-as-of", "", "quote observation time in RFC3339 format; defaults to now for action=quote, action=auto-enter, action=auto-exit, action=cycle-preflight, or action=auto-cycle")
	quoteScanLimit := flag.Int("quote-scan-limit", 1000, "maximum orderbook snapshots scanned by action=quote, action=auto-enter, action=auto-exit, action=cycle-preflight, or action=auto-cycle")
	midPriceValue := flag.String("mid-price", "", "observed market mid price used for conservative simulated execution")
	liquidityValue := flag.String("liquidity", string(domainbacktest.LiquidityTaker), "simulated liquidity role: MAKER or TAKER")
	closeReasonValue := flag.String("close-reason", string(domainpaper.PositionCloseReasonManual), "close reason for action=settle")
	occurredAtValue := flag.String("at", "", "execution timestamp in RFC3339 format")
	spreadBPS := flag.Int("spread-bps", -1, "conservative spread assumption in bps; defaults to risk.max_spread_bps")
	slippageBPS := flag.Int("slippage-bps", -1, "slippage assumption in bps; defaults to slippage.default_bps")
	logLevel := flag.String("log-level", "info", "log level: debug, info, warn, error")
	flag.Parse()

	log := logger.New(*logLevel)
	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Error("failed to load config", "error", err)
		os.Exit(1)
	}
	actionName := strings.ToLower(strings.TrimSpace(*action))
	if err := paperExecutionSafetyPolicy(cfg); err != nil {
		log.Error("invalid paper execution safety policy", "error", err)
		os.Exit(1)
	}
	var costs domainbacktest.CostModel
	var midPrice decimal.Decimal
	var occurredAt time.Time
	if actionRequiresCosts(actionName) {
		var err error
		costs, err = paperExecutionCostModel(cfg, *spreadBPS, *slippageBPS)
		if err != nil {
			log.Error("invalid paper execution cost model", "error", err)
			os.Exit(1)
		}
	}
	if actionRequiresManualMarketObservation(actionName) {
		var err error
		midPrice, err = parseRequiredDecimal("mid-price", *midPriceValue)
		if err != nil {
			log.Error("invalid mid price", "error", err)
			os.Exit(1)
		}
		occurredAt, err = parseRequiredTime("at", *occurredAtValue)
		if err != nil {
			log.Error("invalid execution timestamp", "error", err)
			os.Exit(1)
		}
	}
	liquidity := domainbacktest.LiquidityRole(strings.ToUpper(strings.TrimSpace(*liquidityValue)))

	ctx := context.Background()
	db, err := postgres.Open(ctx, cfg.Database)
	if err != nil {
		log.Error("failed to connect to postgres", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	research := postgres.NewResearchRunRepository(db)
	service := apppaper.NewService(
		research,
		research,
		apppaper.WithValidationRecordRepository(postgres.NewPaperValidationRepository(db)),
		apppaper.WithOrderTicketRepository(postgres.NewPaperOrderTicketRepository(db)),
		apppaper.WithOrderFillRepository(postgres.NewPaperOrderFillRepository(db)),
		apppaper.WithOpenPositionRepository(postgres.NewPaperOpenPositionRepository(db)),
		apppaper.WithPositionCloseRepository(postgres.NewPaperPositionCloseRepository(db)),
		apppaper.WithEquityEventRepository(postgres.NewPaperEquityEventRepository(db)),
		apppaper.WithOrderbookSnapshotRepository(postgres.NewOrderbookSnapshotRepository(db)),
	)

	switch actionName {
	case "quote":
		asOf, parseErr := parseOptionalTime("quote-as-of", *quoteAsOfValue, time.Now().UTC())
		if parseErr != nil {
			log.Error("invalid quote timestamp", "error", parseErr)
			os.Exit(1)
		}
		result, quoteErr := service.SourceOrderbookQuote(ctx, apppaper.SourceOrderbookQuoteRequest{
			Exchange:     cfg.Exchange.Primary,
			Category:     cfg.Exchange.Category,
			Symbol:       *symbol,
			AsOf:         asOf,
			MaxStaleness: time.Duration(cfg.MarketData.MaxDataStalenessMs) * time.Millisecond,
			MaxSpreadBPS: decimal.NewFromInt(int64(cfg.Risk.MaxSpreadBps)),
			ScanLimit:    *quoteScanLimit,
		})
		if quoteErr != nil {
			log.Error("paper quote sourcing failed", "error", quoteErr)
			os.Exit(1)
		}
		log.Info(
			"paper quote sourced",
			"exchange", result.Snapshot.Exchange,
			"category", result.Snapshot.Category,
			"symbol", result.Snapshot.Symbol,
			"bid", result.Bid.String(),
			"ask", result.Ask.String(),
			"mid_price", result.MidPrice.String(),
			"spread_bps", result.SpreadBPS.String(),
			"age_ms", result.Age.Milliseconds(),
			"exchange_time", result.Snapshot.ExchangeTime.Format(time.RFC3339Nano),
		)
	case "pending":
		result, pendingErr := service.ListPendingOrderTickets(ctx, apppaper.ListPendingOrderTicketsRequest{
			ValidationID: *validationID,
			Symbol:       *symbol,
			Interval:     *interval,
			Limit:        *pendingLimit,
			ScanLimit:    *pendingScanLimit,
		})
		if pendingErr != nil {
			log.Error("paper pending ticket selection failed", "error", pendingErr)
			os.Exit(1)
		}
		log.Info(
			"paper pending ticket selection completed",
			"validation_id", result.Record.ValidationID,
			"status", result.Record.Status,
			"pending", len(result.Tickets),
			"scanned", result.ScannedTickets,
			"filled", result.FilledTickets,
		)
		for _, ticket := range result.Tickets {
			log.Info(
				"paper pending ticket",
				"ticket_id", ticket.TicketID,
				"validation_id", ticket.ValidationID,
				"symbol", ticket.Symbol,
				"interval", ticket.Interval,
				"side", ticket.Side,
				"quantity", ticket.Quantity.String(),
				"entry_price", ticket.EntryPrice.String(),
				"stop_loss", ticket.StopLoss.String(),
				"take_profit", ticket.TakeProfit.String(),
				"created_at", ticket.CreatedAt.Format(time.RFC3339),
			)
		}
	case "auto-enter":
		asOf, parseErr := parseOptionalTime("quote-as-of", *quoteAsOfValue, time.Now().UTC())
		if parseErr != nil {
			log.Error("invalid quote timestamp", "error", parseErr)
			os.Exit(1)
		}
		result, enterErr := service.ReconcilePaperEntryWithQuote(ctx, apppaper.ReconcilePaperEntryWithQuoteRequest{
			ValidationID:     *validationID,
			TicketID:         *ticketID,
			FillID:           *fillID,
			PositionID:       *positionID,
			Symbol:           *symbol,
			Interval:         *interval,
			Liquidity:        liquidity,
			Costs:            costs,
			AsOf:             asOf,
			MaxStaleness:     time.Duration(cfg.MarketData.MaxDataStalenessMs) * time.Millisecond,
			MaxSpreadBPS:     decimal.NewFromInt(int64(cfg.Risk.MaxSpreadBps)),
			PendingScanLimit: *pendingScanLimit,
			QuoteScanLimit:   *quoteScanLimit,
		})
		if enterErr != nil {
			log.Error("paper auto entry reconciliation failed", "error", enterErr)
			os.Exit(1)
		}
		log.Info(
			"paper auto entry reconciliation recorded",
			"validation_id", result.Record.ValidationID,
			"ticket_id", result.Ticket.TicketID,
			"fill_id", result.Fill.FillID,
			"position_id", result.Position.PositionID,
			"quote_sourced", result.QuoteSourced,
			"used_existing_fill", result.UsedExistingFill,
			"liquidity", result.Fill.Liquidity,
			"mid_price", result.Fill.MidPrice.String(),
			"executed_price", result.Fill.ExecutedPrice.String(),
			"quantity", result.Fill.Quantity.String(),
			"notional", result.Fill.Notional.String(),
			"fee", result.Fill.Fee.String(),
			"open_risk", result.Position.OpenRisk.String(),
			"fill_inserted", result.FillStats.Inserted,
			"fill_skipped", result.FillStats.Skipped,
			"position_inserted", result.PositionStats.Inserted,
			"position_skipped", result.PositionStats.Skipped,
		)
	case "auto-exit":
		asOf, parseErr := parseOptionalTime("quote-as-of", *quoteAsOfValue, time.Now().UTC())
		if parseErr != nil {
			log.Error("invalid quote timestamp", "error", parseErr)
			os.Exit(1)
		}
		result, exitErr := service.ReconcilePaperExitWithQuote(ctx, apppaper.ReconcilePaperExitWithQuoteRequest{
			ValidationID:      *validationID,
			PositionID:        *positionID,
			CloseID:           *closeID,
			EventID:           *eventID,
			Symbol:            *symbol,
			Interval:          *interval,
			Liquidity:         liquidity,
			Costs:             costs,
			AsOf:              asOf,
			MaxStaleness:      time.Duration(cfg.MarketData.MaxDataStalenessMs) * time.Millisecond,
			MaxSpreadBPS:      decimal.NewFromInt(int64(cfg.Risk.MaxSpreadBps)),
			PositionScanLimit: *positionScanLimit,
			QuoteScanLimit:    *quoteScanLimit,
		})
		if exitErr != nil {
			log.Error("paper auto exit reconciliation failed", "error", exitErr)
			os.Exit(1)
		}
		if !result.PositionFound {
			log.Info(
				"paper auto exit reconciliation skipped",
				"validation_id", result.Record.ValidationID,
				"reason", "no_open_position",
				"scanned", result.ScannedPositions,
				"closed", result.ClosedPositions,
			)
			return
		}
		if !result.ExitTriggered {
			log.Info(
				"paper auto exit reconciliation skipped",
				"validation_id", result.Record.ValidationID,
				"position_id", result.Position.PositionID,
				"reason", "no_exit_trigger",
				"mid_price", result.Quote.MidPrice.String(),
				"stop_loss", result.Position.StopLoss.String(),
				"take_profit", result.Position.TakeProfit.String(),
				"scanned", result.ScannedPositions,
				"closed", result.ClosedPositions,
				"checked", result.CheckedPositions,
			)
			return
		}
		log.Info(
			"paper auto exit reconciliation recorded",
			"validation_id", result.Record.ValidationID,
			"position_id", result.Position.PositionID,
			"close_id", result.Close.CloseID,
			"event_id", result.Event.EventID,
			"quote_sourced", result.QuoteSourced,
			"used_existing_close", result.UsedExistingClose,
			"close_reason", result.CloseReason,
			"liquidity", result.Close.Liquidity,
			"exit_mid_price", result.Close.ExitMidPrice.String(),
			"exit_price", result.Close.ExitPrice.String(),
			"exit_fee", result.Close.ExitFee.String(),
			"net_pnl", result.Close.NetPnL.String(),
			"equity_after", result.Event.EquityAfter.String(),
			"scanned", result.ScannedPositions,
			"closed", result.ClosedPositions,
			"checked", result.CheckedPositions,
			"close_inserted", result.CloseStats.Inserted,
			"close_skipped", result.CloseStats.Skipped,
			"equity_inserted", result.EquityStats.Inserted,
			"equity_skipped", result.EquityStats.Skipped,
		)
	case "cycle-preflight":
		scope, scopeErr := requirePaperExecutionCycleScope(*validationID, *symbol, *interval)
		if scopeErr != nil {
			log.Error("invalid paper execution cycle scope", "error", scopeErr)
			os.Exit(1)
		}
		asOf, parseErr := parseOptionalTime("quote-as-of", *quoteAsOfValue, time.Now().UTC())
		if parseErr != nil {
			log.Error("invalid quote timestamp", "error", parseErr)
			os.Exit(1)
		}
		result, preflightErr := service.PreflightPaperExecutionCycle(ctx, apppaper.PreflightPaperExecutionCycleRequest{
			ValidationID:      scope.ValidationID,
			Exchange:          cfg.Exchange.Primary,
			Category:          cfg.Exchange.Category,
			Symbol:            scope.Symbol,
			Interval:          scope.Interval,
			AsOf:              asOf,
			MaxStaleness:      time.Duration(cfg.MarketData.MaxDataStalenessMs) * time.Millisecond,
			MaxSpreadBPS:      decimal.NewFromInt(int64(cfg.Risk.MaxSpreadBps)),
			PendingScanLimit:  *pendingScanLimit,
			PositionScanLimit: *positionScanLimit,
			QuoteScanLimit:    *quoteScanLimit,
		})
		if preflightErr != nil {
			log.Error("paper execution cycle preflight failed", "error", preflightErr)
			os.Exit(1)
		}
		log.Info(
			"paper execution cycle preflight completed",
			"validation_id", result.Record.ValidationID,
			"status", result.Record.Status,
			"symbol", scope.Symbol,
			"interval", scope.Interval,
			"quote_sourced", result.QuoteSourced,
			"mid_price", result.Quote.MidPrice.String(),
			"spread_bps", result.Quote.SpreadBPS.String(),
			"quote_age_ms", result.Quote.Age.Milliseconds(),
			"pending", result.PendingTickets,
			"scanned_tickets", result.ScannedTickets,
			"filled_tickets", result.FilledTickets,
			"active_positions", result.ActivePositions,
			"closed_positions", result.ClosedPositions,
			"scanned_positions", result.ScannedPositions,
		)
	case "auto-cycle":
		if *cycleLimit <= 0 || *cycleLimit > maxPaperExecutionCycleLimit {
			log.Error("invalid paper execution cycle limit", "cycle_limit", *cycleLimit, "max", maxPaperExecutionCycleLimit)
			os.Exit(1)
		}
		if *cycleDelay < 0 {
			log.Error("invalid paper execution cycle delay", "cycle_delay", cycleDelay.String())
			os.Exit(1)
		}
		scope, scopeErr := requirePaperExecutionCycleScope(*validationID, *symbol, *interval)
		if scopeErr != nil {
			log.Error("invalid paper execution cycle scope", "error", scopeErr)
			os.Exit(1)
		}
		unlock, lockErr := acquirePaperExecutionCycleLock(ctx, db, scope)
		if lockErr != nil {
			log.Error("paper execution cycle lock failed", "error", lockErr)
			os.Exit(1)
		}
		defer func() {
			if unlockErr := unlock(context.Background()); unlockErr != nil {
				log.Error("paper execution cycle lock release failed", "error", unlockErr)
			}
		}()
		for cycle := 1; cycle <= *cycleLimit; cycle++ {
			asOf, parseErr := parseOptionalTime("quote-as-of", *quoteAsOfValue, time.Now().UTC())
			if parseErr != nil {
				log.Error("invalid quote timestamp", "error", parseErr)
				os.Exit(1)
			}
			result, cycleErr := service.RunPaperExecutionCycle(ctx, apppaper.RunPaperExecutionCycleRequest{
				ValidationID:      scope.ValidationID,
				Symbol:            scope.Symbol,
				Interval:          scope.Interval,
				Liquidity:         liquidity,
				Costs:             costs,
				AsOf:              asOf,
				MaxStaleness:      time.Duration(cfg.MarketData.MaxDataStalenessMs) * time.Millisecond,
				MaxSpreadBPS:      decimal.NewFromInt(int64(cfg.Risk.MaxSpreadBps)),
				PendingScanLimit:  *pendingScanLimit,
				PositionScanLimit: *positionScanLimit,
				QuoteScanLimit:    *quoteScanLimit,
			})
			if cycleErr != nil {
				log.Error("paper execution cycle failed", "cycle", cycle, "error", cycleErr)
				os.Exit(1)
			}
			logPaperExecutionCycleResult(log, cycle, result)
			if cycle < *cycleLimit && *cycleDelay > 0 {
				time.Sleep(*cycleDelay)
			}
		}
	case "enter":
		result, enterErr := service.ReconcileTicketFillAtMarket(ctx, apppaper.ReconcileTicketFillAtMarketRequest{
			FillID:     *fillID,
			PositionID: *positionID,
			TicketID:   *ticketID,
			Liquidity:  liquidity,
			MidPrice:   midPrice,
			Costs:      costs,
			FilledAt:   occurredAt,
		})
		if enterErr != nil {
			log.Error("paper entry reconciliation failed", "error", enterErr)
			os.Exit(1)
		}
		log.Info(
			"paper entry reconciliation recorded",
			"validation_id", result.Record.ValidationID,
			"ticket_id", result.Ticket.TicketID,
			"fill_id", result.Fill.FillID,
			"position_id", result.Position.PositionID,
			"liquidity", result.Fill.Liquidity,
			"mid_price", result.Fill.MidPrice.String(),
			"executed_price", result.Fill.ExecutedPrice.String(),
			"quantity", result.Fill.Quantity.String(),
			"notional", result.Fill.Notional.String(),
			"fee", result.Fill.Fee.String(),
			"open_risk", result.Position.OpenRisk.String(),
			"fill_inserted", result.FillStats.Inserted,
			"fill_skipped", result.FillStats.Skipped,
			"position_inserted", result.PositionStats.Inserted,
			"position_skipped", result.PositionStats.Skipped,
		)
	case "fill":
		result, fillErr := service.SimulateOrderFill(ctx, apppaper.SimulateOrderFillRequest{
			FillID:    *fillID,
			TicketID:  *ticketID,
			Liquidity: liquidity,
			MidPrice:  midPrice,
			Costs:     costs,
			FilledAt:  occurredAt,
		})
		if fillErr != nil {
			log.Error("paper simulated fill failed", "error", fillErr)
			os.Exit(1)
		}
		log.Info(
			"paper simulated fill recorded",
			"validation_id", result.Record.ValidationID,
			"ticket_id", result.Ticket.TicketID,
			"fill_id", result.Fill.FillID,
			"liquidity", result.Fill.Liquidity,
			"mid_price", result.Fill.MidPrice.String(),
			"executed_price", result.Fill.ExecutedPrice.String(),
			"quantity", result.Fill.Quantity.String(),
			"notional", result.Fill.Notional.String(),
			"fee", result.Fill.Fee.String(),
			"inserted", result.Stats.Inserted,
			"skipped", result.Stats.Skipped,
		)
	case "settle":
		result, settleErr := service.SettlePositionAtMarket(ctx, apppaper.SettlePositionAtMarketRequest{
			EventID:      *eventID,
			CloseID:      *closeID,
			PositionID:   *positionID,
			Liquidity:    liquidity,
			ExitMidPrice: midPrice,
			Costs:        costs,
			CloseReason:  domainpaper.PositionCloseReason(strings.ToUpper(strings.TrimSpace(*closeReasonValue))),
			ClosedAt:     occurredAt,
		})
		if settleErr != nil {
			log.Error("paper position settlement failed", "error", settleErr)
			os.Exit(1)
		}
		log.Info(
			"paper position settlement recorded",
			"validation_id", result.Record.ValidationID,
			"position_id", result.Position.PositionID,
			"close_id", result.Close.CloseID,
			"event_id", result.Event.EventID,
			"liquidity", result.Close.Liquidity,
			"exit_mid_price", result.Close.ExitMidPrice.String(),
			"exit_price", result.Close.ExitPrice.String(),
			"exit_fee", result.Close.ExitFee.String(),
			"net_pnl", result.Close.NetPnL.String(),
			"equity_after", result.Event.EquityAfter.String(),
			"close_inserted", result.CloseStats.Inserted,
			"close_skipped", result.CloseStats.Skipped,
			"equity_inserted", result.EquityStats.Inserted,
			"equity_skipped", result.EquityStats.Skipped,
		)
	default:
		log.Error("unsupported paper execution action", "action", *action)
		os.Exit(1)
	}
}

func actionRequiresCosts(action string) bool {
	switch action {
	case "auto-enter", "auto-exit", "cycle-preflight", "auto-cycle", "enter", "fill", "settle":
		return true
	default:
		return false
	}
}

func actionRequiresManualMarketObservation(action string) bool {
	switch action {
	case "enter", "fill", "settle":
		return true
	default:
		return false
	}
}

type paperExecutionLogger interface {
	Info(msg string, keyValues ...any)
}

type paperExecutionCycleScope struct {
	ValidationID string
	Symbol       string
	Interval     string
}

type paperExecutionCycleUnlock func(context.Context) error

func requirePaperExecutionCycleScope(validationIDValue string, symbolValue string, intervalValue string) (paperExecutionCycleScope, error) {
	validationID := strings.TrimSpace(validationIDValue)
	symbol := strings.ToUpper(strings.TrimSpace(symbolValue))
	interval := strings.TrimSpace(intervalValue)
	if validationID == "" {
		return paperExecutionCycleScope{}, fmt.Errorf("validation_id is required for paper execution cycle")
	}
	if symbol == "" {
		return paperExecutionCycleScope{}, fmt.Errorf("symbol is required for paper execution cycle")
	}
	if interval == "" {
		return paperExecutionCycleScope{}, fmt.Errorf("interval is required for paper execution cycle")
	}
	return paperExecutionCycleScope{ValidationID: validationID, Symbol: symbol, Interval: interval}, nil
}

func acquirePaperExecutionCycleLock(ctx context.Context, db *sql.DB, scope paperExecutionCycleScope) (paperExecutionCycleUnlock, error) {
	if db == nil {
		return nil, fmt.Errorf("database is required")
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("reserve advisory lock connection: %w", err)
	}

	key := paperExecutionCycleLockKey(scope)
	var acquired bool
	if err := conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock($1)`, key).Scan(&acquired); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("acquire paper execution cycle advisory lock: %w", err)
	}
	if !acquired {
		_ = conn.Close()
		return nil, fmt.Errorf("paper execution cycle already running for validation_id=%q symbol=%q interval=%q", scope.ValidationID, scope.Symbol, scope.Interval)
	}

	return func(unlockCtx context.Context) error {
		var released bool
		err := conn.QueryRowContext(unlockCtx, `SELECT pg_advisory_unlock($1)`, key).Scan(&released)
		closeErr := conn.Close()
		if err != nil {
			return fmt.Errorf("release paper execution cycle advisory lock: %w", err)
		}
		if !released {
			return fmt.Errorf("release paper execution cycle advisory lock: lock was not held")
		}
		if closeErr != nil {
			return fmt.Errorf("close advisory lock connection: %w", closeErr)
		}
		return nil
	}, nil
}

func paperExecutionCycleLockKey(scope paperExecutionCycleScope) int64 {
	hash := fnv.New64a()
	_, _ = hash.Write([]byte("paper-execute:auto-cycle"))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(scope.ValidationID))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(scope.Symbol))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(scope.Interval))
	return int64(hash.Sum64())
}

func logPaperExecutionCycleResult(log paperExecutionLogger, cycle int, result apppaper.RunPaperExecutionCycleResult) {
	switch result.Action {
	case apppaper.PaperExecutionCycleActionExit:
		log.Info(
			"paper execution cycle recorded exit",
			"cycle", cycle,
			"validation_id", result.Record.ValidationID,
			"position_id", result.Exit.Position.PositionID,
			"close_id", result.Exit.Close.CloseID,
			"event_id", result.Exit.Event.EventID,
			"close_reason", result.Exit.CloseReason,
			"used_existing_close", result.Exit.UsedExistingClose,
			"net_pnl", result.Exit.Close.NetPnL.String(),
			"equity_after", result.Exit.Event.EquityAfter.String(),
			"scanned_positions", result.Exit.ScannedPositions,
			"closed_positions", result.Exit.ClosedPositions,
			"checked_positions", result.Exit.CheckedPositions,
			"close_inserted", result.Exit.CloseStats.Inserted,
			"close_skipped", result.Exit.CloseStats.Skipped,
			"equity_inserted", result.Exit.EquityStats.Inserted,
			"equity_skipped", result.Exit.EquityStats.Skipped,
		)
	case apppaper.PaperExecutionCycleActionEntry:
		log.Info(
			"paper execution cycle recorded entry",
			"cycle", cycle,
			"validation_id", result.Record.ValidationID,
			"ticket_id", result.Entry.Ticket.TicketID,
			"fill_id", result.Entry.Fill.FillID,
			"position_id", result.Entry.Position.PositionID,
			"used_existing_fill", result.Entry.UsedExistingFill,
			"mid_price", result.Entry.Fill.MidPrice.String(),
			"executed_price", result.Entry.Fill.ExecutedPrice.String(),
			"open_risk", result.Entry.Position.OpenRisk.String(),
			"pending", result.PendingTickets,
			"scanned_tickets", result.ScannedTickets,
			"filled_tickets", result.FilledTickets,
			"fill_inserted", result.Entry.FillStats.Inserted,
			"fill_skipped", result.Entry.FillStats.Skipped,
			"position_inserted", result.Entry.PositionStats.Inserted,
			"position_skipped", result.Entry.PositionStats.Skipped,
		)
	default:
		log.Info(
			"paper execution cycle skipped",
			"cycle", cycle,
			"validation_id", result.Record.ValidationID,
			"skip_reason", result.SkipReason,
			"exit_position_found", result.Exit.PositionFound,
			"exit_triggered", result.Exit.ExitTriggered,
			"scanned_positions", result.Exit.ScannedPositions,
			"closed_positions", result.Exit.ClosedPositions,
			"checked_positions", result.Exit.CheckedPositions,
			"pending", result.PendingTickets,
			"scanned_tickets", result.ScannedTickets,
			"filled_tickets", result.FilledTickets,
		)
	}
}

func paperExecutionCostModel(cfg *config.Config, spreadBPSOverride int, slippageBPSOverride int) (domainbacktest.CostModel, error) {
	spreadBPS := spreadBPSOverride
	if spreadBPS < 0 {
		spreadBPS = cfg.Risk.MaxSpreadBps
	}
	slippageBPS := slippageBPSOverride
	if slippageBPS < 0 {
		slippageBPS = cfg.Slippage.DefaultBps
	}
	return domainbacktest.NewCostModel(
		cfg.Fees.MakerBps,
		cfg.Fees.TakerBps,
		spreadBPS,
		slippageBPS,
		cfg.Slippage.ConservativeMultiplier,
	)
}

func paperExecutionSafetyPolicy(cfg *config.Config) error {
	initialBalance := strconv.FormatFloat(cfg.Paper.InitialBalance, 'f', -1, 64)
	parsedBalance, err := decimal.NewFromString(initialBalance)
	if err != nil {
		return err
	}
	policy := domainpaper.SafetyPolicy{
		TradingEnabled:              cfg.Trading.Enabled,
		TradingMode:                 cfg.Trading.Mode,
		AllowLive:                   cfg.Trading.AllowLive,
		WithdrawalPermissionAllowed: cfg.Live.WithdrawalPermissionAllowed,
		InitialBalance:              parsedBalance,
		MinimumDays:                 cfg.Paper.MinimumDays,
		SimulateFees:                cfg.Paper.SimulateFees,
		SimulateSlippage:            cfg.Paper.SimulateSlippage,
		SimulateSpread:              cfg.Paper.SimulateSpread,
	}
	if err := domainpaper.ValidateSafetyPolicy(policy); err != nil {
		return err
	}
	if !policy.TradingEnabled {
		return fmt.Errorf("paper trading must be enabled")
	}
	if strings.ToLower(strings.TrimSpace(policy.TradingMode)) != "paper" {
		return fmt.Errorf("trading mode must be paper")
	}
	if policy.AllowLive {
		return fmt.Errorf("live trading must be disabled")
	}
	if policy.WithdrawalPermissionAllowed {
		return fmt.Errorf("withdrawal permission must be disabled")
	}
	if !policy.SimulateFees || !policy.SimulateSlippage || !policy.SimulateSpread {
		return fmt.Errorf("paper execution must include fees, slippage, and spread")
	}
	return nil
}

func parseRequiredDecimal(field string, value string) (decimal.Decimal, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return decimal.Zero, fmt.Errorf("%s is required", field)
	}
	parsed, err := decimal.NewFromString(trimmed)
	if err != nil {
		return decimal.Zero, fmt.Errorf("%s must be a decimal string: %w", field, err)
	}
	return parsed, nil
}

func parseRequiredTime(field string, value string) (time.Time, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return time.Time{}, fmt.Errorf("%s is required", field)
	}
	parsed, err := time.Parse(time.RFC3339, trimmed)
	if err != nil {
		return time.Time{}, fmt.Errorf("%s must be RFC3339: %w", field, err)
	}
	return parsed.UTC(), nil
}

func parseOptionalTime(field string, value string, fallback time.Time) (time.Time, error) {
	if strings.TrimSpace(value) == "" {
		return fallback.UTC(), nil
	}
	return parseRequiredTime(field, value)
}
