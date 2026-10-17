package strategy_test

import (
	"context"
	"errors"
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/VersoIt/Inquisitor/internal/features"
	"github.com/VersoIt/Inquisitor/internal/hypothesis"
	"github.com/VersoIt/Inquisitor/internal/marketdata"
	"github.com/VersoIt/Inquisitor/internal/regime"
	"github.com/VersoIt/Inquisitor/internal/strategy"
	"github.com/shopspring/decimal"
)

func meanReversionConfig() strategy.MeanReversionConfig {
	return strategy.MeanReversionConfig{SelectionPolicy: selectionPolicy(), EntryZoneFraction: decimal.RequireFromString("0.2"), StopBufferFraction: decimal.RequireFromString("0.1"), MinRewardRisk: decimal.RequireFromString("1.5")}
}

func breakoutConfig() strategy.BreakoutConfig {
	return strategy.BreakoutConfig{SelectionPolicy: selectionPolicy(), MaxCompression: 0.75, ConfirmationFraction: decimal.RequireFromString("0.05"), MaxExtensionFraction: decimal.RequireFromString("0.25"), StopInsetFraction: decimal.RequireFromString("0.25"), TakeProfitR: decimal.NewFromInt(2), MinVolumeMultiple: decimal.NewFromInt(2)}
}

func rangeMarket() strategy.MarketContext {
	m := trendMarket()
	m.Approval.StrategyName = strategy.NameMeanReversion
	m.Regime.Regime, m.Regime.CandidateRegime = regime.RegimeRange, regime.RegimeRange
	m.Previous.Open, m.Previous.High, m.Previous.Low, m.Previous.Close = decimal.NewFromInt(100), decimal.NewFromInt(108), decimal.NewFromInt(91), decimal.NewFromInt(100)
	m.Current.Open, m.Current.High, m.Current.Low, m.Current.Close = decimal.NewFromInt(91), decimal.NewFromInt(95), decimal.NewFromInt(90), decimal.NewFromInt(92)
	m.EntryPrice, m.Current.Volume = m.Current.Close, decimal.NewFromInt(30)
	p := m.Previous
	m.PriorPrice = features.PriceFeatures{Exchange: p.Exchange, Category: p.Category, Symbol: p.Symbol, Interval: p.Interval, OpenTime: p.OpenTime, CloseTime: p.CloseTime, Complete: true, RollingLow: decimal.NewFromInt(90), RollingHigh: decimal.NewFromInt(110)}
	m.PriorVolatility = m.Volatility
	m.PriorVolatility.OpenTime, m.PriorVolatility.CloseTime = p.OpenTime, p.CloseTime
	m.PriorVolatility.VolatilityCompression = 0.5
	m.PriorVolume = features.VolumeFeatures{Exchange: p.Exchange, Category: p.Category, Symbol: p.Symbol, Interval: p.Interval, OpenTime: p.OpenTime, CloseTime: p.CloseTime, Complete: true, VolumeMovingAverage: decimal.NewFromInt(10)}
	return m
}

func shortRangeMarket() strategy.MarketContext {
	m := rangeMarket()
	m.AllowShort = true
	m.Current.Open, m.Current.High, m.Current.Low, m.Current.Close = decimal.NewFromInt(109), decimal.NewFromInt(110), decimal.NewFromInt(105), decimal.NewFromInt(108)
	m.EntryPrice = m.Current.Close
	return m
}

func breakoutMarket() strategy.MarketContext {
	m := rangeMarket()
	m.Approval.StrategyName = strategy.NameBreakout
	m.Regime.Regime, m.Regime.CandidateRegime = regime.RegimeBreakoutSetup, regime.RegimeBreakoutSetup
	m.Current.Open, m.Current.High, m.Current.Low, m.Current.Close = decimal.NewFromInt(108), decimal.NewFromInt(115), decimal.NewFromInt(107), decimal.NewFromInt(114)
	m.EntryPrice = m.Current.Close
	return m
}

func shortBreakoutMarket() strategy.MarketContext {
	m := breakoutMarket()
	m.AllowShort = true
	m.Current.Open, m.Current.High, m.Current.Low, m.Current.Close = decimal.NewFromInt(92), decimal.NewFromInt(93), decimal.NewFromInt(85), decimal.NewFromInt(86)
	m.EntryPrice = m.Current.Close
	return m
}

