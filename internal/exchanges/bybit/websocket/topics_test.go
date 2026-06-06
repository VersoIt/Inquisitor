package websocket_test

import (
	"encoding/json"
	"testing"

	"github.com/VersoIt/Inquisitor/internal/exchanges/bybit/websocket"
)

func TestTopicBuildersTableDriven(t *testing.T) {
	tests := []struct {
		name    string
		build   func() (string, error)
		want    string
		wantErr bool
	}{
		{name: "kline topic", build: func() (string, error) { return websocket.KlineTopic("1", "btcusdt") }, want: "kline.1.BTCUSDT"},
		{name: "ticker topic", build: func() (string, error) { return websocket.TickerTopic("ethusdt") }, want: "tickers.ETHUSDT"},
		{name: "orderbook topic", build: func() (string, error) { return websocket.OrderbookTopic(50, "BTCUSDT") }, want: "orderbook.50.BTCUSDT"},
		{name: "trade topic", build: func() (string, error) { return websocket.PublicTradeTopic("BTCUSDT") }, want: "publicTrade.BTCUSDT"},
		{name: "rejects empty symbol", build: func() (string, error) { return websocket.TickerTopic("") }, wantErr: true},
		{name: "rejects invalid depth", build: func() (string, error) { return websocket.OrderbookTopic(0, "BTCUSDT") }, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.build()
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("expected %q, got %q", tt.want, got)
			}
		})
	}
}

func TestBuildSubscribeMessage(t *testing.T) {
	raw, err := websocket.BuildSubscribeMessage("test", []string{"orderbook.1.BTCUSDT", "publicTrade.BTCUSDT"})
	if err != nil {
		t.Fatalf("build subscribe message: %v", err)
	}

	var message map[string]any
	if err := json.Unmarshal(raw, &message); err != nil {
		t.Fatalf("decode subscribe message: %v", err)
	}
	if message["req_id"] != "test" || message["op"] != "subscribe" {
		t.Fatalf("unexpected subscribe envelope: %s", string(raw))
	}
	args, ok := message["args"].([]any)
	if !ok || len(args) != 2 {
		t.Fatalf("unexpected subscribe args: %#v", message["args"])
	}
}

func TestBuildSubscribeMessageRejectsEmptyTopics(t *testing.T) {
	if _, err := websocket.BuildSubscribeMessage("", nil); err == nil {
		t.Fatal("expected empty topics error")
	}
	if _, err := websocket.BuildSubscribeMessage("", []string{" "}); err == nil {
		t.Fatal("expected empty topic error")
	}
}
