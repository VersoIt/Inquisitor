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
	ExpectedAccount             domainlive.AccountSnapshotQuery
	AccountBaseCurrency         string
	MaxAccountSnapshotAge       time.Duration
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
	ExpectedAccount            domainlive.AccountSnapshotQuery
	AccountBaseCurrency        string
	MaxAccountSnapshotAge      time.Duration
	AccountSnapshot            domainlive.AccountSnapshot
	AccountSnapshotStats       domainlive.AccountSnapshotStats
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
		ExpectedAccount:           req.ExpectedAccount,
		AccountBaseCurrency:       strings.ToUpper(strings.TrimSpace(req.AccountBaseCurrency)),
		MaxAccountSnapshotAge:     req.MaxAccountSnapshotAge,
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
	if len(result.Problems) == 0 && liveAccountPreflightEnabled(req.ExpectedAccount) {
		snapshot, stats, problems, err := s.preflightExpectedLiveAccount(
			ctx,
			req.ExpectedAccount,
			req.AccountBaseCurrency,
			req.MaxAccountSnapshotAge,
			req.MaxInitialLiveCapitalUSDT,
		)
		result.AccountSnapshot = snapshot
		result.AccountSnapshotStats = stats
		if err != nil {
			return result, err
		}
		result.Problems = append(result.Problems, problems...)
	}
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

func (s *Service) preflightExpectedLiveAccount(
	ctx context.Context,
	query domainlive.AccountSnapshotQuery,
	baseCurrency string,
	maxAge time.Duration,
	maxEquity decimal.Decimal,
) (domainlive.AccountSnapshot, domainlive.AccountSnapshotStats, []string, error) {
	var problems []string
	if err := domainlive.ValidateAccountSnapshotQuery(query); err != nil {
		problems = append(problems, err.Error())
	}
	normalizedBaseCurrency := strings.ToUpper(strings.TrimSpace(baseCurrency))
	if normalizedBaseCurrency == "" {
		problems = append(problems, "account base currency is required")
	}
	if maxAge <= 0 {
		problems = append(problems, "max account snapshot age must be positive")
	}
	if maxEquity.LessThanOrEqual(decimal.Zero) {
		problems = append(problems, "max account equity must be positive")
	}
	if len(problems) > 0 {
		return domainlive.AccountSnapshot{}, domainlive.AccountSnapshotStats{}, problems, nil
	}

	if s.accountReader == nil {
		return domainlive.AccountSnapshot{}, domainlive.AccountSnapshotStats{}, nil, fmt.Errorf("live startup preflight requires account snapshot reader")
	}
	if s.accountJournal == nil {
		return domainlive.AccountSnapshot{}, domainlive.AccountSnapshotStats{}, nil, fmt.Errorf("live startup preflight requires account snapshot journal")
	}
	if s.clock == nil {
		return domainlive.AccountSnapshot{}, domainlive.AccountSnapshotStats{}, nil, fmt.Errorf("live startup preflight requires clock")
	}

	snapshot, err := s.accountReader.GetAccountSnapshot(ctx, query)
	if err != nil {
		return domainlive.AccountSnapshot{}, domainlive.AccountSnapshotStats{}, problems, fmt.Errorf("read live startup account snapshot %q: %w", query.AccountType, err)
	}
	if err := ensureAccountSnapshotMatchesQuery(snapshot, query); err != nil {
		return domainlive.AccountSnapshot{}, domainlive.AccountSnapshotStats{}, problems, err
	}
	stats, err := s.accountJournal.RecordAccountSnapshot(ctx, snapshot)
	if err != nil {
		return domainlive.AccountSnapshot{}, domainlive.AccountSnapshotStats{}, problems, fmt.Errorf("record live startup account snapshot %q: %w", snapshot.AccountType, err)
	}
	if stats.Total() == 0 {
		return domainlive.AccountSnapshot{}, domainlive.AccountSnapshotStats{}, problems, fmt.Errorf("live startup account snapshot journal did not record %q", snapshot.AccountType)
	}

	problems = append(problems, validateStartupAccountReadiness(snapshot, normalizedBaseCurrency, maxEquity, s.clock.Now(), maxAge)...)
	return snapshot, stats, problems, nil
}

func liveAccountPreflightEnabled(query domainlive.AccountSnapshotQuery) bool {
	return strings.TrimSpace(query.Exchange) != "" || strings.TrimSpace(string(query.AccountType)) != ""
}

