package main

import (
	"context"
	"flag"
	"os"
	"strings"

	appresearch "github.com/VersoIt/Inquisitor/internal/app/research"
	"github.com/VersoIt/Inquisitor/internal/config"
	"github.com/VersoIt/Inquisitor/internal/logger"
	domainresearch "github.com/VersoIt/Inquisitor/internal/research"
	"github.com/VersoIt/Inquisitor/internal/storage/postgres"
)

func main() {
	configPath := flag.String("config", "configs/config.example.yaml", "path to YAML config")
	runID := flag.String("run-id", "", "research run id")
	finalStatus := flag.String("final-status", string(domainresearch.StatusFailed), "final status: COMPLETED, FAILED, CANCELLED")
	outcome := flag.String("outcome", string(domainresearch.OutcomeNotExecuted), "outcome: NOT_EXECUTED, INCONCLUSIVE, REJECTED, CANDIDATE")
	summary := flag.String("summary", "Strategy executor is intentionally not implemented yet.", "human-readable result summary")
	reasonsValue := flag.String("reasons", "scaffold_only", "optional comma-separated reasons")
	trades := flag.Int("trades", 0, "number of evaluated trades")
	feesIncluded := flag.Bool("fees-included", false, "whether fees were included in evaluated metrics")
	spreadIncluded := flag.Bool("spread-included", false, "whether spread was included in evaluated metrics")
	slippageIncluded := flag.Bool("slippage-included", false, "whether slippage was included in evaluated metrics")
	outOfSample := flag.Bool("out-of-sample", false, "whether out-of-sample validation was included")
	walkForward := flag.Bool("walk-forward", false, "whether walk-forward validation was included")
	regimeAnalysis := flag.Bool("regime-analysis", false, "whether regime analysis was included")
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

	recorder := postgres.NewResearchRunRepository(db)
	result, err := appresearch.NewService(
		nil,
		recorder,
		appresearch.WithResultRecorder(recorder),
	).RecordResult(ctx, appresearch.RecordResultRequest{
		RunID:       *runID,
		FinalStatus: domainresearch.Status(strings.ToUpper(strings.TrimSpace(*finalStatus))),
		Outcome:     domainresearch.Outcome(strings.ToUpper(strings.TrimSpace(*outcome))),
		Summary:     *summary,
		Metrics: domainresearch.Metrics{
			Trades:                 *trades,
			FeesIncluded:           *feesIncluded,
			SpreadIncluded:         *spreadIncluded,
			SlippageIncluded:       *slippageIncluded,
			OutOfSample:            *outOfSample,
			WalkForward:            *walkForward,
			RegimeAnalysisIncluded: *regimeAnalysis,
		},
		Reasons: splitCSV(*reasonsValue),
	})
	if err != nil {
		log.Error("research result recording failed", "error", err)
		os.Exit(1)
	}

	log.Info(
		"research result recorded",
		"run_id", result.Run.RunID,
		"final_status", result.Result.FinalStatus,
		"outcome", result.Result.Outcome,
		"recorded_at", result.Result.RecordedAt,
		"run_updated", result.Stats.RunUpdated,
		"result_inserted", result.Stats.ResultInserted,
		"result_updated", result.Stats.ResultUpdated,
	)
}

func splitCSV(value string) []string {
	parts := strings.Split(value, ",")
	cleaned := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			cleaned = append(cleaned, part)
		}
	}
	return cleaned
}
