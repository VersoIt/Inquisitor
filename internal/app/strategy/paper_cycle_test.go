package strategy_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	appstrategy "github.com/VersoIt/Inquisitor/internal/app/strategy"
	"github.com/VersoIt/Inquisitor/internal/features"
	"github.com/VersoIt/Inquisitor/internal/marketdata"
	"github.com/VersoIt/Inquisitor/internal/paper"
	"github.com/VersoIt/Inquisitor/internal/risk"
	"github.com/VersoIt/Inquisitor/internal/strategy"
	"github.com/VersoIt/Inquisitor/internal/testutil"
	"github.com/shopspring/decimal"
)

var errCycleInterrupted = errors.New("cycle persistence interrupted")

type cycleSignalJournal struct {
	row                               strategy.SignalAuditRecord
	writes                            int
	failBefore, failAfter, invalidAck bool
	afterWrite                        func()
}

func (r *cycleSignalJournal) RecordSignal(_ context.Context, row strategy.SignalAuditRecord) (strategy.SignalWriteStats, error) {
	r.writes++
	if r.failBefore {
		return strategy.SignalWriteStats{}, errCycleInterrupted
	}
	if r.row.Signal.SignalID != "" {
		left, right := r.row, row
		left.RecordedAt, right.RecordedAt = time.Time{}, time.Time{}
		a, _ := json.Marshal(left)
		b, _ := json.Marshal(right)
		if !bytes.Equal(a, b) {
			return strategy.SignalWriteStats{}, strategy.ErrSignalConflict
		}
		return strategy.SignalWriteStats{Skipped: 1}, nil
	}
	r.row = row
	if r.afterWrite != nil {
		r.afterWrite()
	}
	if r.failAfter {
		return strategy.SignalWriteStats{}, errCycleInterrupted
	}
	if r.invalidAck {
		return strategy.SignalWriteStats{}, nil
	}
	return strategy.SignalWriteStats{Inserted: 1}, nil
}
func (r *cycleSignalJournal) ListSignals(_ context.Context, q strategy.SignalQuery) ([]strategy.SignalAuditRecord, error) {
	if r.row.Signal.SignalID == q.SignalID {
		return []strategy.SignalAuditRecord{r.row}, nil
	}
	return nil, nil
}

type cycleRiskJournal struct {
	paperRiskJournal
	failBefore, failAfter bool
}

type cycleTickets struct {
	signalTickets
	afterWrite func()
}

func (r *cycleTickets) RecordOrderTicket(ctx context.Context, ticket paper.OrderTicket) (paper.OrderTicketStats, error) {
	stats, err := r.signalTickets.RecordOrderTicket(ctx, ticket)
	if r.afterWrite != nil {
		r.afterWrite()
	}
	return stats, err
}

func (r *cycleRiskJournal) SavePaperRiskEvaluation(ctx context.Context, e strategy.PaperRiskEvaluation) (strategy.PaperRiskEvaluation, risk.DecisionAuditStats, error) {
	if r.failBefore {
		return strategy.PaperRiskEvaluation{}, risk.DecisionAuditStats{}, errCycleInterrupted
	}
	saved, stats, err := r.paperRiskJournal.SavePaperRiskEvaluation(ctx, e)
	if r.failAfter {
		return strategy.PaperRiskEvaluation{}, risk.DecisionAuditStats{}, errCycleInterrupted
	}
	return saved, stats, err
}

type cycleHarness struct {
	service   *appstrategy.PaperSignalCycleService
	req       appstrategy.RunPaperSignalCycleRequest
	clock     *paperRiskClock
	approvals *approvalRepo
	signals   *cycleSignalJournal
	journal   *cycleRiskJournal
	tickets   *cycleTickets
	engine    *paperRiskEngine
	switches  *paperRiskSwitch
}

