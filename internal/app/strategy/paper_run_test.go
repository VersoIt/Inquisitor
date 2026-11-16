package strategy_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	appstrategy "github.com/VersoIt/Inquisitor/internal/app/strategy"
	"github.com/VersoIt/Inquisitor/internal/paper"
	"github.com/VersoIt/Inquisitor/internal/risk"
	"github.com/VersoIt/Inquisitor/internal/strategy"
	"github.com/VersoIt/Inquisitor/internal/testutil"
)

type runMarket struct {
	snapshot strategy.PaperMarketSnapshot
	calls    int
	err      error
}

func (r *runMarket) Snapshot(context.Context, strategy.PaperMarketScope) (strategy.PaperMarketSnapshot, error) {
	r.calls++
	return r.snapshot, r.err
}

type runAccount struct {
	snapshot paper.AccountSnapshot
	calls    int
	err      error
}

func (r *runAccount) Snapshot(context.Context, string) (paper.AccountSnapshot, error) {
	r.calls++
	return r.snapshot, r.err
}

type runLock struct {
	calls int
	err   error
}

func (r *runLock) WithPaperAdmissionLock(ctx context.Context, _ string, fn func(context.Context) error) error {
	r.calls++
	if r.err != nil {
		return r.err
	}
	return fn(ctx)
}

type runJournal struct {
	record                                                       strategy.PaperRunRecord
	inputWrites, receiptWrites                                   int
	loadErr                                                      error
	inputBefore, inputAfter, receiptBefore, receiptAfter, badAck bool
	afterInput, afterReceipt                                     func()
}

func cloneRun(in strategy.PaperRunRecord) strategy.PaperRunRecord {
	raw, _ := json.Marshal(in)
	var out strategy.PaperRunRecord
	_ = json.Unmarshal(raw, &out)
	return out
}
func (r *runJournal) LoadPaperRun(context.Context, string) (strategy.PaperRunRecord, error) {
	if r.loadErr != nil {
		return strategy.PaperRunRecord{}, r.loadErr
	}
	if r.record.Input.RunID == "" {
		return strategy.PaperRunRecord{}, strategy.ErrPaperRunNotFound
	}
	return cloneRun(r.record), nil
}
func (r *runJournal) RecordPaperRunInput(_ context.Context, in strategy.PaperRunInput) error {
	r.inputWrites++
	if r.inputBefore {
		return errCycleInterrupted
	}
	r.record.Input = cloneRun(strategy.PaperRunRecord{Input: in}).Input
	if r.afterInput != nil {
		r.afterInput()
	}
	if r.inputAfter {
		return errCycleInterrupted
	}
	return nil
}
func (r *runJournal) RecordPaperRunReceipt(_ context.Context, in strategy.PaperRunReceipt) (strategy.PaperRunReceipt, error) {
	r.receiptWrites++
	if r.receiptBefore {
		return strategy.PaperRunReceipt{}, errCycleInterrupted
	}
	r.record.Receipt = &in
	if r.afterReceipt != nil {
		r.afterReceipt()
	}
	if r.receiptAfter {
		return strategy.PaperRunReceipt{}, errCycleInterrupted
	}
	if r.badAck {
		in.TicketID = "forged"
	}
	return in, nil
}

type runHarness struct {
	cycle    *cycleHarness
	market   *runMarket
	account  *runAccount
	journal  *runJournal
	lock     *runLock
	deps     appstrategy.PaperRunDependencies
	settings appstrategy.PaperRunSettings
	req      appstrategy.RunSourcedPaperRequest
}

