package main

import (
	"context"
	"flag"
	"os"
	"strconv"

	"github.com/shopspring/decimal"

	apppaper "github.com/VersoIt/Inquisitor/internal/app/paper"
	"github.com/VersoIt/Inquisitor/internal/config"
	"github.com/VersoIt/Inquisitor/internal/logger"
	domainpaper "github.com/VersoIt/Inquisitor/internal/paper"
	"github.com/VersoIt/Inquisitor/internal/storage/postgres"
)

func main() {
	configPath := flag.String("config", "configs/config.example.yaml", "path to YAML config")
	runID := flag.String("run-id", "", "candidate research run id")
	logLevel := flag.String("log-level", "info", "log level: debug, info, warn, error")
	flag.Parse()

	log := logger.New(*logLevel)
	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Error("failed to load config", "error", err)
		os.Exit(1)
	}
	policy, err := paperSafetyPolicy(cfg)
	if err != nil {
		log.Error("invalid paper safety policy", "error", err)
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
	result, err := apppaper.NewService(researchRepo, researchRepo).ValidateCandidate(ctx, apppaper.ValidateCandidateRequest{
		RunID:  *runID,
		Policy: policy,
	})
	if err != nil {
		log.Error("paper validation readiness check failed", "error", err)
		os.Exit(1)
	}

	log.Info(
		"paper validation readiness checked",
		"run_id", result.Plan.RunID,
		"allowed", result.Plan.Allowed,
		"reasons", result.Plan.Reasons,
		"mode", result.Plan.Mode,
		"minimum_days", result.Plan.MinimumDays,
		"initial_balance", result.Plan.InitialBalance.String(),
		"hypothesis_name", result.Run.HypothesisName,
		"hypothesis_version", result.Run.HypothesisVersion,
		"outcome", result.Result.Outcome,
		"requested_at", result.Plan.RequestedAt,
	)
	if !result.Plan.Allowed {
		os.Exit(1)
	}
}

func paperSafetyPolicy(cfg *config.Config) (domainpaper.SafetyPolicy, error) {
	initialBalance := strconv.FormatFloat(cfg.Paper.InitialBalance, 'f', -1, 64)
	parsedBalance, err := decimal.NewFromString(initialBalance)
	if err != nil {
		return domainpaper.SafetyPolicy{}, err
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
		return domainpaper.SafetyPolicy{}, err
	}
	return policy, nil
}
