package strategy_test

import (
	"context"
	"errors"
	"testing"
	"time"

	appstrategy "github.com/VersoIt/Inquisitor/internal/app/strategy"
	"github.com/VersoIt/Inquisitor/internal/risk"
	"github.com/VersoIt/Inquisitor/internal/strategy"
	"github.com/VersoIt/Inquisitor/internal/testutil"
	"github.com/shopspring/decimal"
)

type riskSignalReader struct {
	rows  []strategy.SignalAuditRecord
	err   error
	query strategy.SignalQuery
}

func (r *riskSignalReader) ListSignals(_ context.Context, q strategy.SignalQuery) ([]strategy.SignalAuditRecord, error) {
	r.query = q
	return r.rows, r.err
}
func (*riskSignalReader) RecordSignal(context.Context, strategy.SignalAuditRecord) (strategy.SignalWriteStats, error) {
	panic("risk evaluation must not create a signal")
}

type paperRiskJournal struct {
	stored           strategy.PaperRiskEvaluation
	loadErr, saveErr error
	saves            int
	ack              *risk.DecisionAuditStats
	change           func(*strategy.PaperRiskEvaluation)
}

func (r *paperRiskJournal) LoadPaperRiskEvaluation(context.Context, string) (strategy.PaperRiskEvaluation, error) {
	if r.loadErr != nil {
		return strategy.PaperRiskEvaluation{}, r.loadErr
	}
	if r.stored.SignalID == "" {
		return strategy.PaperRiskEvaluation{}, strategy.ErrPaperRiskNotFound
	}
	return r.stored, nil
}
func (r *paperRiskJournal) SavePaperRiskEvaluation(_ context.Context, e strategy.PaperRiskEvaluation) (strategy.PaperRiskEvaluation, risk.DecisionAuditStats, error) {
	r.saves++
	if r.saveErr != nil {
		return strategy.PaperRiskEvaluation{}, risk.DecisionAuditStats{}, r.saveErr
	}
	r.stored = e
	if r.change != nil {
		r.change(&e)
	}
	stats := risk.DecisionAuditStats{Inserted: 1}
	if r.ack != nil {
		stats = *r.ack
	}
	return e, stats, nil
}

type paperRiskSwitch struct {
	state risk.KillSwitchState
	err   error
}

func (s *paperRiskSwitch) CurrentKillSwitchState(context.Context) (risk.KillSwitchState, error) {
	return s.state, s.err
}

type paperRiskClock struct{ at time.Time }

func (c *paperRiskClock) Now() time.Time { return c.at }

type paperRiskEngine struct {
	actual *risk.TradeRiskEngine
	calls  int
	err    error
	change func(*risk.Decision)
	cancel func()
}

func (e *paperRiskEngine) PolicySnapshot() risk.Policy { return e.actual.PolicySnapshot() }

func (e *paperRiskEngine) Evaluate(i risk.EvaluationInput) (risk.Decision, error) {
	e.calls++
	if e.cancel != nil {
		e.cancel()
	}
	if e.err != nil {
		return risk.Decision{}, e.err
	}
	d, err := e.actual.Evaluate(i)
	if e.change != nil {
		e.change(&d)
	}
	return d, err
}

type paperRiskHarness struct {
	f         testutil.PaperRiskFixture
	s         *appstrategy.PaperRiskService
	req       appstrategy.EvaluatePaperSignalRequest
	signals   *riskSignalReader
	approvals *approvalRepo
	journal   *paperRiskJournal
	switches  *paperRiskSwitch
	engine    *paperRiskEngine
	clock     *paperRiskClock
}

