package rest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/VersoIt/Inquisitor/internal/exchanges"
	"github.com/VersoIt/Inquisitor/internal/marketdata"
	"github.com/VersoIt/Inquisitor/internal/marketdata/validator"
)

const exchangeName = "bybit"

type Client struct {
	baseURL    *url.URL
	httpClient *http.Client
	maxRetries int
	backoff    time.Duration
	now        func() time.Time
}

type Option func(*Client)

func WithHTTPClient(client *http.Client) Option {
	return func(c *Client) {
		if client != nil {
			c.httpClient = client
		}
	}
}

func WithRetry(maxRetries int, backoff time.Duration) Option {
	return func(c *Client) {
		if maxRetries >= 0 {
			c.maxRetries = maxRetries
		}
		if backoff > 0 {
			c.backoff = backoff
		}
	}
}

func WithClock(now func() time.Time) Option {
	return func(c *Client) {
		if now != nil {
			c.now = now
		}
	}
}

func New(baseURL string, options ...Option) (*Client, error) {
	parsed, err := url.Parse(strings.TrimRight(baseURL, "/"))
	if err != nil {
		return nil, fmt.Errorf("parse bybit base url: %w", err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("bybit base url must be absolute")
	}

	client := &Client{
		baseURL: parsed,
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
		},
		maxRetries: 2,
		backoff:    250 * time.Millisecond,
		now:        func() time.Time { return time.Now().UTC() },
	}
	for _, option := range options {
		option(client)
	}

	return client, nil
}

func (c *Client) GetServerTime(ctx context.Context) (time.Time, error) {
	var result serverTimeResult
	if err := c.get(ctx, "/v5/market/time", nil, &result); err != nil {
		return time.Time{}, err
	}

	if result.TimeNano != "" {
		nano, err := strconv.ParseInt(result.TimeNano, 10, 64)
		if err != nil {
			return time.Time{}, fmt.Errorf("parse bybit server timeNano: %w", err)
		}
		return time.Unix(0, nano).UTC(), nil
	}
	if result.TimeSecond != "" {
		seconds, err := strconv.ParseInt(result.TimeSecond, 10, 64)
		if err != nil {
			return time.Time{}, fmt.Errorf("parse bybit server timeSecond: %w", err)
		}
		return time.Unix(seconds, 0).UTC(), nil
	}

	return time.Time{}, errors.New("bybit server time response did not include timeNano or timeSecond")
}

func (c *Client) GetInstrumentsInfo(ctx context.Context, req exchanges.InstrumentsInfoRequest) ([]marketdata.Instrument, error) {
	if strings.TrimSpace(req.Category) == "" {
		return nil, errors.New("category is required")
	}

	query := url.Values{}
	query.Set("category", req.Category)
	if req.Symbol != "" {
		query.Set("symbol", req.Symbol)
	}

	var instruments []marketdata.Instrument
	cursor := ""
	for {
		pageQuery := cloneValues(query)
		pageQuery.Set("limit", "1000")
		if cursor != "" {
			pageQuery.Set("cursor", cursor)
		}

		var result instrumentsInfoResult
		if err := c.get(ctx, "/v5/market/instruments-info", pageQuery, &result); err != nil {
			return nil, err
		}

		for _, item := range result.List {
			instrument, err := mapInstrument(req.Category, item)
			if err != nil {
				return nil, err
			}
			instruments = append(instruments, instrument)
		}

		cursor = result.NextPageCursor
		if cursor == "" || req.Symbol != "" {
			break
		}
	}

	return instruments, nil
}

