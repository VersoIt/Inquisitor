package regime_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	appfeatures "github.com/VersoIt/Inquisitor/internal/app/features"
	appregime "github.com/VersoIt/Inquisitor/internal/app/regime"
	"github.com/VersoIt/Inquisitor/internal/clock"
	domainregime "github.com/VersoIt/Inquisitor/internal/regime"
)

func TestServiceRunClassifiesAndStoresAllPairs(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)
	assembler := &dynamicFeatureAssembler{
		noTradeSymbols: map[string]bool{"ETHUSDT": true},
	}
	repository := &fakeRegimeRepository{stats: domainregime.WriteStats{Inserted: 1}}
	service := testRunService(t, assembler, repository, now)

	got, err := service.Run(ctx, appregime.RunRequest{
		Exchange:      " bybit ",
		Category:      " linear ",
		Symbols:       []string{"BTCUSDT", "ETHUSDT"},
		Intervals:     []string{"1", "5"},
		Start:         now.Add(-24 * time.Hour),
		End:           now,
		CandleLimit:   300,
		TradeLimit:    200,
		SnapshotLimit: 50,
		Runtime: appfeatures.RuntimeState{
			WebSocketConnected: true,
			OrderbookValid:     true,
		},
	})
	if err != nil {
		t.Fatalf("run regime classification: %v", err)
	}

	want := appregime.RunResult{
		Symbols:    2,
		Intervals:  2,
		Attempts:   4,
		Classified: 4,
		Stored:     4,
		Inserted:   4,
		NoTrade:    2,
	}
	if got != want {
		t.Fatalf("result mismatch: got %#v want %#v", got, want)
	}
	if len(assembler.requests) != 4 {
		t.Fatalf("feature assembler requests mismatch: got %d want 4", len(assembler.requests))
	}
	assertRunRequest(t, assembler.requests[0], "BTCUSDT", "1", now)
	assertRunRequest(t, assembler.requests[1], "BTCUSDT", "5", now)
	assertRunRequest(t, assembler.requests[2], "ETHUSDT", "1", now)
	assertRunRequest(t, assembler.requests[3], "ETHUSDT", "5", now)
	if len(repository.states) != 4 {
		t.Fatalf("stored states mismatch: got %d want 4", len(repository.states))
	}
	for _, state := range repository.states {
		if state.Symbol == "ETHUSDT" && !state.NoTrade {
			t.Fatalf("expected ETHUSDT states to be no-trade, got %#v", state)
		}
	}
}

func TestServiceRunReturnsPartialResultOnFailure(t *testing.T) {
	now := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)
	assembler := &dynamicFeatureAssembler{errAtCall: 2}
	repository := &fakeRegimeRepository{stats: domainregime.WriteStats{Inserted: 1}}
	service := testRunService(t, assembler, repository, now)

	got, err := service.Run(context.Background(), appregime.RunRequest{
		Exchange:  "bybit",
		Category:  "linear",
		Symbols:   []string{"BTCUSDT", "ETHUSDT"},
		Intervals: []string{"1"},
		Start:     now.Add(-24 * time.Hour),
		End:       now,
		Runtime: appfeatures.RuntimeState{
			WebSocketConnected: true,
			OrderbookValid:     true,
		},
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "ETHUSDT 1") {
		t.Fatalf("expected pair context in error, got %v", err)
	}
	want := appregime.RunResult{
		Symbols:    2,
		Intervals:  1,
		Attempts:   2,
		Classified: 1,
		Stored:     1,
		Inserted:   1,
	}
	if got != want {
		t.Fatalf("partial result mismatch: got %#v want %#v", got, want)
	}
}

func TestServiceRunRejectsInvalidRequestsTableDriven(t *testing.T) {
	now := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)
	validReq := appregime.RunRequest{
		Exchange:  "bybit",
		Category:  "linear",
		Symbols:   []string{"BTCUSDT"},
		Intervals: []string{"1"},
		Start:     now.Add(-24 * time.Hour),
		End:       now,
	}

	tests := []struct {
		name       string
		mutate     func(*appregime.RunRequest)
		wantErrSub string
	}{
		{
			name: "missing exchange",
			mutate: func(req *appregime.RunRequest) {
				req.Exchange = ""
			},
			wantErrSub: "exchange",
		},
		{
			name: "empty symbols",
			mutate: func(req *appregime.RunRequest) {
				req.Symbols = nil
			},
			wantErrSub: "symbols",
		},
		{
			name: "duplicate intervals",
			mutate: func(req *appregime.RunRequest) {
				req.Intervals = []string{"1", " 1 "}
			},
			wantErrSub: "duplicates",
		},
		{
			name: "end before start",
			mutate: func(req *appregime.RunRequest) {
				req.End = req.Start
			},
			wantErrSub: "end must be after start",
		},
		{
			name: "negative limit",
			mutate: func(req *appregime.RunRequest) {
				req.CandleLimit = -1
			},
			wantErrSub: "limits",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := validReq
			tt.mutate(&req)
			service := testRunService(t, &dynamicFeatureAssembler{}, &fakeRegimeRepository{}, now)

			_, err := service.Run(context.Background(), req)
			if err == nil {
				t.Fatal("expected validation error")
			}
			if !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
		})
	}
}

