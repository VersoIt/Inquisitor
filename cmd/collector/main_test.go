package main

import (
	"fmt"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	bybitws "github.com/VersoIt/Inquisitor/internal/exchanges/bybit/websocket"
	"github.com/VersoIt/Inquisitor/internal/marketdata"
	realtimequality "github.com/VersoIt/Inquisitor/internal/marketdata/realtime"
)

func TestLogPayloadAssessesOrderbookSnapshotQuality(t *testing.T) {
	dataTime := time.Date(2026, 6, 7, 0, 0, 0, 0, time.UTC)
	payload := fmt.Sprintf(`{"topic":"orderbook.50.BTCUSDT","type":"snapshot","ts":%d,"cts":%d,"data":{"s":"BTCUSDT","b":[["99.5","2"],["99","1"]],"a":[["100.5","3"],["101","1"]],"u":100,"seq":200}}`, dataTime.UnixMilli(), dataTime.UnixMilli())
	log := &captureLogger{}

	logPayload(log, bybitws.NewParser("linear"), []byte(payload), realtimequality.QualityPolicy{
		MaxStaleness: 3 * time.Second,
		MaxSpreadBPS: decimal.NewFromInt(50),
	}, dataTime.Add(5*time.Second))

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

	logPayload(log, bybitws.NewParser("linear"), []byte(payload), realtimequality.QualityPolicy{
		MaxStaleness: 3 * time.Second,
		MaxSpreadBPS: decimal.NewFromInt(50),
	}, dataTime.Add(time.Second))

	if got := log.find("info", "orderbook quality"); got != nil {
		t.Fatalf("delta must not be assessed as snapshot, got %#v", got)
	}
	if got := log.find("warn", "orderbook quality event"); got != nil {
		t.Fatalf("delta must not emit quality events, got %#v", got)
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
