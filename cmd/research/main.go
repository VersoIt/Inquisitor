package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	appresearch "github.com/VersoIt/Inquisitor/internal/app/research"
	"github.com/VersoIt/Inquisitor/internal/config"
	"github.com/VersoIt/Inquisitor/internal/logger"
	"github.com/VersoIt/Inquisitor/internal/storage/postgres"
)

func main() {
	configPath := flag.String("config", "configs/config.example.yaml", "path to YAML config")
	hypothesisName := flag.String("hypothesis-name", "", "imported draft hypothesis name")
	hypothesisVersion := flag.String("hypothesis-version", "", "imported draft hypothesis version")
	startValue := flag.String("start", "", "inclusive UTC research window start in RFC3339 format")
	endValue := flag.String("end", "", "exclusive UTC research window end in RFC3339 format")
	notesValue := flag.String("notes", "", "optional comma-separated notes stored with the planned run")
	logLevel := flag.String("log-level", "info", "log level: debug, info, warn, error")
	flag.Parse()

	log := logger.New(*logLevel)
	start, err := parseRequiredTime(*startValue, "-start")
	if err != nil {
		log.Error("invalid research window start", "error", err)
		os.Exit(1)
	}
	end, err := parseRequiredTime(*endValue, "-end")
	if err != nil {
		log.Error("invalid research window end", "error", err)
		os.Exit(1)
	}

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

	result, err := appresearch.NewService(
		postgres.NewHypothesisRepository(db),
		postgres.NewResearchRunRepository(db),
	).Schedule(ctx, appresearch.ScheduleRequest{
		HypothesisName:    *hypothesisName,
		HypothesisVersion: *hypothesisVersion,
		WindowStart:       start,
		WindowEnd:         end,
		Notes:             splitCSV(*notesValue),
	})
	if err != nil {
		log.Error("research run scheduling failed", "error", err)
		os.Exit(1)
	}

	log.Info(
		"research run scheduled",
		"run_id", result.Run.RunID,
		"hypothesis_name", result.Run.HypothesisName,
		"hypothesis_version", result.Run.HypothesisVersion,
		"status", result.Run.Status,
		"window_start", result.Run.WindowStart.Format(time.RFC3339),
		"window_end", result.Run.WindowEnd.Format(time.RFC3339),
		"symbols", result.Run.Symbols,
		"intervals", result.Run.Intervals,
		"inserted", result.Stats.Inserted,
		"updated", result.Stats.Updated,
	)
}

func parseRequiredTime(value, flagName string) (time.Time, error) {
	if strings.TrimSpace(value) == "" {
		return time.Time{}, fmt.Errorf("%s is required", flagName)
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, err
	}
	return parsed.UTC(), nil
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
