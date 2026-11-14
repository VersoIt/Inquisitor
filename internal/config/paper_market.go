package config

import (
	"fmt"
	"slices"
	"time"

	"github.com/VersoIt/Inquisitor/internal/risk"
	"github.com/VersoIt/Inquisitor/internal/strategy"
	"github.com/shopspring/decimal"
)

func (c Config) PaperMarketPolicy(maxInstrumentAge time.Duration) (strategy.PaperMarketPolicy, error) {
	r, err := c.TradeRiskPolicy()
	if err != nil {
		return strategy.PaperMarketPolicy{}, err
	}
	if r.AllowedMode != risk.ModePaper || c.Trading.AllowLive || c.Trading.BaseCurrency != "USDT" {
		return strategy.PaperMarketPolicy{}, fmt.Errorf("paper market sourcing requires PAPER/USDT config with live disabled")
	}
	factor, err := decimalFromFloat(c.Slippage.ConservativeMultiplier)
	if err != nil {
		return strategy.PaperMarketPolicy{}, err
	}
	p := strategy.PaperMarketPolicy{MaxDataAge: r.MaxDataAge, MaxInstrumentAge: maxInstrumentAge, MaxSpreadBPS: r.MaxSpreadBPS, DepthBandBPS: r.MaxSlippageBPS,
		ExpectedSlippageBPS: decimal.NewFromInt(int64(c.Slippage.DefaultBps)).Mul(factor), AllowedSymbols: slices.Clone(r.AllowedSymbols)}
	if err := strategy.ValidatePaperMarketPolicy(p); err != nil {
		return strategy.PaperMarketPolicy{}, err
	}
	return p, nil
}
