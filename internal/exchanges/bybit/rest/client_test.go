package rest_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/VersoIt/Inquisitor/internal/exchanges"
	"github.com/VersoIt/Inquisitor/internal/exchanges/bybit/rest"
	domainlive "github.com/VersoIt/Inquisitor/internal/live"
)

func TestGetServerTime(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v5/market/time" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"retCode":0,"retMsg":"OK","result":{"timeSecond":"1717200000","timeNano":"1717200000000000000"},"time":1717200000000}`))
	}))
	defer server.Close()

	client, err := rest.New(server.URL)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	got, err := client.GetServerTime(context.Background())
	if err != nil {
		t.Fatalf("get server time: %v", err)
	}
	want := time.Unix(0, 1717200000000000000).UTC()
	if !got.Equal(want) {
		t.Fatalf("expected %s, got %s", want, got)
	}
}

func TestGetInstrumentsInfoMapsDomainModel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v5/market/instruments-info" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if r.URL.Query().Get("category") != "linear" {
			t.Fatalf("unexpected category query: %s", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"retCode":0,
			"retMsg":"OK",
			"result":{
				"category":"linear",
				"nextPageCursor":"",
				"list":[{
					"symbol":"BTCUSDT",
					"contractType":"LinearPerpetual",
					"status":"Trading",
					"baseCoin":"BTC",
					"quoteCoin":"USDT",
					"priceScale":"2",
					"leverageFilter":{"minLeverage":"1","maxLeverage":"100","leverageStep":"0.01"},
					"priceFilter":{"minPrice":"0.10","maxPrice":"999999.00","tickSize":"0.10"},
					"lotSizeFilter":{"maxOrderQty":"100.000","minOrderQty":"0.001","qtyStep":"0.001","maxMktOrderQty":"50.000","minNotionalValue":"5"}
				}]
			},
			"time":1717200000000
		}`))
	}))
	defer server.Close()

	client, err := rest.New(server.URL)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	instruments, err := client.GetInstrumentsInfo(context.Background(), exchanges.InstrumentsInfoRequest{
		Category: "linear",
		Symbol:   "BTCUSDT",
	})
	if err != nil {
		t.Fatalf("get instruments info: %v", err)
	}
	if len(instruments) != 1 {
		t.Fatalf("expected one instrument, got %d", len(instruments))
	}
	instrument := instruments[0]
	if instrument.Exchange != "bybit" || instrument.Symbol != "BTCUSDT" || instrument.BaseCoin != "BTC" {
		t.Fatalf("unexpected instrument: %#v", instrument)
	}
	if !instrument.TickSize.Equal(decimal.RequireFromString("0.10")) {
		t.Fatalf("unexpected tick size: %s", instrument.TickSize)
	}
	if !instrument.MinNotionalValue.Equal(decimal.RequireFromString("5")) {
		t.Fatalf("unexpected min notional: %s", instrument.MinNotionalValue)
	}
}

func TestGetInstrumentsInfoFollowsPaginationCursor(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/v5/market/instruments-info" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if r.URL.Query().Get("limit") != "1000" {
			t.Fatalf("expected limit=1000, got query %s", r.URL.RawQuery)
		}

		w.Header().Set("Content-Type", "application/json")
		switch calls {
		case 1:
			if r.URL.Query().Get("cursor") != "" {
				t.Fatalf("first request must not include cursor, got %s", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`{
				"retCode":0,
				"retMsg":"OK",
				"result":{
					"category":"linear",
					"nextPageCursor":"next-page",
					"list":[{
						"symbol":"BTCUSDT",
						"contractType":"LinearPerpetual",
						"status":"Trading",
						"baseCoin":"BTC",
						"quoteCoin":"USDT",
						"priceScale":"2",
						"leverageFilter":{"minLeverage":"1","maxLeverage":"100","leverageStep":"0.01"},
						"priceFilter":{"minPrice":"0.10","maxPrice":"999999.00","tickSize":"0.10"},
						"lotSizeFilter":{"maxOrderQty":"100.000","minOrderQty":"0.001","qtyStep":"0.001","maxMktOrderQty":"50.000","minNotionalValue":"5"}
					}]
				},
				"time":1717200000000
			}`))
		case 2:
			if r.URL.Query().Get("cursor") != "next-page" {
				t.Fatalf("second request must include cursor, got %s", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`{
				"retCode":0,
				"retMsg":"OK",
				"result":{
					"category":"linear",
					"nextPageCursor":"",
					"list":[{
						"symbol":"ETHUSDT",
						"contractType":"LinearPerpetual",
						"status":"Trading",
						"baseCoin":"ETH",
						"quoteCoin":"USDT",
						"priceScale":"2",
						"leverageFilter":{"minLeverage":"1","maxLeverage":"100","leverageStep":"0.01"},
						"priceFilter":{"minPrice":"0.01","maxPrice":"999999.00","tickSize":"0.01"},
						"lotSizeFilter":{"maxOrderQty":"1000.000","minOrderQty":"0.01","qtyStep":"0.01","maxMktOrderQty":"500.000","minNotionalValue":"5"}
					}]
				},
				"time":1717200000000
			}`))
		default:
			t.Fatalf("unexpected pagination call %d", calls)
		}
	}))
	defer server.Close()

	client, err := rest.New(server.URL)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	instruments, err := client.GetInstrumentsInfo(context.Background(), exchanges.InstrumentsInfoRequest{
		Category: "linear",
	})
	if err != nil {
		t.Fatalf("get instruments info: %v", err)
	}
	if calls != 2 {
		t.Fatalf("expected two pagination calls, got %d", calls)
	}
	if len(instruments) != 2 {
		t.Fatalf("expected two instruments, got %d", len(instruments))
	}
	if instruments[0].Symbol != "BTCUSDT" || instruments[1].Symbol != "ETHUSDT" {
		t.Fatalf("unexpected instruments: %#v", instruments)
	}
}

func TestGetKlinesMapsAndSortsCandles(t *testing.T) {
	now := time.Date(2026, 6, 5, 0, 5, 0, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v5/market/kline" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if r.URL.Query().Get("symbol") != "BTCUSDT" {
			t.Fatalf("unexpected symbol query: %s", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"retCode":0,
			"retMsg":"OK",
			"result":{
				"category":"linear",
				"symbol":"BTCUSDT",
				"list":[
					["1717200060000","101","111","91","106","11","1111"],
					["1717200000000","100","110","90","105","10","1000"]
				]
			},
			"time":1717200000000
		}`))
	}))
	defer server.Close()

	client, err := rest.New(server.URL, rest.WithClock(func() time.Time { return now }))
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	candles, err := client.GetKlines(context.Background(), exchanges.KlinesRequest{
		Category: "linear",
		Symbol:   "BTCUSDT",
		Interval: "1",
		Limit:    2,
	})
	if err != nil {
		t.Fatalf("get klines: %v", err)
	}
	if len(candles) != 2 {
		t.Fatalf("expected two candles, got %d", len(candles))
	}
	if !candles[0].OpenTime.Before(candles[1].OpenTime) {
		t.Fatalf("candles were not sorted ascending: %#v", candles)
	}
	if candles[0].Exchange != "bybit" || candles[0].Category != "linear" || candles[0].Symbol != "BTCUSDT" {
		t.Fatalf("unexpected candle identity: %#v", candles[0])
	}
	if !candles[0].Close.Equal(decimal.RequireFromString("105")) {
		t.Fatalf("unexpected close: %s", candles[0].Close)
	}
}

