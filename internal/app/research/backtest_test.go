package research_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	appresearch "github.com/VersoIt/Inquisitor/internal/app/research"
	domainbacktest "github.com/VersoIt/Inquisitor/internal/backtest"
	"github.com/VersoIt/Inquisitor/internal/clock"
	domainhypothesis "github.com/VersoIt/Inquisitor/internal/hypothesis"
	"github.com/VersoIt/Inquisitor/internal/marketdata"
	domainregime "github.com/VersoIt/Inquisitor/internal/regime"
	domainresearch "github.com/VersoIt/Inquisitor/internal/research"
)

func TestServiceBacktestRulesRecordsCostAwareInconclusiveResult(t *testing.T) {
	plannedAt := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	hypothesis := singleMarketHypothesisRecord(t, plannedAt.Add(-time.Hour))
	run := ruleEvaluationRun(t, plannedAt, hypothesis)
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
	recorder := &fakeResultRecorder{stats: domainresearch.RecordResultStats{RunUpdated: 1, ResultInserted: 1}}
	service := testBacktestService(plannedAt, hypothesis, run, recorder, regimes, assembler, candles)

	got, err := service.BacktestRules(context.Background(), validBacktestRequest(t, run.RunID))
	if err != nil {
		t.Fatalf("backtest rules: %v", err)
	}

	if got.Run.Status != domainresearch.StatusCompleted || got.Result.Outcome != domainresearch.OutcomeInconclusive {
		t.Fatalf("result status mismatch: run=%s outcome=%s", got.Run.Status, got.Result.Outcome)
	}
	if got.Summary.Trades != 3 || got.Summary.Wins != 2 || got.Summary.Losses != 1 {
		t.Fatalf("summary mismatch: %#v", got.Summary)
	}
	if len(got.Trades) != 3 {
		t.Fatalf("trades mismatch: got %d", len(got.Trades))
	}
	if got.Result.Metrics.Trades != 3 || !got.Result.Metrics.FeesIncluded || !got.Result.Metrics.SpreadIncluded || !got.Result.Metrics.SlippageIncluded {
		t.Fatalf("metrics cost flags mismatch: %#v", got.Result.Metrics)
	}
	if got.Result.Metrics.NetPnL != "2" || got.Result.Metrics.InitialEquity != "1000" || got.Result.Metrics.FinalEquity != "1002" {
		t.Fatalf("financial metrics mismatch: %#v", got.Result.Metrics)
	}
	if !containsReason(got.Result.Reasons, "out_of_sample_not_run") || !containsReason(got.Result.Reasons, "walk_forward_not_run") {
		t.Fatalf("missing conservative validation reasons: %#v", got.Result.Reasons)
	}
	if len(candles.queries) != 1 || candles.queries[0].End != run.WindowEnd.Add(time.Minute) {
		t.Fatalf("candle query mismatch: %#v", candles.queries)
	}
	if assembler.calls != 3 {
		t.Fatalf("feature assembler calls mismatch: got %d want 3", assembler.calls)
	}
}

func TestServiceBacktestRulesRecordsOutOfSampleSplitMetrics(t *testing.T) {
	plannedAt := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	hypothesis := singleMarketHypothesisRecord(t, plannedAt.Add(-time.Hour))
	run := ruleEvaluationRun(t, plannedAt, hypothesis)
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
	service := testBacktestService(
		plannedAt,
		hypothesis,
		run,
		&fakeResultRecorder{stats: domainresearch.RecordResultStats{RunUpdated: 1, ResultInserted: 1}},
		regimes,
		&fakeFeatureAssembler{featureSet: ruleFeatureSet("101", "100")},
		candles,
	)
	req := validBacktestRequest(t, run.RunID)
	req.OutOfSampleStart = plannedAt.Add(-2 * time.Minute)

	got, err := service.BacktestRules(context.Background(), req)
	if err != nil {
		t.Fatalf("backtest rules with oos split: %v", err)
	}

	if !got.Split.Included {
		t.Fatal("expected out-of-sample split to be included")
	}
	if got.Split.InSample.Trades != 1 || got.Split.OutOfSample.Trades != 2 {
		t.Fatalf("split trade counts mismatch: %#v", got.Split)
	}
	if !got.Result.Metrics.OutOfSample {
		t.Fatalf("expected result metrics to include OOS: %#v", got.Result.Metrics)
	}
	if got.Result.Metrics.InSampleTrades != 1 || got.Result.Metrics.OutOfSampleTrades != 2 {
		t.Fatalf("metrics split counts mismatch: %#v", got.Result.Metrics)
	}
	if got.Result.Metrics.InSampleNetPnL != "1" || got.Result.Metrics.OutOfSampleNetPnL != "1" {
		t.Fatalf("metrics split net pnl mismatch: %#v", got.Result.Metrics)
	}
	if containsReason(got.Result.Reasons, "out_of_sample_not_run") {
		t.Fatalf("unexpected missing-OOS reason: %#v", got.Result.Reasons)
	}
	if !containsReason(got.Result.Reasons, "out_of_sample_trades:2") {
		t.Fatalf("missing OOS trade reason: %#v", got.Result.Reasons)
	}
}

