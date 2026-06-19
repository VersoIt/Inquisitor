package risk

import (
	"errors"
	"strings"

	"github.com/shopspring/decimal"
)

func ValidatePolicy(policy Policy) error {
	var problems []string
	if !KnownMode(policy.AllowedMode) {
		problems = append(problems, "allowed_mode must be PAPER or LIVE")
	}
	validatePct := func(name string, value decimal.Decimal, max decimal.Decimal) {
		if value.LessThanOrEqual(decimal.Zero) || value.GreaterThan(max) {
			problems = append(problems, name+" must be greater than zero and no more than "+max.String())
		}
	}
	validatePct("risk_per_trade_pct", policy.RiskPerTradePct, decimal.NewFromInt(1))
	validatePct("max_daily_loss_pct", policy.MaxDailyLossPct, decimal.NewFromInt(100))
	validatePct("max_weekly_loss_pct", policy.MaxWeeklyLossPct, decimal.NewFromInt(100))
	validatePct("max_total_drawdown_pct", policy.MaxTotalDrawdownPct, decimal.NewFromInt(100))
	validatePct("max_portfolio_exposure_pct", policy.MaxPortfolioExposurePct, decimal.NewFromInt(100))
	validatePct("max_correlated_exposure_pct", policy.MaxCorrelatedExposurePct, decimal.NewFromInt(100))
	if policy.MaxDailyLossPct.GreaterThan(policy.MaxWeeklyLossPct) {
		problems = append(problems, "max_daily_loss_pct must not exceed max_weekly_loss_pct")
	}
	if policy.MaxWeeklyLossPct.GreaterThan(policy.MaxTotalDrawdownPct) {
		problems = append(problems, "max_weekly_loss_pct must not exceed max_total_drawdown_pct")
	}
	if policy.MaxCorrelatedExposurePct.GreaterThan(policy.MaxPortfolioExposurePct) {
		problems = append(problems, "max_correlated_exposure_pct must not exceed max_portfolio_exposure_pct")
	}
	if policy.MaxLosingStreak <= 0 {
		problems = append(problems, "max_losing_streak must be positive")
	}
	if policy.MaxOpenPositions <= 0 {
		problems = append(problems, "max_open_positions must be positive")
	}
	if policy.MaxLeverage.LessThanOrEqual(decimal.Zero) {
		problems = append(problems, "max_leverage must be positive")
	}
	if policy.MaxSpreadBPS.IsNegative() {
		problems = append(problems, "max_spread_bps must be non-negative")
	}
	if policy.MaxSlippageBPS.IsNegative() {
		problems = append(problems, "max_slippage_bps must be non-negative")
	}
	if policy.MinConfidence < 0 || policy.MinConfidence > 100 {
		problems = append(problems, "min_confidence must be between zero and 100")
	}
	if policy.MinLiquidityQuote.IsNegative() {
		problems = append(problems, "min_liquidity_quote must be non-negative")
	}
	if policy.MaxDataAge <= 0 {
		problems = append(problems, "max_data_age must be positive")
	}
	if len(policy.AllowedSymbols) == 0 {
		problems = append(problems, "allowed_symbols must not be empty")
	}
	seen := make(map[string]struct{}, len(policy.AllowedSymbols))
	for index, symbol := range policy.AllowedSymbols {
		normalized := strings.ToUpper(strings.TrimSpace(symbol))
		if normalized == "" {
			problems = append(problems, "allowed_symbols["+decimal.NewFromInt(int64(index)).String()+"] must not be empty")
			continue
		}
		if symbol != normalized {
			problems = append(problems, "allowed_symbols must be uppercase and trimmed")
		}
		if _, exists := seen[normalized]; exists {
			problems = append(problems, "allowed_symbols must not contain duplicates")
		}
		seen[normalized] = struct{}{}
	}
	if len(problems) > 0 {
		return errors.New("risk policy validation failed: " + strings.Join(problems, "; "))
	}
	return nil
}

func ValidateDecision(decision Decision) error {
	var problems []string
	if strings.TrimSpace(decision.IntentID) == "" {
		problems = append(problems, "intent_id is required")
	}
	if decision.CreatedAt.IsZero() {
		problems = append(problems, "created_at is required")
	}
	if len(decision.Checks) == 0 {
		problems = append(problems, "checks must not be empty")
	}
	failed := 0
	for index, check := range decision.Checks {
		if strings.TrimSpace(check.Name) == "" {
			problems = append(problems, "checks["+decimal.NewFromInt(int64(index)).String()+"].name is required")
		}
		if !check.Passed {
			failed++
			if strings.TrimSpace(check.Reason) == "" {
				problems = append(problems, "failed checks require reason")
			}
		}
	}
	if decision.Approved {
		if failed > 0 {
			problems = append(problems, "approved decision must not contain failed checks")
		}
		if decision.FinalQuantity.LessThanOrEqual(decimal.Zero) || decision.MaxLoss.LessThanOrEqual(decimal.Zero) {
			problems = append(problems, "approved decision requires positive quantity and max_loss")
		}
		if !decision.ReasonIsApproved() {
			problems = append(problems, "approved decision reason is invalid")
		}
	} else {
		if failed == 0 {
			problems = append(problems, "rejected decision requires a failed check")
		}
		if !decision.FinalQuantity.IsZero() || !decision.MaxLoss.IsZero() {
			problems = append(problems, "rejected decision requires zero quantity and max_loss")
		}
		if strings.TrimSpace(decision.Reason) == "" {
			problems = append(problems, "rejected decision requires reason")
		}
	}
	if len(problems) > 0 {
		return errors.New("risk decision validation failed: " + strings.Join(problems, "; "))
	}
	return nil
}

func (d Decision) ReasonIsApproved() bool {
	return d.Reason == "risk_checks_passed"
}

func KnownMode(mode Mode) bool {
	return mode == ModePaper || mode == ModeLive
}

func KnownSide(side Side) bool {
	return side == SideLong || side == SideShort
}