func ensureAccountSnapshotMatchesQuery(snapshot domainlive.AccountSnapshot, query domainlive.AccountSnapshotQuery) error {
	if err := domainlive.ValidateAccountSnapshot(snapshot); err != nil {
		return err
	}
	if snapshot.Exchange != query.Exchange {
		return fmt.Errorf("live startup account exchange %q does not match query %q", snapshot.Exchange, query.Exchange)
	}
	if snapshot.AccountType != query.AccountType {
		return fmt.Errorf("live startup account type %q does not match query %q", snapshot.AccountType, query.AccountType)
	}
	return nil
}

func validateStartupAccountReadiness(
	snapshot domainlive.AccountSnapshot,
	baseCurrency string,
	maxEquity decimal.Decimal,
	now time.Time,
	maxAge time.Duration,
) []string {
	var problems []string
	age := now.Sub(snapshot.ObservedAt)
	if age < 0 {
		problems = append(problems, "live account snapshot observed_at must not be in the future")
	}
	if age > maxAge {
		problems = append(problems, fmt.Sprintf("live account snapshot is stale: age=%s max=%s", age, maxAge))
	}
	if snapshot.TotalEquity.LessThanOrEqual(decimal.Zero) {
		problems = append(problems, "live account total equity must be positive before startup")
	}
	if snapshot.TotalEquity.GreaterThan(maxEquity) {
		problems = append(problems, fmt.Sprintf("live account total equity %s exceeds max %s", snapshot.TotalEquity, maxEquity))
	}
	if snapshot.TotalAvailableBalance.LessThanOrEqual(decimal.Zero) {
		problems = append(problems, "live account total available balance must be positive before startup")
	}
	if !snapshot.TotalPerpUPL.IsZero() {
		problems = append(problems, fmt.Sprintf("live account total perp UPL must be zero before startup: %s", snapshot.TotalPerpUPL))
	}
	if snapshot.TotalInitialMargin.IsPositive() {
		problems = append(problems, fmt.Sprintf("live account initial margin must be zero before startup: %s", snapshot.TotalInitialMargin))
	}
	if snapshot.TotalMaintenanceMargin.IsPositive() {
		problems = append(problems, fmt.Sprintf("live account maintenance margin must be zero before startup: %s", snapshot.TotalMaintenanceMargin))
	}
	if len(snapshot.Coins) == 0 {
		problems = append(problems, "live account coin details are required before startup")
	}
	for _, coin := range snapshot.Coins {
		problems = append(problems, validateStartupAccountCoinReadiness(coin, baseCurrency)...)
	}
	return problems
}

func validateStartupAccountCoinReadiness(coin domainlive.AccountCoinSnapshot, baseCurrency string) []string {
	var problems []string
	if coin.Coin != baseCurrency && (coin.Equity.IsPositive() || coin.USDValue.IsPositive() || coin.WalletBalance.IsPositive()) {
		problems = append(problems, fmt.Sprintf("live account non-base asset %s must be zero before startup", coin.Coin))
	}
	if coin.Locked.IsPositive() {
		problems = append(problems, fmt.Sprintf("live account %s locked balance must be zero before startup: %s", coin.Coin, coin.Locked))
	}
	if coin.BorrowAmount.IsPositive() {
		problems = append(problems, fmt.Sprintf("live account %s borrow amount must be zero before startup: %s", coin.Coin, coin.BorrowAmount))
	}
	if coin.AccruedInterest.IsPositive() {
		problems = append(problems, fmt.Sprintf("live account %s accrued interest must be zero before startup: %s", coin.Coin, coin.AccruedInterest))
	}
	if coin.TotalOrderIM.IsPositive() {
		problems = append(problems, fmt.Sprintf("live account %s order initial margin must be zero before startup: %s", coin.Coin, coin.TotalOrderIM))
	}
	if coin.TotalPositionIM.IsPositive() {
		problems = append(problems, fmt.Sprintf("live account %s position initial margin must be zero before startup: %s", coin.Coin, coin.TotalPositionIM))
	}
	if coin.TotalPositionMM.IsPositive() {
		problems = append(problems, fmt.Sprintf("live account %s position maintenance margin must be zero before startup: %s", coin.Coin, coin.TotalPositionMM))
	}
	if coin.SpotBorrow.IsPositive() {
		problems = append(problems, fmt.Sprintf("live account %s spot borrow must be zero before startup: %s", coin.Coin, coin.SpotBorrow))
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