func TestServiceBacktestRulesRecordsFailedResultWhenCoverageIsIncomplete(t *testing.T) {
	plannedAt := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	hypothesis := singleMarketHypothesisRecord(t, plannedAt.Add(-time.Hour))
	run := ruleEvaluationRun(t, plannedAt, hypothesis)
	regimes := &fakeRegimeRepository{statesByKey: map[string][]domainregime.State{
		"BTCUSDT|1": {
			testRuleRegimeState(plannedAt.Add(-3*time.Minute), domainregime.RegimeTrendUp, 80),
			testRuleRegimeState(plannedAt.Add(-2*time.Minute), domainregime.RegimeTrendUp, 80),
		},
	}}
	candles := &fakeCandleRepository{}
	assembler := &fakeFeatureAssembler{featureSet: ruleFeatureSet("101", "100")}
	service := testBacktestService(
		plannedAt,
		hypothesis,
		run,
		&fakeResultRecorder{stats: domainresearch.RecordResultStats{RunUpdated: 1, ResultInserted: 1}},
		regimes,
		assembler,
		candles,
	)

	got, err := service.BacktestRules(context.Background(), validBacktestRequest(t, run.RunID))
	if err != nil {
		t.Fatalf("backtest rules: %v", err)
	}

	if got.Run.Status != domainresearch.StatusFailed || got.Result.Outcome != domainresearch.OutcomeNotExecuted {
		t.Fatalf("status mismatch: run=%s outcome=%s", got.Run.Status, got.Result.Outcome)
	}
	if got.Coverage.Expected != 3 || got.Coverage.Missing != 1 {
		t.Fatalf("coverage mismatch: %#v", got.Coverage)
	}
	if assembler.calls != 0 || len(candles.queries) != 0 {
		t.Fatalf("backtest should not load features/candles below coverage threshold")
	}
}

func TestServiceBacktestRulesSkipsSignalsWithoutFutureCandles(t *testing.T) {
	plannedAt := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	hypothesis := singleMarketHypothesisRecord(t, plannedAt.Add(-time.Hour))
	run := ruleEvaluationRun(t, plannedAt, hypothesis)
	regimes := &fakeRegimeRepository{statesByKey: map[string][]domainregime.State{
		"BTCUSDT|1": {
			testRuleRegimeState(plannedAt.Add(-3*time.Minute), domainregime.RegimeTrendUp, 80),
			testRuleRegimeState(plannedAt.Add(-2*time.Minute), domainregime.RegimeTrendUp, 80),
			testRuleRegimeState(plannedAt.Add(-time.Minute), domainregime.RegimeTrendUp, 80),
		},
	}}
	candles := &fakeCandleRepository{candles: []marketdata.Candle{
		testBacktestCandle(plannedAt.Add(-3*time.Minute), "100", "101"),
	}}
	service := testBacktestService(
		plannedAt,
		hypothesis,
		run,
		&fakeResultRecorder{stats: domainresearch.RecordResultStats{RunUpdated: 1, ResultInserted: 1}},
		regimes,
		&fakeFeatureAssembler{featureSet: ruleFeatureSet("101", "100")},
		candles,
	)

	got, err := service.BacktestRules(context.Background(), validBacktestRequest(t, run.RunID))
	if err != nil {
		t.Fatalf("backtest rules: %v", err)
	}

	if got.Summary.Trades != 1 || got.Skipped.NoFutureCandles != 2 {
		t.Fatalf("expected one trade and two future-candle skips: summary=%#v skipped=%#v", got.Summary, got.Skipped)
	}
	if got.Result.Metrics.Trades != 1 {
		t.Fatalf("metrics trades mismatch: %#v", got.Result.Metrics)
	}
}

