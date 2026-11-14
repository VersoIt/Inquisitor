package strategy_test

import (
	"context"
	"errors"
	"testing"
	"time"

	appstrategy "github.com/VersoIt/Inquisitor/internal/app/strategy"
	"github.com/VersoIt/Inquisitor/internal/clock"
	"github.com/VersoIt/Inquisitor/internal/strategy"
	"github.com/VersoIt/Inquisitor/internal/testutil"
)

type paperMarketReader struct {
	facts  strategy.PaperMarketFacts
	query  strategy.PaperMarketQuery
	calls  int
	err    error
	cancel func()
}

func (r *paperMarketReader) LoadPaperMarket(_ context.Context, q strategy.PaperMarketQuery) (strategy.PaperMarketFacts, error) {
	r.calls++
	r.query = q
	if r.cancel != nil {
		r.cancel()
	}
	return r.facts, r.err
}

func TestPaperMarketServiceOwnsPolicyAndUsesClockObservation(t *testing.T) {
	q, p, f := testutil.PaperMarket()
	reader := &paperMarketReader{facts: f}
	s, err := appstrategy.NewPaperMarketService(reader, clock.FixedClock{Time: q.AsOf.Add(987 * time.Nanosecond)}, p)
	if err != nil {
		t.Fatal(err)
	}
	p.AllowedSymbols[0] = "ETHUSDT"
	first, err := s.Snapshot(context.Background(), q.Scope)
	if err != nil || !reader.query.AsOf.Equal(q.AsOf) || reader.query.Scope != q.Scope || first.SourceSHA256 == "" {
		t.Fatalf("source %+v %v query=%+v", first, err, reader.query)
	}
	first.Policy.AllowedSymbols[0] = "ETHUSDT"
	if _, err := s.Snapshot(context.Background(), q.Scope); err != nil {
		t.Fatal("returned policy aliases service", err)
	}
}

func TestPaperMarketServiceFailsClosed(t *testing.T) {
	for _, scenario := range []string{"invalid scope", "unconfigured symbol", "storage failure", "foreign source", "cancelled before read", "cancelled after read"} {
		t.Run(scenario, func(t *testing.T) {
			q, p, f := testutil.PaperMarket()
			reader := &paperMarketReader{facts: f}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch scenario {
			case "invalid scope":
				q.Scope.Symbol = " padded "
			case "unconfigured symbol":
				q.Scope.Symbol = "ETHUSDT"
			case "storage failure":
				reader.err = errors.New("database unavailable")
			case "foreign source":
				reader.facts.Instrument.Category = "spot"
			case "cancelled before read":
				cancel()
			case "cancelled after read":
				reader.cancel = cancel
			}
			s, err := appstrategy.NewPaperMarketService(reader, clock.FixedClock{Time: q.AsOf}, p)
			if err != nil {
				t.Fatal(err)
			}
			got, err := s.Snapshot(ctx, q.Scope)
			if err == nil || got.SourceSHA256 != "" {
				t.Fatalf("bad source returned %+v %v", got, err)
			}
			if (scenario == "invalid scope" || scenario == "unconfigured symbol" || scenario == "cancelled before read") && reader.calls != 0 {
				t.Fatal("invalid request reached storage")
			}
		})
	}
	q, p, f := testutil.PaperMarket()
	if _, err := appstrategy.NewPaperMarketService(nil, clock.SystemClock{}, p); err == nil {
		t.Fatal("nil reader")
	}
	if _, err := appstrategy.NewPaperMarketService(&paperMarketReader{facts: f}, nil, p); err == nil {
		t.Fatal("nil clock")
	}
	p.MaxDataAge = 0
	if _, err := appstrategy.NewPaperMarketService(&paperMarketReader{}, clock.SystemClock{}, p); err == nil {
		t.Fatal("invalid policy")
	}
	var s *appstrategy.PaperMarketService
	if _, err := s.Snapshot(context.Background(), q.Scope); err == nil {
		t.Fatal("nil service")
	}
}
