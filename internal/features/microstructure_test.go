package features_test

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/VersoIt/Inquisitor/internal/features"
	"github.com/VersoIt/Inquisitor/internal/marketdata"
)

func TestComputeMicrostructureFeaturesTableDriven(t *testing.T) {
	now := time.Date(2026, 6, 11, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name       string
		snapshot   marketdata.OrderbookSnapshot
		trades     []marketdata.PublicTrade
		cfg        features.MicrostructureFeatureConfig
		assertions func(t *testing.T, got features.MicrostructureFeatures)
	}{
		{
			name:     "computes spread liquidity and trade aggressor imbalance",
			snapshot: testOrderbookSnapshot(now),
			trades: []marketdata.PublicTrade{
				testMicroTrade("trade-1", "Buy", "0.6", now.Add(-30*time.Second)),
				testMicroTrade("trade-2", "Sell", "0.2", now.Add(-10*time.Second)),
				testMicroTrade("trade-old", "Sell", "5", now.Add(-2*time.Minute)),
			},
			cfg: features.MicrostructureFeatureConfig{
				LiquidityLevels: 2,
				TradeWindow:     time.Minute,
			},
			assertions: func(t *testing.T, got features.MicrostructureFeatures) {
				t.Helper()
				if !got.Complete {
					t.Fatalf("expected complete microstructure features: %#v", got)
				}
				assertDecimal(t, got.BestBid, decimal.RequireFromString("100"))
				assertDecimal(t, got.BestAsk, decimal.RequireFromString("101"))
				assertDecimal(t, got.Spread, decimal.RequireFromString("1"))
				assertDecimal(t, got.SpreadBPS, decimal.RequireFromString("99.502487562189"))
				assertDecimal(t, got.BidLiquidityTopN, decimal.RequireFromString("299"))
				assertDecimal(t, got.AskLiquidityTopN, decimal.RequireFromString("405"))
				assertDecimal(t, got.OrderbookImbalance, decimal.RequireFromString("-106").Div(decimal.RequireFromString("704")))
				assertDecimal(t, got.BuyTradeQuantity, decimal.RequireFromString("0.6"))
				assertDecimal(t, got.SellTradeQuantity, decimal.RequireFromString("0.2"))
				assertDecimal(t, got.TradeAggressorImbalance, decimal.RequireFromString("0.4").Div(decimal.RequireFromString("0.8")))
			},
		},
		{
			name:     "no trades in window keeps orderbook features and marks incomplete",
			snapshot: testOrderbookSnapshot(now),
			trades: []marketdata.PublicTrade{
				testMicroTrade("trade-old", "Buy", "1", now.Add(-2*time.Minute)),
			},
			cfg: features.MicrostructureFeatureConfig{
				LiquidityLevels: 1,
				TradeWindow:     time.Minute,
			},
			assertions: func(t *testing.T, got features.MicrostructureFeatures) {
				t.Helper()
				if got.Complete {
					t.Fatalf("expected incomplete microstructure features without trades: %#v", got)
				}
				assertMissingReasons(t, got.MissingReasons, []string{"trade_window"})
				assertDecimal(t, got.BidLiquidityTopN, decimal.RequireFromString("200"))
				assertDecimal(t, got.AskLiquidityTopN, decimal.RequireFromString("303"))
				assertDecimal(t, got.TradeAggressorImbalance, decimal.Zero)
			},
		},
		{
			name:     "default config is accepted",
			snapshot: testOrderbookSnapshot(now),
			trades: []marketdata.PublicTrade{
				testMicroTrade("trade-1", "Buy", "1", now.Add(-30*time.Second)),
			},
			assertions: func(t *testing.T, got features.MicrostructureFeatures) {
				t.Helper()
				if !got.Complete {
					t.Fatalf("expected complete microstructure features with default config: %#v", got)
				}
				assertDecimal(t, got.BuyTradeQuantity, decimal.RequireFromString("1"))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := features.ComputeMicrostructureFeatures(tt.snapshot, tt.trades, tt.cfg)
			if err != nil {
				t.Fatalf("compute microstructure features: %v", err)
			}
			tt.assertions(t, got)
		})
	}
}