func newRunHarness(t *testing.T) *runHarness {
	t.Helper()
	in := testutil.PaperRun(t)
	c := newCycleHarness(t)
	c.clock.at = in.CreatedAt
	a, err := appstrategy.NewApprovalService(c.approvals, c.clock, testutil.PaperApproval(t).Policy)
	if err != nil {
		t.Fatal(err)
	}
	h := &runHarness{cycle: c, market: &runMarket{snapshot: in.Market}, account: &runAccount{snapshot: in.Account}, journal: &runJournal{}, lock: &runLock{}, settings: appstrategy.PaperRunSettings{ConfigSHA256: in.ConfigSHA256, TradingEnabled: true}, req: appstrategy.RunSourcedPaperRequest{RunID: in.RunID, ApprovalID: in.ApprovalID, ValidationID: in.ValidationID, Scope: in.Market.Query.Scope}}
	h.deps = appstrategy.PaperRunDependencies{Market: h.market, Account: h.account, Approvals: a, Cycle: c.service, Journal: h.journal, Locker: h.lock, Tickets: c.tickets, Clock: c.clock}
	return h
}
func (h *runHarness) service(t *testing.T) *appstrategy.PaperRunService {
	t.Helper()
	s, err := appstrategy.NewPaperRunService(h.deps, h.settings)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func (h *runHarness) ownPending() {
	h.account.snapshot.FlatLedgerReady = false
	h.account.snapshot.Account = nil
	h.account.snapshot.Blockers = []string{"pending_tickets"}
	h.account.snapshot.Inventory.Tickets++
	h.account.snapshot.Inventory.PendingTickets++
}

func TestSourcedPaperRunnerOutcomesAndHistoricalReceipts(t *testing.T) {
	for _, scenario := range []string{"ticket", "disabled", "kill switch", "risk rejection"} {
		t.Run(scenario, func(t *testing.T) {
			h := newRunHarness(t)
			want := "TICKET_RECORDED"
			switch scenario {
			case "disabled":
				h.settings.TradingEnabled = false
				want = "NO_TRADE"
			case "kill switch":
				h.cycle.switches.state = risk.KillSwitchState{Active: true, Reason: "stop", Source: "operator", UpdatedAt: h.cycle.clock.at}
				want = "NO_TRADE"
			case "risk rejection":
				h.market.snapshot.RiskMarket.LiquidityQuote = h.market.snapshot.RiskMarket.LiquidityQuote.Div(h.market.snapshot.RiskMarket.LiquidityQuote)
				want = "RISK_REJECTED"
			}
			first, err := h.service(t).Run(context.Background(), h.req)
			if err != nil || first.Replayed || first.Receipt.Outcome != want || h.journal.inputWrites != 1 || h.journal.receiptWrites != 1 {
				t.Fatalf("first %+v %v", first, err)
			}
			writes := h.cycle.signals.writes
			h.cycle.clock.at = h.cycle.clock.at.Add(time.Hour)
			h.market.err = errors.New("source offline")
			h.account.err = h.market.err
			h.cycle.approvals.authorization.Revocation = strategy.ApprovalRevocation{RevokedAt: h.cycle.clock.at, RevokedBy: "operator", Reason: "stop"}
			again, err := h.service(t).Run(context.Background(), h.req)
			if err != nil || !again.Replayed || again.Receipt != first.Receipt || h.cycle.signals.writes != writes || h.market.calls != 1 || h.account.calls != 1 {
				t.Fatalf("historical retry %+v %v", again, err)
			}
		})
	}
}

func TestSourcedPaperRunnerRecoversAmbiguousCommits(t *testing.T) {
	for _, scenario := range []string{"input acknowledgement", "signal acknowledgement", "risk acknowledgement", "completion before commit", "completion acknowledgement", "cancel after input", "cancel after completion"} {
		t.Run(scenario, func(t *testing.T) {
			h := newRunHarness(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch scenario {
			case "input acknowledgement":
				h.journal.inputAfter = true
			case "signal acknowledgement":
				h.cycle.signals.failAfter = true
			case "risk acknowledgement":
				h.cycle.journal.failAfter = true
			case "completion before commit":
				h.journal.receiptBefore = true
			case "completion acknowledgement":
				h.journal.receiptAfter = true
			case "cancel after input":
				h.journal.afterInput = cancel
			case "cancel after completion":
				h.journal.afterReceipt = cancel
			}
			if got, err := h.service(t).Run(ctx, h.req); err == nil || got.Receipt.RunID != "" {
				t.Fatalf("ambiguous write returned usable result %+v %v", got, err)
			}
			frozen := h.journal.record.Input.SHA256
			h.journal.inputAfter = false
			h.cycle.signals.failAfter = false
			h.cycle.journal.failAfter = false
			h.journal.receiptBefore = false
			h.journal.receiptAfter = false
			h.journal.afterInput = nil
			h.journal.afterReceipt = nil
			if len(h.cycle.tickets.rows) > 0 {
				h.ownPending()
			}
			got, err := h.service(t).Run(context.Background(), h.req)
			if err != nil || !got.Replayed || got.Receipt.Outcome != "TICKET_RECORDED" || h.journal.record.Input.SHA256 != frozen || len(h.cycle.tickets.rows) != 1 || h.market.calls != 1 {
				t.Fatalf("recovery %+v %v tickets=%d", got, err, len(h.cycle.tickets.rows))
			}
		})
	}
}

func TestSourcedPaperRunnerFailClosedBoundaries(t *testing.T) {
	for _, scenario := range []string{"busy", "journal read", "market read", "account read", "blocked account", "foreign market", "foreign approval account", "revoked", "input failure", "bad completion acknowledgement", "invalid ID", "cancelled"} {
		t.Run(scenario, func(t *testing.T) {
			h := newRunHarness(t)
			ctx := context.Background()
			failure := errors.New("unavailable")
			switch scenario {
			case "busy":
				h.lock.err = strategy.ErrPaperAdmissionBusy
			case "journal read":
				h.journal.loadErr = failure
			case "market read":
				h.market.err = failure
			case "account read":
				h.account.err = failure
			case "blocked account":
				h.ownPending()
			case "foreign market":
				h.market.snapshot.Query.Scope.Symbol = "ETHUSDT"
			case "foreign approval account":
				h.req.ValidationID = "foreign"
				h.account.snapshot.ValidationID = "foreign"
			case "revoked":
				h.cycle.approvals.authorization.Revocation = strategy.ApprovalRevocation{RevokedAt: h.cycle.clock.at, RevokedBy: "operator", Reason: "stop"}
			case "input failure":
				h.journal.inputBefore = true
			case "bad completion acknowledgement":
				h.journal.badAck = true
			case "invalid ID":
				h.req.RunID = " padded "
			case "cancelled":
				c, cancel := context.WithCancel(ctx)
				cancel()
				ctx = c
			}
			got, err := h.service(t).Run(ctx, h.req)
			if err == nil || got.Receipt.RunID != "" {
				t.Fatalf("unsafe runner result %+v %v", got, err)
			}
			if scenario != "bad completion acknowledgement" && h.cycle.signals.writes != 0 {
				t.Fatal("invalid admission wrote signals")
			}
		})
	}
}

func TestSourcedPaperRunnerRefusesChangedOrExposedResume(t *testing.T) {
	for _, scenario := range []string{"config", "approval", "validation", "scope", "checksum", "account balance", "inventory", "own ticket plus another", "foreign pending ticket", "filled position", "revoked", "future receipt"} {
		t.Run(scenario, func(t *testing.T) {
			h := newRunHarness(t)
			h.journal.receiptBefore = true
			if _, err := h.service(t).Run(context.Background(), h.req); err == nil {
				t.Fatal("expected lost completion")
			}
			h.journal.receiptBefore = false
			h.ownPending()
			writes := h.cycle.signals.writes
			switch scenario {
			case "config":
				h.settings.ConfigSHA256 = strings.Repeat("b", 64)
			case "approval":
				h.req.ApprovalID = "foreign"
			case "validation":
				h.req.ValidationID = "foreign"
			case "scope":
				h.req.Scope.Symbol = "ETHUSDT"
			case "checksum":
				h.journal.record.Input.Market.SourceSHA256 = strings.Repeat("b", 64)
			case "account balance":
				h.account.snapshot.RealizedEquity = h.account.snapshot.RealizedEquity.Add(h.account.snapshot.RealizedEquity)
			case "inventory":
				h.account.snapshot.Inventory.Events++
			case "own ticket plus another":
				h.account.snapshot.Inventory.Tickets++
				h.account.snapshot.Inventory.PendingTickets++
			case "foreign pending ticket":
				h.cycle.tickets.rows[0].SourceSignalID = "foreign"
			case "filled position":
				h.account.snapshot.Blockers = []string{"active_positions_require_marked_accounting"}
			case "revoked":
				h.cycle.approvals.authorization.Revocation = strategy.ApprovalRevocation{RevokedAt: h.cycle.clock.at, RevokedBy: "operator", Reason: "stop"}
			case "future receipt":
				in := h.journal.record.Input
				h.journal.record.Receipt = &strategy.PaperRunReceipt{RunID: in.RunID, InputSHA256: in.SHA256, SignalID: strategy.PaperRunSignalID(in.RunID), Outcome: "NO_TRADE", CompletedAt: in.CreatedAt.Add(time.Hour)}
			}
			if got, err := h.service(t).Run(context.Background(), h.req); err == nil || got.Receipt.RunID != "" {
				t.Fatalf("changed resume returned %+v %v", got, err)
			}
			if h.cycle.signals.writes != writes {
				t.Fatal("unsafe resume advanced cycle")
			}
		})
	}
}

func TestSourcedPaperRunnerLateRetryCannotCreateFreshTicket(t *testing.T) {
	h := newRunHarness(t)
	h.cycle.tickets.writeErr = errCycleInterrupted
	if _, err := h.service(t).Run(context.Background(), h.req); err == nil {
		t.Fatal("expected failed ticket write")
	}
	h.cycle.tickets.writeErr = nil
	h.cycle.clock.at = h.cycle.clock.at.Add(time.Hour)
	h.account.snapshot.AsOf = h.cycle.clock.at
	if got, err := h.service(t).Run(context.Background(), h.req); err == nil || got.Receipt.RunID != "" || len(h.cycle.tickets.rows) != 0 {
		t.Fatalf("stale retry created ticket %+v %v", got, err)
	}
}

func TestSourcedPaperRunnerLateOwnTicketRecoveryIsHistoricalOnly(t *testing.T) {
	h := newRunHarness(t)
	h.journal.receiptBefore = true
	if _, err := h.service(t).Run(context.Background(), h.req); err == nil {
		t.Fatal("expected interrupted completion")
	}
	ticket := h.cycle.tickets.rows[0]
	h.journal.receiptBefore = false
	h.ownPending()
	h.cycle.clock.at = h.cycle.clock.at.Add(time.Hour)
	h.account.snapshot.AsOf = h.cycle.clock.at
	got, err := h.service(t).Run(context.Background(), h.req)
	if err != nil || !got.Replayed || got.Receipt.TicketID != ticket.TicketID || len(h.cycle.tickets.rows) != 1 || !h.cycle.tickets.rows[0].CreatedAt.Equal(ticket.CreatedAt) {
		t.Fatalf("late own-ticket recovery %+v %v", got, err)
	}
}

func TestSourcedPaperRunnerConstructorRequiresAllDependencies(t *testing.T) {
	for _, field := range []string{"market", "account", "approval", "cycle", "journal", "lock", "tickets", "clock", "checksum"} {
		t.Run(field, func(t *testing.T) {
			h := newRunHarness(t)
			switch field {
			case "market":
				h.deps.Market = nil
			case "account":
				h.deps.Account = nil
			case "approval":
				h.deps.Approvals = nil
			case "cycle":
				h.deps.Cycle = nil
			case "journal":
				h.deps.Journal = nil
			case "lock":
				h.deps.Locker = nil
			case "tickets":
				h.deps.Tickets = nil
			case "clock":
				h.deps.Clock = nil
			case "checksum":
				h.settings.ConfigSHA256 = "bad"
			}
			if _, err := appstrategy.NewPaperRunService(h.deps, h.settings); err == nil {
				t.Fatal("missing dependency accepted")
			}
		})
	}
	var s *appstrategy.PaperRunService
	if _, err := s.Run(context.Background(), appstrategy.RunSourcedPaperRequest{}); err == nil {
		t.Fatal("nil service")
	}
}
