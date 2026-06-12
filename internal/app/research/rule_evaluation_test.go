package research_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	appfeatures "github.com/VersoIt/Inquisitor/internal/app/features"
	appresearch "github.com/VersoIt/Inquisitor/internal/app/research"
	"github.com/VersoIt/Inquisitor/internal/clock"
	domainfeatures "github.com/VersoIt/Inquisitor/internal/features"
	domainhypothesis "github.com/VersoIt/Inquisitor/internal/hypothesis"
	domainregime "github.com/VersoIt/Inquisitor/internal/regime"
	domainresearch "github.com/VersoIt/Inquisitor/internal/research"
)

func TestServiceEvaluateRulesRecordsInconclusiveResultWhenRulesAreEvaluated(t *testing.T) {
	plannedAt := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)
	hypothesis := singleMarketHypothesisRecord(t, plannedAt.Add(-time.Hour))
	run := ruleEvaluationRun(t, plannedAt, hypothesis)
	regimes := &fakeRegimeRepository{statesByKey: map[string][]domainregime.State{
		"BTCUSDT|1": {
			testRuleRegimeState(plannedAt.Add(-3*time.Minute), domainregime.RegimeTrendUp, 80),
			testRuleRegimeState(plannedAt.Add(-2*time.Minute), domainregime.RegimeTrendUp, 80),
			testRuleRegimeState(plannedAt.Add(-time.Minute), domainregime.RegimeTrendUp, 80),
		},
	}}
	assembler := &fakeFeatureAssembler{featureSet: ruleFeatureSet("101", "100")}
	recorder := &fakeResultRecorder{stats: domainresearch.RecordResultStats{RunUpdated: 1, ResultInserted: 1}}
	service := testRuleEvaluationService(plannedAt, hypothesis, run, recorder, regimes, assembler)

	got, err := service.EvaluateRules(context.Background(), appresearch.RuleEvaluationRequest{RunID: run.RunID})
	if err != nil {
		t.Fatalf("evaluate rules: %v", err)
	}

	if got.Run.Status != domainresearch.StatusCompleted || got.Result.Outcome != domainresearch.OutcomeInconclusive {
		t.Fatalf("result status mismatch: run=%s outcome=%s", got.Run.Status, got.Result.Outcome)
	}
	if got.Summary.Observations != 3 || got.Summary.RegimeAllowed != 3 || got.Summary.RuleEvaluations != 3 || got.Summary.SignalMatches != 3 {
		t.Fatalf("summary mismatch: %#v", got.Summary)
	}
	if got.Result.Metrics.RuleObservations != 3 || got.Result.Metrics.SignalRulePasses != 3 || got.Result.Metrics.Trades != 0 {
		t.Fatalf("metrics mismatch: %#v", got.Result.Metrics)
	}
	if assembler.calls != 3 {
		t.Fatalf("feature assembler calls mismatch: got %d want 3", assembler.calls)
	}
	if recorder.run.Status != domainresearch.StatusCompleted || recorder.result.Outcome != domainresearch.OutcomeInconclusive {
		t.Fatalf("recorded payload mismatch: run=%#v result=%#v", recorder.run, recorder.result)
	}
	if !containsReason(got.Result.Reasons, "trades_not_evaluated") {
		t.Fatalf("expected no-trades reason: %#v", got.Result.Reasons)
	}
}

func TestServiceEvaluateRulesSkipsFeatureEvaluationWhenRegimeIsBlocked(t *testing.T) {
	plannedAt := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)
	hypothesis := singleMarketHypothesisRecord(t, plannedAt.Add(-time.Hour))
	run := ruleEvaluationRun(t, plannedAt, hypothesis)
	regimes := &fakeRegimeRepository{statesByKey: map[string][]domainregime.State{
		"BTCUSDT|1": {
			testRuleRegimeState(plannedAt.Add(-3*time.Minute), domainregime.RegimeNoTrade, 0),
			testRuleRegimeState(plannedAt.Add(-2*time.Minute), domainregime.RegimeNoTrade, 0),
			testRuleRegimeState(plannedAt.Add(-time.Minute), domainregime.RegimeNoTrade, 0),
		},
	}}
	assembler := &fakeFeatureAssembler{featureSet: ruleFeatureSet("101", "100")}
	service := testRuleEvaluationService(
		plannedAt,
		hypothesis,
		run,
		&fakeResultRecorder{stats: domainresearch.RecordResultStats{RunUpdated: 1, ResultInserted: 1}},
		regimes,
		assembler,
	)

	got, err := service.EvaluateRules(context.Background(), appresearch.RuleEvaluationRequest{RunID: run.RunID})
	if err != nil {
		t.Fatalf("evaluate rules: %v", err)
	}

	if got.Summary.Observations != 3 || got.Summary.RegimeBlocked != 3 || got.Summary.RuleEvaluations != 0 {
		t.Fatalf("summary mismatch: %#v", got.Summary)
	}
	if assembler.calls != 0 {
		t.Fatalf("feature assembler should not be called for blocked regimes, got %d calls", assembler.calls)
	}
	if got.Result.Metrics.RegimeBlockedObservations != 3 || got.Result.Metrics.SignalMatches != 0 {
		t.Fatalf("metrics mismatch: %#v", got.Result.Metrics)
	}
}