func mustMeanReversion(t *testing.T, cfg strategy.MeanReversionConfig) *strategy.MeanReversionStrategy {
	t.Helper()
	s, err := strategy.NewMeanReversionStrategy(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func mustBreakout(t *testing.T, cfg strategy.BreakoutConfig) *strategy.BreakoutStrategy {
	t.Helper()
	s, err := strategy.NewBreakoutStrategy(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestRangeStrategiesGenerateExactSignalsThroughSelector(t *testing.T) {
	mean, breakout := mustMeanReversion(t, meanReversionConfig()), mustBreakout(t, breakoutConfig())
	selector, err := strategy.NewSelector(selectionPolicy(), mean, breakout)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name                 string
		market               func() strategy.MarketContext
		side                 strategy.Side
		stop, target, reason string
	}{
		{"range long", rangeMarket, strategy.SideLong, "88", "100", "range_low_rebound_confirmed"},
		{"range short", shortRangeMarket, strategy.SideShort, "112", "100", "range_high_rebound_confirmed"},
		{"breakout long", breakoutMarket, strategy.SideLong, "105", "132", "breakout_up_confirmed"},
		{"breakout short", shortBreakoutMarket, strategy.SideShort, "95", "68", "breakout_down_confirmed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, before := tt.market(), tt.market()
			got, err := selector.GenerateSignal(context.Background(), m)
			if err != nil || got.Trade == nil {
				t.Fatalf("got %+v, %v", got, err)
			}
			if !reflect.DeepEqual(m, before) {
				t.Fatal("generation mutated inputs")
			}
			trade := *got.Trade
			wantType := strategy.SignalBuy
			if tt.side == strategy.SideShort {
				wantType = strategy.SignalSell
			}
			if got.Type != wantType || trade.Side != tt.side || got.Reason != tt.reason || trade.Reason != tt.reason || got.StrategyName != m.Approval.StrategyName {
				t.Fatalf("unexpected proposal: %+v / %+v", got, trade)
			}
			if !trade.StopLoss.Equal(decimal.RequireFromString(tt.stop)) || !trade.TakeProfit.Equal(decimal.RequireFromString(tt.target)) {
				t.Fatalf("prices: stop=%s target=%s", trade.StopLoss, trade.TakeProfit)
			}
			if err := strategy.ValidateTradeSignal(trade); err != nil {
				t.Fatal(err)
			}
			intent, err := trade.RiskIntent("intent-range-1")
			if err != nil {
				t.Fatal(err)
			}
			if !intent.EntryPrice.Equal(m.EntryPrice) || !intent.StopLoss.Equal(trade.StopLoss) || !intent.TakeProfit.Equal(trade.TakeProfit) || !intent.Leverage.Equal(decimal.NewFromInt(1)) || !intent.HypothesisMaxRiskPct.Equal(m.Approval.MaxRiskPct) || !intent.HypothesisApproved || intent.HypothesisMinConfidence != m.Approval.MinConfidence {
				t.Fatalf("intent lost values: %+v", intent)
			}
			if got.SignalID != trade.SignalID || got.HypothesisID != trade.HypothesisID || got.HypothesisVersion != trade.HypothesisVersion || got.Symbol != trade.Symbol || got.Interval != m.Current.Interval || !got.CreatedAt.Equal(m.Now) || !trade.ObservedAt.Equal(m.ObservedAt) {
				t.Fatalf("identity lost: %+v / %+v", got, trade)
			}
		})
	}
}

func assertAbstention(t *testing.T, s strategy.Strategy, m strategy.MarketContext, reason string) {
	t.Helper()
	got, err := s.GenerateSignal(context.Background(), m)
	if err != nil || got.Type != strategy.SignalNoTrade || got.Trade != nil || got.Reason != reason {
		t.Fatalf("got %+v, %v; want NO_TRADE/%s", got, err, reason)
	}
}

func TestMeanReversionRejectsBrokenRangesAndWeakEntries(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*strategy.MarketContext, *strategy.MeanReversionConfig)
		reason string
	}{
		{"wrong regime", func(m *strategy.MarketContext, _ *strategy.MeanReversionConfig) {
			m.Regime.Regime = regime.RegimeTrendUp
		}, "range_regime_required"},
		{"upper break", func(m *strategy.MarketContext, _ *strategy.MeanReversionConfig) {
			m.Current.High = decimal.NewFromInt(111)
		}, "range_broken"},
		{"lower break", func(m *strategy.MarketContext, _ *strategy.MeanReversionConfig) {
			m.Current.Low = decimal.NewFromInt(89)
		}, "range_broken"},
		{"middle entry", func(m *strategy.MarketContext, _ *strategy.MeanReversionConfig) {
			m.Current.High, m.Current.Close, m.EntryPrice = decimal.NewFromInt(101), decimal.NewFromInt(100), decimal.NewFromInt(100)
		}, "range_entry_not_confirmed"},
		{"falling near low", func(m *strategy.MarketContext, _ *strategy.MeanReversionConfig) {
			m.Current.Open = decimal.NewFromInt(93)
		}, "range_entry_not_confirmed"},
		{"doji", func(m *strategy.MarketContext, _ *strategy.MeanReversionConfig) { m.Current.Open = m.Current.Close }, "range_entry_not_confirmed"},
		{"rising near high", func(m *strategy.MarketContext, _ *strategy.MeanReversionConfig) {
			*m = shortRangeMarket()
			m.Current.Open = decimal.NewFromInt(107)
		}, "range_entry_not_confirmed"},
		{"quote outside lower range", func(m *strategy.MarketContext, _ *strategy.MeanReversionConfig) {
			m.EntryPrice = decimal.NewFromInt(89)
		}, "range_entry_not_confirmed"},
		{"quote left entry zone", func(m *strategy.MarketContext, _ *strategy.MeanReversionConfig) {
			m.EntryPrice = decimal.NewFromInt(95)
		}, "range_entry_not_confirmed"},
		{"short quote left entry zone", func(m *strategy.MarketContext, _ *strategy.MeanReversionConfig) {
			*m = shortRangeMarket()
			m.EntryPrice = decimal.NewFromInt(105)
		}, "range_entry_not_confirmed"},
		{"insufficient reward", func(_ *strategy.MarketContext, c *strategy.MeanReversionConfig) {
			c.MinRewardRisk = decimal.RequireFromString("2.01")
		}, "range_reward_risk_insufficient"},
		{"short disabled", func(m *strategy.MarketContext, _ *strategy.MeanReversionConfig) {
			*m = shortRangeMarket()
			m.AllowShort = false
		}, "shorts_disabled"},
		{"short prohibited by hypothesis", func(m *strategy.MarketContext, _ *strategy.MeanReversionConfig) {
			*m = shortRangeMarket()
			m.Approval.Direction = hypothesis.DirectionLong
		}, "hypothesis_direction_mismatch"},
		{"long prohibited by hypothesis", func(m *strategy.MarketContext, _ *strategy.MeanReversionConfig) {
			m.Approval.Direction = hypothesis.DirectionShort
		}, "hypothesis_direction_mismatch"},
		{"nonpositive stop", func(m *strategy.MarketContext, c *strategy.MeanReversionConfig) {
			m.PriorPrice.RollingLow, m.Previous.Low, m.Current.Low, m.Current.Open, m.Current.Close, m.EntryPrice = decimal.NewFromInt(1), decimal.NewFromInt(1), decimal.NewFromInt(1), decimal.NewFromInt(2), decimal.NewFromInt(3), decimal.NewFromInt(3)
			c.StopBufferFraction = decimal.NewFromInt(1)
		}, "range_price_geometry_invalid"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, cfg := rangeMarket(), meanReversionConfig()
			tt.mutate(&m, &cfg)
			assertAbstention(t, mustMeanReversion(t, cfg), m, tt.reason)
		})
	}
}