func TestGetKlinesSendsTimeRangeAndLimitQueryParams(t *testing.T) {
	start := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v5/market/kline" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		query := r.URL.Query()
		if query.Get("category") != "linear" || query.Get("symbol") != "BTCUSDT" || query.Get("interval") != "1" {
			t.Fatalf("unexpected identity query params: %s", r.URL.RawQuery)
		}
		if query.Get("start") != "1780272000000" {
			t.Fatalf("unexpected start query param: %s", r.URL.RawQuery)
		}
		if query.Get("end") != "1780275600000" {
			t.Fatalf("unexpected end query param: %s", r.URL.RawQuery)
		}
		if query.Get("limit") != "500" {
			t.Fatalf("unexpected limit query param: %s", r.URL.RawQuery)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"retCode":0,
			"retMsg":"OK",
			"result":{"category":"linear","symbol":"BTCUSDT","list":[]},
			"time":1717200000000
		}`))
	}))
	defer server.Close()

	client, err := rest.New(server.URL)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	candles, err := client.GetKlines(context.Background(), exchanges.KlinesRequest{
		Category: "linear",
		Symbol:   "BTCUSDT",
		Interval: "1",
		Start:    start,
		End:      end,
		Limit:    500,
	})
	if err != nil {
		t.Fatalf("get klines: %v", err)
	}
	if len(candles) != 0 {
		t.Fatalf("expected no candles, got %d", len(candles))
	}
}

func TestClientRetriesRateLimitedRequests(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"retCode":0,"retMsg":"OK","result":{"timeSecond":"1717200000","timeNano":"1717200000000000000"},"time":1717200000000}`))
	}))
	defer server.Close()

	client, err := rest.New(server.URL, rest.WithRetry(1, time.Nanosecond))
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	if _, err := client.GetServerTime(context.Background()); err != nil {
		t.Fatalf("expected retry to recover request, got %v", err)
	}
	if calls != 2 {
		t.Fatalf("expected two calls, got %d", calls)
	}
}

