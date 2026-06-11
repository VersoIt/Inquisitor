package main

import (
	"context"
	"flag"
	"os"
	"strings"

	apphypothesis "github.com/VersoIt/Inquisitor/internal/app/hypothesis"
	"github.com/VersoIt/Inquisitor/internal/logger"
)

func main() {
	file := flag.String("file", "", "path to hypothesis YAML file")
	logLevel := flag.String("log-level", "info", "log level: debug, info, warn, error")
	flag.Parse()

	log := logger.New(*logLevel)
	if strings.TrimSpace(*file) == "" {
		log.Error("hypothesis file is required", "flag", "-file")
		os.Exit(1)
	}

	result, err := apphypothesis.NewService().ValidateImport(context.Background(), apphypothesis.ImportRequest{
		Path: *file,
	})
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
		"symbols", spec.Market.Symbols,
		"intervals", spec.Market.Intervals,
		"allowed_regimes", spec.Regime.Allowed,
		"blocked_regimes", spec.Regime.Blocked,
		"signals", len(spec.Signals),
	)
}
