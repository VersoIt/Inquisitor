package config

import (
	"fmt"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/VersoIt/Inquisitor/internal/marketdata"

	"gopkg.in/yaml.v3"
)

var envPattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)}`)

type Config struct {
	App        AppConfig        `yaml:"app"`
	Exchange   ExchangeConfig   `yaml:"exchange"`
	Database   DatabaseConfig   `yaml:"database"`
	MarketData MarketDataConfig `yaml:"market_data"`
	Fees       FeesConfig       `yaml:"fees"`
	Slippage   SlippageConfig   `yaml:"slippage"`
	Trading    TradingConfig    `yaml:"trading"`
	Risk       RiskConfig       `yaml:"risk"`
	Regime     RegimeConfig     `yaml:"regime"`
	Research   ResearchConfig   `yaml:"research"`
	Paper      PaperConfig      `yaml:"paper"`
	Live       LiveConfig       `yaml:"live"`
	EdgeDecay  EdgeDecayConfig  `yaml:"edge_decay"`
	Monitoring MonitoringConfig `yaml:"monitoring"`
}

type AppConfig struct {
	Name     string `yaml:"name"`
	Env      string `yaml:"env"`
	Mode     string `yaml:"mode"`
	LogLevel string `yaml:"log_level"`
}

type ExchangeConfig struct {
	Primary              string   `yaml:"primary"`
	RestBaseURL          string   `yaml:"rest_base_url"`
	PublicWSURL          string   `yaml:"public_ws_url"`
	Testnet              bool     `yaml:"testnet"`
	Category             string   `yaml:"category"`
	Symbols              []string `yaml:"symbols"`
	MultiExchangeEnabled bool     `yaml:"multi_exchange_enabled"`
}

type DatabaseConfig struct {
	DSN          string `yaml:"dsn"`
	MaxOpenConns int    `yaml:"max_open_conns"`
	MaxIdleConns int    `yaml:"max_idle_conns"`
}

type MarketDataConfig struct {
	CandleIntervals         []string `yaml:"candle_intervals"`
	BackfillDays            int      `yaml:"backfill_days"`
	OrderbookDepth          int      `yaml:"orderbook_depth"`
	StoreTrades             bool     `yaml:"store_trades"`
	StoreOrderbookSnapshots bool     `yaml:"store_orderbook_snapshots"`
	StoreFunding            bool     `yaml:"store_funding"`
	StoreOpenInterest       bool     `yaml:"store_open_interest"`
	MaxDataStalenessMs      int      `yaml:"max_data_staleness_ms"`
	GapBackfillEnabled      bool     `yaml:"gap_backfill_enabled"`
	ReconnectBackoffMs      int      `yaml:"reconnect_backoff_ms"`
}

type FeesConfig struct {
	MakerBps                  int  `yaml:"maker_bps"`
	TakerBps                  int  `yaml:"taker_bps"`
	UseAccountFeeRateIfExists bool `yaml:"use_account_fee_rate_if_available"`
}

type SlippageConfig struct {
	DefaultBps             int     `yaml:"default_bps"`
	UseOrderbookModel      bool    `yaml:"use_orderbook_model"`
	ConservativeMultiplier float64 `yaml:"conservative_multiplier"`
}

type TradingConfig struct {
	Enabled          bool   `yaml:"enabled"`
	Mode             string `yaml:"mode"`
	AllowLive        bool   `yaml:"allow_live"`
	AllowShort       bool   `yaml:"allow_short"`
	MaxOpenPositions int    `yaml:"max_open_positions"`
	MaxLeverage      int    `yaml:"max_leverage"`
	BaseCurrency     string `yaml:"base_currency"`
}

type RiskConfig struct {
	RiskPerTradePct                   float64 `yaml:"risk_per_trade_pct"`
	MaxDailyLossPct                   float64 `yaml:"max_daily_loss_pct"`
	MaxWeeklyLossPct                  float64 `yaml:"max_weekly_loss_pct"`
	MaxTotalDrawdownPct               float64 `yaml:"max_total_drawdown_pct"`
	MaxLosingStreak                   int     `yaml:"max_losing_streak"`
	MaxSpreadBps                      int     `yaml:"max_spread_bps"`
	MaxSlippageBps                    int     `yaml:"max_slippage_bps"`
	MinConfidence                     int     `yaml:"min_confidence"`
	MinLiquidityUSDT                  float64 `yaml:"min_liquidity_usdt"`
	PortfolioMaxCryptoExposurePct     float64 `yaml:"portfolio_max_crypto_exposure_pct"`
	PortfolioMaxCorrelatedExposurePct float64 `yaml:"portfolio_max_correlated_exposure_pct"`
}

type RegimeConfig struct {
	MinConfidence      int     `yaml:"min_confidence"`
	ADXTrendThreshold  float64 `yaml:"adx_trend_threshold"`
	ADXRangeThreshold  float64 `yaml:"adx_range_threshold"`
	ATRSpikeMultiplier float64 `yaml:"atr_spike_multiplier"`
}

type ResearchConfig struct {
	MinTrades             int     `yaml:"min_trades"`
	MinProfitFactor       float64 `yaml:"min_profit_factor"`
	MinExpectancyR        float64 `yaml:"min_expectancy_r"`
	MaxDrawdownPct        float64 `yaml:"max_drawdown_pct"`
	RequireOutOfSample    bool    `yaml:"require_out_of_sample"`
	RequireWalkForward    bool    `yaml:"require_walk_forward"`
	RequireRegimeAnalysis bool    `yaml:"require_regime_analysis"`
}

type PaperConfig struct {
	InitialBalance   float64 `yaml:"initial_balance"`
	MinimumDays      int     `yaml:"minimum_days"`
	SimulateFees     bool    `yaml:"simulate_fees"`
	SimulateSlippage bool    `yaml:"simulate_slippage"`
	SimulateSpread   bool    `yaml:"simulate_spread"`
}

type LiveConfig struct {
	RequireEnvConfirmation      bool    `yaml:"require_env_confirmation"`
	ConfirmationEnv             string  `yaml:"confirmation_env"`
	APIKeyEnv                   string  `yaml:"api_key_env"`
	APISecretEnv                string  `yaml:"api_secret_env"`
	RequireSubaccount           bool    `yaml:"require_subaccount"`
	WithdrawalPermissionAllowed bool    `yaml:"withdrawal_permission_allowed"`
	InitialLiveCapitalUSDT      float64 `yaml:"initial_live_capital_usdt"`
}

type EdgeDecayConfig struct {
	Enabled               bool    `yaml:"enabled"`
	RollingWindowDays     int     `yaml:"rolling_window_days"`
	MinRecentProfitFactor float64 `yaml:"min_recent_profit_factor"`
	MaxRecentDrawdownPct  float64 `yaml:"max_recent_drawdown_pct"`
	DisableOnDecay        bool    `yaml:"disable_on_decay"`
}

type MonitoringConfig struct {
	HealthPort          int    `yaml:"health_port"`
	DashboardAPIEnabled bool   `yaml:"dashboard_api_enabled"`
	TelegramEnabled     bool   `yaml:"telegram_enabled"`
	TelegramTokenEnv    string `yaml:"telegram_token_env"`
	TelegramChatIDEnv   string `yaml:"telegram_chat_id_env"`
}

func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %q: %w", path, err)
	}

	expanded, err := expandEnvStrict(string(raw))
	if err != nil {
		return nil, err
	}

	var cfg Config
	if err := yaml.Unmarshal([]byte(expanded), &cfg); err != nil {
		return nil, fmt.Errorf("parse config %q: %w", path, err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return &cfg, nil
}

func (c Config) Validate() error {
	var problems []string

	if strings.TrimSpace(c.App.Name) == "" {
		problems = append(problems, "app.name is required")
	}
	if strings.TrimSpace(c.App.LogLevel) == "" {
		problems = append(problems, "app.log_level is required")
	}
	if !oneOf(c.App.LogLevel, "debug", "info", "warn", "warning", "error") {
		problems = append(problems, "app.log_level must be one of debug, info, warn, error")
	}
	if strings.TrimSpace(c.Exchange.Primary) != "bybit" {
		problems = append(problems, "exchange.primary must be bybit in Phase 1")
	}
	if err := validateHTTPURL(c.Exchange.RestBaseURL); err != nil {
		problems = append(problems, "exchange.rest_base_url "+err.Error())
	}
	if err := validateWSURL(c.Exchange.PublicWSURL); err != nil {
		problems = append(problems, "exchange.public_ws_url "+err.Error())
	}
	if strings.TrimSpace(c.Exchange.Category) == "" {
		problems = append(problems, "exchange.category is required")
	}
	if strings.TrimSpace(c.Exchange.Category) != "" && !oneOf(c.Exchange.Category, "linear") {
		problems = append(problems, "exchange.category must be linear in Phase 1")
	}
	if len(c.Exchange.Symbols) == 0 {
		problems = append(problems, "exchange.symbols must not be empty")
	}
	problems = append(problems, validateUniqueNonEmpty("exchange.symbols", c.Exchange.Symbols)...)
	if strings.TrimSpace(c.Database.DSN) == "" {
		problems = append(problems, "database.dsn is required")
	}
	if c.Database.MaxOpenConns < 0 {
		problems = append(problems, "database.max_open_conns must be greater than or equal to zero")
	}
	if c.Database.MaxIdleConns < 0 {
		problems = append(problems, "database.max_idle_conns must be greater than or equal to zero")
	}
	if len(c.MarketData.CandleIntervals) == 0 {
		problems = append(problems, "market_data.candle_intervals must not be empty")
	}
	problems = append(problems, validateUniqueNonEmpty("market_data.candle_intervals", c.MarketData.CandleIntervals)...)
	for _, interval := range c.MarketData.CandleIntervals {
		if strings.TrimSpace(interval) == "" {
			continue
		}
		if _, err := marketdata.IntervalDuration(interval); err != nil {
			problems = append(problems, "market_data.candle_intervals contains unsupported interval "+interval)
		}
	}
	if c.MarketData.BackfillDays <= 0 {
		problems = append(problems, "market_data.backfill_days must be positive")
	}
	if c.MarketData.OrderbookDepth <= 0 {
		problems = append(problems, "market_data.orderbook_depth must be positive")
	}
	if c.MarketData.MaxDataStalenessMs <= 0 {
		problems = append(problems, "market_data.max_data_staleness_ms must be positive")
	}
	if c.MarketData.ReconnectBackoffMs <= 0 {
		problems = append(problems, "market_data.reconnect_backoff_ms must be positive")
	}
	if c.Fees.MakerBps < 0 || c.Fees.TakerBps < 0 {
		problems = append(problems, "fees maker_bps and taker_bps must be greater than or equal to zero")
	}
	if c.Slippage.DefaultBps < 0 {
		problems = append(problems, "slippage.default_bps must be greater than or equal to zero")
	}
	if c.Slippage.ConservativeMultiplier < 1 {
		problems = append(problems, "slippage.conservative_multiplier must be greater than or equal to 1")
	}
	if !oneOf(c.Trading.Mode, "paper") {
		problems = append(problems, "trading.mode must be paper in Phase 1")
	}
	if c.Trading.AllowLive || c.Live.WithdrawalPermissionAllowed {
		problems = append(problems, "live trading and withdrawal permissions must be disabled by default")
	}
	if c.Trading.Enabled && c.Trading.Mode == "live" && !c.Trading.AllowLive {
		problems = append(problems, "trading.mode=live requires trading.allow_live=true")
	}
	if c.Risk.RiskPerTradePct <= 0 {
		problems = append(problems, "risk.risk_per_trade_pct must be positive")
	}
	if c.Risk.MaxDailyLossPct <= 0 || c.Risk.MaxWeeklyLossPct <= 0 || c.Risk.MaxTotalDrawdownPct <= 0 {
		problems = append(problems, "risk loss limits must be positive")
	}
	if c.Risk.MinConfidence < 0 || c.Risk.MinConfidence > 100 {
		problems = append(problems, "risk.min_confidence must be between 0 and 100")
	}

	if len(problems) > 0 {
		return fmt.Errorf("config validation failed: %s", strings.Join(problems, "; "))
	}
	return nil
}

func expandEnvStrict(input string) (string, error) {
	missing := map[string]struct{}{}
	output := envPattern.ReplaceAllStringFunc(input, func(match string) string {
		name := strings.TrimSuffix(strings.TrimPrefix(match, "${"), "}")
		value, ok := os.LookupEnv(name)
		if !ok {
			missing[name] = struct{}{}
			return match
		}
		return value
	})

	if len(missing) == 0 {
		return output, nil
	}

	names := make([]string, 0, len(missing))
	for name := range missing {
		names = append(names, name)
	}
	sort.Strings(names)
	return "", fmt.Errorf("missing required environment variables: %s", strings.Join(names, ", "))
}

func validateHTTPURL(value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("is required")
	}

	parsed, err := url.Parse(value)
	if err != nil {
		return fmt.Errorf("must be a valid URL: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("must use http or https")
	}
	if parsed.Host == "" {
		return fmt.Errorf("must include a host")
	}
	return nil
}

func validateWSURL(value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("is required")
	}

	parsed, err := url.Parse(value)
	if err != nil {
		return fmt.Errorf("must be a valid URL: %w", err)
	}
	if parsed.Scheme != "ws" && parsed.Scheme != "wss" {
		return fmt.Errorf("must use ws or wss")
	}
	if parsed.Host == "" {
		return fmt.Errorf("must include a host")
	}
	return nil
}

func validateUniqueNonEmpty(field string, values []string) []string {
	var problems []string
	seen := map[string]struct{}{}
	for i, value := range values {
		normalized := strings.ToUpper(strings.TrimSpace(value))
		if normalized == "" {
			problems = append(problems, fmt.Sprintf("%s[%d] must not be empty", field, i))
			continue
		}
		if _, exists := seen[normalized]; exists {
			problems = append(problems, field+" must not contain duplicates")
			continue
		}
		seen[normalized] = struct{}{}
	}
	return problems
}

func oneOf(value string, allowed ...string) bool {
	normalized := strings.ToLower(strings.TrimSpace(value))
	for _, candidate := range allowed {
		if normalized == strings.ToLower(candidate) {
			return true
		}
	}
	return false
}
