package main

import (
	"context"
	"flag"
	"os"
	"strings"

	apphypothesis "github.com/VersoIt/Inquisitor/internal/app/hypothesis"
	"github.com/VersoIt/Inquisitor/internal/config"
	"github.com/VersoIt/Inquisitor/internal/logger"
	"github.com/VersoIt/Inquisitor/internal/storage/postgres"
)

func main() {
	configPath := flag.String("config", "configs/config.example.yaml", "path to YAML config; required only with -store")
	file := flag.String("file", "", "path to hypothesis YAML file")
	store := flag.Bool("store", false, "persist the validated hypothesis draft to PostgreSQL")
	logLevel := flag.String("log-level", "info", "log level: debug, info, warn, error")
	flag.Parse()

	log := logger.New(*logLevel)
	if strings.TrimSpace(*file) == "" {
		log.Error("hypothesis file is required", "flag", "-file")
		os.Exit(1)
	}

	ctx := context.Background()
	request := apphypothesis.ImportRequest{
		Path: *file,
	}
	if *store {
		cfg, err := config.Load(*configPath)
		if err != nil {
			log.Error("failed to load config", "error", err)
			os.Exit(1)
		}
		db, err := postgres.Open(ctx, cfg.Database)
		if err != nil {
			log.Error("failed to connect to postgres", "error", err)
			os.Exit(1)
		}
		defer db.Close()

		result, err := apphypothesis.NewService(
			apphypothesis.WithRepository(postgres.NewHypothesisRepository(db)),
		).ImportAndStore(ctx, request)
		if err != nil {
			log.Error("hypothesis import failed", "file", *file, "error", err)
			os.Exit(1)
		}
		log.Info(
			"hypothesis import completed",
			"file", result.Path,
			"name", result.Hypothesis.Name,
			"version", result.Hypothesis.Version,
			"status", result.Hypothesis.Status,
			"content_sha256", result.ContentSHA256,
			"inserted", result.Stats.Inserted,
			"updated", result.Stats.Updated,
		)
		return
	}

	result, err := apphypothesis.NewService().ValidateImport(ctx, request)
	if err != nil {
		log.Error("hypothesis import validation failed", "file", *file, "error", err)
		os.Exit(1)
	}
	spec := result.Hypothesis

	log.Info(
		"hypothesis import validation passed",
		"file", result.Path,
		"name", spec.Name,
		"version", spec.Version,
		"status", spec.Status,
		"content_sha256", result.ContentSHA256,
		"symbols", spec.Market.Symbols,
		"intervals", spec.Market.Intervals,
		"allowed_regimes", spec.Regime.Allowed,
		"blocked_regimes", spec.Regime.Blocked,
		"signals", len(spec.Signals),
	)
}
