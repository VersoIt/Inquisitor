package config

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	domainrisk "github.com/VersoIt/Inquisitor/internal/risk"
	"github.com/shopspring/decimal"
)

// TradeRiskPolicy translates external configuration into the exact values used
// by the risk domain. Runtime controls such as trading enabled remain outside it.
func (c Config) TradeRiskPolicy() (domainrisk.Policy, error) {
	if err := c.Validate(); err != nil {
		return domainrisk.Policy{}, fmt.Errorf("build trade risk policy: %w", err)
	}

	riskPerTrade, err := decimalFromFloat(c.Risk.RiskPerTradePct)
	if err != nil {
		return domainrisk.Policy{}, err
	}
	maxDailyLoss, err := decimalFromFloat(c.Risk.MaxDailyLossPct)
	if err != nil {
		return domainrisk.Policy{}, err
	}
	maxWeeklyLoss, err := decimalFromFloat(c.Risk.MaxWeeklyLossPct)
	if err != nil {
		return domainrisk.Policy{}, err
	}
	maxDrawdown, err := decimalFromFloat(c.Risk.MaxTotalDrawdownPct)
	if err != nil {
		return domainrisk.Policy{}, err
	}
	minLiquidity, err := decimalFromFloat(c.Risk.MinLiquidityUSDT)
	if err != nil {
		return domainrisk.Policy{}, err
	}
	maxPortfolioExposure, err := decimalFromFloat(c.Risk.PortfolioMaxCryptoExposurePct)
	if err != nil {
		return domainrisk.Policy{}, err
	}
	maxCorrelatedExposure, err := decimalFromFloat(c.Risk.PortfolioMaxCorrelatedExposurePct)
	if err != nil {
		return domainrisk.Policy{}, err
	}

	allowedSymbols := make([]string, len(c.Exchange.Symbols))
	for index, symbol := range c.Exchange.Symbols {
		allowedSymbols[index] = strings.ToUpper(strings.TrimSpace(symbol))
	}
	policy := domainrisk.Policy{
		AllowedMode:              domainrisk.Mode(strings.ToUpper(strings.TrimSpace(c.Trading.Mode))),
		AllowShort:               c.Trading.AllowShort,
		RiskPerTradePct:          riskPerTrade,
		MaxDailyLossPct:          maxDailyLoss,
		MaxWeeklyLossPct:         maxWeeklyLoss,
		MaxTotalDrawdownPct:      maxDrawdown,
		MaxLosingStreak:          c.Risk.MaxLosingStreak,
		MaxOpenPositions:         c.Trading.MaxOpenPositions,
		MaxLeverage:              decimal.NewFromInt(int64(c.Trading.MaxLeverage)),
		MaxSpreadBPS:             decimal.NewFromInt(int64(c.Risk.MaxSpreadBps)),
		MaxSlippageBPS:           decimal.NewFromInt(int64(c.Risk.MaxSlippageBps)),
		MinConfidence:            c.Risk.MinConfidence,
		MinLiquidityQuote:        minLiquidity,
		MaxPortfolioExposurePct:  maxPortfolioExposure,
		MaxCorrelatedExposurePct: maxCorrelatedExposure,
		MaxDataAge:               time.Duration(c.MarketData.MaxDataStalenessMs) * time.Millisecond,
		AllowedSymbols:           allowedSymbols,
	}
	if err := domainrisk.ValidatePolicy(policy); err != nil {
		return domainrisk.Policy{}, fmt.Errorf("build trade risk policy: %w", err)
	}
	return policy, nil
}

func decimalFromFloat(value float64) (decimal.Decimal, error) {
	result, err := decimal.NewFromString(strconv.FormatFloat(value, 'f', -1, 64))
	if err != nil {
		return decimal.Zero, fmt.Errorf("convert risk config value %v to decimal: %w", value, err)
	}
	return result, nil
}