func newPaperRiskHarness(t *testing.T) *paperRiskHarness {
	t.Helper()
	f := testutil.PaperRisk(t)
	h := &paperRiskHarness{f: f, signals: &riskSignalReader{rows: []strategy.SignalAuditRecord{f.Signal}}, approvals: &approvalRepo{authorization: strategy.PaperAuthorization{Grant: f.Approval.Grant, Evidence: f.Approval.Evidence}},
		journal: &paperRiskJournal{}, switches: &paperRiskSwitch{}, clock: &paperRiskClock{at: f.Approval.Now}}
	engine, err := risk.NewTradeRiskEngine(f.Policy)
	if err != nil {
		t.Fatal(err)
	}
	h.engine = &paperRiskEngine{actual: engine}
	approvals, err := appstrategy.NewApprovalService(h.approvals, h.clock, f.Approval.Policy)
	if err != nil {
		t.Fatal(err)
	}
	h.s, err = appstrategy.NewPaperRiskService(h.signals, approvals, h.journal, h.switches, h.engine, h.clock)
	if err != nil {
		t.Fatal(err)
	}
	h.req = appstrategy.EvaluatePaperSignalRequest{SignalID: f.Signal.Signal.SignalID, ApprovalID: f.Approval.Grant.ApprovalID, Regime: f.Regime, Account: f.Input.Account, Market: f.Input.Market, Runtime: f.Input.Runtime}
	return h
}

func TestPaperRiskUsesCurrentLimitsAndAuditsRejections(t *testing.T) {
	for _, tt := range []struct {
		name   string
		change func(*paperRiskHarness)
		failed string
	}{
		{"approved and sized by risk", func(*paperRiskHarness) {}, ""},
		{"persisted kill switch", func(h *paperRiskHarness) {
			h.switches.state = risk.KillSwitchState{Active: true, Reason: "stop", Source: "operator", UpdatedAt: h.clock.at}
		}, "kill_switch_inactive"},
		{"caller kill switch cannot be cleared", func(h *paperRiskHarness) { h.req.Runtime.KillSwitchActive = true }, "kill_switch_inactive"},
		{"trading disabled", func(h *paperRiskHarness) { h.req.Runtime.TradingEnabled = false }, "trading_enabled"},
		{"current confidence deteriorated", func(h *paperRiskHarness) { h.req.Regime.Confidence = 60 }, "signal_confidence"},
		{"old signal cannot be refreshed with quote", func(h *paperRiskHarness) {
			h.signals.rows[0].ObservedAt = h.clock.at.Add(-4 * time.Second)
			h.signals.rows[0].Signal.Trade.ObservedAt = h.signals.rows[0].ObservedAt
			h.req.Market.DataTime = h.clock.at
		}, "data_fresh"},
		{"old candle cannot be refreshed with calculation", func(h *paperRiskHarness) {
			h.req.Regime.CloseTime = h.clock.at.Add(-4 * time.Second)
			h.req.Regime.CalculatedAt = h.clock.at
		}, "data_fresh"},
		{"freshness inclusive boundary", func(h *paperRiskHarness) { h.req.Market.DataTime = h.clock.at.Add(-3 * time.Second) }, ""},
		{"past freshness boundary", func(h *paperRiskHarness) { h.req.Market.DataTime = h.clock.at.Add(-3*time.Second - time.Microsecond) }, "data_fresh"},
		{"missing quote time", func(h *paperRiskHarness) { h.req.Market.DataTime = time.Time{} }, "data_fresh"},
		{"future quote time is not hidden", func(h *paperRiskHarness) { h.req.Market.DataTime = h.clock.at.Add(time.Second) }, "data_fresh"},
		{"daily loss budget", func(h *paperRiskHarness) { h.req.Account.DailyLoss = decimal.NewFromInt(10) }, "daily_loss_available"},
		{"position limit", func(h *paperRiskHarness) { h.req.Account.OpenPositions = 2 }, "position_slot_available"},
		{"spread limit", func(h *paperRiskHarness) { h.req.Market.SpreadBPS = decimal.NewFromInt(6) }, "spread_acceptable"},
		{"unavailable instrument", func(h *paperRiskHarness) { h.req.Market.Instrument.Available = false }, "instrument_available"},
		{"untrusted signal caps overridden", func(h *paperRiskHarness) {
			h.signals.rows[0].Signal.Trade.HypothesisMaxRiskPct = decimal.RequireFromString("0.9")
			h.signals.rows[0].Signal.Trade.HypothesisMinConfidence = 0
			h.signals.rows[0].Signal.Trade.HypothesisApprovedForMode = false
		}, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := newPaperRiskHarness(t)
			tt.change(h)
			got, err := h.s.EvaluateStoredSignal(context.Background(), h.req)
			if err != nil {
				t.Fatal(err)
			}
			e := got.Evaluation
			if got.Replayed || h.journal.saves != 1 || h.engine.calls != 1 || e.SignalID != h.req.SignalID || e.ApprovalID != h.req.ApprovalID || h.signals.query.Limit != 2 || h.signals.query.SignalID != h.req.SignalID {
				t.Fatalf("incorrect orchestration: %+v", got)
			}
			if err := strategy.ValidatePaperRiskEvaluation(e); err != nil {
				t.Fatal(err)
			}
			d := e.Audit.Decision
			if tt.failed == "" {
				if !d.Approved || !d.FinalQuantity.Equal(decimal.RequireFromString("0.62")) || !d.MaxLoss.Equal(decimal.RequireFromString("2.48")) || !e.Context.Input.Intent.HypothesisMaxRiskPct.Equal(decimal.RequireFromString("0.25")) {
					t.Fatalf("wrong risk sizing: %+v", d)
				}
			} else {
				if d.Approved || !d.FinalQuantity.IsZero() || !d.MaxLoss.IsZero() {
					t.Fatalf("unsafe approval: %+v", d)
				}
				found := false
				for _, c := range d.Checks {
					if c.Name == tt.failed && !c.Passed && c.Reason != "" {
						found = true
					}
				}
				if !found {
					t.Fatalf("missing %s failure: %+v", tt.failed, d)
				}
			}
		})
	}
}

