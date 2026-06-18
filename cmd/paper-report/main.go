package main

import (
	"context"
	"flag"
	"os"
	"strings"

	apppaper "github.com/VersoIt/Inquisitor/internal/app/paper"
	"github.com/VersoIt/Inquisitor/internal/config"
	"github.com/VersoIt/Inquisitor/internal/logger"
	"github.com/VersoIt/Inquisitor/internal/storage/postgres"
)

func main() {
	configPath := flag.String("config", "configs/config.example.yaml", "path to YAML config")
	validationID := flag.String("validation-id", "", "paper validation record id")
	action := flag.String("action", "report", "action: report, start, complete, cancel")
	cancelReason := flag.String("reason", "", "required cancellation reason")
	recordDaily := flag.Bool("record-daily", false, "persist idempotent daily performance snapshots")
	logLevel := flag.String("log-level", "info", "log level: debug, info, warn, error")
	flag.Parse()

	log := logger.New(*logLevel)
	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Error("failed to load config", "error", err)
		os.Exit(1)
	}
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
		apppaper.WithValidationTradeRepository(postgres.NewPaperValidationTradeRepository(db)),
		apppaper.WithDailyPerformanceRepository(postgres.NewPaperDailyPerformanceRepository(db)),
	)

	actionValue := strings.ToLower(strings.TrimSpace(*action))
	switch actionValue {
	case "report":
		report, reportErr := service.BuildPerformanceReport(ctx, apppaper.PerformanceReportRequest{
			ValidationID: *validationID,
			RecordDaily:  *recordDaily,
		})
		if reportErr != nil {
			log.Error("paper performance report failed", "error", reportErr)
			os.Exit(1)
		}
		log.Info(
			"paper performance report built",
			"validation_id", report.Record.ValidationID,
			"status", report.Record.Status,
			"trades", report.Summary.Trades,
			"wins", report.Summary.Wins,
			"losses", report.Summary.Losses,
			"net_pnl", report.Summary.NetPnL.String(),
			"fees", report.Summary.TotalFees.String(),
			"expectancy", report.Summary.Expectancy.String(),
			"win_rate", report.Summary.WinRate.String(),
			"max_drawdown", report.Summary.MaxDrawdown.String(),
			"final_equity", report.Summary.FinalEquity.String(),
			"days", len(report.Daily),
			"daily_inserted", report.DailyStats.Inserted,
			"daily_updated", report.DailyStats.Updated,
		)
	case "start", "complete", "cancel":
		var lifecycle apppaper.LifecycleResult
		if actionValue == "start" {
			lifecycle, err = service.StartValidation(ctx, *validationID)
		} else if actionValue == "complete" {
			lifecycle, err = service.CompleteValidation(ctx, *validationID)
		} else {
			lifecycle, err = service.CancelValidation(ctx, *validationID, *cancelReason)
		}
		if err != nil {
			log.Error("paper validation lifecycle transition failed", "action", *action, "error", err)
			os.Exit(1)
		}
		log.Info(
			"paper validation lifecycle transitioned",
			"validation_id", lifecycle.Record.ValidationID,
			"status", lifecycle.Record.Status,
			"status_reason", lifecycle.Record.StatusReason,
			"started_at", lifecycle.Record.StartedAt,
			"completed_at", lifecycle.Record.CompletedAt,
			"cancelled_at", lifecycle.Record.CancelledAt,
			"updated", lifecycle.Stats.Updated,
		)
	default:
		log.Error("unsupported paper report action", "action", *action)
		os.Exit(1)
	}
}