func TestBreakoutRejectsUnconfirmedAndExhaustedMoves(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*strategy.MarketContext, *strategy.BreakoutConfig)
		reason string
	}{
		{"wrong regime", func(m *strategy.MarketContext, _ *strategy.BreakoutConfig) { m.Regime.Regime = regime.RegimeRange }, "breakout_regime_required"},
		{"no compression", func(m *strategy.MarketContext, _ *strategy.BreakoutConfig) {
			m.PriorVolatility.VolatilityCompression = 0.76
		}, "compression_not_confirmed"},
		{"negative compression", func(m *strategy.MarketContext, _ *strategy.BreakoutConfig) {
			m.PriorVolatility.VolatilityCompression = -1
		}, "compression_invalid"},
		{"NaN compression", func(m *strategy.MarketContext, _ *strategy.BreakoutConfig) {
			m.PriorVolatility.VolatilityCompression = math.NaN()
		}, "compression_invalid"},
		{"infinite compression", func(m *strategy.MarketContext, _ *strategy.BreakoutConfig) {
			m.PriorVolatility.VolatilityCompression = math.Inf(1)
		}, "compression_invalid"},
		{"volume below minimum", func(m *strategy.MarketContext, _ *strategy.BreakoutConfig) {
			m.Current.Volume = decimal.RequireFromString("19.99999999")
		}, "breakout_volume_insufficient"},
		{"zero volume baseline", func(m *strategy.MarketContext, _ *strategy.BreakoutConfig) {
			m.PriorVolume.VolumeMovingAverage = decimal.Zero
		}, "volume_baseline_invalid"},
		{"negative volume baseline", func(m *strategy.MarketContext, _ *strategy.BreakoutConfig) {
			m.PriorVolume.VolumeMovingAverage = decimal.NewFromInt(-1)
		}, "volume_baseline_invalid"},
		{"gap above range", func(m *strategy.MarketContext, _ *strategy.BreakoutConfig) { m.Current.Open = decimal.NewFromInt(112) }, "breakout_already_in_progress"},
		{"gap below range", func(m *strategy.MarketContext, _ *strategy.BreakoutConfig) {
			*m = shortBreakoutMarket()
			m.Current.Open = decimal.NewFromInt(88)
		}, "breakout_already_in_progress"},
		{"both range boundaries swept", func(m *strategy.MarketContext, _ *strategy.BreakoutConfig) { m.Current.Low = decimal.NewFromInt(89) }, "breakout_whipsaw"},
		{"wick only", func(m *strategy.MarketContext, _ *strategy.BreakoutConfig) {
			m.Current.Close, m.EntryPrice = decimal.NewFromInt(109), decimal.NewFromInt(109)
		}, "breakout_not_confirmed"},
		{"exact confirmation threshold", func(m *strategy.MarketContext, _ *strategy.BreakoutConfig) {
			m.Current.Close, m.EntryPrice = decimal.NewFromInt(111), decimal.NewFromInt(111)
		}, "breakout_not_confirmed"},
		{"short exact confirmation threshold", func(m *strategy.MarketContext, _ *strategy.BreakoutConfig) {
			*m = shortBreakoutMarket()
			m.Current.Close, m.EntryPrice = decimal.NewFromInt(89), decimal.NewFromInt(89)
		}, "breakout_not_confirmed"},
		{"quote returned inside range", func(m *strategy.MarketContext, _ *strategy.BreakoutConfig) { m.EntryPrice = decimal.NewFromInt(110) }, "breakout_not_confirmed"},
		{"exhausted quote", func(m *strategy.MarketContext, _ *strategy.BreakoutConfig) {
			m.EntryPrice = decimal.RequireFromString("115.00000001")
		}, "breakout_overextended"},
		{"exhausted close", func(m *strategy.MarketContext, _ *strategy.BreakoutConfig) {
			m.Current.High, m.Current.Close = decimal.NewFromInt(117), decimal.NewFromInt(116)
		}, "breakout_overextended"},
		{"short exhausted quote", func(m *strategy.MarketContext, _ *strategy.BreakoutConfig) {
			*m = shortBreakoutMarket()
			m.EntryPrice = decimal.RequireFromString("84.99999999")
		}, "breakout_overextended"},
		{"short exhausted close", func(m *strategy.MarketContext, _ *strategy.BreakoutConfig) {
			*m = shortBreakoutMarket()
			m.Current.Low, m.Current.Close = decimal.NewFromInt(83), decimal.NewFromInt(84)
		}, "breakout_overextended"},
		{"short disabled", func(m *strategy.MarketContext, _ *strategy.BreakoutConfig) {
			*m = shortBreakoutMarket()
			m.AllowShort = false
		}, "shorts_disabled"},
		{"short prohibited by hypothesis", func(m *strategy.MarketContext, _ *strategy.BreakoutConfig) {
			*m = shortBreakoutMarket()
			m.Approval.Direction = hypothesis.DirectionLong
		}, "hypothesis_direction_mismatch"},
		{"long prohibited by hypothesis", func(m *strategy.MarketContext, _ *strategy.BreakoutConfig) {
			m.Approval.Direction = hypothesis.DirectionShort
		}, "hypothesis_direction_mismatch"},
		{"nonpositive short target", func(m *strategy.MarketContext, c *strategy.BreakoutConfig) {
			*m = shortBreakoutMarket()
			c.TakeProfitR = decimal.NewFromInt(100)
		}, "breakout_price_geometry_invalid"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, cfg := breakoutMarket(), breakoutConfig()
			tt.mutate(&m, &cfg)
			assertAbstention(t, mustBreakout(t, cfg), m, tt.reason)
		})
	}
}

