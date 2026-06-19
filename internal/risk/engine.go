package risk

import (
	"errors"
	"strings"

	"github.com/shopspring/decimal"
)

type TradeRiskEngine struct {
	policy Policy
}

func NewTradeRiskEngine(policy Policy) (*TradeRiskEngine, error) {
	if err := ValidatePolicy(policy); err != nil {
		return nil, err
	}
	policy.AllowedSymbols = append([]string(nil), policy.AllowedSymbols...)
	return &TradeRiskEngine{policy: policy}, nil
}

func (e *TradeRiskEngine) Evaluate(input EvaluationInput) (Decision, error) {
	if e == nil {
		return Decision{}, errors.New("risk engine is required")
	}
	if input.EvaluatedAt.IsZero() {
		return Decision{}, errors.New("risk evaluation failed: evaluated_at is required")
	}
	now := input.EvaluatedAt.UTC()
	intent := normalizeIntent(input.Intent)
	decision := Decision{
		IntentID:   intent.IntentID,
		StopLoss:   intent.StopLoss,
		TakeProfit: intent.TakeProfit,
		CreatedAt:  now,
		Checks:     make([]Check, 0, 32),
	}
	add := func(name string, passed bool, reason string) {
		check := Check{Name: name, Passed: passed}
		if !passed {
			check.Reason = reason
		}
		decision.Checks = append(decision.Checks, check)
	}

	add("trading_enabled", input.Runtime.TradingEnabled, "trading_disabled")
	add("mode_allowed", input.Runtime.Mode == e.policy.AllowedMode && KnownMode(input.Runtime.Mode), "trading_mode_not_allowed")
	add("hypothesis_approved", intent.HypothesisApproved, "hypothesis_not_approved_for_mode")
	add("kill_switch_inactive", !input.Runtime.KillSwitchActive, "kill_switch_active")
	identityValid := intent.IntentID != "" && intent.HypothesisID != "" && intent.StrategyName != "" && intent.Symbol != "" && KnownSide(intent.Side)
	add("intent_identity", identityValid, "intent_identity_invalid")
	add("intent_reason", intent.Reason != "", "intent_reason_missing")
	confidenceFloor := e.policy.MinConfidence
	if intent.HypothesisMinConfidence > confidenceFloor {
		confidenceFloor = intent.HypothesisMinConfidence
	}
	confidenceValid := intent.Confidence >= 0 && intent.Confidence <= 100 && intent.HypothesisMinConfidence >= 0 && intent.HypothesisMinConfidence <= 100
	add("signal_confidence", confidenceValid && intent.Confidence >= confidenceFloor, "signal_confidence_below_threshold")
	add("intent_time", !intent.CreatedAt.IsZero() && !intent.CreatedAt.After(now), "intent_time_invalid")

	accountValid := validAccountState(input.Account)
	add("account_state", accountValid, "account_state_invalid")
	dataFresh := !input.Market.DataTime.IsZero() && !input.Market.DataTime.After(now) && now.Sub(input.Market.DataTime.UTC()) <= e.policy.MaxDataAge
	add("data_fresh", dataFresh, "market_data_stale_or_invalid")
	add("spread_acceptable", !input.Market.SpreadBPS.IsNegative() && input.Market.SpreadBPS.LessThanOrEqual(e.policy.MaxSpreadBPS), "spread_limit_exceeded")
	add("slippage_acceptable", !input.Market.ExpectedSlippageBPS.IsNegative() && input.Market.ExpectedSlippageBPS.LessThanOrEqual(e.policy.MaxSlippageBPS), "slippage_limit_exceeded")
	add("volatility_acceptable", input.Market.VolatilityAcceptable, "volatility_unacceptable")
	add("orderbook_valid", input.Market.OrderbookValid, "orderbook_invalid")
	add("liquidity_sufficient", !input.Market.LiquidityQuote.IsNegative() && input.Market.LiquidityQuote.GreaterThanOrEqual(e.policy.MinLiquidityQuote), "liquidity_below_threshold")

	entryPresent := intent.EntryPrice.GreaterThan(decimal.Zero)
	stopPresent := intent.StopLoss.GreaterThan(decimal.Zero)
	add("entry_price_present", entryPresent, "entry_price_missing_or_invalid")
	add("stop_loss_present", stopPresent, "stop_loss_missing")
	stopDistance, stopGeometryValid := stopDistance(intent)
	add("stop_distance_valid", stopGeometryValid, "stop_distance_invalid_for_side")
	takeProfitValid := intent.TakeProfit.IsZero() ||
		(intent.Side == SideLong && intent.TakeProfit.GreaterThan(intent.EntryPrice)) ||
		(intent.Side == SideShort && intent.TakeProfit.LessThan(intent.EntryPrice) && intent.TakeProfit.IsPositive())
	add("take_profit_valid", takeProfitValid, "take_profit_invalid_for_side")

	add("daily_loss_available", accountValid && belowLossLimit(input.Account.DailyLoss, input.Account.DayStartEquity, e.policy.MaxDailyLossPct), "daily_loss_limit_exceeded")
	add("weekly_loss_available", accountValid && belowLossLimit(input.Account.WeeklyLoss, input.Account.WeekStartEquity, e.policy.MaxWeeklyLossPct), "weekly_loss_limit_exceeded")
	add("drawdown_available", accountValid && belowDrawdownLimit(input.Account.Equity, input.Account.PeakEquity, e.policy.MaxTotalDrawdownPct), "total_drawdown_limit_exceeded")
	add("losing_streak_available", accountValid && input.Account.LosingStreak < e.policy.MaxLosingStreak, "losing_streak_limit_exceeded")
	add("position_slot_available", accountValid && input.Account.OpenPositions < e.policy.MaxOpenPositions, "max_open_positions_reached")
	add("symbol_allowed", input.Market.SymbolAllowed && symbolAllowed(intent.Symbol, e.policy.AllowedSymbols), "symbol_not_allowed")
	add("side_allowed", intent.Side != SideShort || e.policy.AllowShort, "short_side_not_allowed")
	leverageValid := intent.Leverage.GreaterThan(decimal.Zero) && intent.Leverage.LessThanOrEqual(e.policy.MaxLeverage)
	add("leverage_allowed", leverageValid, "leverage_limit_exceeded")

	instrumentValid := validInstrument(intent.Symbol, input.Market.Instrument)
	add("instrument_available", instrumentValid, "instrument_info_missing_or_invalid")
	pricesAligned := instrumentValid && entryPresent && aligned(intent.EntryPrice, input.Market.Instrument.TickSize) && stopPresent && aligned(intent.StopLoss, input.Market.Instrument.TickSize)
	if intent.TakeProfit.IsPositive() {
		pricesAligned = pricesAligned && aligned(intent.TakeProfit, input.Market.Instrument.TickSize)
	} else if intent.TakeProfit.IsNegative() {
		pricesAligned = false
	}
	add("tick_size_respected", pricesAligned, "price_not_aligned_to_tick_size")

	riskPctValid := intent.HypothesisMaxRiskPct.GreaterThan(decimal.Zero) && intent.HypothesisMaxRiskPct.LessThanOrEqual(decimal.NewFromInt(1))
	add("hypothesis_risk_limit", riskPctValid, "hypothesis_risk_limit_invalid")
	riskPct := decimal.Zero
	if riskPctValid {
		riskPct = decimal.Min(e.policy.RiskPerTradePct, intent.HypothesisMaxRiskPct)
	}
	riskAmount := decimal.Zero
	rawQuantity := decimal.Zero
	finalQuantity := decimal.Zero
	if accountValid && stopGeometryValid && instrumentValid && riskPct.IsPositive() {
		riskAmount = input.Account.Equity.Mul(riskPct).Div(decimal.NewFromInt(100))
		rawQuantity = riskAmount.Div(stopDistance)
		finalQuantity = roundDownToStep(rawQuantity, input.Market.Instrument.QuantityStep)
	}
	quantityValid := finalQuantity.IsPositive() && instrumentValid && finalQuantity.LessThanOrEqual(input.Market.Instrument.MaxOrderQuantity)
	add("quantity_valid", quantityValid, "calculated_quantity_invalid")
	add("quantity_step_respected", quantityValid && aligned(finalQuantity, input.Market.Instrument.QuantityStep), "quantity_not_aligned_to_step")
	add("min_order_quantity", quantityValid && finalQuantity.GreaterThanOrEqual(input.Market.Instrument.MinOrderQuantity), "quantity_below_minimum")
	notional := finalQuantity.Mul(intent.EntryPrice)
	add("min_notional", quantityValid && notional.GreaterThanOrEqual(input.Market.Instrument.MinNotional), "notional_below_minimum")
	maxLoss := finalQuantity.Mul(stopDistance)
	add("trade_risk_within_budget", quantityValid && maxLoss.IsPositive() && maxLoss.LessThanOrEqual(riskAmount), "trade_risk_budget_exceeded")
	portfolioLimit := input.Account.Equity.Mul(e.policy.MaxPortfolioExposurePct).Div(decimal.NewFromInt(100))
	add("portfolio_exposure", accountValid && quantityValid && input.Account.TotalExposure.Add(notional).LessThanOrEqual(portfolioLimit), "portfolio_exposure_limit_exceeded")
	correlatedLimit := input.Account.Equity.Mul(e.policy.MaxCorrelatedExposurePct).Div(decimal.NewFromInt(100))
	add("correlated_exposure", accountValid && quantityValid && input.Account.CorrelatedExposure.Add(notional).LessThanOrEqual(correlatedLimit), "correlated_exposure_limit_exceeded")

	decision.Approved = allChecksPassed(decision.Checks)
	if decision.Approved {
		decision.FinalQuantity = finalQuantity
		decision.MaxLoss = maxLoss
		decision.Reason = "risk_checks_passed"
	} else {
		decision.FinalQuantity = decimal.Zero
		decision.MaxLoss = decimal.Zero
		decision.Reason = firstFailedReason(decision.Checks)
	}
	if err := ValidateDecision(decision); err != nil {
		return Decision{}, err
	}
	return decision, nil
}

