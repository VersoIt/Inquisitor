package strategy_test

import (
	"context"
	"errors"
	"testing"
	"time"

	appstrategy "github.com/VersoIt/Inquisitor/internal/app/strategy"
	"github.com/VersoIt/Inquisitor/internal/clock"
	"github.com/VersoIt/Inquisitor/internal/hypothesis"
	"github.com/VersoIt/Inquisitor/internal/regime"
	"github.com/VersoIt/Inquisitor/internal/risk"
	"github.com/VersoIt/Inquisitor/internal/strategy"
	"github.com/shopspring/decimal"
)

type fakeGenerator struct {
	signal strategy.Signal
	err    error
	calls  int
	cancel func()
}

func (g *fakeGenerator) GenerateSignal(context.Context, strategy.MarketContext) (strategy.Signal, error) {
	g.calls++
	if g.cancel != nil {
		g.cancel()
	}
	return g.signal, g.err
}

type fakeSignalRepo struct {
	record strategy.SignalAuditRecord
	stats  strategy.SignalWriteStats
	err    error
	calls  int
}

func (r *fakeSignalRepo) RecordSignal(_ context.Context, record strategy.SignalAuditRecord) (strategy.SignalWriteStats, error) {
	r.calls++
	r.record = record
	return r.stats, r.err
}
func (*fakeSignalRepo) ListSignals(context.Context, strategy.SignalQuery) ([]strategy.SignalAuditRecord, error) {
	return nil, nil
}

func generationInput() (strategy.MarketContext, strategy.Signal) {
	trade := validAppTradeSignal()
	m := strategy.MarketContext{SignalID: trade.SignalID, Now: trade.CreatedAt, ObservedAt: trade.ObservedAt, EntryPrice: trade.EntryPrice, Mode: risk.ModePaper, TradingEnabled: true,
		Approval: strategy.ApprovalSnapshot{HypothesisID: trade.HypothesisID, Version: trade.HypothesisVersion, StrategyName: trade.StrategyName, Mode: risk.ModePaper, Direction: hypothesis.DirectionLong, Exchange: "bybit", Category: "linear", Symbol: "BTCUSDT", Interval: "1", Enabled: true, MaxRiskPct: trade.HypothesisMaxRiskPct, MinConfidence: trade.HypothesisMinConfidence},
		Regime:   regime.State{Exchange: "bybit", Category: "linear", Symbol: "BTCUSDT", Interval: "1", Regime: regime.RegimeTrendUp, CandidateRegime: regime.RegimeTrendUp, Confidence: trade.Confidence, OpenTime: trade.CreatedAt.Add(-time.Minute), CloseTime: trade.ObservedAt, CalculatedAt: trade.ObservedAt},
	}
	s := strategy.Signal{SignalID: trade.SignalID, HypothesisID: trade.HypothesisID, HypothesisVersion: trade.HypothesisVersion, StrategyName: trade.StrategyName, Symbol: trade.Symbol, Interval: "1", Type: strategy.SignalBuy, Reason: trade.Reason, CreatedAt: trade.CreatedAt, Trade: &trade}
	return m, s
}

func TestGenerationServicePersistsBeforeReturningTradeOrAbstention(t *testing.T) {
	for _, abstain := range []bool{false, true} {
		for _, stats := range []strategy.SignalWriteStats{{Inserted: 1}, {Skipped: 1}} {
			m, signal := generationInput()
			if abstain {
				signal.Type, signal.Trade, signal.Reason = strategy.SignalNoTrade, nil, "trading_disabled"
			}
			g, repo := &fakeGenerator{signal: signal}, &fakeSignalRepo{stats: stats}
			service := appstrategy.NewGenerationService(g, repo, clock.FixedClock{Time: m.Now.Add(time.Second)})
			got, err := service.GenerateAndRecord(context.Background(), m)
			if err != nil {
				t.Fatal(err)
			}
			if g.calls != 1 || repo.calls != 1 || got.Stats != stats || got.Signal.Type != signal.Type || got.Signal.Reason != signal.Reason {
				t.Fatalf("incorrect orchestration: %+v, writes=%d", got, repo.calls)
			}
			if err := strategy.ValidateSignalAuditRecord(repo.record); err != nil {
				t.Fatal(err)
			}
			if repo.record.Mode != m.Mode || repo.record.Exchange != m.Regime.Exchange || repo.record.Features["entry_price"] != m.EntryPrice.String() {
				t.Fatalf("lost context: %+v", repo.record)
			}
			if signal.Trade != nil {
				signal.Trade.Reason = "mutated"
				if got.Signal.Trade.Reason == "mutated" {
					t.Fatal("returned signal aliases generator")
				}
			}
		}
	}
}

