package main

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	realtimeapp "github.com/VersoIt/Inquisitor/internal/app/realtime"
	bybitws "github.com/VersoIt/Inquisitor/internal/exchanges/bybit/websocket"
	"github.com/VersoIt/Inquisitor/internal/marketdata"
	realtimequality "github.com/VersoIt/Inquisitor/internal/marketdata/realtime"
)

func TestLogPayloadAssessesOrderbookSnapshotQuality(t *testing.T) {
	dataTime := time.Date(2026, 6, 7, 0, 0, 0, 0, time.UTC)
	payload := fmt.Sprintf(`{"topic":"orderbook.50.BTCUSDT","type":"snapshot","ts":%d,"cts":%d,"data":{"s":"BTCUSDT","b":[["99.5","2"],["99","1"]],"a":[["100.5","3"],["101","1"]],"u":100,"seq":200}}`, dataTime.UnixMilli(), dataTime.UnixMilli())
	log := &captureLogger{}

	logPayload(context.Background(), log, bybitws.NewParser("linear"), []byte(payload), realtimequality.QualityPolicy{
		MaxStaleness: 3 * time.Second,
		MaxSpreadBPS: decimal.NewFromInt(50),
	}, dataTime.Add(5*time.Second), nil)

	qualityLog := log.find("info", "orderbook quality")
	if qualityLog == nil {
		t.Fatalf("expected orderbook quality log, got %#v", log.entries)
	}
	if got := qualityLog.arg("spread_bps"); got != "100" {
		t.Fatalf("spread_bps mismatch: got %#v want 100", got)
	}
	if got := qualityLog.arg("stale"); got != true {
		t.Fatalf("stale mismatch: got %#v want true", got)
	}
	if got := qualityLog.arg("spread_too_wide"); got != true {
		t.Fatalf("spread_too_wide mismatch: got %#v want true", got)
	}

	eventTypes := log.argsFor("warn", "orderbook quality event", "event_type")
	want := []any{marketdata.DataQualityEventStaleData, marketdata.DataQualityEventSpreadTooWide}
	if len(eventTypes) != len(want) {
		t.Fatalf("event count mismatch: got %#v want %#v", eventTypes, want)
	}
	for i := range want {
		if eventTypes[i] != want[i] {
			t.Fatalf("event[%d] mismatch: got %#v want %#v", i, eventTypes[i], want[i])
		}
	}
}

func TestLogPayloadSkipsOrderbookDeltaQualityAssessment(t *testing.T) {
	dataTime := time.Date(2026, 6, 7, 0, 0, 0, 0, time.UTC)
	payload := fmt.Sprintf(`{"topic":"orderbook.50.BTCUSDT","type":"delta","ts":%d,"cts":%d,"data":{"s":"BTCUSDT","b":[["99.5","0"]],"a":[],"u":101,"seq":201}}`, dataTime.UnixMilli(), dataTime.UnixMilli())
	log := &captureLogger{}

	logPayload(context.Background(), log, bybitws.NewParser("linear"), []byte(payload), realtimequality.QualityPolicy{
		MaxStaleness: 3 * time.Second,
		MaxSpreadBPS: decimal.NewFromInt(50),
	}, dataTime.Add(time.Second), nil)

	if got := log.find("info", "orderbook quality"); got != nil {
		t.Fatalf("delta must not be assessed as snapshot, got %#v", got)
	}
	if got := log.find("warn", "orderbook quality event"); got != nil {
		t.Fatalf("delta must not emit quality events, got %#v", got)
	}
}

