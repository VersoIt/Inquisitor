package research

import (
	"github.com/shopspring/decimal"

	appfeatures "github.com/VersoIt/Inquisitor/internal/app/features"
	domainfeatures "github.com/VersoIt/Inquisitor/internal/features"
	domainhypothesis "github.com/VersoIt/Inquisitor/internal/hypothesis"
)

func hypothesisFeatureSnapshot(featureSet appfeatures.FeatureSet) domainhypothesis.FeatureSnapshot {
	values := map[string]domainhypothesis.FeatureValue{}
	if price := latestPriceFeature(featureSet.Price); price != nil {
		addDecimalFeature(values, "price.return", price.Return, price.Complete, price.MissingReasons)
		addFloatFeature(values, "price.log_return", price.LogReturn, price.Complete, price.MissingReasons)
		addDecimalFeature(values, "price.rolling_return", price.RollingReturn, price.Complete, price.MissingReasons)
		addDecimalFeature(values, "price.rolling_high", price.RollingHigh, price.Complete, price.MissingReasons)
		addDecimalFeature(values, "price.rolling_low", price.RollingLow, price.Complete, price.MissingReasons)
		addDecimalFeature(values, "price.candle_body_pct", price.CandleBodyPct, price.Complete, price.MissingReasons)
		addDecimalFeature(values, "price.upper_wick_pct", price.UpperWickPct, price.Complete, price.MissingReasons)
		addDecimalFeature(values, "price.lower_wick_pct", price.LowerWickPct, price.Complete, price.MissingReasons)
		addDecimalFeature(values, "price.close_position_in_range", price.ClosePositionInRange, price.Complete, price.MissingReasons)
	}
	if trend := latestTrendFeature(featureSet.Trend); trend != nil {
		addDecimalFeature(values, "trend.ma20", trend.MA20, trend.Complete, trend.MissingReasons)
		addDecimalFeature(values, "trend.ma50", trend.MA50, trend.Complete, trend.MissingReasons)
		addDecimalFeature(values, "trend.ma200", trend.MA200, trend.Complete, trend.MissingReasons)
		addDecimalFeature(values, "trend.ema20", trend.EMA20, trend.Complete, trend.MissingReasons)
		addDecimalFeature(values, "trend.ema50", trend.EMA50, trend.Complete, trend.MissingReasons)
		addDecimalFeature(values, "trend.ma50_slope", trend.MA50Slope, trend.Complete, trend.MissingReasons)
		addDecimalFeature(values, "trend.ma200_slope", trend.MA200Slope, trend.Complete, trend.MissingReasons)
		addDecimalFeature(values, "trend.adx", trend.ADX, trend.Complete, trend.MissingReasons)
		addIntFeature(values, "trend.higher_high_count", trend.HigherHighCount, trend.Complete, trend.MissingReasons)
		addIntFeature(values, "trend.higher_low_count", trend.HigherLowCount, trend.Complete, trend.MissingReasons)
		addIntFeature(values, "trend.lower_high_count", trend.LowerHighCount, trend.Complete, trend.MissingReasons)
		addIntFeature(values, "trend.lower_low_count", trend.LowerLowCount, trend.Complete, trend.MissingReasons)
	}
	if volatility := latestVolatilityFeature(featureSet.Volatility); volatility != nil {
		addDecimalFeature(values, "volatility.atr", volatility.ATR, volatility.Complete, volatility.MissingReasons)
		addDecimalFeature(values, "volatility.atr_percentage", volatility.ATRPercentage, volatility.Complete, volatility.MissingReasons)
		addFloatFeature(values, "volatility.rolling_volatility", volatility.RollingVolatility, volatility.Complete, volatility.MissingReasons)
		addFloatFeature(values, "volatility.volatility_z_score", volatility.VolatilityZScore, volatility.Complete, volatility.MissingReasons)
		addFloatFeature(values, "volatility.bollinger_middle", volatility.BollingerMiddle, volatility.Complete, volatility.MissingReasons)
		addFloatFeature(values, "volatility.bollinger_upper", volatility.BollingerUpper, volatility.Complete, volatility.MissingReasons)
		addFloatFeature(values, "volatility.bollinger_lower", volatility.BollingerLower, volatility.Complete, volatility.MissingReasons)
		addFloatFeature(values, "volatility.bollinger_width", volatility.BollingerWidth, volatility.Complete, volatility.MissingReasons)
		addFloatFeature(values, "volatility.volatility_compression", volatility.VolatilityCompression, volatility.Complete, volatility.MissingReasons)
	}
	if volume := latestVolumeFeature(featureSet.Volume); volume != nil {
		addDecimalFeature(values, "volume.volume_moving_average", volume.VolumeMovingAverage, volume.Complete, volume.MissingReasons)
		addFloatFeature(values, "volume.volume_z_score", volume.VolumeZScore, volume.Complete, volume.MissingReasons)
		addDecimalFeature(values, "volume.volume_change", volume.VolumeChange, volume.Complete, volume.MissingReasons)
		addDecimalFeature(values, "volume.turnover_change", volume.TurnoverChange, volume.Complete, volume.MissingReasons)
	}
	if microstructure := featureSet.Microstructure; microstructure != nil {
		addDecimalFeature(values, "microstructure.best_bid", microstructure.BestBid, microstructure.Complete, microstructure.MissingReasons)
		addDecimalFeature(values, "microstructure.best_ask", microstructure.BestAsk, microstructure.Complete, microstructure.MissingReasons)
		addDecimalFeature(values, "microstructure.spread", microstructure.Spread, microstructure.Complete, microstructure.MissingReasons)
		addDecimalFeature(values, "microstructure.spread_bps", microstructure.SpreadBPS, microstructure.Complete, microstructure.MissingReasons)
		addDecimalFeature(values, "microstructure.orderbook_imbalance", microstructure.OrderbookImbalance, microstructure.Complete, microstructure.MissingReasons)
		addDecimalFeature(values, "microstructure.bid_liquidity_top_n", microstructure.BidLiquidityTopN, microstructure.Complete, microstructure.MissingReasons)
		addDecimalFeature(values, "microstructure.ask_liquidity_top_n", microstructure.AskLiquidityTopN, microstructure.Complete, microstructure.MissingReasons)
		addDecimalFeature(values, "microstructure.trade_aggressor_imbalance", microstructure.TradeAggressorImbalance, microstructure.Complete, microstructure.MissingReasons)
		addDecimalFeature(values, "microstructure.buy_trade_quantity", microstructure.BuyTradeQuantity, microstructure.Complete, microstructure.MissingReasons)
		addDecimalFeature(values, "microstructure.sell_trade_quantity", microstructure.SellTradeQuantity, microstructure.Complete, microstructure.MissingReasons)
	}

	dataQuality := featureSet.DataQuality
	addInt64Feature(values, "data_quality.data_freshness_ms", dataQuality.DataFreshnessMS, dataQuality.Complete, dataQuality.MissingReasons)
	addIntFeature(values, "data_quality.missing_candle_count", dataQuality.MissingCandleCount, dataQuality.Complete, dataQuality.MissingReasons)
	addBoolFeature(values, "data_quality.websocket_connected", dataQuality.WebSocketConnected, dataQuality.Complete, dataQuality.MissingReasons)
	addBoolFeature(values, "data_quality.orderbook_valid", dataQuality.OrderbookValid, dataQuality.Complete, dataQuality.MissingReasons)
	addDecimalFeature(values, "data_quality.feature_completeness_score", dataQuality.FeatureCompletenessScore, dataQuality.Complete, dataQuality.MissingReasons)

	return domainhypothesis.NewFeatureSnapshot(values)
}

