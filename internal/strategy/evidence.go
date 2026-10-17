package strategy

import (
	"strconv"
	"strings"
	"time"
)

// Evidence uses strings so even rejected NaN/Inf statistical inputs remain auditable.
func SignalEvidence(m MarketContext) map[string]string {
	e := map[string]string{
		"entry_price": m.EntryPrice.String(), "regime.name": string(m.Regime.Regime),
		"regime.confidence": strconv.Itoa(m.Regime.Confidence), "regime.no_trade": strconv.FormatBool(m.Regime.NoTrade),
		"regime.candidate": string(m.Regime.CandidateRegime), "regime.reasons": strings.Join(m.Regime.Reasons, ","),
		"regime.calculated_at":    m.Regime.CalculatedAt.UTC().Format(time.RFC3339Nano),
		"runtime.trading_enabled": strconv.FormatBool(m.TradingEnabled), "runtime.kill_switch_active": strconv.FormatBool(m.KillSwitchActive), "runtime.allow_short": strconv.FormatBool(m.AllowShort),
		"approval.mode": string(m.Approval.Mode), "approval.strategy_name": m.Approval.StrategyName, "approval.direction": string(m.Approval.Direction),
		"approval.enabled": strconv.FormatBool(m.Approval.Enabled), "approval.edge_decayed": strconv.FormatBool(m.Approval.EdgeDecayed),
		"approval.max_risk_pct": m.Approval.MaxRiskPct.String(), "approval.min_confidence": strconv.Itoa(m.Approval.MinConfidence),
		"prior_price.rolling_high": m.PriorPrice.RollingHigh.String(), "prior_price.rolling_low": m.PriorPrice.RollingLow.String(),
		"prior_volatility.compression": strconv.FormatFloat(m.PriorVolatility.VolatilityCompression, 'g', -1, 64),
		"prior_volume.moving_average":  m.PriorVolume.VolumeMovingAverage.String(),
		"current.volume":               m.Current.Volume.String(), "current.turnover": m.Current.Turnover.String(),
		"previous.volume": m.Previous.Volume.String(), "previous.turnover": m.Previous.Turnover.String(),
		"trend.ema20": m.Trend.EMA20.String(), "trend.ema50": m.Trend.EMA50.String(), "trend.ma50_slope": m.Trend.MA50Slope.String(), "volatility.atr": m.Volatility.ATR.String(),
	}
	for _, item := range []struct {
		prefix, exchange, category, symbol, interval string
		open, close                                  time.Time
		complete                                     bool
		missing                                      []string
	}{
		{"prior_price", m.PriorPrice.Exchange, m.PriorPrice.Category, m.PriorPrice.Symbol, m.PriorPrice.Interval, m.PriorPrice.OpenTime, m.PriorPrice.CloseTime, m.PriorPrice.Complete, m.PriorPrice.MissingReasons},
		{"prior_volatility", m.PriorVolatility.Exchange, m.PriorVolatility.Category, m.PriorVolatility.Symbol, m.PriorVolatility.Interval, m.PriorVolatility.OpenTime, m.PriorVolatility.CloseTime, m.PriorVolatility.Complete, m.PriorVolatility.MissingReasons},
		{"prior_volume", m.PriorVolume.Exchange, m.PriorVolume.Category, m.PriorVolume.Symbol, m.PriorVolume.Interval, m.PriorVolume.OpenTime, m.PriorVolume.CloseTime, m.PriorVolume.Complete, m.PriorVolume.MissingReasons},
		{"trend", m.Trend.Exchange, m.Trend.Category, m.Trend.Symbol, m.Trend.Interval, m.Trend.OpenTime, m.Trend.CloseTime, m.Trend.Complete, m.Trend.MissingReasons},
		{"volatility", m.Volatility.Exchange, m.Volatility.Category, m.Volatility.Symbol, m.Volatility.Interval, m.Volatility.OpenTime, m.Volatility.CloseTime, m.Volatility.Complete, m.Volatility.MissingReasons},
	} {
		e[item.prefix+".exchange"], e[item.prefix+".category"], e[item.prefix+".symbol"], e[item.prefix+".interval"] = item.exchange, item.category, item.symbol, item.interval
		e[item.prefix+".open_time"], e[item.prefix+".close_time"] = item.open.UTC().Format(time.RFC3339Nano), item.close.UTC().Format(time.RFC3339Nano)
		e[item.prefix+".complete"], e[item.prefix+".missing_reasons"] = strconv.FormatBool(item.complete), strings.Join(item.missing, ",")
	}
	for _, item := range []struct {
		prefix, exchange, category, symbol, interval string
		open, close                                  time.Time
	}{
		{"approval", m.Approval.Exchange, m.Approval.Category, m.Approval.Symbol, m.Approval.Interval, time.Time{}, time.Time{}},
		{"current", m.Current.Exchange, m.Current.Category, m.Current.Symbol, m.Current.Interval, m.Current.OpenTime, m.Current.CloseTime},
		{"previous", m.Previous.Exchange, m.Previous.Category, m.Previous.Symbol, m.Previous.Interval, m.Previous.OpenTime, m.Previous.CloseTime},
	} {
		e[item.prefix+".exchange"], e[item.prefix+".category"], e[item.prefix+".symbol"], e[item.prefix+".interval"] = item.exchange, item.category, item.symbol, item.interval
		e[item.prefix+".open_time"], e[item.prefix+".close_time"] = item.open.UTC().Format(time.RFC3339Nano), item.close.UTC().Format(time.RFC3339Nano)
	}
	for _, item := range []struct {
		prefix                 string
		open, high, low, close string
		closed                 bool
	}{
		{"current", m.Current.Open.String(), m.Current.High.String(), m.Current.Low.String(), m.Current.Close.String(), m.Current.IsClosed},
		{"previous", m.Previous.Open.String(), m.Previous.High.String(), m.Previous.Low.String(), m.Previous.Close.String(), m.Previous.IsClosed},
	} {
		e[item.prefix+".open"], e[item.prefix+".high"], e[item.prefix+".low"], e[item.prefix+".close"] = item.open, item.high, item.low, item.close
		e[item.prefix+".is_closed"] = strconv.FormatBool(item.closed)
	}
	return e
}
