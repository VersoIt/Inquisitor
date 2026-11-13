package paper_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/VersoIt/Inquisitor/internal/paper"
	"github.com/shopspring/decimal"
)

func accountLedger(t *testing.T, pnls []string, times []time.Time) paper.AccountLedger {
	t.Helper()
	start := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	base := validPositionClose()
	ledger := paper.AccountLedger{Validation: paper.ValidationRecord{ValidationID: base.ValidationID, RunID: "account_test_run", Status: paper.ValidationStatusRunning, Mode: "paper", InitialBalance: decimal.NewFromInt(1000), MinimumDays: 30, PlannedAt: start.Add(-time.Hour), StartedAt: start}}
	equity := ledger.Validation.InitialBalance
	for i, value := range pnls {
		c := base
		c.CloseID = fmt.Sprintf("close_%d", i)
		c.PositionID = fmt.Sprintf("position_%d", i)
		c.EntryFillID = fmt.Sprintf("fill_%d", i)
		c.TicketID = fmt.Sprintf("ticket_%d", i)
		c.OpenedAt, c.ClosedAt, c.RecordedAt = times[i].Add(-time.Hour), times[i], times[i]
		c.ExitFee, c.ExitFeeBPS, c.SpreadBPS, c.SlippageBPS = decimal.Zero, decimal.Zero, decimal.Zero, decimal.Zero
		c.ExitPrice = c.EntryPrice.Add(decimal.RequireFromString(value).Add(c.EntryFee).DivRound(c.Quantity, 32))
		c.ExitMidPrice = c.ExitPrice
		c.ExitNotional = c.ExitPrice.Mul(c.Quantity)
		recomputePositionClose(&c)
		e, err := paper.NewEquityEvent(paper.EquityEventInput{EventID: fmt.Sprintf("event_%d", i), Close: c, Sequence: i + 1, EquityBefore: equity, RecordedAt: times[i]})
		if err != nil {
			t.Fatal(err)
		}
		equity = e.EquityAfter
		ledger.Closes = append(ledger.Closes, c)
		ledger.Events = append(ledger.Events, e)
	}
	n := len(pnls)
	ledger.Inventory = paper.AccountInventory{Tickets: n, Fills: n, Positions: n, Closes: n, Events: n}
	return ledger
}

func TestAccountSnapshotReconcilesUTCLossBudgetsAndPeak(t *testing.T) {
	parse := func(s string) time.Time {
		v, err := time.Parse(time.RFC3339, s)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	times := []time.Time{parse("2026-11-08T23:59:59Z"), parse("2026-11-09T00:00:00Z"), parse("2026-11-12T23:59:59Z"), parse("2026-11-13T00:00:00Z"), parse("2026-11-13T01:00:00Z"), parse("2026-11-13T11:00:00Z"), parse("2026-11-13T12:00:00Z")}
	ledger := accountLedger(t, []string{"-10", "-20", "50", "-5", "10", "-7", "-3"}, times)
	before, err := json.Marshal(ledger)
	if err != nil {
		t.Fatal(err)
	}
	got, err := paper.BuildAccountSnapshot(ledger, times[len(times)-1])
	if err != nil {
		t.Fatal(err)
	}
	if !got.FlatLedgerReady || got.Account == nil || len(got.Blockers) != 0 {
		t.Fatalf("flat account blocked %+v", got)
	}
	a := got.Account
	for _, field := range []struct {
		name string
		got  decimal.Decimal
		want string
	}{
		{"equity", a.Equity, "1015"}, {"realized", got.RealizedEquity, "1015"}, {"day start", a.DayStartEquity, "1020"}, {"week start", a.WeekStartEquity, "990"}, {"peak", a.PeakEquity, "1025"}, {"daily gross loss", a.DailyLoss, "15"}, {"weekly gross loss", a.WeeklyLoss, "35"},
	} {
		if !field.got.Equal(decimal.RequireFromString(field.want)) {
			t.Errorf("%s got=%s want=%s", field.name, field.got, field.want)
		}
	}
	if a.LosingStreak != 2 || a.OpenPositions != 0 || !a.TotalExposure.IsZero() || !a.CorrelatedExposure.IsZero() || !got.DayStart.Equal(parse("2026-11-13T00:00:00Z")) || !got.WeekStart.Equal(parse("2026-11-09T00:00:00Z")) {
		t.Fatalf("bad derived account %+v", got)
	}
	after, _ := json.Marshal(ledger)
	if !bytes.Equal(before, after) {
		t.Fatal("input ledger mutated")
	}
	slices.Reverse(ledger.Events)
	slices.Reverse(ledger.Closes)
	reordered, err := paper.BuildAccountSnapshot(ledger, times[len(times)-1])
	if err != nil {
		t.Fatal(err)
	}
	left, _ := json.Marshal(got)
	right, _ := json.Marshal(reordered)
	if !bytes.Equal(left, right) {
		t.Fatal("input ordering changes snapshot")
	}
}

func TestAccountSnapshotUTCWeekAndDayBoundaries(t *testing.T) {
	sunday := time.Date(2026, 11, 8, 23, 59, 59, 0, time.UTC)
	monday := sunday.Add(time.Second)
	for _, tc := range []struct {
		name           string
		asOf           time.Time
		includeMonday  bool
		loss, baseline string
	}{
		{"last second Sunday", sunday, false, "10", "1000"},
		{"Monday midnight inclusive", monday, true, "20", "990"},
		{"local zone is still UTC Monday", monday.In(time.FixedZone("local", 3*3600)), true, "20", "990"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pnls, times := []string{"-10"}, []time.Time{sunday}
			if tc.includeMonday {
				pnls = append(pnls, "-20")
				times = append(times, monday)
			}
			got, err := paper.BuildAccountSnapshot(accountLedger(t, pnls, times), tc.asOf)
			if err != nil {
				t.Fatal(err)
			}
			if got.Account == nil || !got.Account.DailyLoss.Equal(decimal.RequireFromString(tc.loss)) || !got.Account.WeeklyLoss.Equal(decimal.RequireFromString(tc.loss)) || !got.Account.DayStartEquity.Equal(decimal.RequireFromString(tc.baseline)) || !got.Account.WeekStartEquity.Equal(decimal.RequireFromString(tc.baseline)) {
				t.Fatalf("boundary account %+v", got)
			}
		})
	}
}