func normalizeIntent(intent TradeIntent) TradeIntent {
	intent.IntentID = strings.TrimSpace(intent.IntentID)
	intent.HypothesisID = strings.TrimSpace(intent.HypothesisID)
	intent.StrategyName = strings.TrimSpace(intent.StrategyName)
	intent.Symbol = strings.ToUpper(strings.TrimSpace(intent.Symbol))
	intent.Side = Side(strings.ToUpper(strings.TrimSpace(string(intent.Side))))
	intent.Reason = strings.TrimSpace(intent.Reason)
	intent.CreatedAt = intent.CreatedAt.UTC()
	return intent
}

func validAccountState(account AccountState) bool {
	return account.Equity.GreaterThan(decimal.Zero) &&
		account.DayStartEquity.GreaterThan(decimal.Zero) &&
		account.WeekStartEquity.GreaterThan(decimal.Zero) &&
		account.PeakEquity.GreaterThanOrEqual(account.Equity) &&
		!account.DailyLoss.IsNegative() && !account.WeeklyLoss.IsNegative() &&
		account.LosingStreak >= 0 && account.OpenPositions >= 0 &&
		!account.TotalExposure.IsNegative() && !account.CorrelatedExposure.IsNegative() &&
		account.CorrelatedExposure.LessThanOrEqual(account.TotalExposure)
}

