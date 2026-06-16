package backtest

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

const bpsDenominator = 10000

type Direction string

const (
	DirectionLong  Direction = "LONG"
	DirectionShort Direction = "SHORT"
)

type LiquidityRole string

const (
	LiquidityMaker LiquidityRole = "MAKER"
	LiquidityTaker LiquidityRole = "TAKER"
)

type CostModel struct {
	MakerFeeBPS                decimal.Decimal
	TakerFeeBPS                decimal.Decimal
	SpreadBPS                  decimal.Decimal
	SlippageBPS                decimal.Decimal
	SlippageConservativeFactor decimal.Decimal
}

type RoundTripInput struct {
	Direction Direction

	EntryTime     time.Time
	ExitTime      time.Time
	EntryMidPrice decimal.Decimal
	ExitMidPrice  decimal.Decimal
	Quantity      decimal.Decimal

	EntryLiquidity LiquidityRole
	ExitLiquidity  LiquidityRole
	Costs          CostModel
}

type Fill struct {
	Time          time.Time
	MidPrice      decimal.Decimal
	ExecutedPrice decimal.Decimal
	Quantity      decimal.Decimal
	Notional      decimal.Decimal
	Fee           decimal.Decimal
	FeeBPS        decimal.Decimal
	SpreadBPS     decimal.Decimal
	SlippageBPS   decimal.Decimal
}

type RoundTrip struct {
	Direction Direction
	Entry     Fill
	Exit      Fill

	GrossPnL decimal.Decimal
	Fees     decimal.Decimal
	NetPnL   decimal.Decimal
	Return   decimal.Decimal
}

func NewCostModel(makerFeeBPS, takerFeeBPS int, spreadBPS int, slippageBPS int, conservativeSlippageFactor float64) (CostModel, error) {
	model := CostModel{
		MakerFeeBPS:                decimal.NewFromInt(int64(makerFeeBPS)),
		TakerFeeBPS:                decimal.NewFromInt(int64(takerFeeBPS)),
		SpreadBPS:                  decimal.NewFromInt(int64(spreadBPS)),
		SlippageBPS:                decimal.NewFromInt(int64(slippageBPS)),
		SlippageConservativeFactor: decimal.NewFromFloat(conservativeSlippageFactor),
	}
	if conservativeSlippageFactor == 0 {
		model.SlippageConservativeFactor = decimal.NewFromInt(1)
	}
	if err := ValidateCostModel(model); err != nil {
		return CostModel{}, err
	}
	return model, nil
}

func EvaluateRoundTrip(input RoundTripInput) (RoundTrip, error) {
	input.EntryLiquidity = normalizeLiquidity(input.EntryLiquidity)
	input.ExitLiquidity = normalizeLiquidity(input.ExitLiquidity)
	input.Direction = Direction(strings.ToUpper(strings.TrimSpace(string(input.Direction))))
	if input.EntryLiquidity == "" {
		input.EntryLiquidity = LiquidityTaker
	}
	if input.ExitLiquidity == "" {
		input.ExitLiquidity = LiquidityTaker
	}
	if err := ValidateRoundTripInput(input); err != nil {
		return RoundTrip{}, err
	}

	entry := executableFill(input.Direction, input.EntryTime, input.EntryMidPrice, input.Quantity, input.EntryLiquidity, input.Costs, true)
	exit := executableFill(input.Direction, input.ExitTime, input.ExitMidPrice, input.Quantity, input.ExitLiquidity, input.Costs, false)
	gross := grossPnL(input.Direction, entry.ExecutedPrice, exit.ExecutedPrice, input.Quantity)
	fees := entry.Fee.Add(exit.Fee)
	net := gross.Sub(fees)
	result := RoundTrip{
		Direction: input.Direction,
		Entry:     entry,
		Exit:      exit,
		GrossPnL:  gross,
		Fees:      fees,
		NetPnL:    net,
	}
	if !entry.Notional.IsZero() {
		result.Return = net.Div(entry.Notional)
	}
	return result, nil
}

func ValidateCostModel(model CostModel) error {
	var problems []string
	addNonNegative := func(name string, value decimal.Decimal) {
		if value.IsNegative() {
			problems = append(problems, name+" must be greater than or equal to zero")
		}
	}
	addNonNegative("maker_fee_bps", model.MakerFeeBPS)
	addNonNegative("taker_fee_bps", model.TakerFeeBPS)
	addNonNegative("spread_bps", model.SpreadBPS)
	addNonNegative("slippage_bps", model.SlippageBPS)
	if model.SlippageConservativeFactor.IsZero() {
		problems = append(problems, "slippage_conservative_factor must be positive")
	}
	if model.SlippageConservativeFactor.IsNegative() {
		problems = append(problems, "slippage_conservative_factor must be positive")
	}
	if len(problems) == 0 {
		impactBPS := model.SpreadBPS.Div(decimal.NewFromInt(2)).Add(model.SlippageBPS.Mul(model.SlippageConservativeFactor))
		if impactBPS.GreaterThanOrEqual(decimal.NewFromInt(bpsDenominator)) {
			problems = append(problems, "combined half-spread and slippage impact must be less than 10000 bps")
		}
	}
	if len(problems) > 0 {
		return errors.New("backtest cost model validation failed: " + strings.Join(problems, "; "))
	}
	return nil
}

