package main

import (
	"context"
	"flag"
	"fmt"
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

func main() {
	configPath := flag.String("config", "configs/config.example.yaml", "path to YAML config")
	action := flag.String("action", "", "action: pending, enter, fill, settle")
	validationID := flag.String("validation-id", "", "paper validation id for action=pending")
	fillID := flag.String("fill-id", "", "stable paper fill id for action=enter or action=fill")
	ticketID := flag.String("ticket-id", "", "paper order ticket id for action=enter or action=fill")
	eventID := flag.String("event-id", "", "stable paper equity event id for action=settle")
	closeID := flag.String("close-id", "", "stable paper position close id for action=settle")
	positionID := flag.String("position-id", "", "paper open position id for action=enter or action=settle")
	symbol := flag.String("symbol", "", "optional symbol filter for action=pending")
	interval := flag.String("interval", "", "optional interval filter for action=pending")
	pendingLimit := flag.Int("pending-limit", 100, "maximum pending tickets returned by action=pending")
	pendingScanLimit := flag.Int("pending-scan-limit", 1000, "maximum tickets scanned by action=pending")
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
	if actionName != "pending" {
		var err error
		costs, err = paperExecutionCostModel(cfg, *spreadBPS, *slippageBPS)
		if err != nil {
			log.Error("invalid paper execution cost model", "error", err)
			os.Exit(1)
		}
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
	)

	switch actionName {
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
