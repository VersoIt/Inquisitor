package config_test

import (
	"math"
	"testing"
	"time"

	"github.com/VersoIt/Inquisitor/internal/config"
)

func TestPaperMarketPolicyMapsConfiguredRiskAndConservativeCosts(t *testing.T) {
	c := validConfig()
	c.Trading.BaseCurrency = "USDT"
	c.Slippage.DefaultBps = 3
	c.Slippage.ConservativeMultiplier = 1.5
	c.Risk.MaxSlippageBps = 10
	c.Risk.MaxSpreadBps = 5
	p, err := c.PaperMarketPolicy(24 * time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if p.MaxDataAge != 3*time.Second || p.MaxInstrumentAge != 24*time.Hour || p.AllowedSymbols[0] != "BTCUSDT" {
		t.Fatalf("bad mapping %+v", p)
	}
	assertDecimalEqual(t, "spread", p.MaxSpreadBPS, "5")
	assertDecimalEqual(t, "band", p.DepthBandBPS, "10")
	assertDecimalEqual(t, "slippage", p.ExpectedSlippageBPS, "4.5")
	p.AllowedSymbols[0] = "ETHUSDT"
	if c.Exchange.Symbols[0] != "BTCUSDT" {
		t.Fatal("policy aliases config")
	}
}

func TestPaperMarketPolicyRejectsUnsafeConfigAndAges(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*config.Config)
		age    time.Duration
	}{
		{"non-USDT", func(c *config.Config) { c.Trading.BaseCurrency = "USDC" }, time.Hour},
		{"live", func(c *config.Config) { c.Trading.Mode = "live" }, time.Hour},
		{"allow live", func(c *config.Config) { c.Trading.AllowLive = true }, time.Hour},
		{"zero age", func(*config.Config) {}, 0}, {"stale metadata policy", func(*config.Config) {}, 25 * time.Hour},
		{"nonfinite multiplier", func(c *config.Config) { c.Slippage.ConservativeMultiplier = math.NaN() }, time.Hour},
		{"slippage outside limit", func(c *config.Config) { c.Slippage.DefaultBps = 20 }, time.Hour},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := validConfig()
			c.Trading.BaseCurrency = "USDT"
			c.Risk.MaxSlippageBps = 10
			tc.mutate(&c)
			if _, err := c.PaperMarketPolicy(tc.age); err == nil {
				t.Fatal("unsafe source policy accepted")
			}
		})
	}
}