func TestRangeStrategiesRejectInvalidSharedInputs(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*strategy.MarketContext)
		reason string
	}{
		{"kill switch", func(m *strategy.MarketContext) { m.KillSwitchActive = true }, "kill_switch_active"},
		{"disabled trading", func(m *strategy.MarketContext) { m.TradingEnabled = false }, "trading_disabled"},
		{"disabled strategy", func(m *strategy.MarketContext) { m.Approval.Enabled = false }, "strategy_disabled"},
		{"decayed edge", func(m *strategy.MarketContext) { m.Approval.EdgeDecayed = true }, "edge_decayed"},
		{"wrong approval mode", func(m *strategy.MarketContext) { m.Approval.Mode = "LIVE" }, "hypothesis_not_approved_for_mode"},
		{"stale market", func(m *strategy.MarketContext) { m.Now = m.Now.Add(time.Minute) }, "market_data_stale"},
		{"low confidence", func(m *strategy.MarketContext) { m.Regime.Confidence = 69 }, "low_confidence"},
		{"strategy mismatch", func(m *strategy.MarketContext) { m.Approval.StrategyName = strategy.NameTrend }, "hypothesis_strategy_mismatch"},
		{"incomplete range", func(m *strategy.MarketContext) { m.PriorPrice.Complete = false }, "features_incomplete"},
		{"contradictory range completeness", func(m *strategy.MarketContext) { m.PriorPrice.MissingReasons = []string{"window"} }, "features_incomplete"},
		{"current candle range leaks into baseline", func(m *strategy.MarketContext) {
			m.PriorPrice.OpenTime, m.PriorPrice.CloseTime = m.Current.OpenTime, m.Current.CloseTime
		}, "feature_scope_or_time_mismatch"},
		{"old range", func(m *strategy.MarketContext) { m.PriorPrice.CloseTime = m.PriorPrice.CloseTime.Add(-time.Minute) }, "feature_scope_or_time_mismatch"},
		{"wrong range symbol", func(m *strategy.MarketContext) { m.PriorPrice.Symbol = "ETHUSDT" }, "feature_scope_or_time_mismatch"},
		{"wrong range interval", func(m *strategy.MarketContext) { m.PriorPrice.Interval = "5" }, "feature_scope_or_time_mismatch"},
		{"zero range", func(m *strategy.MarketContext) { m.PriorPrice.RollingHigh = m.PriorPrice.RollingLow }, "prior_range_invalid"},
		{"inverted range", func(m *strategy.MarketContext) {
			m.PriorPrice.RollingHigh, m.PriorPrice.RollingLow = m.PriorPrice.RollingLow, m.PriorPrice.RollingHigh
		}, "prior_range_invalid"},
		{"nonpositive lower bound", func(m *strategy.MarketContext) { m.PriorPrice.RollingLow = decimal.Zero }, "prior_range_invalid"},
		{"range excludes previous high", func(m *strategy.MarketContext) { m.PriorPrice.RollingHigh = decimal.NewFromInt(107) }, "prior_range_invalid"},
		{"range excludes previous low", func(m *strategy.MarketContext) { m.PriorPrice.RollingLow = decimal.NewFromInt(92) }, "prior_range_invalid"},
		{"zero entry", func(m *strategy.MarketContext) { m.EntryPrice = decimal.Zero }, "entry_price_invalid"},
		{"open candle", func(m *strategy.MarketContext) { m.Current.IsClosed = false }, "candles_invalid"},
		{"broken candle", func(m *strategy.MarketContext) { m.Current.High = decimal.Zero }, "candles_invalid"},
		{"history gap", func(m *strategy.MarketContext) {
			m.Previous.OpenTime = m.Previous.OpenTime.Add(-time.Minute)
			m.Previous.CloseTime = m.Previous.CloseTime.Add(-time.Minute)
		}, "candle_scope_or_time_mismatch"},
	}
	for _, factory := range []struct {
		name           string
		market         func() strategy.MarketContext
		implementation strategy.Strategy
	}{
		{strategy.NameMeanReversion, rangeMarket, mustMeanReversion(t, meanReversionConfig())},
		{strategy.NameBreakout, breakoutMarket, mustBreakout(t, breakoutConfig())},
	} {
		for _, tt := range tests {
			t.Run(factory.name+"/"+tt.name, func(t *testing.T) {
				m := factory.market()
				tt.mutate(&m)
				assertAbstention(t, factory.implementation, m, tt.reason)
			})
		}
	}
}

