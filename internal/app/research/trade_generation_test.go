package research_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	appresearch "github.com/VersoIt/Inquisitor/internal/app/research"
	domainbacktest "github.com/VersoIt/Inquisitor/internal/backtest"
	domainhypothesis "github.com/VersoIt/Inquisitor/internal/hypothesis"
	"github.com/VersoIt/Inquisitor/internal/marketdata"
	domainregime "github.com/VersoIt/Inquisitor/internal/regime"
	domainresearch "github.com/VersoIt/Inquisitor/internal/research"
)

func TestServiceGenerateRuleTradesReadsFinalizedRunWithoutMutatingResult(t *testing.T) {
	plannedAt := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	hypothesis := singleMarketHypothesisRecord(t, plannedAt.Add(-time.Hour))
	run := ruleEvaluationRun(t, plannedAt, hypothesis)
	run.Status = domainresearch.StatusCompleted
	regimes := &fakeRegimeRepository{statesByKey: map[string][]domainregime.State{
		"BTCUSDT|1": {
			testRuleRegimeState(plannedAt.Add(-3*time.Minute), domainregime.RegimeTrendUp, 80),
			testRuleRegimeState(plannedAt.Add(-2*time.Minute), domainregime.RegimeTrendUp, 80),
			testRuleRegimeState(plannedAt.Add(-time.Minute), domainregime.RegimeTrendUp, 80),
		},
	}}
	candles := &fakeCandleRepository{candles: []marketdata.Candle{
		testBacktestCandle(plannedAt.Add(-3*time.Minute), "100", "101"),
		testBacktestCandle(plannedAt.Add(-2*time.Minute), "100", "99"),
		testBacktestCandle(plannedAt.Add(-time.Minute), "100", "102"),
	}}
	assembler := &fakeFeatureAssembler{featureSet: ruleFeatureSet("101", "100")}
	service := appresearch.NewService(
		&fakeHypothesisRepository{records: []domainhypothesis.Record{hypothesis}},
		nil,
		appresearch.WithRegimeRepository(regimes),
		appresearch.WithFeatureAssembler(assembler),
		appresearch.WithCandleRepository(candles),
	)

	got, err := service.GenerateRuleTrades(context.Background(), validTradeGenerationRequest(t, run))
	if err != nil {
		t.Fatalf("generate rule trades: %v", err)
	}

	if !got.CoverageSufficient || got.Coverage.Expected != 3 || got.Coverage.Missing != 0 {
		t.Fatalf("coverage mismatch: %#v", got.Coverage)
	}
	if got.Symbol != "BTCUSDT" || got.Interval != "1" || len(got.Trades) != 3 {
		t.Fatalf("generation mismatch: %#v", got)
	}
	if got.Skipped.RegimeBlocked != 0 || assembler.calls != 3 {
		t.Fatalf("skipped/feature calls mismatch: skipped=%#v calls=%d", got.Skipped, assembler.calls)
	}
	if len(candles.queries) != 1 || candles.queries[0].Symbol != "BTCUSDT" || candles.queries[0].Interval != "1" {
		t.Fatalf("candle query mismatch: %#v", candles.queries)
	}
}

func TestServiceGenerateRuleTradesReturnsCoverageFailureWithoutFeatures(t *testing.T) {
	plannedAt := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	hypothesis := singleMarketHypothesisRecord(t, plannedAt.Add(-time.Hour))
	run := ruleEvaluationRun(t, plannedAt, hypothesis)
	run.Status = domainresearch.StatusCompleted
	assembler := &fakeFeatureAssembler{featureSet: ruleFeatureSet("101", "100")}
	candles := &fakeCandleRepository{}
	service := appresearch.NewService(
		&fakeHypothesisRepository{records: []domainhypothesis.Record{hypothesis}},
		nil,
		appresearch.WithRegimeRepository(&fakeRegimeRepository{statesByKey: map[string][]domainregime.State{
			"BTCUSDT|1": {
				testRuleRegimeState(plannedAt.Add(-3*time.Minute), domainregime.RegimeTrendUp, 80),
			},
		}}),
		appresearch.WithFeatureAssembler(assembler),
		appresearch.WithCandleRepository(candles),
	)

	got, err := service.GenerateRuleTrades(context.Background(), validTradeGenerationRequest(t, run))
	if err != nil {
		t.Fatalf("generate rule trades: %v", err)
	}

	if got.CoverageSufficient || got.Coverage.Expected != 3 || got.Coverage.Missing != 2 {
		t.Fatalf("expected insufficient coverage: %#v", got.Coverage)
	}
	if len(got.Trades) != 0 || assembler.calls != 0 || len(candles.queries) != 0 {
		t.Fatalf("insufficient coverage must not compute trades: trades=%d calls=%d queries=%d", len(got.Trades), assembler.calls, len(candles.queries))
	}
}

