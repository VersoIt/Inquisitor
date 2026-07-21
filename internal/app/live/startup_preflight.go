package live

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	domainlive "github.com/VersoIt/Inquisitor/internal/live"
)

type EnvironmentReader interface {
	LookupEnv(name string) (string, bool)
}

type PreflightLiveStartupRequest struct {
	TradingEnabled              bool
	TradingMode                 string
	AllowLive                   bool
	RequireEnvConfirmation      bool
	ConfirmationEnv             string
	APIKeyEnv                   string
	APISecretEnv                string
	RequireSubaccount           bool
	SubaccountConfirmed         bool
	WithdrawalPermissionAllowed bool
	InitialLiveCapitalUSDT      decimal.Decimal
	MaxInitialLiveCapitalUSDT   decimal.Decimal
	ExpectedFlatPositions       []domainlive.PositionSnapshotQuery
	MaxPositionSnapshotAge      time.Duration
}

type PreflightLiveStartupResult struct {
	Ready                      bool
	TradingEnabled             bool
	TradingMode                string
	AllowLive                  bool
	ConfirmationEnv            string
	ConfirmationAccepted       bool
	APIKeyEnv                  string
	APIKeyPresent              bool
	APISecretEnv               string
	APISecretPresent           bool
	SubaccountConfirmed        bool
	WithdrawalPermissionDenied bool
	InitialLiveCapitalUSDT     decimal.Decimal
	MaxInitialLiveCapitalUSDT  decimal.Decimal
	KillSwitchActive           bool
	KillSwitchReason           string
	KillSwitchSource           string
	ExpectedFlatPositions      []domainlive.PositionSnapshotQuery
	MaxPositionSnapshotAge     time.Duration
	PositionSnapshots          []domainlive.PositionSnapshot
	PositionSnapshotStats      domainlive.PositionSnapshotStats
	Problems                   []string
}

type osEnvironmentReader struct{}

func (osEnvironmentReader) LookupEnv(name string) (string, bool) {
	return os.LookupEnv(name)
}

func WithEnvironmentReader(reader EnvironmentReader) Option {
	return func(service *Service) {
		service.env = reader
	}
}

func (s *Service) PreflightLiveStartup(ctx context.Context, req PreflightLiveStartupRequest) (PreflightLiveStartupResult, error) {
	if err := ctx.Err(); err != nil {
		return PreflightLiveStartupResult{}, err
	}
	if s == nil || s.killSwitch == nil {
		return PreflightLiveStartupResult{}, fmt.Errorf("live startup preflight requires kill switch repository")
	}
	if s.env == nil {
		return PreflightLiveStartupResult{}, fmt.Errorf("live startup preflight requires environment reader")
	}

	result := PreflightLiveStartupResult{
		TradingEnabled:            req.TradingEnabled,
		TradingMode:               strings.ToLower(strings.TrimSpace(req.TradingMode)),
		AllowLive:                 req.AllowLive,
		ConfirmationEnv:           strings.TrimSpace(req.ConfirmationEnv),
		APIKeyEnv:                 strings.TrimSpace(req.APIKeyEnv),
		APISecretEnv:              strings.TrimSpace(req.APISecretEnv),
		SubaccountConfirmed:       req.SubaccountConfirmed,
		InitialLiveCapitalUSDT:    req.InitialLiveCapitalUSDT,
		MaxInitialLiveCapitalUSDT: req.MaxInitialLiveCapitalUSDT,
		ExpectedFlatPositions:     append([]domainlive.PositionSnapshotQuery(nil), req.ExpectedFlatPositions...),
		MaxPositionSnapshotAge:    req.MaxPositionSnapshotAge,
	}

	result.Problems = append(result.Problems, validateLiveStartupPolicy(req)...)
	result.ConfirmationAccepted = liveConfirmationAccepted(s.env, req.RequireEnvConfirmation, result.ConfirmationEnv)
	if req.RequireEnvConfirmation && !result.ConfirmationAccepted {
		result.Problems = append(result.Problems, "live confirmation environment variable must be explicitly true")
	}
	result.APIKeyPresent = liveSecretPresent(s.env, result.APIKeyEnv)
	if !result.APIKeyPresent {
		result.Problems = append(result.Problems, "live API key environment variable is required")
	}
	result.APISecretPresent = liveSecretPresent(s.env, result.APISecretEnv)
	if !result.APISecretPresent {
		result.Problems = append(result.Problems, "live API secret environment variable is required")
	}

	state, err := s.killSwitch.CurrentKillSwitchState(ctx)
	if err != nil {
		return result, fmt.Errorf("load kill switch before live startup: %w", err)
	}
	result.KillSwitchActive = state.Active
	result.KillSwitchReason = state.Reason
	result.KillSwitchSource = state.Source
	if state.Active {
		result.Problems = append(result.Problems, "kill switch must be inactive")
	}

	result.WithdrawalPermissionDenied = !req.WithdrawalPermissionAllowed
	if len(result.Problems) == 0 && len(req.ExpectedFlatPositions) > 0 {
		snapshots, stats, problems, err := s.preflightExpectedFlatLivePositions(ctx, req.ExpectedFlatPositions, req.MaxPositionSnapshotAge)
		result.PositionSnapshots = snapshots
		result.PositionSnapshotStats = stats
		if err != nil {
			return result, err
		}
		result.Problems = append(result.Problems, problems...)
	}

	result.Ready = len(result.Problems) == 0
	if !result.Ready {
		return result, fmt.Errorf("live startup preflight failed: %s", strings.Join(result.Problems, "; "))
	}
	return result, nil
}