func ValidateRoundTripInput(input RoundTripInput) error {
	var problems []string
	if !KnownDirection(input.Direction) {
		problems = append(problems, "direction must be LONG or SHORT")
	}
	if !KnownLiquidity(input.EntryLiquidity) {
		problems = append(problems, "entry_liquidity must be MAKER or TAKER")
	}
	if !KnownLiquidity(input.ExitLiquidity) {
		problems = append(problems, "exit_liquidity must be MAKER or TAKER")
	}
	if input.EntryTime.IsZero() {
		problems = append(problems, "entry_time is required")
	}
	if input.ExitTime.IsZero() {
		problems = append(problems, "exit_time is required")
	}
	if !input.EntryTime.IsZero() && !input.ExitTime.IsZero() && !input.ExitTime.After(input.EntryTime) {
		problems = append(problems, "exit_time must be after entry_time")
	}
	if input.EntryMidPrice.LessThanOrEqual(decimal.Zero) {
		problems = append(problems, "entry_mid_price must be positive")
	}
	if input.ExitMidPrice.LessThanOrEqual(decimal.Zero) {
		problems = append(problems, "exit_mid_price must be positive")
	}
	if input.Quantity.LessThanOrEqual(decimal.Zero) {
		problems = append(problems, "quantity must be positive")
	}
	if err := ValidateCostModel(input.Costs); err != nil {
		problems = append(problems, err.Error())
	}
	if len(problems) > 0 {
		return errors.New("backtest round trip validation failed: " + strings.Join(problems, "; "))
	}
	return nil
}

func KnownDirection(direction Direction) bool {
	switch direction {
	case DirectionLong, DirectionShort:
		return true
	default:
		return false
	}
}

func KnownLiquidity(liquidity LiquidityRole) bool {
	switch liquidity {
	case LiquidityMaker, LiquidityTaker:
		return true
	default:
		return false
	}
}

func executableFill(direction Direction, fillTime time.Time, midPrice decimal.Decimal, quantity decimal.Decimal, liquidity LiquidityRole, costs CostModel, entry bool) Fill {
	spreadBPS := costs.SpreadBPS.Div(decimal.NewFromInt(2))
	slippageBPS := costs.SlippageBPS.Mul(costs.SlippageConservativeFactor)
	impactBPS := spreadBPS.Add(slippageBPS)
	price := applyPriceImpact(direction, midPrice, impactBPS, entry)
	notional := price.Mul(quantity)
	feeBPS := feeBPSForLiquidity(costs, liquidity)
	fee := notional.Mul(bpsRatio(feeBPS))
	return Fill{
		Time:          fillTime.UTC(),
		MidPrice:      midPrice,
		ExecutedPrice: price,
		Quantity:      quantity,
		Notional:      notional,
		Fee:           fee,
		FeeBPS:        feeBPS,
		SpreadBPS:     spreadBPS,
		SlippageBPS:   slippageBPS,
	}
}

func applyPriceImpact(direction Direction, midPrice decimal.Decimal, impactBPS decimal.Decimal, entry bool) decimal.Decimal {
	ratio := bpsRatio(impactBPS)
	switch {
	case direction == DirectionLong && entry:
		return midPrice.Mul(decimal.NewFromInt(1).Add(ratio))
	case direction == DirectionLong && !entry:
		return midPrice.Mul(decimal.NewFromInt(1).Sub(ratio))
	case direction == DirectionShort && entry:
		return midPrice.Mul(decimal.NewFromInt(1).Sub(ratio))
	case direction == DirectionShort && !entry:
		return midPrice.Mul(decimal.NewFromInt(1).Add(ratio))
	default:
		return midPrice
	}
}

func grossPnL(direction Direction, entryPrice, exitPrice, quantity decimal.Decimal) decimal.Decimal {
	switch direction {
	case DirectionLong:
		return exitPrice.Sub(entryPrice).Mul(quantity)
	case DirectionShort:
		return entryPrice.Sub(exitPrice).Mul(quantity)
	default:
		return decimal.Zero
	}
}

func feeBPSForLiquidity(costs CostModel, liquidity LiquidityRole) decimal.Decimal {
	if liquidity == LiquidityMaker {
		return costs.MakerFeeBPS
	}
	return costs.TakerFeeBPS
}

func bpsRatio(value decimal.Decimal) decimal.Decimal {
	return value.Div(decimal.NewFromInt(bpsDenominator))
}

func normalizeLiquidity(value LiquidityRole) LiquidityRole {
	return LiquidityRole(strings.ToUpper(strings.TrimSpace(string(value))))
}

func (t RoundTrip) String() string {
	return fmt.Sprintf("%s net_pnl=%s return=%s", t.Direction, t.NetPnL.String(), t.Return.String())
}