func TestAccountSnapshotBlocksUnsettledInventoryWithoutRiskState(t *testing.T) {
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name, blocker string
		mutate        func(*paper.AccountLedger)
	}{
		{"pending ticket", "pending_tickets", func(l *paper.AccountLedger) { l.Inventory.Tickets++; l.Inventory.PendingTickets++ }},
		{"fill not opened", "unopened_fills", func(l *paper.AccountLedger) { l.Inventory.Tickets++; l.Inventory.Fills++; l.Inventory.UnopenedFills++ }},
		{"active position", "active_positions_require_marked_accounting", func(l *paper.AccountLedger) {
			l.Inventory.Tickets++
			l.Inventory.Fills++
			l.Inventory.Positions++
			l.Inventory.ActivePositions++
		}},
		{"close not accounted", "unaccounted_closes", func(l *paper.AccountLedger) { l.Events = nil; l.Inventory.Events = 0 }},
		{"legacy simulation", "legacy_simulation_ledger_present", func(l *paper.AccountLedger) { l.Inventory.LegacyTrades = 1 }},
		{"cancelled validation", "validation_not_running", func(l *paper.AccountLedger) {
			l.Validation.Status = paper.ValidationStatusCancelled
			l.Validation.CancelledAt = now
			l.Validation.StatusReason = "operator stop"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := accountLedger(t, []string{"1"}, []time.Time{now.Add(-time.Hour)})
			tc.mutate(&l)
			got, err := paper.BuildAccountSnapshot(l, now)
			if err != nil || got.FlatLedgerReady || got.Account != nil || !slices.Contains(got.Blockers, tc.blocker) {
				t.Fatalf("unsafe account not blocked %+v %v", got, err)
			}
		})
	}
	l := accountLedger(t, []string{"-1000"}, []time.Time{now})
	got, err := paper.BuildAccountSnapshot(l, now)
	if err != nil || got.Account != nil || !slices.Contains(got.Blockers, "equity_exhausted") {
		t.Fatalf("exhausted account %+v %v", got, err)
	}
}