func (c *Client) GetKlines(ctx context.Context, req exchanges.KlinesRequest) ([]marketdata.Candle, error) {
	if strings.TrimSpace(req.Category) == "" {
		return nil, errors.New("category is required")
	}
	if strings.TrimSpace(req.Symbol) == "" {
		return nil, errors.New("symbol is required")
	}
	if strings.TrimSpace(req.Interval) == "" {
		return nil, errors.New("interval is required")
	}

	query := url.Values{}
	query.Set("category", req.Category)
	query.Set("symbol", req.Symbol)
	query.Set("interval", req.Interval)
	if !req.Start.IsZero() {
		query.Set("start", strconv.FormatInt(req.Start.UTC().UnixMilli(), 10))
	}
	if !req.End.IsZero() {
		query.Set("end", strconv.FormatInt(req.End.UTC().UnixMilli(), 10))
	}
	if req.Limit > 0 {
		query.Set("limit", strconv.Itoa(req.Limit))
	}

	var result klineResult
	if err := c.get(ctx, "/v5/market/kline", query, &result); err != nil {
		return nil, err
	}

	candles := make([]marketdata.Candle, 0, len(result.List))
	for _, item := range result.List {
		candle, err := c.mapKline(req, item)
		if err != nil {
			return nil, err
		}
		candles = append(candles, candle)
	}
	sort.Slice(candles, func(i, j int) bool {
		return candles[i].OpenTime.Before(candles[j].OpenTime)
	})
	if err := validator.ValidateCandles(candles); err != nil {
		return nil, err
	}

	return candles, nil
}

func (c *Client) get(ctx context.Context, path string, query url.Values, result any) error {
	var lastErr error
	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		if attempt > 0 {
			timer := time.NewTimer(c.backoff * time.Duration(attempt))
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}

		err := c.getOnce(ctx, path, query, result)
		if err == nil {
			return nil
		}
		lastErr = err
		if !isRetryable(err) {
			break
		}
	}

	return lastErr
}

