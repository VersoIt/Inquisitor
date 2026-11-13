package research

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/VersoIt/Inquisitor/internal/backtest"
	domain "github.com/VersoIt/Inquisitor/internal/research"
	"github.com/shopspring/decimal"
)

// Deliberately excludes validation/stress trades and validation metrics.
type DevelopmentDiagnosticInput struct {
	Plan        domain.TrendExperimentPlan
	PlanSHA256  string
	Development TrendExperimentPeriod
}

type AttributionTotals struct {
	Trades, Wins, Losses, Breakeven      int
	HoldingSeconds                       int64
	MidPricePnL, EntryImpact, ExitImpact decimal.Decimal
	GrossPnL, Fees, NetPnL               decimal.Decimal
}

type AttributionGroup struct {
	Key string
	AttributionTotals
}

type DevelopmentDiagnostics struct {
	Version, ExperimentID, PlanSHA256         string
	Start, End                                time.Time
	Totals                                    AttributionTotals
	ByExitReason, ByEntryMonth, ByHoldingTime []AttributionGroup
	Limitations                               []string
}

func DiagnoseDevelopment(ctx context.Context, in DevelopmentDiagnosticInput) (DevelopmentDiagnostics, error) {
	if err := ctx.Err(); err != nil {
		return DevelopmentDiagnostics{}, err
	}
	if err := domain.ValidateTrendExperimentPlan(in.Plan); err != nil {
		return DevelopmentDiagnostics{}, err
	}
	hash, err := domain.ExperimentHash(in.Plan)
	if err != nil || hash != in.PlanSHA256 {
		return DevelopmentDiagnostics{}, fmt.Errorf("development diagnostic plan fingerprint mismatch")
	}
	p := in.Development
	if !p.Start.Equal(in.Plan.DevelopmentStart) || !p.End.Equal(in.Plan.ValidationStart) {
		return DevelopmentDiagnostics{}, fmt.Errorf("diagnostics require exactly the development window")
	}
	r := DevelopmentDiagnostics{Version: "development-ledger-v1", ExperimentID: in.Plan.ID, PlanSHA256: hash, Start: p.Start, End: p.End,
		Limitations: []string{
			"Descriptive development-only ledger attribution; no parameter selection or new approval",
			"Mid-price PnL uses original cost-aware quantities and decisions, not an executable zero-cost strategy",
			"Entry/exit impact combines modeled half-spread and conservative slippage; fees are separate",
			"Holding time uses modeled bar timestamps, not observed intrabar execution times",
			"Month groups use entry UTC month and include the full trade; they are not monthly equity returns",
			"Group differences are descriptive, not causal evidence or out-of-sample performance",
		}}
	exits, months, durations := map[string]AttributionTotals{}, map[string]AttributionTotals{}, map[string]AttributionTotals{}
	trades := make([]backtest.RoundTrip, 0, len(p.Trades))
	lastExit, equity := p.Start, in.Plan.InitialEquity
	for index, row := range p.Trades {
		if err := ctx.Err(); err != nil {
			return DevelopmentDiagnostics{}, err
		}
		t := row.Trade
		if t.Entry.Time.Before(lastExit) || t.Entry.Time.Before(p.Start) || !t.Entry.Time.Before(p.End) || t.Exit.Time.After(p.End) || !row.SignalTime.Equal(t.Entry.Time) {
			return DevelopmentDiagnostics{}, fmt.Errorf("development trade %d overlaps or escapes its window/signal", index)
		}
		if t.Entry.Notional.Add(t.Entry.Fee).GreaterThan(equity) || !t.Entry.Quantity.Mod(in.Plan.QuantityStep).IsZero() || t.Entry.Notional.LessThan(in.Plan.MinNotional) {
			return DevelopmentDiagnostics{}, fmt.Errorf("development trade %d violates spot cash/lot constraints", index)
		}
		switch row.ExitReason {
		case "stop", "stop_gap", "target", "target_gap_capped", "time_limit", "window_end":
		default:
			return DevelopmentDiagnostics{}, fmt.Errorf("development trade %d has unknown exit reason", index)
		}
		a, err := domain.AttributeLongTrade(t, in.Plan.Costs)
		if err != nil {
			return DevelopmentDiagnostics{}, fmt.Errorf("development trade %d: %w", index, err)
		}
		held := t.Exit.Time.Sub(t.Entry.Time)
		if held > time.Duration(in.Plan.MaxHoldingCandles)*time.Hour {
			return DevelopmentDiagnostics{}, fmt.Errorf("development trade %d exceeds holding limit", index)
		}
		addAttribution(&r.Totals, a, held)
		for _, group := range []struct {
			values map[string]AttributionTotals
			key    string
		}{
			{exits, row.ExitReason}, {months, t.Entry.Time.UTC().Format("2006-01")}, {durations, holdingBucket(held)},
		} {
			total := group.values[group.key]
			addAttribution(&total, a, held)
			group.values[group.key] = total
		}
		trades = append(trades, t)
		equity = equity.Add(t.NetPnL)
		lastExit = t.Exit.Time
	}
	summary, err := backtest.SummarizeRoundTrips(in.Plan.InitialEquity, trades)
	if err != nil {
		return DevelopmentDiagnostics{}, err
	}
	expectedHash, err := domain.ExperimentHash(summary)
	if err != nil {
		return DevelopmentDiagnostics{}, err
	}
	actualHash, err := domain.ExperimentHash(p.Summary)
	if err != nil || actualHash != expectedHash {
		return DevelopmentDiagnostics{}, fmt.Errorf("development summary does not reconcile with ledger")
	}
	r.ByExitReason, r.ByEntryMonth, r.ByHoldingTime = sortedGroups(exits), sortedGroups(months), sortedGroups(durations)
	return r, nil
}

func addAttribution(t *AttributionTotals, a domain.TradeCostAttribution, held time.Duration) {
	t.Trades++
	switch a.NetPnL.Sign() {
	case 1:
		t.Wins++
	case -1:
		t.Losses++
	default:
		t.Breakeven++
	}
	t.HoldingSeconds += int64(held / time.Second)
	t.MidPricePnL = t.MidPricePnL.Add(a.MidPricePnL)
	t.EntryImpact = t.EntryImpact.Add(a.EntryImpact)
	t.ExitImpact = t.ExitImpact.Add(a.ExitImpact)
	t.GrossPnL = t.GrossPnL.Add(a.GrossPnL)
	t.Fees = t.Fees.Add(a.Fees)
	t.NetPnL = t.NetPnL.Add(a.NetPnL)
}

func holdingBucket(held time.Duration) string {
	switch {
	case held < 6*time.Hour:
		return "01_under_6h"
	case held < 24*time.Hour:
		return "02_6h_to_under_24h"
	case held < 48*time.Hour:
		return "03_24h_to_under_48h"
	default:
		return "04_48h_or_more"
	}
}

func sortedGroups(values map[string]AttributionTotals) []AttributionGroup {
	groups := make([]AttributionGroup, 0, len(values))
	for key, totals := range values {
		groups = append(groups, AttributionGroup{Key: key, AttributionTotals: totals})
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i].Key < groups[j].Key })
	return groups
}