func TestServiceEvaluateRulesRecordsFailedResultWhenRegimeCoverageIsIncomplete(t *testing.T) {
	plannedAt := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)
	hypothesis := singleMarketHypothesisRecord(t, plannedAt.Add(-time.Hour))
	run := ruleEvaluationRun(t, plannedAt, hypothesis)
	regimes := &fakeRegimeRepository{statesByKey: map[string][]domainregime.State{
		"BTCUSDT|1": {
			testRuleRegimeState(plannedAt.Add(-3*time.Minute), domainregime.RegimeTrendUp, 80),
			testRuleRegimeState(plannedAt.Add(-2*time.Minute), domainregime.RegimeTrendUp, 80),
		},
	}}
	assembler := &fakeFeatureAssembler{featureSet: ruleFeatureSet("101", "100")}
	service := testRuleEvaluationService(
		plannedAt,
		hypothesis,
		run,
		&fakeResultRecorder{stats: domainresearch.RecordResultStats{RunUpdated: 1, ResultInserted: 1}},
		regimes,
		assembler,
	)

	got, err := service.EvaluateRules(context.Background(), appresearch.RuleEvaluationRequest{RunID: run.RunID})
	if err != nil {
		t.Fatalf("evaluate rules: %v", err)
	}

	if got.Run.Status != domainresearch.StatusFailed || got.Result.Outcome != domainresearch.OutcomeNotExecuted {
		t.Fatalf("result status mismatch: run=%s outcome=%s", got.Run.Status, got.Result.Outcome)
	}
	if got.Coverage.Expected != 3 || got.Coverage.Observed != 2 || got.Coverage.Missing != 1 {
		t.Fatalf("coverage mismatch: %#v", got.Coverage)
	}
	if assembler.calls != 0 {
		t.Fatalf("feature assembler should not be called below coverage threshold, got %d calls", assembler.calls)
	}
	if !containsReason(got.Result.Reasons, "regime_coverage_below_threshold") {
		t.Fatalf("missing coverage reason: %#v", got.Result.Reasons)
	}
}

func TestServiceEvaluateRulesRejectsInvalidInputsTableDriven(t *testing.T) {
	plannedAt := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)
	hypothesis := singleMarketHypothesisRecord(t, plannedAt.Add(-time.Hour))
	run := ruleEvaluationRun(t, plannedAt, hypothesis)
	hashMismatchRun := run
	hashMismatchRun.HypothesisContentSHA256 = strings.Repeat("b", 64)

	tests := []struct {
		name       string
		service    *appresearch.Service
		req        appresearch.RuleEvaluationRequest
		wantErrSub string
	}{
		{
			name:       "missing run id",
			service:    testRuleEvaluationService(plannedAt, hypothesis, run, &fakeResultRecorder{}, &fakeRegimeRepository{}, &fakeFeatureAssembler{}),
			req:        appresearch.RuleEvaluationRequest{RunID: " "},
			wantErrSub: "run_id",
		},
		{
			name:       "invalid feature lookback",
			service:    testRuleEvaluationService(plannedAt, hypothesis, run, &fakeResultRecorder{}, &fakeRegimeRepository{}, &fakeFeatureAssembler{}),
			req:        appresearch.RuleEvaluationRequest{RunID: run.RunID, FeatureLookback: -time.Minute},
			wantErrSub: "feature_lookback",
		},
		{
			name:       "hypothesis hash mismatch",
			service:    testRuleEvaluationService(plannedAt, hypothesis, hashMismatchRun, &fakeResultRecorder{}, &fakeRegimeRepository{}, &fakeFeatureAssembler{}),
			req:        appresearch.RuleEvaluationRequest{RunID: hashMismatchRun.RunID},
			wantErrSub: "content hash",
		},
		{
			name: "missing feature assembler dependency",
			service: appresearch.NewService(
				&fakeHypothesisRepository{records: []domainhypothesis.Record{hypothesis}},
				&fakeRunRepository{runs: []domainresearch.Run{run}},
				appresearch.WithResultRecorder(&fakeResultRecorder{}),
				appresearch.WithRegimeRepository(&fakeRegimeRepository{}),
				appresearch.WithClock(clock.FixedClock{Time: plannedAt}),
			),
			req:        appresearch.RuleEvaluationRequest{RunID: run.RunID},
			wantErrSub: "feature assembler",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.service.EvaluateRules(context.Background(), tt.req)
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
		})
	}
}