func (c *Client) getOnce(ctx context.Context, path string, query url.Values, result any) error {
	endpoint := *c.baseURL
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + path
	if query != nil {
		endpoint.RawQuery = query.Encode()
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return fmt.Errorf("create bybit request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "Inquisitor/phase1")

	response, err := c.httpClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()

	if response.StatusCode == http.StatusTooManyRequests {
		return exchanges.ErrRateLimited
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 2048))
		return fmt.Errorf("bybit http status %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}

	var envelope responseEnvelope[json.RawMessage]
	if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil {
		return fmt.Errorf("decode bybit response: %w", err)
	}
	if envelope.RetCode != 0 {
		return exchanges.ExchangeError{
			Exchange: exchangeName,
			RetCode:  envelope.RetCode,
			RetMsg:   envelope.RetMsg,
		}
	}
	if err := json.Unmarshal(envelope.Result, result); err != nil {
		return fmt.Errorf("decode bybit result: %w", err)
	}

	return nil
}

func isRetryable(err error) bool {
	if errors.Is(err, exchanges.ErrRateLimited) {
		return true
	}

	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return true
	}

	return strings.Contains(err.Error(), "bybit http status 5")
}

func (c *Client) mapKline(req exchanges.KlinesRequest, item []string) (marketdata.Candle, error) {
	if len(item) != 7 {
		return marketdata.Candle{}, fmt.Errorf("bybit kline item expected 7 fields, got %d", len(item))
	}

	openTimeMs, err := strconv.ParseInt(item[0], 10, 64)
	if err != nil {
		return marketdata.Candle{}, fmt.Errorf("parse kline startTime: %w", err)
	}
	duration, err := marketdata.IntervalDuration(req.Interval)
	if err != nil {
		return marketdata.Candle{}, err
	}

	open, err := parseDecimal("openPrice", item[1])
	if err != nil {
		return marketdata.Candle{}, err
	}
	high, err := parseDecimal("highPrice", item[2])
	if err != nil {
		return marketdata.Candle{}, err
	}
	low, err := parseDecimal("lowPrice", item[3])
	if err != nil {
		return marketdata.Candle{}, err
	}
	closePrice, err := parseDecimal("closePrice", item[4])
	if err != nil {
		return marketdata.Candle{}, err
	}
	volume, err := parseDecimal("volume", item[5])
	if err != nil {
		return marketdata.Candle{}, err
	}
	turnover, err := parseDecimal("turnover", item[6])
	if err != nil {
		return marketdata.Candle{}, err
	}

	openTime := time.UnixMilli(openTimeMs).UTC()
	closeTime := openTime.Add(duration)
	return marketdata.Candle{
		Exchange:  exchangeName,
		Category:  req.Category,
		Symbol:    req.Symbol,
		Interval:  req.Interval,
		OpenTime:  openTime,
		CloseTime: closeTime,
		Open:      open,
		High:      high,
		Low:       low,
		Close:     closePrice,
		Volume:    volume,
		Turnover:  turnover,
		IsClosed:  !c.now().Before(closeTime),
	}, nil
}

func mapInstrument(category string, item instrumentInfo) (marketdata.Instrument, error) {
	tickSize, err := parseDecimal("priceFilter.tickSize", item.PriceFilter.TickSize)
	if err != nil {
		return marketdata.Instrument{}, err
	}
	qtyStep, err := parseDecimal("lotSizeFilter.qtyStep", item.LotSizeFilter.QtyStep)
	if err != nil {
		return marketdata.Instrument{}, err
	}
	minOrderQty, err := parseDecimal("lotSizeFilter.minOrderQty", item.LotSizeFilter.MinOrderQty)
	if err != nil {
		return marketdata.Instrument{}, err
	}
	maxOrderQty, err := parseDecimal("lotSizeFilter.maxOrderQty", item.LotSizeFilter.MaxOrderQty)
	if err != nil {
		return marketdata.Instrument{}, err
	}
	maxMarketOrderQty, err := parseDecimal("lotSizeFilter.maxMktOrderQty", item.LotSizeFilter.MaxMktOrderQty)
	if err != nil {
		return marketdata.Instrument{}, err
	}
	minNotionalValue, err := parseDecimal("lotSizeFilter.minNotionalValue", item.LotSizeFilter.MinNotionalValue)
	if err != nil {
		return marketdata.Instrument{}, err
	}
	priceScale, err := strconv.Atoi(item.PriceScale)
	if err != nil {
		return marketdata.Instrument{}, fmt.Errorf("parse priceScale: %w", err)
	}

	leverageFilterJSON, err := json.Marshal(item.LeverageFilter)
	if err != nil {
		return marketdata.Instrument{}, fmt.Errorf("marshal leverageFilter: %w", err)
	}
	priceFilterJSON, err := json.Marshal(item.PriceFilter)
	if err != nil {
		return marketdata.Instrument{}, fmt.Errorf("marshal priceFilter: %w", err)
	}
	lotSizeFilterJSON, err := json.Marshal(item.LotSizeFilter)
	if err != nil {
		return marketdata.Instrument{}, fmt.Errorf("marshal lotSizeFilter: %w", err)
	}
	rawJSON, err := json.Marshal(item)
	if err != nil {
		return marketdata.Instrument{}, fmt.Errorf("marshal raw instrument: %w", err)
	}

	return marketdata.Instrument{
		Exchange:           exchangeName,
		Category:           category,
		Symbol:             item.Symbol,
		BaseCoin:           item.BaseCoin,
		QuoteCoin:          item.QuoteCoin,
		Status:             item.Status,
		TickSize:           tickSize,
		QtyStep:            qtyStep,
		MinOrderQty:        minOrderQty,
		MaxOrderQty:        maxOrderQty,
		MaxMarketOrderQty:  maxMarketOrderQty,
		MinNotionalValue:   minNotionalValue,
		PriceScale:         priceScale,
		LeverageFilterJSON: leverageFilterJSON,
		PriceFilterJSON:    priceFilterJSON,
		LotSizeFilterJSON:  lotSizeFilterJSON,
		RawJSON:            rawJSON,
		UpdatedAt:          time.Now().UTC(),
	}, nil
}

func parseDecimal(field, value string) (decimal.Decimal, error) {
	if strings.TrimSpace(value) == "" {
		return decimal.Decimal{}, fmt.Errorf("%s is empty", field)
	}
	parsed, err := decimal.NewFromString(value)
	if err != nil {
		return decimal.Decimal{}, fmt.Errorf("parse %s as decimal: %w", field, err)
	}
	return parsed, nil
}

func cloneValues(values url.Values) url.Values {
	cloned := make(url.Values, len(values))
	for key, items := range values {
		cloned[key] = append([]string(nil), items...)
	}
	return cloned
}