func TestAccountSnapshotRejectsCorruptOrFutureSources(t *testing.T) {
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name   string
		mutate func(*paper.AccountLedger)
	}{
		{"inventory negative", func(l *paper.AccountLedger) { l.Inventory.PendingTickets = -1 }},
		{"partial event read", func(l *paper.AccountLedger) { l.Inventory.Events++ }},
		{"partial close read", func(l *paper.AccountLedger) { l.Inventory.Closes++ }},
		{"flow count wrong", func(l *paper.AccountLedger) { l.Inventory.Tickets++ }},
		{"bad source links", func(l *paper.AccountLedger) { l.Inventory.BrokenLinks++ }},
		{"future active record", func(l *paper.AccountLedger) { l.Inventory.FutureRecords++ }},
		{"future validation", func(l *paper.AccountLedger) { l.Validation.StartedAt = now.Add(time.Hour) }},
		{"future close audit", func(l *paper.AccountLedger) { l.Closes[0].RecordedAt = now.Add(time.Microsecond) }},
		{"future event audit", func(l *paper.AccountLedger) { l.Events[0].RecordedAt = now.Add(time.Microsecond) }},
		{"close before validation", func(l *paper.AccountLedger) { l.Closes[0].OpenedAt = l.Validation.StartedAt.Add(-time.Hour) }},
		{"event continuity", func(l *paper.AccountLedger) {
			l.Events[0].EquityBefore = l.Events[0].EquityBefore.Add(decimal.NewFromInt(1))
			l.Events[0].EquityAfter = l.Events[0].EquityBefore.Add(l.Events[0].NetPnL)
		}},
		{"event pnl differs from close", func(l *paper.AccountLedger) {
			l.Events[0].NetPnL = l.Events[0].NetPnL.Add(decimal.NewFromInt(1))
			l.Events[0].EquityAfter = l.Events[0].EquityBefore.Add(l.Events[0].NetPnL)
		}},
		{"event source missing", func(l *paper.AccountLedger) { l.Closes[0].CloseID = "other_close" }},
		{"foreign close", func(l *paper.AccountLedger) { l.Closes[0].ValidationID = "other_validation" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := accountLedger(t, []string{"1"}, []time.Time{now.Add(-time.Hour)})
			tc.mutate(&l)
			if got, err := paper.BuildAccountSnapshot(l, now); err == nil || got.Account != nil {
				t.Fatalf("corrupt source accepted %+v %v", got, err)
			}
		})
	}
}

func TestEmptyAccountAndBreakevenStreak(t *testing.T) {
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	for _, pnls := range [][]string{nil, {"-5", "0"}, {"-5", "2"}} {
		times := []time.Time{now.Add(-time.Hour), now}
		l := accountLedger(t, pnls, times)
		got, err := paper.BuildAccountSnapshot(l, now)
		if err != nil || got.Account == nil || got.Account.LosingStreak != 0 {
			t.Fatalf("account %+v %v", got, err)
		}
	}
	l := accountLedger(t, nil, nil)
	l.Validation.Status = paper.ValidationStatusPlanned
	l.Validation.StartedAt = time.Time{}
	got, err := paper.BuildAccountSnapshot(l, now)
	if err != nil || got.Account != nil || got.FlatLedgerReady {
		t.Fatal("planned validation admitted", err)
	}
	if _, err := paper.BuildAccountSnapshot(accountLedger(t, nil, nil), time.Time{}); err == nil {
		t.Fatal("missing observation time accepted")
	}
}

func TestAccountSnapshotKeepsSubCentDecimalPnL(t *testing.T) {
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	l := accountLedger(t, []string{"0.00000000000000000001", "-0.00000000000000000003"}, []time.Time{now.Add(-time.Hour), now})
	got, err := paper.BuildAccountSnapshot(l, now)
	if err != nil || got.Account == nil {
		t.Fatalf("fractional account %+v %v", got, err)
	}
	if !got.Account.Equity.Equal(decimal.RequireFromString("999.99999999999999999998")) || !got.Account.DailyLoss.Equal(decimal.RequireFromString("0.00000000000000000003")) || !got.Account.PeakEquity.Equal(decimal.RequireFromString("1000.00000000000000000001")) {
		t.Fatalf("decimal precision lost %+v", got.Account)
	}
}

func TestAccountWeekStartCrossesYearWithoutResettingWeeklyBudget(t *testing.T) {
	now := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	monday := time.Date(2026, 12, 28, 0, 0, 0, 0, time.UTC)
	l := accountLedger(t, []string{"-10", "-20", "5"}, []time.Time{monday.Add(-time.Second), monday, now})
	got, err := paper.BuildAccountSnapshot(l, now)
	if err != nil || got.Account == nil || !got.WeekStart.Equal(monday) || !got.Account.WeekStartEquity.Equal(decimal.NewFromInt(990)) || !got.Account.WeeklyLoss.Equal(decimal.NewFromInt(20)) || !got.Account.DailyLoss.IsZero() || !got.Account.DayStartEquity.Equal(decimal.NewFromInt(970)) {
		t.Fatalf("year boundary reset weekly account: %+v %v", got, err)
	}
}
