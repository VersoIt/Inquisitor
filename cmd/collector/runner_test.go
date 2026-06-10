package main

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestCollectorRunnerTableDriven(t *testing.T) {
	ctx := context.Background()
	readErr := errors.New("connection reset")

	tests := []struct {
		name               string
		client             *fakeCollectorClient
		messages           int
		reconnectAttempts  int
		wantRead           int
		wantErr            bool
		wantConnectCalls   int
		wantSubscribeCalls int
		wantCloseCalls     int
		wantReadDeadlines  int
		wantHandled        []string
		wantSleeps         int
	}{
		{
			name: "reads requested messages without reconnect",
			client: &fakeCollectorClient{
				reads: []fakeRead{
					{payload: []byte("one")},
					{payload: []byte("two")},
				},
			},
			messages:           2,
			reconnectAttempts:  1,
			wantRead:           2,
			wantConnectCalls:   1,
			wantSubscribeCalls: 1,
			wantReadDeadlines:  2,
			wantHandled:        []string{"one", "two"},
		},
		{
			name: "reconnects after read failure and continues",
			client: &fakeCollectorClient{
				reads: []fakeRead{
					{err: readErr},
					{payload: []byte("after-reconnect")},
				},
			},
			messages:           1,
			reconnectAttempts:  1,
			wantRead:           1,
			wantConnectCalls:   2,
			wantSubscribeCalls: 2,
			wantCloseCalls:     1,
			wantReadDeadlines:  2,
			wantHandled:        []string{"after-reconnect"},
			wantSleeps:         1,
		},
		{
			name: "exhausts reconnect attempts",
			client: &fakeCollectorClient{
				reads: []fakeRead{
					{err: readErr},
					{err: readErr},
				},
			},
			messages:           1,
			reconnectAttempts:  1,
			wantErr:            true,
			wantConnectCalls:   2,
			wantSubscribeCalls: 2,
			wantCloseCalls:     1,
			wantReadDeadlines:  2,
			wantSleeps:         1,
		},
		{
			name: "returns initial connect error",
			client: &fakeCollectorClient{
				connectErrs: []error{errors.New("dial refused")},
			},
			messages:          1,
			reconnectAttempts: 1,
			wantErr:           true,
			wantConnectCalls:  1,
		},
		{
			name: "returns reconnect subscribe error",
			client: &fakeCollectorClient{
				reads: []fakeRead{{err: readErr}},
				subscribeErrs: []error{
					nil,
					errors.New("subscribe refused"),
				},
			},
			messages:           1,
			reconnectAttempts:  1,
			wantErr:            true,
			wantConnectCalls:   2,
			wantSubscribeCalls: 2,
			wantCloseCalls:     1,
			wantReadDeadlines:  1,
			wantSleeps:         1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var sleeps []time.Duration
			var handled []string
			runner := collectorRunner{
				client:            tt.client,
				log:               &captureLogger{},
				topics:            []string{"publicTrade.BTCUSDT"},
				reqID:             "collector-test",
				messages:          tt.messages,
				readTimeout:       500 * time.Millisecond,
				reconnectAttempts: tt.reconnectAttempts,
				reconnectBackoff:  25 * time.Millisecond,
				sleep: func(_ context.Context, delay time.Duration) error {
					sleeps = append(sleeps, delay)
					return nil
				},
				handlePayload: func(_ context.Context, payload []byte) collectorPayloadDecision {
					handled = append(handled, string(payload))
					return collectorPayloadDecision{}
				},
			}

			gotRead, err := runner.Run(ctx)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
			} else if err != nil {
				t.Fatalf("run collector: %v", err)
			}
			if gotRead != tt.wantRead {
				t.Fatalf("messages read mismatch: got %d want %d", gotRead, tt.wantRead)
			}
			if tt.client.connectCalls != tt.wantConnectCalls {
				t.Fatalf("connect calls mismatch: got %d want %d", tt.client.connectCalls, tt.wantConnectCalls)
			}
			if tt.client.subscribeCalls != tt.wantSubscribeCalls {
				t.Fatalf("subscribe calls mismatch: got %d want %d", tt.client.subscribeCalls, tt.wantSubscribeCalls)
			}
			if tt.client.closeCalls != tt.wantCloseCalls {
				t.Fatalf("close calls mismatch: got %d want %d", tt.client.closeCalls, tt.wantCloseCalls)
			}
			if tt.client.readDeadlineCalls != tt.wantReadDeadlines {
				t.Fatalf("read deadline calls mismatch: got %d want %d", tt.client.readDeadlineCalls, tt.wantReadDeadlines)
			}
			if len(sleeps) != tt.wantSleeps {
				t.Fatalf("sleep count mismatch: got %d want %d", len(sleeps), tt.wantSleeps)
			}
			assertStrings(t, handled, tt.wantHandled)
		})
	}
}