func TestBreakoutRejectsUnalignedConfirmationFeatures(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*strategy.MarketContext)
		reason string
	}{
		{"incomplete compression", func(m *strategy.MarketContext) { m.PriorVolatility.Complete = false }, "features_incomplete"},
		{"compression missing reasons", func(m *strategy.MarketContext) { m.PriorVolatility.MissingReasons = []string{"window"} }, "features_incomplete"},
		{"future compression", func(m *strategy.MarketContext) { m.PriorVolatility.CloseTime = m.Current.CloseTime }, "feature_scope_or_time_mismatch"},
		{"wrong compression symbol", func(m *strategy.MarketContext) { m.PriorVolatility.Symbol = "ETHUSDT" }, "feature_scope_or_time_mismatch"},
		{"incomplete volume", func(m *strategy.MarketContext) { m.PriorVolume.Complete = false }, "features_incomplete"},
		{"volume missing reasons", func(m *strategy.MarketContext) { m.PriorVolume.MissingReasons = []string{"window"} }, "features_incomplete"},
		{"future volume baseline", func(m *strategy.MarketContext) { m.PriorVolume.CloseTime = m.Current.CloseTime }, "feature_scope_or_time_mismatch"},
		{"wrong volume symbol", func(m *strategy.MarketContext) { m.PriorVolume.Symbol = "ETHUSDT" }, "feature_scope_or_time_mismatch"},
	}
	s := mustBreakout(t, breakoutConfig())
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { m := breakoutMarket(); tt.mutate(&m); assertAbstention(t, s, m, tt.reason) })
	}
}

