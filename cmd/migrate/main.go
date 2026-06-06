package main

import (
	"context"
	"flag"
	"os"

	"github.com/VersoIt/Inquisitor/internal/config"
	"github.com/VersoIt/Inquisitor/internal/logger"
	"github.com/VersoIt/Inquisitor/internal/storage/postgres"
)

func main() {
	configPath := flag.String("config", "configs/config.example.yaml", "path to YAML config")
	migrationsDir := flag.String("migrations", "migrations", "path to SQL migrations directory")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	logLevel := "info"
	if cfg != nil {
		logLevel = cfg.App.LogLevel
	}
	log := logger.New(logLevel)
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

	result, err := postgres.ApplyMigrations(ctx, db, *migrationsDir)
	if err != nil {
		log.Error("failed to apply migrations", "error", err)
		os.Exit(1)
	}

	log.Info("migrations completed", "applied", result.Applied, "skipped", result.Skipped)
}