func TestCollectorRunnerRejectsInvalidConfigTableDriven(t *testing.T) {
	tests := []struct {
		name   string
		runner collectorRunner
	}{
		{
			name:   "missing client",
			runner: collectorRunner{messages: 1, reqID: "test", readTimeout: time.Millisecond, reconnectBackoff: time.Millisecond},
		},
		{
			name:   "non positive messages",
			runner: collectorRunner{client: &fakeCollectorClient{}, reqID: "test", readTimeout: time.Millisecond, reconnectBackoff: time.Millisecond},
		},
		{
			name:   "missing request id",
			runner: collectorRunner{client: &fakeCollectorClient{}, messages: 1, readTimeout: time.Millisecond, reconnectBackoff: time.Millisecond},
		},
		{
			name:   "non positive read timeout",
			runner: collectorRunner{client: &fakeCollectorClient{}, messages: 1, reqID: "test", reconnectBackoff: time.Millisecond},
		},
		{
			name:   "negative ping interval",
			runner: collectorRunner{client: &fakeCollectorClient{}, messages: 1, reqID: "test", readTimeout: time.Millisecond, pingInterval: -time.Millisecond, reconnectBackoff: time.Millisecond},
		},
		{
			name:   "negative reconnect attempts",
			runner: collectorRunner{client: &fakeCollectorClient{}, messages: 1, reqID: "test", readTimeout: time.Millisecond, reconnectAttempts: -1, reconnectBackoff: time.Millisecond},
		},
		{
			name:   "non positive reconnect backoff",
			runner: collectorRunner{client: &fakeCollectorClient{}, messages: 1, reqID: "test", readTimeout: time.Millisecond},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := tt.runner.Run(context.Background()); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestCollectorRunnerReconnectsWhenPayloadRequestsResync(t *testing.T) {
	ctx := context.Background()
	client := &fakeCollectorClient{
		reads: []fakeRead{
			{payload: []byte("invalid-orderbook")},
			{payload: []byte("fresh-snapshot")},
		},
	}
	var handled []string
	var sleeps []time.Duration
	runner := collectorRunner{
		client:            client,
		log:               &captureLogger{},
		topics:            []string{"orderbook.50.BTCUSDT"},
		reqID:             "collector-test",
		messages:          2,
		readTimeout:       time.Second,
		reconnectAttempts: 1,
		reconnectBackoff:  25 * time.Millisecond,
		sleep: func(_ context.Context, delay time.Duration) error {
			sleeps = append(sleeps, delay)
			return nil
		},
		handlePayload: func(_ context.Context, payload []byte) collectorPayloadDecision {
			handled = append(handled, string(payload))
			if string(payload) == "invalid-orderbook" {
				return collectorPayloadDecision{Reconnect: true, Reason: "orderbook snapshot reset requested"}
			}
			return collectorPayloadDecision{}
		},
	}

	gotRead, err := runner.Run(ctx)
	if err != nil {
		t.Fatalf("run collector: %v", err)
	}
	if gotRead != 2 {
		t.Fatalf("messages read mismatch: got %d want 2", gotRead)
	}
	if client.connectCalls != 2 || client.subscribeCalls != 2 || client.closeCalls != 1 {
		t.Fatalf("unexpected reconnect calls: connect=%d subscribe=%d close=%d", client.connectCalls, client.subscribeCalls, client.closeCalls)
	}
	if len(sleeps) != 1 {
		t.Fatalf("sleep count mismatch: got %d want 1", len(sleeps))
	}
	assertStrings(t, handled, []string{"invalid-orderbook", "fresh-snapshot"})
}

func TestCollectorRunnerHeartbeatPingTableDriven(t *testing.T) {
	ctx := context.Background()
	start := time.Date(2026, 6, 10, 0, 0, 0, 0, time.UTC)
	pingErr := errors.New("ping failed")

	tests := []struct {
		name               string
		client             *fakeCollectorClient
		reconnectAttempts  int
		nowValues          []time.Time
		wantErr            bool
		wantPingCalls      int
		wantConnectCalls   int
		wantSubscribeCalls int
		wantCloseCalls     int
		wantHandled        []string
	}{
		{
			name: "sends heartbeat ping when interval elapsed",
			client: &fakeCollectorClient{
				reads: []fakeRead{{payload: []byte("message")}},
			},
			reconnectAttempts:  1,
			nowValues:          []time.Time{start, start.Add(20 * time.Second), start.Add(20 * time.Second)},
			wantPingCalls:      1,
			wantConnectCalls:   1,
			wantSubscribeCalls: 1,
			wantHandled:        []string{"message"},
		},
		{
			name: "reconnects after heartbeat ping failure",
			client: &fakeCollectorClient{
				pingErrs: []error{pingErr},
				reads:    []fakeRead{{payload: []byte("after-ping-reconnect")}},
			},
			reconnectAttempts:  1,
			nowValues:          []time.Time{start, start.Add(20 * time.Second), start.Add(20 * time.Second), start.Add(20 * time.Second)},
			wantPingCalls:      1,
			wantConnectCalls:   2,
			wantSubscribeCalls: 2,
			wantCloseCalls:     1,
			wantHandled:        []string{"after-ping-reconnect"},
		},
		{
			name: "fails when heartbeat ping exhausts reconnect attempts",
			client: &fakeCollectorClient{
				pingErrs: []error{pingErr},
			},
			reconnectAttempts:  0,
			nowValues:          []time.Time{start, start.Add(20 * time.Second)},
			wantErr:            true,
			wantPingCalls:      1,
			wantConnectCalls:   1,
			wantSubscribeCalls: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var handled []string
			runner := collectorRunner{
				client:            tt.client,
				log:               &captureLogger{},
				topics:            []string{"publicTrade.BTCUSDT"},
				reqID:             "collector-test",
				messages:          1,
				readTimeout:       time.Second,
				pingInterval:      20 * time.Second,
				reconnectAttempts: tt.reconnectAttempts,
				reconnectBackoff:  time.Millisecond,
				now:               fakeNow(tt.nowValues...),
				sleep:             func(context.Context, time.Duration) error { return nil },
				handlePayload: func(_ context.Context, payload []byte) collectorPayloadDecision {
					handled = append(handled, string(payload))
					return collectorPayloadDecision{}
				},
			}

			_, err := runner.Run(ctx)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
			} else if err != nil {
				t.Fatalf("run collector: %v", err)
			}
			if tt.client.pingCalls != tt.wantPingCalls {
				t.Fatalf("ping calls mismatch: got %d want %d", tt.client.pingCalls, tt.wantPingCalls)
			}
			if tt.client.connectCalls != tt.wantConnectCalls {
				t.Fatalf("connect calls mismatch: got %d want %d", tt.client.connectCalls, tt.wantConnectCalls)
			}
			if tt.client.subscribeCalls != tt.wantSubscribeCalls {
				t.Fatalf("subscribe calls mismatch: got %d want %d", tt.client.subscribeCalls, tt.wantSubscribeCalls)
			}
			if tt.client.closeCalls != tt.wantCloseCalls {
				t.Fatalf("close calls mismatch: got %d want %d", tt.client.closeCalls, tt.wantCloseCalls)
			}
			if tt.wantPingCalls > 0 && tt.client.pingReqIDs[0] != "collector-test-ping" {
				t.Fatalf("unexpected ping req id: %#v", tt.client.pingReqIDs)
			}
			assertStrings(t, handled, tt.wantHandled)
		})
	}
}

func TestCollectorRunnerStopsWhenBackoffContextIsCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	client := &fakeCollectorClient{
		reads: []fakeRead{{err: errors.New("connection reset")}},
	}
	runner := collectorRunner{
		client:            client,
		log:               &captureLogger{},
		topics:            []string{"publicTrade.BTCUSDT"},
		reqID:             "collector-test",
		messages:          1,
		readTimeout:       time.Millisecond,
		reconnectAttempts: 1,
		reconnectBackoff:  time.Millisecond,
	}

	if _, err := runner.Run(ctx); err == nil {
		t.Fatal("expected context cancellation error")
	}
}