func TestClientNormalizesBybitErrorEnvelope(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"retCode":10001,"retMsg":"invalid category","result":{},"time":1717200000000}`))
	}))
	defer server.Close()

	client, err := rest.New(server.URL)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	_, err = client.GetServerTime(context.Background())
	if err == nil {
		t.Fatal("expected exchange error")
	}

	var exchangeErr exchanges.ExchangeError
	if !errors.As(err, &exchangeErr) {
		t.Fatalf("expected ExchangeError, got %T: %v", err, err)
	}
	if exchangeErr.Exchange != "bybit" || exchangeErr.RetCode != 10001 || exchangeErr.RetMsg != "invalid category" {
		t.Fatalf("unexpected exchange error: %#v", exchangeErr)
	}
}

func TestSubmitOrderSignsAndMapsLiveOrdersTableDriven(t *testing.T) {
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name       string
		submission domainlive.OrderSubmission
		want       map[string]any
		wantAbsent []string
	}{
		{
			name:       "long market entry with protective stops",
			submission: testBybitLiveOrderSubmission(now),
			want: map[string]any{
				"category":    "linear",
				"symbol":      "BTCUSDT",
				"side":        "Buy",
				"orderType":   "Market",
				"qty":         "0.25",
				"timeInForce": "IOC",
				"positionIdx": float64(0),
				"orderLinkId": "live_client_bybit_0001",
				"reduceOnly":  false,
				"takeProfit":  "102000",
				"stopLoss":    "98000",
			},
			wantAbsent: []string{"price"},
		},
		{
			name: "short post-only limit entry",
			submission: func() domainlive.OrderSubmission {
				submission := testBybitLiveOrderSubmission(now)
				submission.Side = domainlive.OrderSideShort
				submission.Type = domainlive.OrderTypeLimit
				submission.TimeInForce = domainlive.TimeInForcePostOnly
				submission.LimitPrice = decimal.RequireFromString("99950")
				submission.StopLoss = decimal.RequireFromString("102000")
				submission.TakeProfit = decimal.RequireFromString("97000")
				return submission
			}(),
			want: map[string]any{
				"side":        "Sell",
				"orderType":   "Limit",
				"price":       "99950",
				"timeInForce": "PostOnly",
				"takeProfit":  "97000",
				"stopLoss":    "102000",
				"reduceOnly":  false,
			},
		},
		{
			name: "reduce only long close reverses side and omits stops",
			submission: func() domainlive.OrderSubmission {
				submission := testBybitLiveOrderSubmission(now)
				submission.ReduceOnly = true
				submission.StopLoss = decimal.Zero
				submission.TakeProfit = decimal.Zero
				submission.MaxLoss = decimal.Zero
				submission.Reason = "position_exit"
				return submission
			}(),
			want: map[string]any{
				"side":       "Sell",
				"orderType":  "Market",
				"reduceOnly": true,
			},
			wantAbsent: []string{"takeProfit", "stopLoss", "price"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var sawRequest bool
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				sawRequest = true
				if r.Method != http.MethodPost || r.URL.Path != "/v5/order/create" {
					t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
				}
				bodyBytes, err := io.ReadAll(r.Body)
				if err != nil {
					t.Fatalf("read request body: %v", err)
				}
				body := string(bodyBytes)
				if r.Header.Get("X-BAPI-API-KEY") != "api-key" {
					t.Fatalf("api key header mismatch")
				}
				timestamp := r.Header.Get("X-BAPI-TIMESTAMP")
				recvWindow := r.Header.Get("X-BAPI-RECV-WINDOW")
				if timestamp != "1784548800000" || recvWindow != "5000" {
					t.Fatalf("timestamp/recv window mismatch: timestamp=%q recv=%q", timestamp, recvWindow)
				}
				wantSignature := testBybitHMAC("api-secret", timestamp+"api-key"+recvWindow+body)
				if r.Header.Get("X-BAPI-SIGN") != wantSignature {
					t.Fatalf("signature mismatch: got %q want %q body=%s", r.Header.Get("X-BAPI-SIGN"), wantSignature, body)
				}

				var got map[string]any
				if err := json.Unmarshal(bodyBytes, &got); err != nil {
					t.Fatalf("decode create order request: %v", err)
				}
				for key, want := range tt.want {
					if got[key] != want {
						t.Fatalf("payload[%s] mismatch: got %#v want %#v full=%#v", key, got[key], want, got)
					}
				}
				for _, key := range tt.wantAbsent {
					if _, exists := got[key]; exists {
						t.Fatalf("payload must omit %s: %#v", key, got)
					}
				}
				if strings.Contains(body, "api-secret") {
					t.Fatalf("request body must not contain API secret: %s", body)
				}

				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"retCode":0,"retMsg":"OK","result":{"orderId":"bybit_order_0001","orderLinkId":"live_client_bybit_0001"},"time":1784548800000}`))
			}))
			defer server.Close()

			client, err := rest.New(
				server.URL,
				rest.WithHMACAuth("api-key", "api-secret"),
				rest.WithClock(func() time.Time { return now }),
				rest.WithRetry(0, time.Nanosecond),
			)
			if err != nil {
				t.Fatalf("new client: %v", err)
			}

			ack, err := client.SubmitOrder(context.Background(), tt.submission)
			if err != nil {
				t.Fatalf("submit order: %v", err)
			}
			if !sawRequest {
				t.Fatal("expected mock server to receive create order request")
			}
			if ack.SubmissionID != tt.submission.SubmissionID ||
				ack.ClientOrderID != tt.submission.ClientOrderID ||
				ack.ExchangeOrderID != "bybit_order_0001" ||
				ack.Exchange != "bybit" ||
				ack.Status != domainlive.OrderStatusAccepted ||
				ack.ReceivedAt != now {
				t.Fatalf("acknowledgement mismatch: %#v", ack)
			}
		})
	}
}