func TestServiceGenerateRuleTradesRejectsInvalidInputsTableDriven(t *testing.T) {
	plannedAt := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	hypothesis := singleMarketHypothesisRecord(t, plannedAt.Add(-time.Hour))
	run := ruleEvaluationRun(t, plannedAt, hypothesis)
	run.Status = domainresearch.StatusCompleted
	validService := appresearch.NewService(
		&fakeHypothesisRepository{records: []domainhypothesis.Record{hypothesis}},
		nil,
		appresearch.WithRegimeRepository(&fakeRegimeRepository{}),
		appresearch.WithFeatureAssembler(&fakeFeatureAssembler{}),
		appresearch.WithCandleRepository(&fakeCandleRepository{}),
	)

	tests := []struct {
		name       string
		service    *appresearch.Service
		req        appresearch.TradeGenerationRequest
		wantErrSub string
	}{
		{
			name:       "missing hypothesis repository",
			service:    appresearch.NewService(nil, nil, appresearch.WithRegimeRepository(&fakeRegimeRepository{}), appresearch.WithFeatureAssembler(&fakeFeatureAssembler{}), appresearch.WithCandleRepository(&fakeCandleRepository{})),
			req:        validTradeGenerationRequest(t, run),
			wantErrSub: "hypothesis repository",
		},
		{
			name:       "missing regime repository",
			service:    appresearch.NewService(&fakeHypothesisRepository{}, nil, appresearch.WithFeatureAssembler(&fakeFeatureAssembler{}), appresearch.WithCandleRepository(&fakeCandleRepository{})),
			req:        validTradeGenerationRequest(t, run),
			wantErrSub: "regime repository",
		},
		{
			name:    "symbol outside scope",
			service: validService,
			req: func() appresearch.TradeGenerationRequest {
				req := validTradeGenerationRequest(t, run)
				req.Symbol = "ETHUSDT"
				return req
			}(),
			wantErrSub: "market scope",
		},
		{
			name:    "invalid holding period",
			service: validService,
			req: func() appresearch.TradeGenerationRequest {
				req := validTradeGenerationRequest(t, run)
				req.HoldingPeriodCandles = 0
				return req
			}(),
			wantErrSub: "holding_period_candles",
		},
		{
			name:    "invalid quantity",
			service: validService,
			req: func() appresearch.TradeGenerationRequest {
				req := validTradeGenerationRequest(t, run)
				req.Quantity = decimal.Zero
				return req
			}(),
			wantErrSub: "quantity",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.service.GenerateRuleTrades(context.Background(), tt.req)
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
		})
	}
}

func validTradeGenerationRequest(t *testing.T, run domainresearch.Run) appresearch.TradeGenerationRequest {
	t.Helper()

	costs, err := domainbacktest.NewCostModel(0, 0, 0, 0, 1)
	if err != nil {
		t.Fatalf("new cost model: %v", err)
	}
	return appresearch.TradeGenerationRequest{
		Run:                  run,
		FeatureLookback:      time.Hour,
		MinRegimeCoveragePct: 100,
		HoldingPeriodCandles: 1,
		Quantity:             decimal.RequireFromString("1"),
		Costs:                costs,
		CandleLimit:          100,
		TradeLimit:           100,
		SnapshotLimit:        100,
	}
}