func TestPaperRiskFailsClosedBeforeEngineForUntrustedSources(t *testing.T) {
	failure := errors.New("database offline")
	for _, tt := range []struct {
		name   string
		change func(*paperRiskHarness)
		want   error
	}{
		{"signal read fails", func(h *paperRiskHarness) { h.signals.err = failure }, failure},
		{"signal missing", func(h *paperRiskHarness) { h.signals.rows = nil }, nil},
		{"ambiguous signal", func(h *paperRiskHarness) { h.signals.rows = append(h.signals.rows, h.f.Signal) }, nil},
		{"wrong signal", func(h *paperRiskHarness) { h.req.SignalID = "other" }, nil},
		{"padded signal ID", func(h *paperRiskHarness) { h.req.SignalID += " " }, nil},
		{"missing approval ID", func(h *paperRiskHarness) { h.req.ApprovalID = "" }, nil},
		{"LIVE request", func(h *paperRiskHarness) { h.req.Runtime.Mode = risk.ModeLive }, nil},
		{"LIVE signal", func(h *paperRiskHarness) { h.signals.rows[0].Mode = risk.ModeLive }, nil},
		{"NO_TRADE", func(h *paperRiskHarness) {
			h.signals.rows[0].Signal.Type = strategy.SignalNoTrade
			h.signals.rows[0].Signal.Trade = nil
		}, nil},
		{"future persistence", func(h *paperRiskHarness) { h.signals.rows[0].RecordedAt = h.clock.at.Add(time.Second) }, nil},
		{"invalid signal", func(h *paperRiskHarness) { h.signals.rows[0].Signal.Trade.StopLoss = decimal.Zero }, nil},
		{"different regime", func(h *paperRiskHarness) { h.req.Regime.Regime = "TREND_DOWN" }, nil},
		{"regime wrong scope", func(h *paperRiskHarness) { h.req.Regime.Exchange = "other" }, nil},
		{"future regime", func(h *paperRiskHarness) { h.req.Regime.CalculatedAt = h.clock.at.Add(time.Second) }, nil},
		{"invalid regime chronology", func(h *paperRiskHarness) { h.req.Regime.CalculatedAt = h.req.Regime.CloseTime.Add(-time.Second) }, nil},
		{"changed hash", func(h *paperRiskHarness) { h.signals.rows[0].Features["approval.hypothesis_sha256"] = "different" }, nil},
		{"unlinked low-level signal", func(h *paperRiskHarness) { delete(h.signals.rows[0].Features, "approval.id") }, nil},
		{"wrong version", func(h *paperRiskHarness) {
			h.signals.rows[0].Signal.HypothesisVersion = "v2"
			h.signals.rows[0].Signal.Trade.HypothesisVersion = "v2"
		}, nil},
		{"approval read fails", func(h *paperRiskHarness) { h.approvals.err = failure }, failure},
		{"revoked", func(h *paperRiskHarness) {
			h.approvals.authorization.Revocation = strategy.ApprovalRevocation{RevokedAt: h.clock.at, RevokedBy: "operator", Reason: "stop"}
		}, strategy.ErrApprovalUnavailable},
		{"kill switch read fails", func(h *paperRiskHarness) { h.switches.err = failure }, failure},
		{"invalid kill switch", func(h *paperRiskHarness) { h.switches.state.Active = true }, nil},
		{"future kill switch", func(h *paperRiskHarness) {
			h.switches.state = risk.KillSwitchState{Reason: "stop", Source: "operator", UpdatedAt: h.clock.at.Add(time.Second)}
		}, nil},
		{"journal read fails", func(h *paperRiskHarness) { h.journal.loadErr = failure }, failure},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := newPaperRiskHarness(t)
			tt.change(h)
			got, err := h.s.EvaluateStoredSignal(context.Background(), h.req)
			if err == nil || tt.want != nil && !errors.Is(err, tt.want) || got.Evaluation.SignalID != "" || h.engine.calls != 0 || h.journal.saves != 0 {
				t.Fatalf("failed open: %+v, %v, engine=%d saves=%d", got, err, h.engine.calls, h.journal.saves)
			}
		})
	}
}