func newCycleHarness(t *testing.T) *cycleHarness {
	t.Helper()
	f := testutil.PaperRisk(t)
	h := &cycleHarness{clock: &paperRiskClock{at: f.Approval.Now}, approvals: &approvalRepo{authorization: strategy.PaperAuthorization{Grant: f.Approval.Grant, Evidence: f.Approval.Evidence}}, signals: &cycleSignalJournal{}, journal: &cycleRiskJournal{}, tickets: &cycleTickets{}, switches: &paperRiskSwitch{}}
	a, err := appstrategy.NewApprovalService(h.approvals, h.clock, f.Approval.Policy)
	if err != nil {
		t.Fatal(err)
	}
	p := strategy.SelectionPolicy{MinConfidence: 70, MaxDataAge: 3 * time.Second}
	trend, err := strategy.NewTrendStrategy(strategy.TrendConfig{SelectionPolicy: p, StopATRMultiple: decimal.NewFromInt(2), TakeProfitR: decimal.NewFromInt(2)})
	if err != nil {
		t.Fatal(err)
	}
	selector, err := strategy.NewSelector(p, trend)
	if err != nil {
		t.Fatal(err)
	}
	generation := appstrategy.NewAuthorizedGenerationService(a, appstrategy.NewGenerationService(selector, h.signals, h.clock))
	engine, err := risk.NewTradeRiskEngine(f.Policy)
	if err != nil {
		t.Fatal(err)
	}
	h.engine = &paperRiskEngine{actual: engine}
	evaluations, err := appstrategy.NewPaperRiskService(h.signals, a, h.journal, h.switches, h.engine, h.clock)
	if err != nil {
		t.Fatal(err)
	}
	submission := appstrategy.NewPaperSignalSubmissionService(evaluations, a, h.tickets, h.clock)
	h.service = appstrategy.NewPaperSignalCycleService(generation, submission, h.switches, h.clock)
	r := f.Regime
	current := marketdata.Candle{Exchange: r.Exchange, Category: r.Category, Symbol: r.Symbol, Interval: r.Interval, OpenTime: r.OpenTime, CloseTime: r.CloseTime, Open: decimal.NewFromInt(98), High: decimal.NewFromInt(101), Low: decimal.NewFromInt(97), Close: decimal.NewFromInt(100), IsClosed: true}
	previous := current
	previous.OpenTime, previous.CloseTime = current.OpenTime.Add(-time.Minute), current.OpenTime
	previous.Open, previous.High, previous.Low, previous.Close = decimal.NewFromInt(96), decimal.NewFromInt(99), decimal.NewFromInt(94), decimal.NewFromInt(98)
	h.req = appstrategy.RunPaperSignalCycleRequest{ApprovalID: f.Approval.Grant.ApprovalID, Account: f.Input.Account, RiskMarket: f.Input.Market, Market: strategy.MarketContext{
		SignalID: f.Signal.Signal.SignalID, Now: f.Approval.Now, ObservedAt: f.Signal.ObservedAt, Mode: risk.ModePaper, TradingEnabled: true, AllowShort: true, EntryPrice: current.Close, Regime: r, Current: current, Previous: previous,
		Trend:      features.TrendFeatures{Exchange: r.Exchange, Category: r.Category, Symbol: r.Symbol, Interval: r.Interval, OpenTime: r.OpenTime, CloseTime: r.CloseTime, Complete: true, EMA20: decimal.NewFromInt(98), EMA50: decimal.NewFromInt(96), MA50Slope: decimal.NewFromInt(1)},
		Volatility: features.VolatilityFeatures{Exchange: r.Exchange, Category: r.Category, Symbol: r.Symbol, Interval: r.Interval, OpenTime: r.OpenTime, CloseTime: r.CloseTime, Complete: true, ATR: decimal.NewFromInt(2)},
	}}
	return h
}