func validateLiveStartupPolicy(req PreflightLiveStartupRequest) []string {
	var problems []string
	if !req.TradingEnabled {
		problems = append(problems, "trading.enabled must be true for live startup")
	}
	if strings.ToLower(strings.TrimSpace(req.TradingMode)) != "live" {
		problems = append(problems, "trading.mode must be live")
	}
	if !req.AllowLive {
		problems = append(problems, "trading.allow_live must be true")
	}
	if strings.TrimSpace(req.ConfirmationEnv) == "" {
		problems = append(problems, "live.confirmation_env is required")
	}
	if strings.TrimSpace(req.APIKeyEnv) == "" {
		problems = append(problems, "live.api_key_env is required")
	}
	if strings.TrimSpace(req.APISecretEnv) == "" {
		problems = append(problems, "live.api_secret_env is required")
	}
	if req.RequireSubaccount && !req.SubaccountConfirmed {
		problems = append(problems, "dedicated live subaccount must be confirmed")
	}
	if req.WithdrawalPermissionAllowed {
		problems = append(problems, "withdrawal permission must be disabled")
	}
	if req.InitialLiveCapitalUSDT.LessThanOrEqual(decimal.Zero) {
		problems = append(problems, "initial live capital must be positive")
	}
	if req.MaxInitialLiveCapitalUSDT.LessThanOrEqual(decimal.Zero) {
		problems = append(problems, "max initial live capital must be positive")
	}
	if req.InitialLiveCapitalUSDT.GreaterThan(req.MaxInitialLiveCapitalUSDT) {
		problems = append(problems, "initial live capital must not exceed max initial live capital")
	}
	return problems
}