func TestPaperRiskRetriesAreHistoricalAndNeverOverwriteInputs(t *testing.T) {
	for _, change := range []string{"none", "account", "market", "runtime", "policy", "kill switch", "revoked", "corrupt record", "future record"} {
		t.Run(change, func(t *testing.T) {
			h := newPaperRiskHarness(t)
			first, err := h.s.EvaluateStoredSignal(context.Background(), h.req)
			if err != nil {
				t.Fatal(err)
			}
			h.clock.at = h.clock.at.Add(time.Second)
			switch change {
			case "account":
				h.req.Account.OpenPositions++
			case "market":
				h.req.Market.SpreadBPS = decimal.NewFromInt(3)
			case "runtime":
				h.req.Runtime.TradingEnabled = false
			case "policy":
				policy := h.f.Policy
				policy.RiskPerTradePct = decimal.RequireFromString("0.1")
				h.engine.actual, err = risk.NewTradeRiskEngine(policy)
				if err != nil {
					t.Fatal(err)
				}
			case "kill switch":
				h.switches.state = risk.KillSwitchState{Active: true, Reason: "stop", Source: "operator", UpdatedAt: h.clock.at}
			case "revoked":
				h.approvals.authorization.Revocation = strategy.ApprovalRevocation{RevokedAt: h.clock.at, RevokedBy: "operator", Reason: "stop"}
			case "corrupt record":
				h.journal.stored.Audit.Symbol = "ETHUSDT"
			case "future record":
				h.clock.at = first.Evaluation.Audit.RecordedAt.Add(-time.Microsecond)
				h.signals.rows[0].RecordedAt = h.clock.at
				h.signals.rows[0].Signal.CreatedAt = h.clock.at
				h.signals.rows[0].Signal.Trade.CreatedAt = h.clock.at
			}
			got, err := h.s.EvaluateStoredSignal(context.Background(), h.req)
			if change == "none" {
				if err != nil || !got.Replayed || !got.Evaluation.Audit.RecordedAt.Equal(first.Evaluation.Audit.RecordedAt) {
					t.Fatalf("retry: %+v %v", got, err)
				}
			} else if err == nil || got.Evaluation.SignalID != "" {
				t.Fatalf("changed request replayed: %+v %v", got, err)
			}
			if h.engine.calls != 1 || h.journal.saves != 1 {
				t.Fatal("retry recomputed or overwrote decision")
			}
		})
	}
}