func TestMeanReversionThresholdBoundaries(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*strategy.MarketContext, *strategy.MeanReversionConfig)
	}{
		{"reward risk exactly at minimum", func(_ *strategy.MarketContext, c *strategy.MeanReversionConfig) {
			c.MinRewardRisk = decimal.NewFromInt(2)
		}},
		{"lower zone edge inclusive", func(m *strategy.MarketContext, c *strategy.MeanReversionConfig) {
			m.Current.Close, m.EntryPrice = decimal.NewFromInt(94), decimal.NewFromInt(94)
			c.MinRewardRisk = decimal.NewFromInt(1)
		}},
		{"upper zone edge inclusive", func(m *strategy.MarketContext, c *strategy.MeanReversionConfig) {
			*m = shortRangeMarket()
			m.Current.Close, m.EntryPrice = decimal.NewFromInt(106), decimal.NewFromInt(106)
			c.MinRewardRisk = decimal.NewFromInt(1)
		}},
		{"quote at range low", func(m *strategy.MarketContext, _ *strategy.MeanReversionConfig) {
			m.EntryPrice = m.PriorPrice.RollingLow
		}},
		{"quote at range high", func(m *strategy.MarketContext, _ *strategy.MeanReversionConfig) {
			*m = shortRangeMarket()
			m.EntryPrice = m.PriorPrice.RollingHigh
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, cfg := rangeMarket(), meanReversionConfig()
			tt.mutate(&m, &cfg)
			got, err := mustMeanReversion(t, cfg).GenerateSignal(context.Background(), m)
			if err != nil || got.Trade == nil {
				t.Fatalf("boundary should generate proposal: %+v, %v", got, err)
			}
			if err := strategy.ValidateTradeSignal(*got.Trade); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestBreakoutThresholdBoundariesAndOptionalVolume(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*strategy.MarketContext, *strategy.BreakoutConfig)
	}{
		{"compression exactly at maximum", func(m *strategy.MarketContext, c *strategy.BreakoutConfig) {
			m.PriorVolatility.VolatilityCompression = c.MaxCompression
		}},
		{"zero compression", func(m *strategy.MarketContext, _ *strategy.BreakoutConfig) {
			m.PriorVolatility.VolatilityCompression = 0
		}},
		{"volume exactly at minimum", func(m *strategy.MarketContext, c *strategy.BreakoutConfig) {
			m.Current.Volume = m.PriorVolume.VolumeMovingAverage.Mul(c.MinVolumeMultiple)
		}},
		{"maximum extension inclusive", func(m *strategy.MarketContext, _ *strategy.BreakoutConfig) {
			m.Current.Close, m.EntryPrice = decimal.NewFromInt(115), decimal.NewFromInt(115)
		}},
		{"short maximum extension inclusive", func(m *strategy.MarketContext, _ *strategy.BreakoutConfig) {
			*m = shortBreakoutMarket()
			m.Current.Close, m.EntryPrice = decimal.NewFromInt(85), decimal.NewFromInt(85)
		}},
		{"minimal confirmed advance", func(m *strategy.MarketContext, _ *strategy.BreakoutConfig) {
			m.Current.Close, m.EntryPrice = decimal.RequireFromString("111.00000001"), decimal.RequireFromString("111.00000001")
		}},
		{"minimal confirmed decline", func(m *strategy.MarketContext, _ *strategy.BreakoutConfig) {
			*m = shortBreakoutMarket()
			m.Current.Close, m.EntryPrice = decimal.RequireFromString("88.99999999"), decimal.RequireFromString("88.99999999")
		}},
		{"open at range high", func(m *strategy.MarketContext, _ *strategy.BreakoutConfig) { m.Current.Open = m.PriorPrice.RollingHigh }},
		{"open at range low", func(m *strategy.MarketContext, _ *strategy.BreakoutConfig) {
			*m = shortBreakoutMarket()
			m.Current.Open = m.PriorPrice.RollingLow
		}},
		{"volume confirmation disabled", func(m *strategy.MarketContext, c *strategy.BreakoutConfig) {
			c.MinVolumeMultiple = decimal.Zero
			m.PriorVolume = features.VolumeFeatures{}
			m.Current.Volume = decimal.Zero
		}},
		{"zero confirmation margin", func(m *strategy.MarketContext, c *strategy.BreakoutConfig) {
			c.ConfirmationFraction = decimal.Zero
			m.Current.Close, m.EntryPrice = decimal.RequireFromString("110.00000001"), decimal.RequireFromString("110.00000001")
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, cfg := breakoutMarket(), breakoutConfig()
			tt.mutate(&m, &cfg)
			got, err := mustBreakout(t, cfg).GenerateSignal(context.Background(), m)
			if err != nil || got.Trade == nil {
				t.Fatalf("boundary should generate proposal: %+v, %v", got, err)
			}
			if err := strategy.ValidateTradeSignal(*got.Trade); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestRangeStrategyConfigurationAndCancellation(t *testing.T) {
	for _, mutate := range []func(*strategy.MeanReversionConfig){
		func(c *strategy.MeanReversionConfig) { c.SelectionPolicy = strategy.SelectionPolicy{} },
		func(c *strategy.MeanReversionConfig) { c.EntryZoneFraction = decimal.Zero },
		func(c *strategy.MeanReversionConfig) { c.EntryZoneFraction = decimal.RequireFromString("0.5") },
		func(c *strategy.MeanReversionConfig) { c.StopBufferFraction = decimal.Zero },
		func(c *strategy.MeanReversionConfig) { c.StopBufferFraction = decimal.RequireFromString("1.01") },
		func(c *strategy.MeanReversionConfig) { c.MinRewardRisk = decimal.Zero },
	} {
		cfg := meanReversionConfig()
		mutate(&cfg)
		if _, err := strategy.NewMeanReversionStrategy(cfg); err == nil {
			t.Fatalf("accepted invalid mean config %+v", cfg)
		}
	}
	for _, mutate := range []func(*strategy.BreakoutConfig){
		func(c *strategy.BreakoutConfig) { c.SelectionPolicy = strategy.SelectionPolicy{} },
		func(c *strategy.BreakoutConfig) { c.MaxCompression = math.NaN() },
		func(c *strategy.BreakoutConfig) { c.MaxCompression = math.Inf(1) },
		func(c *strategy.BreakoutConfig) { c.MaxCompression = 0 },
		func(c *strategy.BreakoutConfig) { c.MaxCompression = 1.01 },
		func(c *strategy.BreakoutConfig) { c.ConfirmationFraction = decimal.NewFromInt(-1) },
		func(c *strategy.BreakoutConfig) { c.MaxExtensionFraction = c.ConfirmationFraction },
		func(c *strategy.BreakoutConfig) { c.MaxExtensionFraction = decimal.NewFromInt(2) },
		func(c *strategy.BreakoutConfig) { c.StopInsetFraction = decimal.Zero },
		func(c *strategy.BreakoutConfig) { c.StopInsetFraction = decimal.RequireFromString("0.51") },
		func(c *strategy.BreakoutConfig) { c.TakeProfitR = decimal.Zero },
		func(c *strategy.BreakoutConfig) { c.MinVolumeMultiple = decimal.NewFromInt(-1) },
	} {
		cfg := breakoutConfig()
		mutate(&cfg)
		if _, err := strategy.NewBreakoutStrategy(cfg); err == nil {
			t.Fatalf("accepted invalid breakout config %+v", cfg)
		}
	}
	var nilMean *strategy.MeanReversionStrategy
	var nilBreakout *strategy.BreakoutStrategy
	for _, s := range []strategy.Strategy{nilMean, nilBreakout, &strategy.MeanReversionStrategy{}, &strategy.BreakoutStrategy{}} {
		if _, err := s.GenerateSignal(context.Background(), rangeMarket()); err == nil {
			t.Fatal("unconfigured strategy accepted")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, s := range []strategy.Strategy{mustMeanReversion(t, meanReversionConfig()), mustBreakout(t, breakoutConfig())} {
		if got, err := s.GenerateSignal(ctx, rangeMarket()); !errors.Is(err, context.Canceled) || got.Trade != nil {
			t.Fatalf("cancelled generation: %+v, %v", got, err)
		}
	}
}

func TestRangeStrategiesPreserveGeometryAcrossPriceScales(t *testing.T) {
	for _, scaleText := range []string{"0.00000001", "0.01", "1", "10000", "100000000"} {
		for _, factory := range []struct {
			name   string
			market func() strategy.MarketContext
			s      strategy.Strategy
		}{
			{"range long", rangeMarket, mustMeanReversion(t, meanReversionConfig())}, {"range short", shortRangeMarket, mustMeanReversion(t, meanReversionConfig())},
			{"breakout long", breakoutMarket, mustBreakout(t, breakoutConfig())}, {"breakout short", shortBreakoutMarket, mustBreakout(t, breakoutConfig())},
		} {
			t.Run(factory.name+"/"+scaleText, func(t *testing.T) {
				m := factory.market()
				scale := decimal.RequireFromString(scaleText)
				for _, c := range []*marketdata.Candle{&m.Current, &m.Previous} {
					c.Open, c.High, c.Low, c.Close = c.Open.Mul(scale), c.High.Mul(scale), c.Low.Mul(scale), c.Close.Mul(scale)
				}
				m.EntryPrice, m.PriorPrice.RollingLow, m.PriorPrice.RollingHigh = m.EntryPrice.Mul(scale), m.PriorPrice.RollingLow.Mul(scale), m.PriorPrice.RollingHigh.Mul(scale)
				got, err := factory.s.GenerateSignal(context.Background(), m)
				if err != nil || got.Trade == nil {
					t.Fatalf("scaled proposal: %+v, %v", got, err)
				}
				if err := strategy.ValidateTradeSignal(*got.Trade); err != nil {
					t.Fatal(err)
				}
				if factory.s.Name() == strategy.NameBreakout && !got.Trade.TakeProfit.Sub(got.Trade.EntryPrice).Abs().Equal(got.Trade.EntryPrice.Sub(got.Trade.StopLoss).Abs().Mul(breakoutConfig().TakeProfitR)) {
					t.Fatal("R multiple changed with price scale")
				}
			})
		}
	}
}