func (s *Service) preflightExpectedFlatLivePositions(
	ctx context.Context,
	queries []domainlive.PositionSnapshotQuery,
	maxAge time.Duration,
) ([]domainlive.PositionSnapshot, domainlive.PositionSnapshotStats, []string, error) {
	var problems []string
	if maxAge <= 0 {
		problems = append(problems, "max position snapshot age must be positive")
	}
	for _, query := range queries {
		if err := domainlive.ValidatePositionSnapshotQuery(query); err != nil {
			problems = append(problems, err.Error())
		}
	}
	if len(problems) > 0 {
		return nil, domainlive.PositionSnapshotStats{}, problems, nil
	}

	if s.positionReader == nil {
		return nil, domainlive.PositionSnapshotStats{}, nil, fmt.Errorf("live startup preflight requires position snapshot reader")
	}
	if s.positionJournal == nil {
		return nil, domainlive.PositionSnapshotStats{}, nil, fmt.Errorf("live startup preflight requires position snapshot journal")
	}
	if s.clock == nil {
		return nil, domainlive.PositionSnapshotStats{}, nil, fmt.Errorf("live startup preflight requires clock")
	}

	now := s.clock.Now()
	var (
		snapshots []domainlive.PositionSnapshot
		stats     domainlive.PositionSnapshotStats
	)
	for _, query := range queries {
		snapshot, err := s.positionReader.GetPositionSnapshot(ctx, query)
		if err != nil {
			return snapshots, stats, problems, fmt.Errorf("read live startup position snapshot %q: %w", query.Symbol, err)
		}
		if err := ensurePositionSnapshotMatchesQuery(snapshot, query); err != nil {
			return snapshots, stats, problems, err
		}

		recordStats, err := s.positionJournal.RecordPositionSnapshot(ctx, snapshot)
		if err != nil {
			return snapshots, stats, problems, fmt.Errorf("record live startup position snapshot %q: %w", snapshot.Symbol, err)
		}
		if recordStats.Total() == 0 {
			return snapshots, stats, problems, fmt.Errorf("live startup position snapshot journal did not record %q", snapshot.Symbol)
		}

		snapshots = append(snapshots, snapshot)
		stats.Inserted += recordStats.Inserted
		stats.Skipped += recordStats.Skipped
		problems = append(problems, validateStartupFlatPosition(snapshot, now, maxAge)...)
	}
	return snapshots, stats, problems, nil
}

func ensurePositionSnapshotMatchesQuery(snapshot domainlive.PositionSnapshot, query domainlive.PositionSnapshotQuery) error {
	if err := domainlive.ValidatePositionSnapshot(snapshot); err != nil {
		return err
	}
	if snapshot.Exchange != query.Exchange {
		return fmt.Errorf("live startup position exchange %q does not match query %q", snapshot.Exchange, query.Exchange)
	}
	if snapshot.Category != query.Category {
		return fmt.Errorf("live startup position category %q does not match query %q", snapshot.Category, query.Category)
	}
	if snapshot.Symbol != query.Symbol {
		return fmt.Errorf("live startup position symbol %q does not match query %q", snapshot.Symbol, query.Symbol)
	}
	return nil
}

func validateStartupFlatPosition(snapshot domainlive.PositionSnapshot, now time.Time, maxAge time.Duration) []string {
	var problems []string
	if snapshot.Open {
		problems = append(problems, fmt.Sprintf("live position %s must be flat before startup: side=%s size=%s", snapshot.Symbol, snapshot.Side, snapshot.Size))
	}
	age := now.Sub(snapshot.ObservedAt)
	if age < 0 {
		problems = append(problems, fmt.Sprintf("live position %s snapshot observed_at must not be in the future", snapshot.Symbol))
	}
	if age > maxAge {
		problems = append(problems, fmt.Sprintf("live position %s snapshot is stale: age=%s max=%s", snapshot.Symbol, age, maxAge))
	}
	return problems
}

func liveConfirmationAccepted(env EnvironmentReader, required bool, name string) bool {
	if !required {
		return true
	}
	value, ok := env.LookupEnv(strings.TrimSpace(name))
	if !ok {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "true", "1", "yes":
		return true
	default:
		return false
	}
}

func liveSecretPresent(env EnvironmentReader, name string) bool {
	value, ok := env.LookupEnv(strings.TrimSpace(name))
	return ok && strings.TrimSpace(value) != ""
}
