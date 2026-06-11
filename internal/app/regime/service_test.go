package regime_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	appfeatures "github.com/VersoIt/Inquisitor/internal/app/features"
	appregime "github.com/VersoIt/Inquisitor/internal/app/regime"
	"github.com/VersoIt/Inquisitor/internal/clock"
	domainfeatures "github.com/VersoIt/Inquisitor/internal/features"
	domainregime "github.com/VersoIt/Inquisitor/internal/regime"
)

func TestServiceClassifyUsesAssembledLatestFeatures(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 6, 11, 12, 0, 0, 0, time.UTC)
	calculatedAt := now.Add(250 * time.Millisecond)
	req := appfeatures.ComputeRequest{
		Exchange: "bybit",
		Category: "linear",
		Symbol:   "BTCUSDT",
		Interval: "1",
		Start:    now.Add(-10 * time.Minute),
		End:      now,
	}
	featureAssembler := &fakeFeatureAssembler{featureSet: testFeatureSet(now)}
	detector, err := domainregime.NewDetector(domainregime.DefaultConfig())
	if err != nil {
		t.Fatalf("new detector: %v", err)
	}
	service := appregime.NewService(
		featureAssembler,
		detector,
		appregime.WithClock(clock.FixedClock{Time: calculatedAt}),
	)

	got, err := service.Classify(ctx, req)
	if err != nil {
		t.Fatalf("classify regime: %v", err)
	}

	if featureAssembler.calls != 1 {
		t.Fatalf("feature assembler calls mismatch: got %d want 1", featureAssembler.calls)
	}
	if featureAssembler.lastReq != req {
		t.Fatalf("feature request mismatch: got %#v want %#v", featureAssembler.lastReq, req)
	}
	if got.Regime.Regime != domainregime.RegimeTrendUp {
		t.Fatalf("regime mismatch: got %s want %s", got.Regime.Regime, domainregime.RegimeTrendUp)
	}
	if got.Regime.NoTrade {
		t.Fatalf("expected strategy evaluation to remain possible, reasons=%#v", got.Regime.Reasons)
	}
	if !got.Regime.CalculatedAt.Equal(calculatedAt) {
		t.Fatalf("calculated_at mismatch: got %s want %s", got.Regime.CalculatedAt, calculatedAt)
	}
	if got.Regime.CloseTime != now {
		t.Fatalf("expected latest feature row close time, got %s want %s", got.Regime.CloseTime, now)
	}
}

func TestServiceClassifyPropagatesFeatureAssemblyError(t *testing.T) {
	detector, err := domainregime.NewDetector(domainregime.DefaultConfig())
	if err != nil {
		t.Fatalf("new detector: %v", err)
	}
	service := appregime.NewService(&fakeFeatureAssembler{err: errors.New("db unavailable")}, detector)

	_, err = service.Classify(context.Background(), appfeatures.ComputeRequest{})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "compute features") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestServiceClassifyEmptyFeatureSetFallsBackToNoTrade(t *testing.T) {
	now := time.Date(2026, 6, 11, 12, 0, 0, 0, time.UTC)
	detector, err := domainregime.NewDetector(domainregime.DefaultConfig())
	if err != nil {
		t.Fatalf("new detector: %v", err)
	}
	service := appregime.NewService(
		&fakeFeatureAssembler{featureSet: appfeatures.FeatureSet{
			DataQuality: domainfeatures.DataQualityFeatures{
				ObservedAt: now,
				Complete:   true,
			},
		}},
		detector,
		appregime.WithClock(clock.FixedClock{Time: now}),
	)

	got, err := service.Classify(context.Background(), appfeatures.ComputeRequest{})
	if err != nil {
		t.Fatalf("classify regime: %v", err)
	}
	if got.Regime.Regime != domainregime.RegimeNoTrade || !got.Regime.NoTrade {
		t.Fatalf("expected no-trade regime, got %#v", got.Regime)
	}
	assertReason(t, got.Regime.Reasons, "feature_missing:price")
	assertReason(t, got.Regime.Reasons, "feature_missing:microstructure")
}

type fakeFeatureAssembler struct {
	featureSet appfeatures.FeatureSet
	err        error
	calls      int
	lastReq    appfeatures.ComputeRequest
}

func (f *fakeFeatureAssembler) Compute(_ context.Context, req appfeatures.ComputeRequest) (appfeatures.FeatureSet, error) {
	f.calls++
	f.lastReq = req
	return f.featureSet, f.err
}

func testFeatureSet(now time.Time) appfeatures.FeatureSet {
	previous := now.Add(-time.Minute)
	return appfeatures.FeatureSet{
		Price: []domainfeatures.PriceFeatures{
			{
				Exchange:  "bybit",
				Category:  "linear",
				Symbol:    "BTCUSDT",
				Interval:  "1",
				OpenTime:  previous.Add(-time.Minute),
				CloseTime: previous,
				Complete:  true,
			},
			{
				Exchange:  "bybit",
				Category:  "linear",
				Symbol:    "BTCUSDT",
				Interval:  "1",
				OpenTime:  previous,
				CloseTime: now,
				Complete:  true,
			},
		},
		Trend: []domainfeatures.TrendFeatures{
			{
				Exchange:   "bybit",
				Category:   "linear",
				Symbol:     "BTCUSDT",
				Interval:   "1",
				OpenTime:   previous,
				CloseTime:  now,
				MA20:       decimal.RequireFromString("110"),
				MA50:       decimal.RequireFromString("105"),
				MA200:      decimal.RequireFromString("100"),
				MA50Slope:  decimal.RequireFromString("0.03"),
				MA200Slope: decimal.RequireFromString("0.01"),
				ADX:        decimal.RequireFromString("32"),
				Complete:   true,
			},
		},
		Volatility: []domainfeatures.VolatilityFeatures{
			{
				Exchange:              "bybit",
				Category:              "linear",
				Symbol:                "BTCUSDT",
				Interval:              "1",
				OpenTime:              previous,
				CloseTime:             now,
				VolatilityZScore:      0.3,
				VolatilityCompression: 1,
				Complete:              true,
			},
		},
		Volume: []domainfeatures.VolumeFeatures{
			{
				Exchange:  "bybit",
				Category:  "linear",
				Symbol:    "BTCUSDT",
				Interval:  "1",
				OpenTime:  previous,
				CloseTime: now,
				Complete:  true,
			},
		},
		Microstructure: &domainfeatures.MicrostructureFeatures{
			Exchange:                "bybit",
			Category:                "linear",
			Symbol:                  "BTCUSDT",
			ExchangeTime:            now,
			OrderbookImbalance:      decimal.RequireFromString("0.1"),
			TradeAggressorImbalance: decimal.RequireFromString("0.2"),
			Complete:                true,
		},
		DataQuality: domainfeatures.DataQualityFeatures{
			Exchange:           "bybit",
			Category:           "linear",
			Symbol:             "BTCUSDT",
			Interval:           "1",
			ObservedAt:         now,
			LatestDataTime:     now,
			WebSocketConnected: true,
			OrderbookValid:     true,
			Complete:           true,
		},
	}
}

func assertReason(t *testing.T, got []string, want string) {
	t.Helper()
	for _, reason := range got {
		if reason == want {
			return
		}
	}
	t.Fatalf("missing reason %q in %#v", want, got)
}