func TestLogPayloadPersistsSupportedRealtimeMessages(t *testing.T) {
	dataTime := time.Date(2026, 6, 7, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name              string
		payload           string
		wantTradeCalls    int
		wantCandleCalls   int
		wantOrderbookCall int
		wantLog           string
		wantDeltasApplied int
	}{
		{
			name: "persists klines",
			payload: fmt.Sprintf(
				`{"topic":"kline.1.BTCUSDT","type":"snapshot","ts":%d,"data":[{"start":%d,"end":%d,"interval":"1","open":"100","close":"105","high":"110","low":"90","volume":"10","turnover":"1000","confirm":true,"timestamp":%d}]}`,
				dataTime.UnixMilli(),
				dataTime.UnixMilli(),
				dataTime.Add(time.Minute).UnixMilli(),
				dataTime.UnixMilli(),
			),
			wantCandleCalls: 1,
			wantLog:         "realtime candles persisted",
		},
		{
			name: "persists public trades",
			payload: fmt.Sprintf(
				`{"topic":"publicTrade.BTCUSDT","type":"snapshot","ts":%d,"data":[{"T":%d,"s":"BTCUSDT","S":"Buy","v":"0.01","p":"100","i":"trade-1","BT":false,"seq":100}]}`,
				dataTime.UnixMilli(),
				dataTime.UnixMilli(),
			),
			wantTradeCalls: 1,
			wantLog:        "public trades persisted",
		},
		{
			name: "persists orderbook snapshots",
			payload: fmt.Sprintf(
				`{"topic":"orderbook.50.BTCUSDT","type":"snapshot","ts":%d,"cts":%d,"data":{"s":"BTCUSDT","b":[["99.5","2"],["99","1"]],"a":[["100.5","3"],["101","1"]],"u":100,"seq":200}}`,
				dataTime.UnixMilli(),
				dataTime.UnixMilli(),
			),
			wantOrderbookCall: 1,
			wantLog:           "orderbook persisted",
		},
		{
			name: "passes orderbook deltas to processor",
			payload: fmt.Sprintf(
				`{"topic":"orderbook.50.BTCUSDT","type":"delta","ts":%d,"cts":%d,"data":{"s":"BTCUSDT","b":[["99.5","0"]],"a":[],"u":101,"seq":201}}`,
				dataTime.UnixMilli(),
				dataTime.UnixMilli(),
			),
			wantOrderbookCall: 1,
			wantLog:           "orderbook persisted",
			wantDeltasApplied: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			log := &captureLogger{}
			orderbookResult := realtimeapp.ProcessOrderbookResult{}
			if tt.wantOrderbookCall > 0 {
				orderbookResult.Received = 1
				orderbookResult.DeltasApplied = tt.wantDeltasApplied
				if tt.wantDeltasApplied == 0 {
					orderbookResult.SnapshotsInserted = 1
					orderbookResult.Valid = true
				}
			}
			processor := &captureProcessor{
				candleResult: realtimeapp.ProcessCandlesResult{
					Received: 1,
					Inserted: 1,
				},
				tradeResult: realtimeapp.ProcessTradesResult{
					Received: 1,
					Inserted: 1,
				},
				orderbookResult: orderbookResult,
			}

			logPayload(context.Background(), log, bybitws.NewParser("linear"), []byte(tt.payload), realtimequality.QualityPolicy{
				MaxStaleness: 3 * time.Second,
				MaxSpreadBPS: decimal.NewFromInt(150),
			}, dataTime.Add(time.Second), processor)

			if processor.tradeCalls != tt.wantTradeCalls {
				t.Fatalf("trade calls mismatch: got %d want %d", processor.tradeCalls, tt.wantTradeCalls)
			}
			if processor.candleCalls != tt.wantCandleCalls {
				t.Fatalf("candle calls mismatch: got %d want %d", processor.candleCalls, tt.wantCandleCalls)
			}
			if processor.orderbookCalls != tt.wantOrderbookCall {
				t.Fatalf("orderbook calls mismatch: got %d want %d", processor.orderbookCalls, tt.wantOrderbookCall)
			}
			gotLog := log.find("info", tt.wantLog)
			if gotLog == nil {
				t.Fatalf("expected log %q, got %#v", tt.wantLog, log.entries)
			}
			if tt.wantDeltasApplied > 0 && gotLog.arg("deltas_applied") != tt.wantDeltasApplied {
				t.Fatalf("deltas_applied log mismatch: got %#v want %d", gotLog.arg("deltas_applied"), tt.wantDeltasApplied)
			}
		})
	}
}

