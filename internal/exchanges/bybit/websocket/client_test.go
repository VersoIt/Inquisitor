package websocket_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	gorilla "github.com/gorilla/websocket"

	"github.com/VersoIt/Inquisitor/internal/exchanges/bybit/websocket"
)

func TestClientConnectSubscribePingReadAndClose(t *testing.T) {
	upgrader := gorilla.Upgrader{}
	received := make(chan map[string]any, 2)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		defer conn.Close()

		for i := 0; i < 2; i++ {
			_, payload, err := conn.ReadMessage()
			if err != nil {
				t.Errorf("read message: %v", err)
				return
			}
			var message map[string]any
			if err := json.Unmarshal(payload, &message); err != nil {
				t.Errorf("decode message: %v", err)
				return
			}
			received <- message
		}

		if err := conn.WriteMessage(gorilla.TextMessage, []byte(`{"topic":"publicTrade.BTCUSDT","type":"snapshot","data":[]}`)); err != nil {
			t.Errorf("write message: %v", err)
		}
	}))
	defer server.Close()

	client, err := websocket.NewClient("ws" + strings.TrimPrefix(server.URL, "http"))
	if err != nil {
		t.Fatalf("new websocket client: %v", err)
	}
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Connect(ctx); err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := client.Subscribe(ctx, "sub-1", []string{"publicTrade.BTCUSDT"}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if err := client.Ping(ctx, "ping-1"); err != nil {
		t.Fatalf("ping: %v", err)
	}

	subscribe := <-received
	if subscribe["op"] != "subscribe" || subscribe["req_id"] != "sub-1" {
		t.Fatalf("unexpected subscribe message: %#v", subscribe)
	}
	ping := <-received
	if ping["op"] != "ping" || ping["req_id"] != "ping-1" {
		t.Fatalf("unexpected ping message: %#v", ping)
	}

	payload, err := client.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(payload), "publicTrade.BTCUSDT") {
		t.Fatalf("unexpected read payload: %s", payload)
	}
}

func TestClientRejectsInvalidURLAndDisconnectedWrites(t *testing.T) {
	if _, err := websocket.NewClient("https://stream-testnet.bybit.com/v5/public/linear"); err == nil {
		t.Fatal("expected invalid websocket scheme error")
	}

	client, err := websocket.NewClient("ws://example.local/ws")
	if err != nil {
		t.Fatalf("new websocket client: %v", err)
	}

	if err := client.Subscribe(context.Background(), "", []string{"publicTrade.BTCUSDT"}); err == nil {
		t.Fatal("expected disconnected subscribe error")
	}
	if _, err := client.Read(context.Background()); err == nil {
		t.Fatal("expected disconnected read error")
	}
}
