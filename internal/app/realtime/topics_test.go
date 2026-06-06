package realtime_test

import (
	"testing"

	"github.com/VersoIt/Inquisitor/internal/app/realtime"
)

func TestBuildBybitTopicsTableDriven(t *testing.T) {
	tests := []struct {
		name    string
		req     realtime.TopicRequest
		want    []string
		wantErr bool
	}{
		{
			name: "builds mixed topics without duplicates",
			req: realtime.TopicRequest{
				Symbols:        []string{"BTCUSDT", "btcusdt"},
				Intervals:      []string{"1"},
				Streams:        []string{"kline", "ticker", "trade", "orderbook"},
				OrderbookDepth: 50,
			},
			want: []string{"kline.1.BTCUSDT", "tickers.BTCUSDT", "publicTrade.BTCUSDT", "orderbook.50.BTCUSDT"},
		},
		{
			name: "rejects unknown stream",
			req: realtime.TopicRequest{
				Symbols: []string{"BTCUSDT"},
				Streams: []string{"liquidation"},
			},
			wantErr: true,
		},
		{
			name: "requires kline intervals",
			req: realtime.TopicRequest{
				Symbols: []string{"BTCUSDT"},
				Streams: []string{"kline"},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := realtime.BuildBybitTopics(tt.req)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("build topics: %v", err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("expected %d topics, got %d: %#v", len(tt.want), len(got), got)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Fatalf("topic[%d] expected %q, got %q", i, tt.want[i], got[i])
				}
			}
		})
	}
}