func TestGenerationServiceFailsClosed(t *testing.T) {
	repoErr := errors.New("database unavailable")
	tests := []struct {
		name   string
		setup  func(*strategy.MarketContext, *fakeGenerator, *fakeSignalRepo)
		want   error
		writes int
	}{
		{"generator failure", func(_ *strategy.MarketContext, g *fakeGenerator, _ *fakeSignalRepo) { g.err = repoErr }, repoErr, 0},
		{"repository failure", func(_ *strategy.MarketContext, _ *fakeGenerator, r *fakeSignalRepo) { r.err = repoErr }, repoErr, 1},
		{"conflicting ID", func(_ *strategy.MarketContext, _ *fakeGenerator, r *fakeSignalRepo) {
			r.err = strategy.ErrSignalConflict
		}, strategy.ErrSignalConflict, 1},
		{"mismatched evaluation", func(_ *strategy.MarketContext, g *fakeGenerator, _ *fakeSignalRepo) { g.signal.SignalID = "other" }, nil, 0},
		{"invalid trade", func(_ *strategy.MarketContext, g *fakeGenerator, _ *fakeSignalRepo) {
			g.signal.Type = strategy.SignalSell
		}, nil, 0},
		{"wrong entry snapshot", func(_ *strategy.MarketContext, g *fakeGenerator, _ *fakeSignalRepo) {
			g.signal.Trade.EntryPrice = decimal.NewFromInt(101)
		}, nil, 0},
		{"wrong confidence snapshot", func(_ *strategy.MarketContext, g *fakeGenerator, _ *fakeSignalRepo) { g.signal.Trade.Confidence++ }, nil, 0},
		{"wrong risk snapshot", func(_ *strategy.MarketContext, g *fakeGenerator, _ *fakeSignalRepo) {
			g.signal.Trade.HypothesisMaxRiskPct = decimal.RequireFromString("0.5")
		}, nil, 0},
		{"wrong approval snapshot", func(_ *strategy.MarketContext, g *fakeGenerator, _ *fakeSignalRepo) {
			g.signal.Trade.HypothesisApprovedForMode = false
		}, nil, 0},
		{"clock before evaluation", func(m *strategy.MarketContext, g *fakeGenerator, _ *fakeSignalRepo) {
			m.Now = m.Now.Add(time.Hour)
			g.signal.CreatedAt = m.Now
			g.signal.Trade.CreatedAt = m.Now
		}, nil, 0},
		{"missing feature envelope scope", func(m *strategy.MarketContext, _ *fakeGenerator, _ *fakeSignalRepo) { m.Regime.Exchange = "" }, nil, 0},
		{"zero write acknowledgement", func(_ *strategy.MarketContext, _ *fakeGenerator, r *fakeSignalRepo) {
			r.stats = strategy.SignalWriteStats{}
		}, nil, 1},
		{"impossible acknowledgement", func(_ *strategy.MarketContext, _ *fakeGenerator, r *fakeSignalRepo) {
			r.stats = strategy.SignalWriteStats{Inserted: 1, Skipped: 1}
		}, nil, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, signal := generationInput()
			fixed := clock.FixedClock{Time: m.Now.Add(time.Second)}
			g, r := &fakeGenerator{signal: signal}, &fakeSignalRepo{stats: strategy.SignalWriteStats{Inserted: 1}}
			tt.setup(&m, g, r)
			got, err := appstrategy.NewGenerationService(g, r, fixed).GenerateAndRecord(context.Background(), m)
			if err == nil || (tt.want != nil && !errors.Is(err, tt.want)) || got.Signal.Trade != nil || got.Signal.SignalID != "" || r.calls != tt.writes {
				t.Fatalf("got %+v, %v, writes=%d", got, err, r.calls)
			}
		})
	}
}

func TestGenerationServiceDependenciesAndCancellation(t *testing.T) {
	m, signal := generationInput()
	g, r := &fakeGenerator{signal: signal}, &fakeSignalRepo{stats: strategy.SignalWriteStats{Inserted: 1}}
	fixed := clock.FixedClock{Time: m.Now}
	for _, s := range []*appstrategy.GenerationService{nil, {}, appstrategy.NewGenerationService(nil, r, fixed), appstrategy.NewGenerationService(g, nil, fixed), appstrategy.NewGenerationService(g, r, nil)} {
		if _, err := s.GenerateAndRecord(context.Background(), m); err == nil {
			t.Fatal("accepted missing dependency")
		}
	}
	for _, duringGeneration := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		g, r := &fakeGenerator{signal: signal}, &fakeSignalRepo{stats: strategy.SignalWriteStats{Inserted: 1}}
		if duringGeneration {
			g.cancel = cancel
		} else {
			cancel()
		}
		got, err := appstrategy.NewGenerationService(g, r, fixed).GenerateAndRecord(ctx, m)
		cancel()
		if !errors.Is(err, context.Canceled) || got.Signal.Trade != nil || r.calls != 0 {
			t.Fatalf("got %+v, %v, writes=%d", got, err, r.calls)
		}
	}
}

func TestGenerationServicePersistsRealSelectorRejection(t *testing.T) {
	m, _ := generationInput()
	m.KillSwitchActive = true
	selector, err := strategy.NewSelector(strategy.SelectionPolicy{MinConfidence: 70, MaxDataAge: 3 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	r := &fakeSignalRepo{stats: strategy.SignalWriteStats{Inserted: 1}}
	got, err := appstrategy.NewGenerationService(selector, r, clock.FixedClock{Time: m.Now}).GenerateAndRecord(context.Background(), m)
	if err != nil || got.Signal.Type != strategy.SignalNoTrade || r.record.Signal.Reason != "kill_switch_active" || r.record.Features["runtime.kill_switch_active"] != "true" {
		t.Fatalf("rejection not audited: %+v, %v", got, err)
	}
}