func TestServiceBacktestRulesRejectsInvalidInputsTableDriven(t *testing.T) {
	plannedAt := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	hypothesis := singleMarketHypothesisRecord(t, plannedAt.Add(-time.Hour))
	run := ruleEvaluationRun(t, plannedAt, hypothesis)
	service := testBacktestService(
		plannedAt,
		hypothesis,
		run,
		&fakeResultRecorder{},
		&fakeRegimeRepository{},
		&fakeFeatureAssembler{},
		&fakeCandleRepository{},
	)

	tests := []struct {
		name       string
		req        appresearch.BacktestRequest
		wantErrSub string
	}{
		{
			name:       "missing run id",
			req:        validBacktestRequest(t, " "),
			wantErrSub: "run_id",
		},
		{
			name: "missing holding period",
			req: func() appresearch.BacktestRequest {
				req := validBacktestRequest(t, run.RunID)
				req.HoldingPeriodCandles = 0
				return req
			}(),
			wantErrSub: "holding_period_candles",
		},
		{
			name: "missing initial equity",
			req: func() appresearch.BacktestRequest {
				req := validBacktestRequest(t, run.RunID)
				req.InitialEquity = decimal.Zero
				return req
			}(),
			wantErrSub: "initial_equity",
		},
		{
			name: "invalid costs",
			req: func() appresearch.BacktestRequest {
				req := validBacktestRequest(t, run.RunID)
				req.Costs.TakerFeeBPS = decimal.RequireFromString("-1")
				return req
			}(),
			wantErrSub: "taker_fee_bps",
		},
		{
			name: "out of sample split outside research window",
			req: func() appresearch.BacktestRequest {
				req := validBacktestRequest(t, run.RunID)
				req.OutOfSampleStart = run.WindowEnd
				return req
			}(),
			wantErrSub: "out_of_sample_start",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := service.BacktestRules(context.Background(), tt.req)
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
		})
	}
}

func testBacktestService(plannedAt time.Time, hypothesis domainhypothesis.Record, run domainresearch.Run, recorder *fakeResultRecorder, regimes *fakeRegimeRepository, assembler *fakeFeatureAssembler, candles *fakeCandleRepository) *appresearch.Service {
	return appresearch.NewService(
		&fakeHypothesisRepository{records: []domainhypothesis.Record{hypothesis}},
		&fakeRunRepository{runs: []domainresearch.Run{run}},
		appresearch.WithResultRecorder(recorder),
		appresearch.WithRegimeRepository(regimes),
		appresearch.WithFeatureAssembler(assembler),
		appresearch.WithCandleRepository(candles),
		appresearch.WithClock(clock.FixedClock{Time: plannedAt}),
	)
}

func validBacktestRequest(t *testing.T, runID string) appresearch.BacktestRequest {
	t.Helper()

	costs, err := domainbacktest.NewCostModel(0, 0, 0, 0, 1)
	if err != nil {
		t.Fatalf("new test costs: %v", err)
	}
	return appresearch.BacktestRequest{
		RunID:                runID,
		FeatureLookback:      time.Hour,
		MinRegimeCoveragePct: 100,
		HoldingPeriodCandles: 1,
		InitialEquity:        decimal.RequireFromString("1000"),
		Quantity:             decimal.RequireFromString("1"),
		Costs:                costs,
		CandleLimit:          100,
		TradeLimit:           100,
		SnapshotLimit:        100,
	}
}

type fakeCandleRepository struct {
	candles []marketdata.Candle
	queries []marketdata.CandleQuery
	err     error
}

func (r *fakeCandleRepository) UpsertCandles(context.Context, []marketdata.Candle) (marketdata.WriteStats, error) {
	return marketdata.WriteStats{}, nil
}

func (r *fakeCandleRepository) ListCandles(_ context.Context, query marketdata.CandleQuery) ([]marketdata.Candle, error) {
	r.queries = append(r.queries, query)
	if r.err != nil {
		return nil, r.err
	}
	var candles []marketdata.Candle
	for _, candle := range r.candles {
		if query.Symbol != "" && candle.Symbol != query.Symbol {
			continue
		}
		if query.Interval != "" && candle.Interval != query.Interval {
			continue
		}
		if !query.Start.IsZero() && candle.OpenTime.Before(query.Start) {
			continue
		}
		if !query.End.IsZero() && !candle.OpenTime.Before(query.End) {
			continue
		}
		candles = append(candles, candle)
	}
	return candles, nil
}

func testBacktestCandle(openTime time.Time, open string, close string) marketdata.Candle {
	return marketdata.Candle{
		Exchange:  "bybit",
		Category:  "linear",
		Symbol:    "BTCUSDT",
		Interval:  "1",
		OpenTime:  openTime.UTC(),
		CloseTime: openTime.Add(time.Minute).UTC(),
		Open:      decimal.RequireFromString(open),
		High:      decimal.RequireFromString("110"),
		Low:       decimal.RequireFromString("90"),
		Close:     decimal.RequireFromString(close),
		Volume:    decimal.RequireFromString("10"),
		Turnover:  decimal.RequireFromString("1000"),
		IsClosed:  true,
	}
}