func validInstrument(symbol string, instrument Instrument) bool {
	return instrument.Available && strings.ToUpper(strings.TrimSpace(instrument.Symbol)) == symbol &&
		instrument.TickSize.GreaterThan(decimal.Zero) && instrument.QuantityStep.GreaterThan(decimal.Zero) &&
		instrument.MinOrderQuantity.GreaterThan(decimal.Zero) && instrument.MaxOrderQuantity.GreaterThanOrEqual(instrument.MinOrderQuantity) &&
		!instrument.MinNotional.IsNegative()
}

func stopDistance(intent TradeIntent) (decimal.Decimal, bool) {
	if intent.EntryPrice.LessThanOrEqual(decimal.Zero) || intent.StopLoss.LessThanOrEqual(decimal.Zero) || !KnownSide(intent.Side) {
		return decimal.Zero, false
	}
	switch intent.Side {
	case SideLong:
		if !intent.StopLoss.LessThan(intent.EntryPrice) {
			return decimal.Zero, false
		}
	case SideShort:
		if !intent.StopLoss.GreaterThan(intent.EntryPrice) {
			return decimal.Zero, false
		}
	}
	distance := intent.EntryPrice.Sub(intent.StopLoss).Abs()
	return distance, distance.GreaterThan(decimal.Zero)
}

func belowLossLimit(loss, base, limitPct decimal.Decimal) bool {
	if loss.IsNegative() || base.LessThanOrEqual(decimal.Zero) {
		return false
	}
	limit := base.Mul(limitPct).Div(decimal.NewFromInt(100))
	return loss.LessThan(limit)
}

func belowDrawdownLimit(equity, peak, limitPct decimal.Decimal) bool {
	if equity.LessThanOrEqual(decimal.Zero) || peak.LessThan(equity) || peak.LessThanOrEqual(decimal.Zero) {
		return false
	}
	drawdown := peak.Sub(equity).Div(peak).Mul(decimal.NewFromInt(100))
	return drawdown.LessThan(limitPct)
}

func symbolAllowed(symbol string, allowed []string) bool {
	for _, candidate := range allowed {
		if symbol == candidate {
			return true
		}
	}
	return false
}

func aligned(value, step decimal.Decimal) bool {
	return value.GreaterThanOrEqual(decimal.Zero) && step.GreaterThan(decimal.Zero) && value.Mod(step).IsZero()
}

func roundDownToStep(value, step decimal.Decimal) decimal.Decimal {
	if value.LessThanOrEqual(decimal.Zero) || step.LessThanOrEqual(decimal.Zero) {
		return decimal.Zero
	}
	return value.Div(step).Floor().Mul(step)
}

func allChecksPassed(checks []Check) bool {
	for _, check := range checks {
		if !check.Passed {
			return false
		}
	}
	return true
}

func firstFailedReason(checks []Check) string {
	for _, check := range checks {
		if !check.Passed {
			return check.Reason
		}
	}
	return "risk_checks_failed"
}