func TestPaperCycleSeparatesAbstentionRejectionAndTicketPersistence(t *testing.T) {
	for _, tc := range []struct {
		name    string
		change  func(*cycleHarness)
		outcome appstrategy.PaperSignalCycleOutcome
	}{
		{"approved", func(*cycleHarness) {}, appstrategy.PaperSignalCycleTicketRecorded},
		{"disabled trading", func(h *cycleHarness) { h.req.Market.TradingEnabled = false }, appstrategy.PaperSignalCycleNoTrade},
		{"persisted stop", func(h *cycleHarness) {
			h.switches.state = risk.KillSwitchState{Active: true, Reason: "stop", Source: "operator", UpdatedAt: h.clock.at}
		}, appstrategy.PaperSignalCycleNoTrade},
		{"caller stop cannot be cleared", func(h *cycleHarness) { h.req.Market.KillSwitchActive = true }, appstrategy.PaperSignalCycleNoTrade},
		{"incomplete features", func(h *cycleHarness) { h.req.Market.Trend.Complete = false }, appstrategy.PaperSignalCycleNoTrade},
		{"position limit", func(h *cycleHarness) { h.req.Account.OpenPositions = 2 }, appstrategy.PaperSignalCycleRiskRejected},
		{"daily loss", func(h *cycleHarness) { h.req.Account.DailyLoss = decimal.NewFromInt(10) }, appstrategy.PaperSignalCycleRiskRejected},
		{"stale quote", func(h *cycleHarness) { h.req.RiskMarket.DataTime = h.clock.at.Add(-4 * time.Second) }, appstrategy.PaperSignalCycleRiskRejected},
		{"wide spread", func(h *cycleHarness) { h.req.RiskMarket.SpreadBPS = decimal.NewFromInt(6) }, appstrategy.PaperSignalCycleRiskRejected},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newCycleHarness(t)
			tc.change(h)
			got, err := h.service.Run(context.Background(), h.req)
			if err != nil || got.Outcome != tc.outcome || h.signals.row.Signal.SignalID != h.req.Market.SignalID {
				t.Fatalf("cycle %+v %v", got, err)
			}
			if tc.outcome == appstrategy.PaperSignalCycleNoTrade {
				if got.Submission != nil || h.journal.saves != 0 || h.engine.calls != 0 || h.tickets.writes != 0 || got.Generation.Signal.Trade != nil {
					t.Fatal("abstention reached risk/ticket")
				}
				return
			}
			if got.Submission == nil || h.journal.saves != 1 || h.engine.calls != 1 {
				t.Fatal("risk result missing")
			}
			approved := tc.outcome == appstrategy.PaperSignalCycleTicketRecorded
			if got.Submission.Evaluation.Audit.Decision.Approved != approved || (got.Submission.Ticket.TicketID != "") != approved || (h.tickets.writes == 1) != approved {
				t.Fatal("rejected decision created ticket or approved decision lost it")
			}
		})
	}
}

func TestPaperCycleRecoversPersistenceFailuresWithoutDuplicateDecisions(t *testing.T) {
	for _, scenario := range []string{"signal before write", "signal acknowledgement lost", "invalid signal acknowledgement", "risk before write", "risk acknowledgement lost", "ticket before write"} {
		t.Run(scenario, func(t *testing.T) {
			h := newCycleHarness(t)
			switch scenario {
			case "signal before write":
				h.signals.failBefore = true
			case "signal acknowledgement lost":
				h.signals.failAfter = true
			case "invalid signal acknowledgement":
				h.signals.invalidAck = true
			case "risk before write":
				h.journal.failBefore = true
			case "risk acknowledgement lost":
				h.journal.failAfter = true
			case "ticket before write":
				h.tickets.writeErr = errCycleInterrupted
			}
			failed, err := h.service.Run(context.Background(), h.req)
			if err == nil || failed.Outcome != "" || failed.Submission != nil || failed.Generation.Signal.SignalID != "" || len(h.tickets.rows) != 0 {
				t.Fatalf("failure returned usable state: %+v %v", failed, err)
			}
			if (scenario == "signal before write" || scenario == "signal acknowledgement lost" || scenario == "invalid signal acknowledgement") && h.engine.calls != 0 {
				t.Fatal("unacknowledged signal reached risk")
			}
			h.signals.failBefore, h.signals.failAfter, h.signals.invalidAck = false, false, false
			h.journal.failBefore, h.journal.failAfter = false, false
			h.tickets.writeErr = nil
			h.clock.at = h.clock.at.Add(time.Microsecond)
			got, err := h.service.Run(context.Background(), h.req)
			if err != nil || got.Outcome != appstrategy.PaperSignalCycleTicketRecorded || got.Submission == nil || len(h.tickets.rows) != 1 || h.journal.saves != 1 {
				t.Fatalf("retry failed %+v %v", got, err)
			}
			firstRiskAt, firstTicketAt := got.Submission.Evaluation.Audit.RecordedAt, got.Submission.Ticket.CreatedAt
			h.clock.at = h.clock.at.Add(time.Hour)
			again, err := h.service.Run(context.Background(), h.req)
			if err != nil || again.Generation.Stats.Skipped != 1 || again.Submission == nil || !again.Submission.Replayed || !again.Submission.Evaluation.Audit.RecordedAt.Equal(firstRiskAt) || !again.Submission.Ticket.CreatedAt.Equal(firstTicketAt) || len(h.tickets.rows) != 1 {
				t.Fatalf("historical replay duplicated/refreshed state %+v %v", again, err)
			}
		})
	}
}