type fakeCollectorClient struct {
	connectErrs   []error
	subscribeErrs []error
	pingErrs      []error
	reads         []fakeRead

	connectCalls   int
	subscribeCalls int
	pingCalls      int
	closeCalls     int
	readCalls      int

	readDeadlineCalls int
	pingReqIDs        []string
}

type fakeRead struct {
	payload []byte
	err     error
}

func (c *fakeCollectorClient) Connect(context.Context) error {
	c.connectCalls++
	if c.connectCalls <= len(c.connectErrs) {
		return c.connectErrs[c.connectCalls-1]
	}
	return nil
}

func (c *fakeCollectorClient) Subscribe(context.Context, string, []string) error {
	c.subscribeCalls++
	if c.subscribeCalls <= len(c.subscribeErrs) {
		return c.subscribeErrs[c.subscribeCalls-1]
	}
	return nil
}

func (c *fakeCollectorClient) Ping(_ context.Context, reqID string) error {
	c.pingCalls++
	c.pingReqIDs = append(c.pingReqIDs, reqID)
	if c.pingCalls <= len(c.pingErrs) {
		return c.pingErrs[c.pingCalls-1]
	}
	return nil
}

func (c *fakeCollectorClient) Read(ctx context.Context) ([]byte, error) {
	c.readCalls++
	if _, ok := ctx.Deadline(); ok {
		c.readDeadlineCalls++
	}
	if c.readCalls <= len(c.reads) {
		read := c.reads[c.readCalls-1]
		return read.payload, read.err
	}
	return nil, errors.New("no fake read configured")
}

func (c *fakeCollectorClient) Close() error {
	c.closeCalls++
	return nil
}

func assertStrings(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("string slice length mismatch: got %#v want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("string[%d] mismatch: got %q want %q", i, got[i], want[i])
		}
	}
}

func fakeNow(values ...time.Time) func() time.Time {
	index := 0
	return func() time.Time {
		if len(values) == 0 {
			return time.Now()
		}
		if index >= len(values) {
			return values[len(values)-1]
		}
		value := values[index]
		index++
		return value
	}
}
