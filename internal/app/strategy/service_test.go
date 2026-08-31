package strategy_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	appstrategy "github.com/VersoIt/Inquisitor/internal/app/strategy"
	domainrisk "github.com/VersoIt/Inquisitor/internal/risk"
	domainstrategy "github.com/VersoIt/Inquisitor/internal/strategy"
)

func TestServiceBuildRiskIntentCanonicalizesSignal(t *testing.T) {
	signal := validAppTradeSignal()
	signal.Symbol = " btcusdt "
	signal.Side = " long "
	signal.Reason = " signal rules passed "

	got, err := appstrategy.NewService().BuildRiskIntent(context.Background(), appstrategy.BuildRiskIntentRequest{
		IntentID: " intent_0001 ",
		Signal:   signal,
	})
	if err != nil {
		t.Fatalf("build risk intent: %v", err)
	}
	if got.Signal.Symbol != "BTCUSDT" || got.Signal.Side != domainstrategy.SideLong || got.Signal.Reason != "signal rules passed" {
		t.Fatalf("signal was not canonicalized: %#v", got.Signal)
	}
	if got.Intent.IntentID != "intent_0001" || got.Intent.Symbol != "BTCUSDT" || got.Intent.Side != domainrisk.SideLong {
		t.Fatalf("intent mismatch: %#v", got.Intent)
	}
	if !got.Intent.HypothesisApproved || !got.Intent.HypothesisMaxRiskPct.Equal(decimal.RequireFromString("0.25")) {
		t.Fatalf("hypothesis risk snapshot mismatch: %#v", got.Intent)
	}
}

func TestServiceBuildRiskIntentRejectsInvalidInputsTableDriven(t *testing.T) {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	tests := []struct {
		name       string
		service    *appstrategy.Service
		ctx        context.Context
		req        appstrategy.BuildRiskIntentRequest
		wantErrSub string
	}{
		{
			name:       "cancelled context",
			service:    appstrategy.NewService(),
			ctx:        canceled,
			req:        validBuildRiskIntentRequest(),
			wantErrSub: context.Canceled.Error(),
		},
		{
			name:       "nil service",
			service:    nil,
			ctx:        context.Background(),
			req:        validBuildRiskIntentRequest(),
			wantErrSub: "requires service",
		},
		{
			name:    "missing intent id",
			service: appstrategy.NewService(),
			ctx:     context.Background(),
			req: func() appstrategy.BuildRiskIntentRequest {
				req := validBuildRiskIntentRequest()
				req.IntentID = " "
				return req
			}(),
			wantErrSub: "intent_id",
		},
		{
			name:    "invalid signal",
			service: appstrategy.NewService(),
			ctx:     context.Background(),
			req: func() appstrategy.BuildRiskIntentRequest {
				req := validBuildRiskIntentRequest()
				req.Signal.StopLoss = decimal.Zero
				return req
			}(),
			wantErrSub: "strategy trade signal validation failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.service.BuildRiskIntent(tt.ctx, tt.req)
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.wantErrSub) && !errors.Is(err, context.Canceled) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
		})
	}
}

func validBuildRiskIntentRequest() appstrategy.BuildRiskIntentRequest {
	return appstrategy.BuildRiskIntentRequest{
		IntentID: "intent_0001",
		Signal:   validAppTradeSignal(),
	}
}

func validAppTradeSignal() domainstrategy.TradeSignal {
	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	return domainstrategy.TradeSignal{
		SignalID:                  "signal_0001",
		HypothesisID:              "hypothesis_0001",
		HypothesisVersion:         "v1",
		StrategyName:              "trend_momentum",
		Symbol:                    "BTCUSDT",
		Side:                      domainstrategy.SideLong,
		Confidence:                80,
		EntryPrice:                decimal.RequireFromString("100"),
		StopLoss:                  decimal.RequireFromString("95"),
		TakeProfit:                decimal.RequireFromString("112"),
		Leverage:                  decimal.RequireFromString("1"),
		HypothesisApprovedForMode: true,
		HypothesisMaxRiskPct:      decimal.RequireFromString("0.25"),
		HypothesisMinConfidence:   70,
		Reason:                    "signal rules passed",
		ObservedAt:                now.Add(-time.Second),
		CreatedAt:                 now,
	}
}