func TestPaperRiskDoesNotReturnUnauditedOrInconsistentApproval(t *testing.T) {
	failure := errors.New("write unavailable")
	for _, scenario := range []string{"engine error", "wrong intent", "changed stop", "future decision", "write error", "zero ack", "double ack", "wrong saved source", "wrong saved outcome", "cancel during engine"} {
		t.Run(scenario, func(t *testing.T) {
			h := newPaperRiskHarness(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch scenario {
			case "engine error":
				h.engine.err = failure
			case "wrong intent":
				h.engine.change = func(d *risk.Decision) { d.IntentID = "wrong" }
			case "changed stop":
				h.engine.change = func(d *risk.Decision) {
					d.StopLoss = decimal.NewFromInt(95)
					d.MaxLoss = d.FinalQuantity.Mul(decimal.NewFromInt(5))
				}
			case "future decision":
				h.engine.change = func(d *risk.Decision) { d.CreatedAt = h.clock.at.Add(time.Second) }
			case "write error":
				h.journal.saveErr = failure
			case "zero ack":
				h.journal.ack = &risk.DecisionAuditStats{}
			case "double ack":
				h.journal.ack = &risk.DecisionAuditStats{Inserted: 1, Skipped: 1}
			case "wrong saved source":
				h.journal.change = func(e *strategy.PaperRiskEvaluation) { e.ApprovalID = "wrong" }
			case "wrong saved outcome":
				h.journal.change = func(e *strategy.PaperRiskEvaluation) { e.Context.Input.Account.OpenPositions++ }
			case "cancel during engine":
				h.engine.cancel = cancel
			}
			got, err := h.s.EvaluateStoredSignal(ctx, h.req)
			if err == nil || got.Evaluation.SignalID != "" {
				t.Fatalf("failed open: %+v %v", got, err)
			}
			if scenario == "cancel during engine" && (!errors.Is(err, context.Canceled) || h.journal.saves != 0) {
				t.Fatal("cancelled evaluation was saved")
			}
		})
	}
}

func TestPaperRiskDependenciesAndCancellation(t *testing.T) {
	h := newPaperRiskHarness(t)
	approvals := approvalService(t, h.approvals, h.f.Approval)
	for _, missing := range []string{"signals", "approvals", "journal", "kill switch", "engine", "clock"} {
		t.Run(missing, func(t *testing.T) {
			var signals strategy.SignalRepository = h.signals
			a := approvals
			var journal strategy.PaperRiskRepository = h.journal
			var switches appstrategy.PaperRiskKillSwitchReader = h.switches
			var engine appstrategy.PaperRiskEngine = h.engine
			var c interface{ Now() time.Time } = h.clock
			switch missing {
			case "signals":
				signals = nil
			case "approvals":
				a = nil
			case "journal":
				journal = nil
			case "kill switch":
				switches = nil
			case "engine":
				engine = nil
			case "clock":
				c = nil
			}
			if _, err := appstrategy.NewPaperRiskService(signals, a, journal, switches, engine, c); err == nil {
				t.Fatal("missing dependency accepted")
			}
		})
	}
	for _, s := range []*appstrategy.PaperRiskService{nil, {}} {
		if _, err := s.EvaluateStoredSignal(context.Background(), h.req); err == nil {
			t.Fatal("uninitialized service")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := h.s.EvaluateStoredSignal(ctx, h.req); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if h.signals.query.SignalID != "" || h.engine.calls != 0 || h.journal.saves != 0 {
		t.Fatal("cancelled request performed IO")
	}
}