func addDecimalFeature(values map[string]domainhypothesis.FeatureValue, path string, value decimal.Decimal, complete bool, reasons []string) {
	values[path] = domainhypothesis.FeatureValue{Value: value, Complete: complete, MissingReasons: reasons}
}

func addFloatFeature(values map[string]domainhypothesis.FeatureValue, path string, value float64, complete bool, reasons []string) {
	values[path] = domainhypothesis.FeatureValue{Value: decimal.NewFromFloat(value), Complete: complete, MissingReasons: reasons}
}

func addIntFeature(values map[string]domainhypothesis.FeatureValue, path string, value int, complete bool, reasons []string) {
	values[path] = domainhypothesis.FeatureValue{Value: decimal.NewFromInt(int64(value)), Complete: complete, MissingReasons: reasons}
}

func addInt64Feature(values map[string]domainhypothesis.FeatureValue, path string, value int64, complete bool, reasons []string) {
	values[path] = domainhypothesis.FeatureValue{Value: decimal.NewFromInt(value), Complete: complete, MissingReasons: reasons}
}

func addBoolFeature(values map[string]domainhypothesis.FeatureValue, path string, value bool, complete bool, reasons []string) {
	numeric := int64(0)
	if value {
		numeric = 1
	}
	values[path] = domainhypothesis.FeatureValue{Value: decimal.NewFromInt(numeric), Complete: complete, MissingReasons: reasons}
}

func latestPriceFeature(rows []domainfeatures.PriceFeatures) *domainfeatures.PriceFeatures {
	if len(rows) == 0 {
		return nil
	}
	return &rows[len(rows)-1]
}

func latestTrendFeature(rows []domainfeatures.TrendFeatures) *domainfeatures.TrendFeatures {
	if len(rows) == 0 {
		return nil
	}
	return &rows[len(rows)-1]
}

func latestVolatilityFeature(rows []domainfeatures.VolatilityFeatures) *domainfeatures.VolatilityFeatures {
	if len(rows) == 0 {
		return nil
	}
	return &rows[len(rows)-1]
}

func latestVolumeFeature(rows []domainfeatures.VolumeFeatures) *domainfeatures.VolumeFeatures {
	if len(rows) == 0 {
		return nil
	}
	return &rows[len(rows)-1]
}