func TestPaperCycleRechecksSafetyAfterSignalCommit(t *testing.T) {
	for _, scenario := range []string{"revoked", "kill switch activated", "cancelled"} {
		t.Run(scenario, func(t *testing.T) {
			h := newCycleHarness(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			h.signals.afterWrite = func() {
				switch scenario {
				case "revoked":
					h.approvals.authorization.Revocation = strategy.ApprovalRevocation{RevokedAt: h.clock.at, RevokedBy: "operator", Reason: "stop"}
				case "kill switch activated":
					h.switches.state = risk.KillSwitchState{Active: true, Reason: "stop", Source: "operator", UpdatedAt: h.clock.at}
				case "cancelled":
					cancel()
				}
			}
			got, err := h.service.Run(ctx, h.req)
			if scenario == "kill switch activated" {
				if err != nil || got.Outcome != appstrategy.PaperSignalCycleRiskRejected || h.journal.saves != 1 {
					t.Fatalf("stop was not rechecked %+v %v", got, err)
				}
			} else if err == nil || got.Outcome != "" {
				t.Fatal("interrupted cycle succeeded")
			}
			if h.signals.row.Signal.SignalID == "" || h.tickets.writes != 0 {
				t.Fatal("partial persistence lost or unsafe ticket created")
			}
		})
	}
}

func TestPaperCycleCannotRefreshStaleRiskOrOverwriteChangedRetries(t *testing.T) {
	for _, scenario := range []string{"stale risk without ticket", "changed account", "changed signal"} {
		t.Run(scenario, func(t *testing.T) {
			h := newCycleHarness(t)
			if scenario == "stale risk without ticket" {
				h.tickets.writeErr = errCycleInterrupted
			}
			_, firstErr := h.service.Run(context.Background(), h.req)
			if (firstErr != nil) != (scenario == "stale risk without ticket") {
				t.Fatal(firstErr)
			}
			firstRiskAt := h.journal.stored.Audit.RecordedAt
			switch scenario {
			case "stale risk without ticket":
				h.tickets.writeErr = nil
				h.clock.at = h.clock.at.Add(time.Hour)
			case "changed account":
				h.req.Account.OpenPositions++
			case "changed signal":
				h.req.Market.EntryPrice = decimal.NewFromInt(101)
			}
			got, err := h.service.Run(context.Background(), h.req)
			if err == nil || got.Submission != nil || !h.journal.stored.Audit.RecordedAt.Equal(firstRiskAt) {
				t.Fatal("conflict refreshed or replaced state")
			}
			if scenario == "stale risk without ticket" && len(h.tickets.rows) != 0 {
				t.Fatal("old decision opened new ticket")
			}
		})
	}
}

func TestPaperCycleRejectsInvalidInvocationBeforeGeneration(t *testing.T) {
	for _, scenario := range []string{"empty approval", "padded signal", "live", "missing time", "future time", "switch error", "future switch", "cancelled"} {
		t.Run(scenario, func(t *testing.T) {
			h := newCycleHarness(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch scenario {
			case "empty approval":
				h.req.ApprovalID = ""
			case "padded signal":
				h.req.Market.SignalID = " padded "
			case "live":
				h.req.Market.Mode = risk.ModeLive
			case "missing time":
				h.req.Market.Now = time.Time{}
			case "future time":
				h.req.Market.Now = h.clock.at.Add(time.Microsecond)
			case "switch error":
				h.switches.err = errCycleInterrupted
			case "future switch":
				h.switches.state = risk.KillSwitchState{Active: true, Reason: "stop", Source: "operator", UpdatedAt: h.clock.at.Add(time.Microsecond)}
			case "cancelled":
				cancel()
			}
			if _, err := h.service.Run(ctx, h.req); err == nil || h.signals.writes != 0 || h.tickets.writes != 0 {
				t.Fatal("invalid cycle reached persistence")
			}
		})
	}
	for _, s := range []*appstrategy.PaperSignalCycleService{nil, appstrategy.NewPaperSignalCycleService(nil, nil, nil, nil)} {
		if _, err := s.Run(context.Background(), appstrategy.RunPaperSignalCycleRequest{}); err == nil {
			t.Fatal("missing dependencies accepted")
		}
	}
}

func TestPaperCycleRecoversAmbiguousTicketAcknowledgement(t *testing.T) {
	h := newCycleHarness(t)
	h.tickets.writeErr, h.tickets.committed = errCycleInterrupted, true
	first, err := h.service.Run(context.Background(), h.req)
	if err != nil || first.Submission == nil || !first.Submission.Replayed || h.engine.calls != 1 || h.tickets.writes != 1 || len(h.tickets.rows) != 1 {
		t.Fatalf("ambiguous ticket was not reconciled %+v %v", first, err)
	}
	again, err := h.service.Run(context.Background(), h.req)
	if err != nil || again.Submission == nil || again.Submission.Ticket.TicketID != first.Submission.Ticket.TicketID || h.engine.calls != 1 || h.tickets.writes != 1 {
		t.Fatalf("reconciled cycle duplicated ticket/risk %+v %v", again, err)
	}
}

func TestPaperCycleReplaysAbstentionAndRiskRejection(t *testing.T) {
	for _, reject := range []bool{false, true} {
		t.Run(map[bool]string{false: "NO_TRADE", true: "RISK_REJECTED"}[reject], func(t *testing.T) {
			h := newCycleHarness(t)
			if reject {
				h.req.Account.OpenPositions = 2
			} else {
				h.req.Market.TradingEnabled = false
			}
			first, err := h.service.Run(context.Background(), h.req)
			if err != nil {
				t.Fatal(err)
			}
			h.clock.at = h.clock.at.Add(time.Hour)
			again, err := h.service.Run(context.Background(), h.req)
			if err != nil || again.Outcome != first.Outcome || again.Generation.Stats.Skipped != 1 || h.tickets.writes != 0 {
				t.Fatalf("rejected cycle changed on retry %+v %v", again, err)
			}
			if reject && (h.engine.calls != 1 || h.journal.saves != 1 || again.Submission == nil) {
				t.Fatal("historical rejection was recomputed")
			}
		})
	}
}

func TestPaperCycleCancellationAfterTicketCommitReturnsNoUsableResult(t *testing.T) {
	h := newCycleHarness(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h.tickets.afterWrite = cancel
	got, err := h.service.Run(ctx, h.req)
	if !errors.Is(err, context.Canceled) || got.Submission != nil || got.Outcome != "" || len(h.tickets.rows) != 1 {
		t.Fatalf("cancelled cycle lost durable ticket or returned usable result %+v %v", got, err)
	}
	h.tickets.afterWrite = nil
	recovered, err := h.service.Run(context.Background(), h.req)
	if err != nil || recovered.Submission == nil || !recovered.Submission.Replayed || len(h.tickets.rows) != 1 || h.tickets.writes != 1 {
		t.Fatalf("cancelled commit did not recover %+v %v", recovered, err)
	}
}