func testRuleEvaluationService(plannedAt time.Time, hypothesis domainhypothesis.Record, run domainresearch.Run, recorder *fakeResultRecorder, regimes *fakeRegimeRepository, assembler *fakeFeatureAssembler) *appresearch.Service {
	return appresearch.NewService(
		&fakeHypothesisRepository{records: []domainhypothesis.Record{hypothesis}},
		&fakeRunRepository{runs: []domainresearch.Run{run}},
		appresearch.WithResultRecorder(recorder),
		appresearch.WithRegimeRepository(regimes),
		appresearch.WithFeatureAssembler(assembler),
		appresearch.WithClock(clock.FixedClock{Time: plannedAt}),
	)
}

func singleMarketHypothesisRecord(t *testing.T, importedAt time.Time) domainhypothesis.Record {
	t.Helper()

	raw := []byte(`name: trend_momentum_draft
version: "0.1.0"
status: DRAFT
description: Draft research hypothesis for directional momentum with regime gating.
thesis: Strong directional markets may persist after feature confirmation.
market:
  exchange: bybit
  category: linear
  symbols:
    - BTCUSDT
  intervals:
    - "1"
regime:
  allowed:
    - TREND_UP
  blocked:
    - NO_TRADE
direction: LONG
signals:
  - name: ma_alignment
    description: Fast trend average should stay above slow average.
    feature: trend.ma20
    operator: ">"
    value: trend.ma50
risk:
  max_risk_per_trade_pct: 0.25
  min_confidence: 70
  require_stop_loss: true
validation:
  min_trades: 150
  require_out_of_sample: true
  require_walk_forward: true
  require_regime_analysis: true
costs:
  include_fees: true
  include_spread: true
  include_slippage: true
`)
	spec, err := domainhypothesis.ParseYAML(raw)
	if err != nil {
		t.Fatalf("parse single-market hypothesis: %v", err)
	}
	record, err := domainhypothesis.NewRecord(spec, "hypotheses/test.yaml", raw, importedAt)
	if err != nil {
		t.Fatalf("new single-market hypothesis record: %v", err)
	}
	return record
}

func ruleEvaluationRun(t *testing.T, plannedAt time.Time, hypothesis domainhypothesis.Record) domainresearch.Run {
	t.Helper()

	run, err := domainresearch.NewPlannedRun(domainresearch.PlanInput{
		RunID:                   "research_rules_0001",
		HypothesisName:          hypothesis.Name,
		HypothesisVersion:       hypothesis.Version,
		HypothesisContentSHA256: hypothesis.ContentSHA256,
		Exchange:                hypothesis.Spec.Market.Exchange,
		Category:                hypothesis.Spec.Market.Category,
		WindowStart:             plannedAt.Add(-3 * time.Minute),
		WindowEnd:               plannedAt,
		PlannedAt:               plannedAt,
		Symbols:                 hypothesis.Spec.Market.Symbols,
		Intervals:               hypothesis.Spec.Market.Intervals,
	})
	if err != nil {
		t.Fatalf("new rule evaluation run: %v", err)
	}
	return run
}

func testRuleRegimeState(closeTime time.Time, regime domainregime.Regime, confidence int) domainregime.State {
	return domainregime.State{
		Exchange:        "bybit",
		Category:        "linear",
		Symbol:          "BTCUSDT",
		Interval:        "1",
		OpenTime:        closeTime.Add(-time.Minute),
		CloseTime:       closeTime,
		CalculatedAt:    closeTime.Add(100 * time.Millisecond),
		Regime:          regime,
		CandidateRegime: regime,
		Confidence:      confidence,
		NoTrade:         regime == domainregime.RegimeNoTrade,
		Reasons:         []string{"test_regime"},
	}
}

func ruleFeatureSet(ma20, ma50 string) appfeatures.FeatureSet {
	return appfeatures.FeatureSet{
		Trend: []domainfeatures.TrendFeatures{
			{
				MA20:     decimal.RequireFromString(ma20),
				MA50:     decimal.RequireFromString(ma50),
				Complete: true,
			},
		},
	}
}

type fakeFeatureAssembler struct {
	featureSet appfeatures.FeatureSet
	err        error
	calls      int
	requests   []appfeatures.ComputeRequest
}

func (f *fakeFeatureAssembler) Compute(_ context.Context, req appfeatures.ComputeRequest) (appfeatures.FeatureSet, error) {
	f.calls++
	f.requests = append(f.requests, req)
	if f.err != nil {
		return appfeatures.FeatureSet{}, f.err
	}
	return f.featureSet, nil
}