func TestSubmitOrderRequiresPrivateCredentialsBeforeHTTPRequest(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		calls++
	}))
	defer server.Close()

	client, err := rest.New(server.URL, rest.WithClock(func() time.Time {
		return time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	}))
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	_, err = client.SubmitOrder(context.Background(), testBybitLiveOrderSubmission(time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)))
	if err == nil || !strings.Contains(err.Error(), "API key") {
		t.Fatalf("expected missing credentials error, got %v", err)
	}
	if calls != 0 {
		t.Fatalf("missing credentials must block before HTTP request, got calls=%d", calls)
	}
}

func TestSubmitOrderNormalizesBybitCreateOrderErrorEnvelope(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"retCode":110007,"retMsg":"ab not enough for new order","result":{},"time":1784548800000}`))
	}))
	defer server.Close()

	client, err := rest.New(
		server.URL,
		rest.WithHMACAuth("api-key", "api-secret"),
		rest.WithClock(func() time.Time { return time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC) }),
		rest.WithRetry(0, time.Nanosecond),
	)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	_, err = client.SubmitOrder(context.Background(), testBybitLiveOrderSubmission(time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)))
	if err == nil {
		t.Fatal("expected exchange error")
	}

	var exchangeErr exchanges.ExchangeError
	if !errors.As(err, &exchangeErr) {
		t.Fatalf("expected ExchangeError, got %T: %v", err, err)
	}
	if exchangeErr.Exchange != "bybit" || exchangeErr.RetCode != 110007 || exchangeErr.RetMsg != "ab not enough for new order" {
		t.Fatalf("unexpected exchange error: %#v", exchangeErr)
	}
}

func TestGetOrderStatusSignsAuthenticatedQueryAndMapsResponse(t *testing.T) {
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	var sawRequest bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawRequest = true
		if r.Method != http.MethodGet || r.URL.Path != "/v5/order/realtime" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
		if r.URL.Query().Get("category") != "linear" ||
			r.URL.Query().Get("symbol") != "BTCUSDT" ||
			r.URL.Query().Get("orderLinkId") != "live_client_bybit_0001" ||
			r.URL.Query().Get("limit") != "1" {
			t.Fatalf("query mismatch: %s", r.URL.RawQuery)
		}
		timestamp := r.Header.Get("X-BAPI-TIMESTAMP")
		recvWindow := r.Header.Get("X-BAPI-RECV-WINDOW")
		if timestamp != "1784721600000" || recvWindow != "5000" {
			t.Fatalf("timestamp/recv window mismatch: timestamp=%q recv=%q", timestamp, recvWindow)
		}
		wantSignature := testBybitHMAC("api-secret", timestamp+"api-key"+recvWindow+r.URL.RawQuery)
		if r.Header.Get("X-BAPI-SIGN") != wantSignature {
			t.Fatalf("signature mismatch: got %q want %q query=%s", r.Header.Get("X-BAPI-SIGN"), wantSignature, r.URL.RawQuery)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"retCode":0,
			"retMsg":"OK",
			"result":{
				"category":"linear",
				"list":[{
					"orderId":"bybit_order_0001",
					"orderLinkId":"live_client_bybit_0001",
					"symbol":"BTCUSDT",
					"price":"0",
					"qty":"0.25",
					"side":"Buy",
					"orderStatus":"Filled",
					"avgPrice":"100001",
					"leavesQty":"0",
					"cumExecQty":"0.25",
					"cumExecValue":"25000.25",
					"cumExecFee":"15",
					"timeInForce":"IOC",
					"orderType":"Market",
					"rejectReason":"EC_NoError",
					"reduceOnly":false,
					"createdTime":"1784721599000",
					"updatedTime":"1784721600000"
				}]
			},
			"time":1784721600000
		}`))
	}))
	defer server.Close()

	client, err := rest.New(
		server.URL,
		rest.WithHMACAuth("api-key", "api-secret"),
		rest.WithClock(func() time.Time { return now }),
		rest.WithRetry(0, time.Nanosecond),
	)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	got, err := client.GetOrderStatus(context.Background(), domainlive.OrderStatusQuery{
		Exchange:      "bybit",
		Category:      "linear",
		Symbol:        "BTCUSDT",
		ClientOrderID: "live_client_bybit_0001",
	})
	if err != nil {
		t.Fatalf("get order status: %v", err)
	}
	if !sawRequest {
		t.Fatal("expected mock server to receive order status request")
	}
	if got.ClientOrderID != "live_client_bybit_0001" ||
		got.ExchangeOrderID != "bybit_order_0001" ||
		got.ExchangeStatus != domainlive.ExchangeOrderStatusFilled ||
		got.Side != domainlive.OrderSideLong ||
		got.Type != domainlive.OrderTypeMarket ||
		got.TimeInForce != domainlive.TimeInForceIOC ||
		!got.Quantity.Equal(decimal.RequireFromString("0.25")) ||
		!got.CumulativeExecutedQuantity.Equal(decimal.RequireFromString("0.25")) ||
		got.ObservedAt != now {
		t.Fatalf("status snapshot mismatch: %#v", got)
	}
}