func TestLogPayloadRequestsReconnectWhenOrderbookNeedsSnapshotReset(t *testing.T) {
	dataTime := time.Date(2026, 6, 7, 0, 0, 0, 0, time.UTC)
	payload := fmt.Sprintf(
		`{"topic":"orderbook.50.BTCUSDT","type":"delta","ts":%d,"cts":%d,"data":{"s":"BTCUSDT","b":[["101","1"]],"a":[],"u":101,"seq":201}}`,
		dataTime.UnixMilli(),
		dataTime.UnixMilli(),
	)
	log := &captureLogger{}
	processor := &captureProcessor{
		orderbookResult: realtimeapp.ProcessOrderbookResult{
			Received:              1,
			NeedsSnapshotReset:    true,
			QualityEventsInserted: 1,
		},
	}

	got := logPayload(context.Background(), log, bybitws.NewParser("linear"), []byte(payload), realtimequality.QualityPolicy{
		MaxStaleness: 3 * time.Second,
		MaxSpreadBPS: decimal.NewFromInt(150),
	}, dataTime.Add(time.Second), processor)

	if !got.Reconnect {
		t.Fatalf("expected reconnect decision, got %#v", got)
	}
	if !strings.Contains(got.Reason, "snapshot reset") {
		t.Fatalf("unexpected reconnect reason: %q", got.Reason)
	}
	if processor.orderbookCalls != 1 {
		t.Fatalf("orderbook calls mismatch: got %d want 1", processor.orderbookCalls)
	}
	if gotLog := log.find("warn", "orderbook snapshot resync requested"); gotLog == nil {
		t.Fatalf("expected snapshot resync log, got %#v", log.entries)
	}
}

type captureLogger struct {
	entries []captureEntry
}

type captureEntry struct {
	level string
	msg   string
	args  []any
}

func (l *captureLogger) Info(msg string, args ...any) {
	l.entries = append(l.entries, captureEntry{level: "info", msg: msg, args: args})
}

func (l *captureLogger) Warn(msg string, args ...any) {
	l.entries = append(l.entries, captureEntry{level: "warn", msg: msg, args: args})
}

func (l *captureLogger) find(level, msg string) *captureEntry {
	for i := range l.entries {
		if l.entries[i].level == level && l.entries[i].msg == msg {
			return &l.entries[i]
		}
	}
	return nil
}

func (l *captureLogger) argsFor(level, msg, key string) []any {
	var values []any
	for i := range l.entries {
		if l.entries[i].level == level && l.entries[i].msg == msg {
			values = append(values, l.entries[i].arg(key))
		}
	}
	return values
}

func (e captureEntry) arg(key string) any {
	for i := 0; i+1 < len(e.args); i += 2 {
		if e.args[i] == key {
			return e.args[i+1]
		}
	}
	return nil
}

type captureProcessor struct {
	candleCalls     int
	tradeCalls      int
	orderbookCalls  int
	candleResult    realtimeapp.ProcessCandlesResult
	tradeResult     realtimeapp.ProcessTradesResult
	orderbookResult realtimeapp.ProcessOrderbookResult
}

func (p *captureProcessor) ProcessCandles(context.Context, []marketdata.Candle) (realtimeapp.ProcessCandlesResult, error) {
	p.candleCalls++
	return p.candleResult, nil
}

func (p *captureProcessor) ProcessTrades(context.Context, []marketdata.PublicTrade) (realtimeapp.ProcessTradesResult, error) {
	p.tradeCalls++
	return p.tradeResult, nil
}

func (p *captureProcessor) ProcessOrderbook(context.Context, marketdata.Orderbook) (realtimeapp.ProcessOrderbookResult, error) {
	p.orderbookCalls++
	return p.orderbookResult, nil
}