func TestComputeMicrostructureFeaturesRejectsInvalidInputTableDriven(t *testing.T) {
	now := time.Date(2026, 6, 11, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name     string
		snapshot marketdata.OrderbookSnapshot
		trades   []marketdata.PublicTrade
		cfg      features.MicrostructureFeatureConfig
		code     string
	}{
		{
			name:     "rejects non positive liquidity levels",
			snapshot: testOrderbookSnapshot(now),
			cfg: features.MicrostructureFeatureConfig{
				LiquidityLevels: -1,
				TradeWindow:     time.Minute,
			},
			code: "must_be_positive",
		},
		{
			name:     "rejects non positive trade window",
			snapshot: testOrderbookSnapshot(now),
			cfg: features.MicrostructureFeatureConfig{
				LiquidityLevels: 1,
				TradeWindow:     -time.Second,
			},
			code: "must_be_positive",
		},
		{
			name:     "rejects trade identity mismatch",
			snapshot: testOrderbookSnapshot(now),
			trades: []marketdata.PublicTrade{
				func() marketdata.PublicTrade {
					trade := testMicroTrade("trade-1", "Buy", "1", now)
					trade.Symbol = "ETHUSDT"
					return trade
				}(),
			},
			cfg: features.MicrostructureFeatureConfig{
				LiquidityLevels: 1,
				TradeWindow:     time.Minute,
			},
			code: "identity_mismatch",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := features.ComputeMicrostructureFeatures(tt.snapshot, tt.trades, tt.cfg)
			assertValidationCode(t, err, tt.code)
		})
	}
}

func testOrderbookSnapshot(exchangeTime time.Time) marketdata.OrderbookSnapshot {
	bestBid := decimal.RequireFromString("100")
	bestAsk := decimal.RequireFromString("101")
	spread := bestAsk.Sub(bestBid)
	mid := bestAsk.Add(bestBid).Div(decimal.NewFromInt(2))
	return marketdata.OrderbookSnapshot{
		Exchange: "bybit",
		Category: "linear",
		Symbol:   "BTCUSDT",
		Depth:    3,
		Bids: []marketdata.OrderbookLevel{
			{Price: bestBid, Quantity: decimal.RequireFromString("2")},
			{Price: decimal.RequireFromString("99"), Quantity: decimal.RequireFromString("1")},
			{Price: decimal.RequireFromString("98"), Quantity: decimal.RequireFromString("3")},
		},
		Asks: []marketdata.OrderbookLevel{
			{Price: bestAsk, Quantity: decimal.RequireFromString("3")},
			{Price: decimal.RequireFromString("102"), Quantity: decimal.RequireFromString("1")},
			{Price: decimal.RequireFromString("103"), Quantity: decimal.RequireFromString("2")},
		},
		BestBid:            bestBid,
		BestAsk:            bestAsk,
		Spread:             spread,
		SpreadBPS:          spread.Div(mid).Mul(decimal.NewFromInt(10000)),
		UpdateID:           100,
		Sequence:           200,
		ExchangeTime:       exchangeTime,
		MatchingEngineTime: exchangeTime.Add(-10 * time.Millisecond),
		CreatedAt:          exchangeTime.Add(100 * time.Millisecond),
	}
}

func testMicroTrade(tradeID, side, quantity string, tradeTime time.Time) marketdata.PublicTrade {
	return marketdata.PublicTrade{
		Exchange:  "bybit",
		Category:  "linear",
		Symbol:    "BTCUSDT",
		TradeID:   tradeID,
		Side:      side,
		Price:     decimal.RequireFromString("100"),
		Quantity:  decimal.RequireFromString(quantity),
		TradeTime: tradeTime,
		Sequence:  100,
	}
}