func TestGetOrderStatusRejectsMissingCredentialsBeforeHTTPRequest(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		calls++
	}))
	defer server.Close()

	client, err := rest.New(server.URL)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	_, err = client.GetOrderStatus(context.Background(), domainlive.OrderStatusQuery{
		Exchange:      "bybit",
		Category:      "linear",
		Symbol:        "BTCUSDT",
		ClientOrderID: "live_client_bybit_0001",
	})
	if err == nil || !strings.Contains(err.Error(), "API key") {
		t.Fatalf("expected missing credentials error, got %v", err)
	}
	if calls != 0 {
		t.Fatalf("missing credentials must block before HTTP request, got calls=%d", calls)
	}
}

func TestGetOrderStatusRejectsUnsafeExchangePayloadsTableDriven(t *testing.T) {
	tests := []struct {
		name       string
		resultJSON string
		wantErrSub string
	}{
		{
			name:       "not found",
			resultJSON: `{"category":"linear","list":[]}`,
			wantErrSub: "not found",
		},
		{
			name: "unknown status",
			resultJSON: `{"category":"linear","list":[{
				"orderId":"bybit_order_0001",
				"orderLinkId":"live_client_bybit_0001",
				"symbol":"BTCUSDT",
				"price":"0",
				"qty":"0.25",
				"side":"Buy",
				"orderStatus":"Pending",
				"avgPrice":"0",
				"leavesQty":"0.25",
				"cumExecQty":"0",
				"cumExecValue":"0",
				"cumExecFee":"0",
				"timeInForce":"IOC",
				"orderType":"Market",
				"rejectReason":"EC_NoError",
				"createdTime":"1784721599000",
				"updatedTime":"1784721600000"
			}]}`,
			wantErrSub: "orderStatus",
		},
		{
			name: "bad quantity decimal",
			resultJSON: `{"category":"linear","list":[{
				"orderId":"bybit_order_0001",
				"orderLinkId":"live_client_bybit_0001",
				"symbol":"BTCUSDT",
				"price":"0",
				"qty":"nope",
				"side":"Buy",
				"orderStatus":"New",
				"avgPrice":"0",
				"leavesQty":"0.25",
				"cumExecQty":"0",
				"cumExecValue":"0",
				"cumExecFee":"0",
				"timeInForce":"IOC",
				"orderType":"Market",
				"rejectReason":"EC_NoError",
				"createdTime":"1784721599000",
				"updatedTime":"1784721600000"
			}]}`,
			wantErrSub: "qty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"retCode":0,"retMsg":"OK","result":` + tt.resultJSON + `,"time":1784721600000}`))
			}))
			defer server.Close()

			client, err := rest.New(
				server.URL,
				rest.WithHMACAuth("api-key", "api-secret"),
				rest.WithClock(func() time.Time { return time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC) }),
				rest.WithRetry(0, time.Nanosecond),
			)
			if err != nil {
				t.Fatalf("new client: %v", err)
			}

			_, err = client.GetOrderStatus(context.Background(), domainlive.OrderStatusQuery{
				Exchange:      "bybit",
				Category:      "linear",
				Symbol:        "BTCUSDT",
				ClientOrderID: "live_client_bybit_0001",
			})
			if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
		})
	}
}

