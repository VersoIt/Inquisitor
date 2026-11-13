package config

import (
	"fmt"

	"github.com/VersoIt/Inquisitor/internal/research"
	"github.com/VersoIt/Inquisitor/internal/strategy"
)

// PaperApprovalPolicy is shared by grant and entry checks, never by revocation.
func (c Config) PaperApprovalPolicy() (research.ResultGatePolicy, error) {
	pf, err := decimalFromFloat(c.Research.MinProfitFactor)
	if err != nil {
		return research.ResultGatePolicy{}, err
	}
	expectancy, err := decimalFromFloat(c.Research.MinExpectancyR)
	if err != nil {
		return research.ResultGatePolicy{}, err
	}
	p := research.ResultGatePolicy{Enabled: true, MinTrades: c.Research.MinTrades,
		MinProfitFactor: pf, MinExpectancy: expectancy, MaxDrawdownPct: c.Research.MaxDrawdownPct,
		RequireOutOfSample: c.Research.RequireOutOfSample, RequireWalkForward: c.Research.RequireWalkForward,
		RequireRegimeAnalysis: c.Research.RequireRegimeAnalysis, RequireCosts: true}
	if err := strategy.ValidatePaperApprovalPolicy(p); err != nil {
		return research.ResultGatePolicy{}, fmt.Errorf("paper approval policy: %w", err)
	}
	return p, nil
}
