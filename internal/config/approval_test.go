package config_test

import (
	"math"
	"testing"

	"github.com/VersoIt/Inquisitor/internal/config"
)

func TestPaperApprovalPolicyMappingAndConservativeBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*config.Config)
		valid  bool
	}{
		{"example", func(*config.Config) {}, true},
		{"minimum sample", func(c *config.Config) { c.Research.MinTrades = 100 }, true},
		{"trading disabled", func(c *config.Config) { c.Trading.Enabled = false }, true},
		{"small sample", func(c *config.Config) { c.Research.MinTrades = 99 }, false},
		{"unit profit factor", func(c *config.Config) { c.Research.MinProfitFactor = 1 }, false},
		{"zero expectancy", func(c *config.Config) { c.Research.MinExpectancyR = 0 }, false},
		{"no OOS", func(c *config.Config) { c.Research.RequireOutOfSample = false }, false},
		{"no walk forward", func(c *config.Config) { c.Research.RequireWalkForward = false }, false},
		{"no regime analysis", func(c *config.Config) { c.Research.RequireRegimeAnalysis = false }, false},
		{"NaN PF", func(c *config.Config) { c.Research.MinProfitFactor = math.NaN() }, false},
		{"infinite expectancy", func(c *config.Config) { c.Research.MinExpectancyR = math.Inf(1) }, false},
		{"NaN drawdown", func(c *config.Config) { c.Research.MaxDrawdownPct = math.NaN() }, false},
		{"infinite drawdown", func(c *config.Config) { c.Research.MaxDrawdownPct = math.Inf(1) }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := validConfig()
			tc.mutate(&c)
			p, err := c.PaperApprovalPolicy()
			if !tc.valid {
				if err == nil {
					t.Fatal("unsafe policy accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			assertDecimalEqual(t, "PF", p.MinProfitFactor, "1.15")
			assertDecimalEqual(t, "expectancy", p.MinExpectancy, "0.05")
			if !p.Enabled || !p.RequireCosts || !p.RequireOutOfSample || !p.RequireWalkForward || !p.RequireRegimeAnalysis || p.MinTrades != c.Research.MinTrades || p.MaxDrawdownPct != c.Research.MaxDrawdownPct {
				t.Fatalf("mapping mismatch: %+v", p)
			}
		})
	}
}