func TestGetPositionSnapshotSignsAuthenticatedQueryAndMapsOpenPosition(t *testing.T) {
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	var sawRequest bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawRequest = true
		if r.Method != http.MethodGet || r.URL.Path != "/v5/position/list" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
		if r.URL.Query().Get("category") != "linear" ||
			r.URL.Query().Get("symbol") != "BTCUSDT" ||
			r.URL.Query().Get("limit") != "20" {
			t.Fatalf("query mismatch: %s", r.URL.RawQuery)
		}
		timestamp := r.Header.Get("X-BAPI-TIMESTAMP")
		recvWindow := r.Header.Get("X-BAPI-RECV-WINDOW")
		if timestamp != "1784721600000" || recvWindow != "5000" {
			t.Fatalf("timestamp/recv window mismatch: timestamp=%q recv=%q", timestamp, recvWindow)
		}
		wantSignature := testBybitHMAC("api-secret", timestamp+"api-key"+recvWindow+r.URL.RawQuery)
		if r.Header.Get("X-BAPI-SIGN") != wantSignature {
			t.Fatalf("signature mismatch: got %q want %q query=%s", r.Header.Get("X-BAPI-SIGN"), wantSignature, r.URL.RawQuery)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"retCode":0,
			"retMsg":"OK",
			"result":{
				"category":"linear",
				"list":[{
					"positionIdx":0,
					"symbol":"BTCUSDT",
					"side":"Buy",
					"size":"0.25",
					"avgPrice":"100001",
					"positionValue":"25000.25",
					"positionStatus":"Normal",
					"leverage":"1",
					"markPrice":"100100",
					"liqPrice":"50000",
					"unrealisedPnl":"24.75",
					"curRealisedPnl":"-15",
					"cumRealisedPnl":"10",
					"seq":12345,
					"isReduceOnly":false,
					"createdTime":"1784721599000",
					"updatedTime":"1784721600000"
				}]
			},
			"time":1784721600000
		}`))
	}))
	defer server.Close()

	client, err := rest.New(
		server.URL,
		rest.WithHMACAuth("api-key", "api-secret"),
		rest.WithClock(func() time.Time { return now }),
		rest.WithRetry(0, time.Nanosecond),
	)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	got, err := client.GetPositionSnapshot(context.Background(), domainlive.PositionSnapshotQuery{
		Exchange: "bybit",
		Category: "linear",
		Symbol:   "BTCUSDT",
	})
	if err != nil {
		t.Fatalf("get position snapshot: %v", err)
	}
	if !sawRequest {
		t.Fatal("expected mock server to receive position request")
	}
	if !got.Open ||
		got.Side != domainlive.OrderSideLong ||
		got.ExchangeStatus != domainlive.ExchangePositionStatusNormal ||
		!got.Size.Equal(decimal.RequireFromString("0.25")) ||
		!got.AveragePrice.Equal(decimal.RequireFromString("100001")) ||
		!got.UnrealisedPnL.Equal(decimal.RequireFromString("24.75")) ||
		got.Sequence != 12345 ||
		got.ObservedAt != now {
		t.Fatalf("position snapshot mismatch: %#v", got)
	}
}

func TestGetPositionSnapshotMapsFlatBybitPosition(t *testing.T) {
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"retCode":0,
			"retMsg":"OK",
			"result":{
				"category":"linear",
				"list":[{
					"positionIdx":0,
					"symbol":"BTCUSDT",
					"side":"",
					"size":"0",
					"avgPrice":"",
					"positionValue":"0",
					"positionStatus":"Normal",
					"leverage":"",
					"markPrice":"100100",
					"liqPrice":"",
					"unrealisedPnl":"0",
					"curRealisedPnl":"0",
					"cumRealisedPnl":"0",
					"seq":-1,
					"isReduceOnly":false,
					"createdTime":"",
					"updatedTime":""
				}]
			},
			"time":1784721600000
		}`))
	}))
	defer server.Close()

	client, err := rest.New(
		server.URL,
		rest.WithHMACAuth("api-key", "api-secret"),
		rest.WithClock(func() time.Time { return now }),
		rest.WithRetry(0, time.Nanosecond),
	)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	got, err := client.GetPositionSnapshot(context.Background(), domainlive.PositionSnapshotQuery{
		Exchange: "bybit",
		Category: "linear",
		Symbol:   "BTCUSDT",
	})
	if err != nil {
		t.Fatalf("get flat position snapshot: %v", err)
	}
	if got.Open || got.Side != "" || !got.Size.IsZero() || got.ExchangeStatus != domainlive.ExchangePositionStatusNormal {
		t.Fatalf("flat position snapshot mismatch: %#v", got)
	}
}

func TestGetPositionSnapshotRejectsMissingCredentialsBeforeHTTPRequest(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		calls++
	}))
	defer server.Close()

	client, err := rest.New(server.URL)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	_, err = client.GetPositionSnapshot(context.Background(), domainlive.PositionSnapshotQuery{
		Exchange: "bybit",
		Category: "linear",
		Symbol:   "BTCUSDT",
	})
	if err == nil || !strings.Contains(err.Error(), "API key") {
		t.Fatalf("expected missing credentials error, got %v", err)
	}
	if calls != 0 {
		t.Fatalf("missing credentials must block before HTTP request, got calls=%d", calls)
	}
}

