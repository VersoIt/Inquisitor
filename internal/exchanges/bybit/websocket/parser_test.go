package websocket_test

import (
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/VersoIt/Inquisitor/internal/exchanges/bybit/websocket"
)

func TestParserParseKline(t *testing.T) {
	parser := websocket.NewParser("linear")

	candles, err := parser.ParseKline([]byte(`{
		"topic": "kline.5.BTCUSDT",
		"data": [{
			"start": 1672324800000,
			"end": 1672325099999,
			"interval": "5",
			"open": "16649.5",
			"close": "16677",
			"high": "16677",
			"low": "16608",
			"volume": "2.081",
			"turnover": "34666.4005",
			"confirm": false,
			"timestamp": 1672324988882
		}],
		"ts": 1672324988882,
		"type": "snapshot"
	}`))
	if err != nil {
		t.Fatalf("parse kline: %v", err)
	}
	if len(candles) != 1 {
		t.Fatalf("expected one candle, got %d", len(candles))
	}
	candle := candles[0]
	if candle.Exchange != "bybit" || candle.Category != "linear" || candle.Symbol != "BTCUSDT" || candle.Interval != "5" {
		t.Fatalf("unexpected candle identity: %#v", candle)
	}
	if !candle.Open.Equal(decimal.RequireFromString("16649.5")) || !candle.Turnover.Equal(decimal.RequireFromString("34666.4005")) {
		t.Fatalf("unexpected candle prices: %#v", candle)
	}
	wantOpen := time.UnixMilli(1672324800000).UTC()
	if !candle.OpenTime.Equal(wantOpen) || !candle.CloseTime.Equal(wantOpen.Add(5*time.Minute)) {
		t.Fatalf("unexpected candle times: open=%s close=%s", candle.OpenTime, candle.CloseTime)
	}
	if candle.IsClosed {
		t.Fatal("expected non-confirmed candle to be open")
	}
}

func TestParserParseTicker(t *testing.T) {
	parser := websocket.NewParser("linear")

	ticker, err := parser.ParseTicker([]byte(`{
		"topic": "tickers.BTCUSDT",
		"type": "snapshot",
		"data": {
			"symbol": "BTCUSDT",
			"lastPrice": "66666.60",
			"markPrice": "66666.60",
			"indexPrice": "115418.19",
			"openInterest": "492373.72",
			"fundingRate": "-0.005",
			"bid1Price": "66666.60",
			"bid1Size": "23789.165",
			"ask1Price": "66666.70",
			"ask1Size": "23775.469"
		},
		"cs": 9532239429,
		"ts": 1760325052630
	}`))
	if err != nil {
		t.Fatalf("parse ticker: %v", err)
	}
	if ticker.Symbol != "BTCUSDT" || ticker.Category != "linear" {
		t.Fatalf("unexpected ticker identity: %#v", ticker)
	}
	if !ticker.LastPrice.Equal(decimal.RequireFromString("66666.60")) || !ticker.FundingRate.Equal(decimal.RequireFromString("-0.005")) {
		t.Fatalf("unexpected ticker values: %#v", ticker)
	}
	if !ticker.ExchangeTime.Equal(time.UnixMilli(1760325052630).UTC()) {
		t.Fatalf("unexpected ticker exchange time: %s", ticker.ExchangeTime)
	}
}

func TestParserParseTrades(t *testing.T) {
	parser := websocket.NewParser("linear")

	trades, err := parser.ParseTrades([]byte(`{
		"topic": "publicTrade.BTCUSDT",
		"type": "snapshot",
		"ts": 1672304486868,
		"data": [{
			"T": 1672304486865,
			"s": "BTCUSDT",
			"S": "Buy",
			"v": "0.001",
			"p": "16578.50",
			"i": "20f43950-d8dd-5b31-9112-a178eb6023af",
			"BT": false,
			"seq": 1783284617
		}]
	}`))
	if err != nil {
		t.Fatalf("parse trades: %v", err)
	}
	if len(trades) != 1 {
		t.Fatalf("expected one trade, got %d", len(trades))
	}
	trade := trades[0]
	if trade.TradeID == "" || trade.Side != "Buy" || !trade.Price.Equal(decimal.RequireFromString("16578.50")) {
		t.Fatalf("unexpected trade: %#v", trade)
	}
	if !trade.TradeTime.Equal(time.UnixMilli(1672304486865).UTC()) {
		t.Fatalf("unexpected trade time: %s", trade.TradeTime)
	}
}

func TestParserParseOrderbook(t *testing.T) {
	parser := websocket.NewParser("linear")

	orderbook, err := parser.ParseOrderbook([]byte(`{
		"topic": "orderbook.50.BTCUSDT",
		"type": "snapshot",
		"ts": 1672304484978,
		"data": {
			"s": "BTCUSDT",
			"b": [["16493.50", "0.006"]],
			"a": [["16611.00", "0.029"]],
			"u": 18521288,
			"seq": 7961638724
		},
		"cts": 1672304484976
	}`))
	if err != nil {
		t.Fatalf("parse orderbook: %v", err)
	}
	if orderbook.Symbol != "BTCUSDT" || orderbook.Type != "snapshot" {
		t.Fatalf("unexpected orderbook identity: %#v", orderbook)
	}
	if len(orderbook.Bids) != 1 || len(orderbook.Asks) != 1 {
		t.Fatalf("unexpected levels: %#v", orderbook)
	}
	if !orderbook.Bids[0].Price.Equal(decimal.RequireFromString("16493.50")) || !orderbook.Asks[0].Quantity.Equal(decimal.RequireFromString("0.029")) {
		t.Fatalf("unexpected orderbook values: %#v", orderbook)
	}
	if orderbook.UpdateID != 18521288 || orderbook.Sequence != 7961638724 {
		t.Fatalf("unexpected orderbook sequence: %#v", orderbook)
	}
}

func TestParserRejectsMalformedMessagesTableDriven(t *testing.T) {
	parser := websocket.NewParser("linear")

	tests := []struct {
		name       string
		parse      func() error
		wantErrSub string
	}{
		{
			name: "invalid ticker decimal",
			parse: func() error {
				_, err := parser.ParseTicker([]byte(`{"topic":"tickers.BTCUSDT","ts":1,"data":{"symbol":"BTCUSDT","lastPrice":"not-a-number"}}`))
				return err
			},
			wantErrSub: "lastPrice",
		},
		{
			name: "invalid orderbook level shape",
			parse: func() error {
				_, err := parser.ParseOrderbook([]byte(`{"topic":"orderbook.50.BTCUSDT","type":"snapshot","ts":1,"data":{"s":"BTCUSDT","b":[["1"]],"a":[]}}`))
				return err
			},
			wantErrSub: "expected 2 fields",
		},
		{
			name: "unsupported kline interval",
			parse: func() error {
				_, err := parser.ParseKline([]byte(`{"topic":"kline.7.BTCUSDT","data":[{"start":1,"interval":"7","open":"1","close":"1","high":"1","low":"1","volume":"1","turnover":"1"}]}`))
				return err
			},
			wantErrSub: "unsupported candle interval",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.parse()
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
		})
	}
}