func TestServiceRunRequiresRepository(t *testing.T) {
	now := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)
	detector, err := domainregime.NewDetector(domainregime.DefaultConfig())
	if err != nil {
		t.Fatalf("new detector: %v", err)
	}
	service := appregime.NewService(&dynamicFeatureAssembler{}, detector)

	_, err = service.Run(context.Background(), appregime.RunRequest{
		Exchange:  "bybit",
		Category:  "linear",
		Symbols:   []string{"BTCUSDT"},
		Intervals: []string{"1"},
		Start:     now.Add(-24 * time.Hour),
		End:       now,
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "regime repository") {
		t.Fatalf("unexpected error: %v", err)
	}
}

type dynamicFeatureAssembler struct {
	requests       []appfeatures.ComputeRequest
	noTradeSymbols map[string]bool
	errAtCall      int
}

func (f *dynamicFeatureAssembler) Compute(_ context.Context, req appfeatures.ComputeRequest) (appfeatures.FeatureSet, error) {
	f.requests = append(f.requests, req)
	if f.errAtCall > 0 && len(f.requests) == f.errAtCall {
		return appfeatures.FeatureSet{}, errors.New("feature assembly failed")
	}

	featureSet := testFeatureSet(req.End)
	patchFeatureSetIdentity(&featureSet, req)
	if f.noTradeSymbols[req.Symbol] {
		featureSet.DataQuality.Complete = false
		featureSet.DataQuality.MissingReasons = []string{"stale_data"}
	}
	return featureSet, nil
}

func testRunService(t *testing.T, assembler *dynamicFeatureAssembler, repository *fakeRegimeRepository, now time.Time) *appregime.Service {
	t.Helper()
	detector, err := domainregime.NewDetector(domainregime.DefaultConfig())
	if err != nil {
		t.Fatalf("new detector: %v", err)
	}
	return appregime.NewService(
		assembler,
		detector,
		appregime.WithRepository(repository),
		appregime.WithClock(clock.FixedClock{Time: now}),
	)
}

func patchFeatureSetIdentity(featureSet *appfeatures.FeatureSet, req appfeatures.ComputeRequest) {
	for index := range featureSet.Price {
		featureSet.Price[index].Exchange = req.Exchange
		featureSet.Price[index].Category = req.Category
		featureSet.Price[index].Symbol = req.Symbol
		featureSet.Price[index].Interval = req.Interval
	}
	for index := range featureSet.Trend {
		featureSet.Trend[index].Exchange = req.Exchange
		featureSet.Trend[index].Category = req.Category
		featureSet.Trend[index].Symbol = req.Symbol
		featureSet.Trend[index].Interval = req.Interval
	}
	for index := range featureSet.Volatility {
		featureSet.Volatility[index].Exchange = req.Exchange
		featureSet.Volatility[index].Category = req.Category
		featureSet.Volatility[index].Symbol = req.Symbol
		featureSet.Volatility[index].Interval = req.Interval
	}
	for index := range featureSet.Volume {
		featureSet.Volume[index].Exchange = req.Exchange
		featureSet.Volume[index].Category = req.Category
		featureSet.Volume[index].Symbol = req.Symbol
		featureSet.Volume[index].Interval = req.Interval
	}
	if featureSet.Microstructure != nil {
		featureSet.Microstructure.Exchange = req.Exchange
		featureSet.Microstructure.Category = req.Category
		featureSet.Microstructure.Symbol = req.Symbol
	}
	featureSet.DataQuality.Exchange = req.Exchange
	featureSet.DataQuality.Category = req.Category
	featureSet.DataQuality.Symbol = req.Symbol
	featureSet.DataQuality.Interval = req.Interval
}

func assertRunRequest(t *testing.T, got appfeatures.ComputeRequest, symbol, interval string, end time.Time) {
	t.Helper()
	if got.Exchange != "bybit" || got.Category != "linear" || got.Symbol != symbol || got.Interval != interval {
		t.Fatalf("request identity mismatch: got %#v", got)
	}
	if got.CandleLimit != 300 || got.TradeLimit != 200 || got.SnapshotLimit != 50 {
		t.Fatalf("request limits mismatch: got %#v", got)
	}
	if !got.End.Equal(end) {
		t.Fatalf("request end mismatch: got %s want %s", got.End, end)
	}
	if !got.Runtime.WebSocketConnected || !got.Runtime.OrderbookValid {
		t.Fatalf("runtime flags mismatch: %#v", got.Runtime)
	}
}