func TestGetPositionSnapshotRejectsUnsafeExchangePayloadsTableDriven(t *testing.T) {
	tests := []struct {
		name       string
		resultJSON string
		wantErrSub string
	}{
		{
			name:       "not found",
			resultJSON: `{"category":"linear","list":[]}`,
			wantErrSub: "not found",
		},
		{
			name: "multiple rows",
			resultJSON: `{"category":"linear","list":[
				{"positionIdx":1,"symbol":"BTCUSDT","side":"Buy","size":"0.1","avgPrice":"100000","positionValue":"10000","positionStatus":"Normal","createdTime":"1784721599000","updatedTime":"1784721600000"},
				{"positionIdx":2,"symbol":"BTCUSDT","side":"Sell","size":"0","avgPrice":"","positionValue":"0","positionStatus":"Normal","createdTime":"","updatedTime":""}
			]}`,
			wantErrSub: "not unique",
		},
		{
			name: "unknown side",
			resultJSON: `{"category":"linear","list":[{
				"positionIdx":0,
				"symbol":"BTCUSDT",
				"side":"Both",
				"size":"0.25",
				"avgPrice":"100001",
				"positionValue":"25000.25",
				"positionStatus":"Normal",
				"createdTime":"1784721599000",
				"updatedTime":"1784721600000"
			}]}`,
			wantErrSub: "side",
		},
		{
			name: "unknown status",
			resultJSON: `{"category":"linear","list":[{
				"positionIdx":0,
				"symbol":"BTCUSDT",
				"side":"Buy",
				"size":"0.25",
				"avgPrice":"100001",
				"positionValue":"25000.25",
				"positionStatus":"Paused",
				"createdTime":"1784721599000",
				"updatedTime":"1784721600000"
			}]}`,
			wantErrSub: "positionStatus",
		},
		{
			name: "bad size decimal",
			resultJSON: `{"category":"linear","list":[{
				"positionIdx":0,
				"symbol":"BTCUSDT",
				"side":"Buy",
				"size":"nope",
				"avgPrice":"100001",
				"positionValue":"25000.25",
				"positionStatus":"Normal",
				"createdTime":"1784721599000",
				"updatedTime":"1784721600000"
			}]}`,
			wantErrSub: "size",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"retCode":0,"retMsg":"OK","result":` + tt.resultJSON + `,"time":1784721600000}`))
			}))
			defer server.Close()

			client, err := rest.New(
				server.URL,
				rest.WithHMACAuth("api-key", "api-secret"),
				rest.WithClock(func() time.Time { return time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC) }),
				rest.WithRetry(0, time.Nanosecond),
			)
			if err != nil {
				t.Fatalf("new client: %v", err)
			}

			_, err = client.GetPositionSnapshot(context.Background(), domainlive.PositionSnapshotQuery{
				Exchange: "bybit",
				Category: "linear",
				Symbol:   "BTCUSDT",
			})
			if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
		})
	}
}

func TestGetAccountSnapshotSignsAuthenticatedQueryAndMapsUnifiedWalletBalance(t *testing.T) {
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	var sawRequest bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawRequest = true
		if r.Method != http.MethodGet || r.URL.Path != "/v5/account/wallet-balance" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
		if r.URL.Query().Get("accountType") != "UNIFIED" {
			t.Fatalf("query mismatch: %s", r.URL.RawQuery)
		}
		timestamp := r.Header.Get("X-BAPI-TIMESTAMP")
		recvWindow := r.Header.Get("X-BAPI-RECV-WINDOW")
		if timestamp != "1784721600000" || recvWindow != "5000" {
			t.Fatalf("timestamp/recv window mismatch: timestamp=%q recv=%q", timestamp, recvWindow)
		}
		wantSignature := testBybitHMAC("api-secret", timestamp+"api-key"+recvWindow+r.URL.RawQuery)
		if r.Header.Get("X-BAPI-SIGN") != wantSignature {
			t.Fatalf("signature mismatch: got %q want %q query=%s", r.Header.Get("X-BAPI-SIGN"), wantSignature, r.URL.RawQuery)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"retCode":0,
			"retMsg":"OK",
			"result":{
				"list":[{
					"accountType":"UNIFIED",
					"totalEquity":"50.25",
					"totalWalletBalance":"50.25",
					"totalMarginBalance":"50.25",
					"totalAvailableBalance":"50.25",
					"totalPerpUPL":"0",
					"totalInitialMargin":"0",
					"totalMaintenanceMargin":"0",
					"coin":[{
						"coin":"USDT",
						"equity":"50.25",
						"usdValue":"50.25",
						"walletBalance":"50.25",
						"locked":"0",
						"borrowAmount":"0",
						"accruedInterest":"0",
						"totalOrderIM":"0",
						"totalPositionIM":"0",
						"totalPositionMM":"0",
						"unrealisedPnl":"0",
						"cumRealisedPnl":"0",
						"spotBorrow":"0",
						"marginCollateral":true,
						"collateralSwitch":true
					}]
				}]
			},
			"time":1784721600000
		}`))
	}))
	defer server.Close()

	client, err := rest.New(
		server.URL,
		rest.WithHMACAuth("api-key", "api-secret"),
		rest.WithClock(func() time.Time { return now }),
		rest.WithRetry(0, time.Nanosecond),
	)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	got, err := client.GetAccountSnapshot(context.Background(), domainlive.AccountSnapshotQuery{
		Exchange:    "bybit",
		AccountType: domainlive.AccountTypeUnified,
	})
	if err != nil {
		t.Fatalf("get account snapshot: %v", err)
	}
	if !sawRequest {
		t.Fatal("expected mock server to receive account request")
	}
	if got.Exchange != "bybit" ||
		got.AccountType != domainlive.AccountTypeUnified ||
		!got.TotalEquity.Equal(decimal.RequireFromString("50.25")) ||
		!got.TotalAvailableBalance.Equal(decimal.RequireFromString("50.25")) ||
		got.ObservedAt != now ||
		len(got.Coins) != 1 ||
		got.Coins[0].Coin != "USDT" ||
		!got.Coins[0].WalletBalance.Equal(decimal.RequireFromString("50.25")) ||
		!got.Coins[0].MarginCollateral ||
		!got.Coins[0].CollateralSwitch {
		t.Fatalf("account snapshot mismatch: %#v", got)
	}
}

