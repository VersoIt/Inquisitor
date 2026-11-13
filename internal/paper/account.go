package paper

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/VersoIt/Inquisitor/internal/risk"
	"github.com/shopspring/decimal"
)

const MaxAccountLedgerRows = 100_000

type AccountInventory struct {
	Tickets, Fills, Positions, Closes, Events      int
	PendingTickets, UnopenedFills, ActivePositions int
	LegacyTrades, BrokenLinks, FutureRecords       int
}

type AccountLedger struct {
	Validation ValidationRecord
	Events     []EquityEvent
	Closes     []PositionClose
	Inventory  AccountInventory
}

type AccountLedgerQuery struct {
	ValidationID string
	AsOf         time.Time
}

type AccountLedgerReader interface {
	LoadPaperAccountLedger(context.Context, AccountLedgerQuery) (AccountLedger, error)
}

type AccountSnapshot struct {
	ValidationID              string
	AsOf, DayStart, WeekStart time.Time
	Inventory                 AccountInventory
	RealizedEquity            decimal.Decimal
	FlatLedgerReady           bool
	Blockers                  []string
	Account                   *risk.AccountState `json:",omitempty"`
}

func ValidateAccountLedgerQuery(q AccountLedgerQuery) error {
	if q.ValidationID == "" || q.ValidationID != strings.TrimSpace(q.ValidationID) || q.AsOf.IsZero() {
		return fmt.Errorf("paper account requires trimmed validation ID and observation time")
	}
	return nil
}

// BuildAccountSnapshot supplies risk inputs only for a fully reconciled, flat
// validation-local ledger. Open exposure requires marked portfolio accounting,
// not an optimistic realized-only equity estimate.
func BuildAccountSnapshot(ledger AccountLedger, asOf time.Time) (AccountSnapshot, error) {
	v, n := ledger.Validation, ledger.Inventory
	if err := ValidateValidationRecord(v); err != nil {
		return AccountSnapshot{}, err
	}
	if asOf.IsZero() || v.PlannedAt.After(asOf) || v.StartedAt.After(asOf) || v.CompletedAt.After(asOf) || v.CancelledAt.After(asOf) {
		return AccountSnapshot{}, fmt.Errorf("paper account validation has missing/future chronology")
	}
	if len(ledger.Events) > MaxAccountLedgerRows || len(ledger.Closes) > MaxAccountLedgerRows {
		return AccountSnapshot{}, fmt.Errorf("paper account ledger exceeds safety limit")
	}
	for _, count := range []int{n.Tickets, n.Fills, n.Positions, n.Closes, n.Events, n.PendingTickets, n.UnopenedFills, n.ActivePositions, n.LegacyTrades, n.BrokenLinks, n.FutureRecords} {
		if count < 0 {
			return AccountSnapshot{}, fmt.Errorf("negative paper account inventory")
		}
	}
	if n.Events != len(ledger.Events) || n.Closes != len(ledger.Closes) ||
		n.Tickets < n.Fills || n.Fills < n.Positions || n.Positions < n.Closes || n.Closes < n.Events ||
		n.PendingTickets != n.Tickets-n.Fills || n.UnopenedFills != n.Fills-n.Positions ||
		n.ActivePositions != n.Positions-n.Closes || n.BrokenLinks != 0 || n.FutureRecords != 0 {
		return AccountSnapshot{}, fmt.Errorf("paper account inventory/source links are inconsistent or future")
	}
	if err := ValidateEquityEventSequence(v.ValidationID, v.InitialBalance, ledger.Events); err != nil {
		return AccountSnapshot{}, err
	}
	closes := make(map[string]PositionClose, len(ledger.Closes))
	positions := make(map[string]bool, len(ledger.Closes))
	for _, c := range ledger.Closes {
		if err := ValidatePositionClose(c); err != nil {
			return AccountSnapshot{}, err
		}
		if c.ValidationID != v.ValidationID || v.StartedAt.IsZero() || c.OpenedAt.Before(v.StartedAt) || c.ClosedAt.After(asOf) || c.RecordedAt.After(asOf) {
			return AccountSnapshot{}, fmt.Errorf("paper account close escapes validation/time scope")
		}
		if _, ok := closes[c.CloseID]; ok || positions[c.PositionID] {
			return AccountSnapshot{}, fmt.Errorf("duplicate paper account close/position")
		}
		closes[c.CloseID] = c
		positions[c.PositionID] = true
	}
	utc := asOf.UTC()
	day := time.Date(utc.Year(), utc.Month(), utc.Day(), 0, 0, 0, 0, time.UTC)
	week := day.AddDate(0, 0, -(int(day.Weekday())+6)%7)
	out := AccountSnapshot{ValidationID: v.ValidationID, AsOf: utc, DayStart: day, WeekStart: week, Inventory: n, Blockers: []string{}}
	account := risk.AccountState{Equity: v.InitialBalance, DayStartEquity: v.InitialBalance, WeekStartEquity: v.InitialBalance, PeakEquity: v.InitialBalance}
	for _, e := range orderEquityEvents(ledger.Events) {
		c, ok := closes[e.CloseID]
		if !ok {
			return AccountSnapshot{}, fmt.Errorf("paper equity event has no source close")
		}
		if err := ValidateEquityEventForClose(e, c); err != nil {
			return AccountSnapshot{}, err
		}
		if e.OccurredAt.After(asOf) || e.RecordedAt.After(asOf) || e.OccurredAt.Before(v.StartedAt) {
			return AccountSnapshot{}, fmt.Errorf("paper account event escapes observation/validation time")
		}
		if e.OccurredAt.Before(day) {
			account.DayStartEquity = e.EquityAfter
		} else if e.NetPnL.IsNegative() {
			account.DailyLoss = account.DailyLoss.Add(e.NetPnL.Abs())
		}
		if e.OccurredAt.Before(week) {
			account.WeekStartEquity = e.EquityAfter
		} else if e.NetPnL.IsNegative() {
			account.WeeklyLoss = account.WeeklyLoss.Add(e.NetPnL.Abs())
		}
		account.Equity = e.EquityAfter
		if e.EquityAfter.GreaterThan(account.PeakEquity) {
			account.PeakEquity = e.EquityAfter
		}
		if e.NetPnL.IsNegative() {
			account.LosingStreak++
		} else {
			account.LosingStreak = 0
		}
	}
	out.RealizedEquity = account.Equity
	if v.Status != ValidationStatusRunning {
		out.Blockers = append(out.Blockers, "validation_not_running")
	}
	if n.PendingTickets > 0 {
		out.Blockers = append(out.Blockers, "pending_tickets")
	}
	if n.UnopenedFills > 0 {
		out.Blockers = append(out.Blockers, "unopened_fills")
	}
	if n.ActivePositions > 0 {
		out.Blockers = append(out.Blockers, "active_positions_require_marked_accounting")
	}
	if n.Closes != n.Events {
		out.Blockers = append(out.Blockers, "unaccounted_closes")
	}
	if n.LegacyTrades > 0 {
		out.Blockers = append(out.Blockers, "legacy_simulation_ledger_present")
	}
	if !account.Equity.IsPositive() {
		out.Blockers = append(out.Blockers, "equity_exhausted")
	}
	out.FlatLedgerReady = len(out.Blockers) == 0
	if out.FlatLedgerReady {
		out.Account = &account
	}
	return out, nil
}
