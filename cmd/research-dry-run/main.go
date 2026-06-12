package main

import (
	"context"
	"flag"
	"os"

	appresearch "github.com/VersoIt/Inquisitor/internal/app/research"
	"github.com/VersoIt/Inquisitor/internal/config"
	"github.com/VersoIt/Inquisitor/internal/logger"
	"github.com/VersoIt/Inquisitor/internal/storage/postgres"
)

func main() {
	configPath := flag.String("config", "configs/config.example.yaml", "path to YAML config")
	runID := flag.String("run-id", "", "research run id")
	minRegimeCoverage := flag.Float64("min-regime-coverage-pct", 100, "minimum historical regime-state coverage percentage")
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

	researchRepo := postgres.NewResearchRunRepository(db)
	result, err := appresearch.NewService(
		nil,
		researchRepo,
		appresearch.WithResultRecorder(researchRepo),
		appresearch.WithRegimeRepository(postgres.NewRegimeStateRepository(db)),
	).DryRun(ctx, appresearch.DryRunRequest{
		RunID:                *runID,
		MinRegimeCoveragePct: *minRegimeCoverage,
	})
	if err != nil {
		log.Error("research dry-run failed", "error", err)
		os.Exit(1)
	}

	log.Info(
		"research dry-run completed",
		"run_id", result.Run.RunID,
		"final_status", result.Result.FinalStatus,
		"outcome", result.Result.Outcome,
		"coverage_expected", result.Coverage.Expected,
		"coverage_observed", result.Coverage.Observed,
		"coverage_missing", result.Coverage.Missing,
		"coverage_pct", result.Coverage.Percent,
		"run_updated", result.Stats.RunUpdated,
		"result_inserted", result.Stats.ResultInserted,
		"result_updated", result.Stats.ResultUpdated,
	)
}