func TestGetAccountSnapshotRejectsMissingCredentialsBeforeHTTPRequest(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		calls++
	}))
	defer server.Close()

	client, err := rest.New(server.URL)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	_, err = client.GetAccountSnapshot(context.Background(), domainlive.AccountSnapshotQuery{
		Exchange:    "bybit",
		AccountType: domainlive.AccountTypeUnified,
	})
	if err == nil || !strings.Contains(err.Error(), "API key") {
		t.Fatalf("expected missing credentials error, got %v", err)
	}
	if calls != 0 {
		t.Fatalf("missing credentials must block before HTTP request, got calls=%d", calls)
	}
}

func TestGetAccountSnapshotRejectsUnsafeExchangePayloadsTableDriven(t *testing.T) {
	tests := []struct {
		name       string
		resultJSON string
		wantErrSub string
	}{
		{
			name:       "not found",
			resultJSON: `{"list":[]}`,
			wantErrSub: "not found",
		},
		{
			name: "multiple rows",
			resultJSON: `{"list":[
				{"accountType":"UNIFIED","totalEquity":"50","totalInitialMargin":"0","totalMaintenanceMargin":"0"},
				{"accountType":"UNIFIED","totalEquity":"50","totalInitialMargin":"0","totalMaintenanceMargin":"0"}
			]}`,
			wantErrSub: "not unique",
		},
		{
			name: "unsupported account type",
			resultJSON: `{"list":[{
				"accountType":"SPOT",
				"totalEquity":"50",
				"totalInitialMargin":"0",
				"totalMaintenanceMargin":"0"
			}]}`,
			wantErrSub: "account_type",
		},
		{
			name: "bad total equity decimal",
			resultJSON: `{"list":[{
				"accountType":"UNIFIED",
				"totalEquity":"nope",
				"totalInitialMargin":"0",
				"totalMaintenanceMargin":"0"
			}]}`,
			wantErrSub: "totalEquity",
		},
		{
			name: "negative borrow amount",
			resultJSON: `{"list":[{
				"accountType":"UNIFIED",
				"totalEquity":"50",
				"totalInitialMargin":"0",
				"totalMaintenanceMargin":"0",
				"coin":[{
					"coin":"USDT",
					"borrowAmount":"-1"
				}]
			}]}`,
			wantErrSub: "borrow_amount",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"retCode":0,"retMsg":"OK","result":` + tt.resultJSON + `,"time":1784721600000}`))
			}))
			defer server.Close()

			client, err := rest.New(
				server.URL,
				rest.WithHMACAuth("api-key", "api-secret"),
				rest.WithClock(func() time.Time { return time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC) }),
				rest.WithRetry(0, time.Nanosecond),
			)
			if err != nil {
				t.Fatalf("new client: %v", err)
			}

			_, err = client.GetAccountSnapshot(context.Background(), domainlive.AccountSnapshotQuery{
				Exchange:    "bybit",
				AccountType: domainlive.AccountTypeUnified,
			})
			if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
		})
	}
}

func TestGetKlinesRejectsMalformedKlineRows(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"retCode":0,
			"retMsg":"OK",
			"result":{
				"category":"linear",
				"symbol":"BTCUSDT",
				"list":[["1717200000000","100"]]
			},
			"time":1717200000000
		}`))
	}))
	defer server.Close()

	client, err := rest.New(server.URL)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	_, err = client.GetKlines(context.Background(), exchanges.KlinesRequest{
		Category: "linear",
		Symbol:   "BTCUSDT",
		Interval: "1",
	})
	if err == nil {
		t.Fatal("expected malformed kline error")
	}
	if !strings.Contains(err.Error(), "expected 7 fields") {
		t.Fatalf("expected field count error, got %v", err)
	}
}

func testBybitLiveOrderSubmission(now time.Time) domainlive.OrderSubmission {
	return domainlive.OrderSubmission{
		SubmissionID:     "live_submission_bybit_0001",
		ClientOrderID:    "live_client_bybit_0001",
		DecisionID:       "risk_decision_bybit_0001",
		DecisionApproved: true,
		IntentID:         "risk_intent_bybit_0001",
		RiskMode:         domainlive.RiskModeLive,
		Exchange:         "bybit",
		Category:         "linear",
		Symbol:           "BTCUSDT",
		Side:             domainlive.OrderSideLong,
		Type:             domainlive.OrderTypeMarket,
		TimeInForce:      domainlive.TimeInForceIOC,
		Quantity:         decimal.RequireFromString("0.25"),
		ReferencePrice:   decimal.RequireFromString("100000"),
		StopLoss:         decimal.RequireFromString("98000"),
		TakeProfit:       decimal.RequireFromString("102000"),
		Leverage:         decimal.RequireFromString("1"),
		MaxLoss:          decimal.RequireFromString("500"),
		Notional:         decimal.RequireFromString("25000"),
		Confidence:       80,
		Reason:           "risk_checks_passed",
		CreatedAt:        now,
	}
}

func testBybitHMAC(secret string, payload string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(payload))
	return hex.EncodeToString(mac.Sum(nil))
}
