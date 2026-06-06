package rest_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/VersoIt/Inquisitor/internal/exchanges"
	"github.com/VersoIt/Inquisitor/internal/exchanges/bybit/rest"
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
