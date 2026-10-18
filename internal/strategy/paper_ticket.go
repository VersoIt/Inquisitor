package strategy

import (
	"fmt"
	"time"

	"github.com/VersoIt/Inquisitor/internal/hypothesis"
	"github.com/VersoIt/Inquisitor/internal/paper"
	"github.com/VersoIt/Inquisitor/internal/risk"
	"github.com/shopspring/decimal"
)

func PaperSignalTicketID(signalID string) string {
	_, decisionID := PaperRiskIDs(signalID)
	return "ticket_" + decisionID
}

// NewPaperSignalTicket binds quantities only from the persisted Risk Engine result.
func NewPaperSignalTicket(e PaperRiskEvaluation, a ApprovalSnapshot, at time.Time) (paper.OrderTicket, error) {
	if err := ValidatePaperRiskEvaluation(e); err != nil {
		return paper.OrderTicket{}, err
	}
	i, audit, r := e.Context.Input.Intent, e.Audit, e.Context.Regime
	if !audit.Decision.Approved || !a.Enabled || a.EdgeDecayed || a.Mode != risk.ModePaper || a.ApprovalID != e.ApprovalID || a.ValidationID == "" || a.HypothesisID != i.HypothesisID || a.StrategyName != i.StrategyName || a.Exchange != r.Exchange || a.Category != r.Category || a.Symbol != r.Symbol || a.Interval != r.Interval || !a.MaxRiskPct.Equal(i.HypothesisMaxRiskPct) || a.MinConfidence != i.HypothesisMinConfidence {
		return paper.OrderTicket{}, fmt.Errorf("paper ticket requires matching current PAPER approval and approved risk result")
	}
	if a.Direction == hypothesis.DirectionLong && i.Side != risk.SideLong || a.Direction == hypothesis.DirectionShort && i.Side != risk.SideShort {
		return paper.OrderTicket{}, fmt.Errorf("paper ticket side is not authorized")
	}
	if at.IsZero() || at.Before(audit.RecordedAt) {
		return paper.OrderTicket{}, fmt.Errorf("paper ticket cannot precede its risk audit")
	}
	ticket, err := paper.NewOrderTicket(paper.OrderTicketInput{TicketID: PaperSignalTicketID(e.SignalID), ValidationID: a.ValidationID, DecisionID: audit.DecisionID, IntentID: i.IntentID,
		Exchange: r.Exchange, Category: r.Category, Symbol: r.Symbol, Interval: r.Interval, Side: paper.OrderSide(i.Side), Quantity: audit.Decision.FinalQuantity,
		EntryPrice: i.EntryPrice, StopLoss: audit.Decision.StopLoss, TakeProfit: audit.Decision.TakeProfit, Leverage: i.Leverage, MaxLoss: audit.Decision.MaxLoss, Confidence: i.Confidence, Reason: audit.Decision.Reason, CreatedAt: auditTime(at)})
	if err != nil {
		return paper.OrderTicket{}, err
	}
	ticket.SourceSignalID, ticket.SourceApprovalID = e.SignalID, e.ApprovalID
	return ticket, nil
}

func CheckPaperSignalTicket(ticket paper.OrderTicket, e PaperRiskEvaluation, a ApprovalSnapshot) error {
	if err := paper.ValidateOrderTicket(ticket); err != nil {
		return err
	}
	expected, err := NewPaperSignalTicket(e, a, ticket.CreatedAt)
	if err != nil {
		return err
	}
	if !sameRiskJSON(expected, ticket) {
		return fmt.Errorf("paper ticket differs from its persisted signal/risk provenance")
	}
	return nil
}

func RequireFreshPaperRisk(e PaperRiskEvaluation, at time.Time) error {
	if err := ValidatePaperRiskEvaluation(e); err != nil {
		return err
	}
	dataTime := e.Context.Input.Market.DataTime
	if at.IsZero() || at.Before(e.Audit.RecordedAt) || dataTime.IsZero() || dataTime.After(at) || at.Sub(dataTime) > e.Context.Policy.MaxDataAge {
		return fmt.Errorf("paper risk evaluation is stale or from the future")
	}
	return nil
}

func CheckPaperRiskPolicy(e PaperRiskEvaluation, current risk.Policy) error {
	if err := risk.ValidatePolicy(current); err != nil {
		return err
	}
	if !sameRiskJSON(e.Context.Policy, current) {
		return fmt.Errorf("risk policy changed after signal evaluation; a new signal is required")
	}
	return nil
}

// CheckPaperSignalFillBudget follows provenance/freshness checks. It bounds the
// modeled stop loss plus entry fee, not future exit fees or stop execution gaps.
func CheckPaperSignalFillBudget(e PaperRiskEvaluation, ticket paper.OrderTicket, executedPrice, fee, spreadBPS, slippageBPS decimal.Decimal) error {
	p, i := e.Context.Policy, e.Context.Input
	if !executedPrice.IsPositive() || fee.IsNegative() || spreadBPS.IsNegative() || spreadBPS.GreaterThan(p.MaxSpreadBPS) || slippageBPS.IsNegative() || slippageBPS.GreaterThan(p.MaxSlippageBPS) {
		return fmt.Errorf("paper fill price or execution costs exceed risk policy")
	}
	if ticket.Side == paper.OrderSideLong && (executedPrice.LessThanOrEqual(ticket.StopLoss) || ticket.TakeProfit.IsPositive() && executedPrice.GreaterThanOrEqual(ticket.TakeProfit)) || ticket.Side == paper.OrderSideShort && (executedPrice.GreaterThanOrEqual(ticket.StopLoss) || ticket.TakeProfit.IsPositive() && executedPrice.LessThanOrEqual(ticket.TakeProfit)) {
		return fmt.Errorf("paper fill crossed protective prices")
	}
	riskPct := decimal.Min(p.RiskPerTradePct, i.Intent.HypothesisMaxRiskPct)
	budget := i.Account.Equity.Mul(riskPct).Div(decimal.NewFromInt(100))
	loss := executedPrice.Sub(ticket.StopLoss).Abs().Mul(ticket.Quantity).Add(fee)
	if loss.GreaterThan(budget) {
		return fmt.Errorf("paper fill loss including entry fee exceeds risk budget")
	}
	return nil
}
