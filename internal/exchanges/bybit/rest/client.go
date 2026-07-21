package rest

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
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
	domainlive "github.com/VersoIt/Inquisitor/internal/live"
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
	apiKey     string
	apiSecret  string
	recvWindow time.Duration
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

func WithHMACAuth(apiKey string, apiSecret string) Option {
	return func(c *Client) {
		c.apiKey = strings.TrimSpace(apiKey)
		c.apiSecret = strings.TrimSpace(apiSecret)
	}
}

func WithRecvWindow(window time.Duration) Option {
	return func(c *Client) {
		if window > 0 {
			c.recvWindow = window
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
		recvWindow: 5 * time.Second,
	}
	for _, option := range options {
		option(client)
	}

	return client, nil
}

func (c *Client) SubmitOrder(ctx context.Context, submission domainlive.OrderSubmission) (domainlive.OrderAcknowledgement, error) {
	if err := ctx.Err(); err != nil {
		return domainlive.OrderAcknowledgement{}, err
	}
	if c == nil {
		return domainlive.OrderAcknowledgement{}, fmt.Errorf("bybit client is required")
	}
	if err := domainlive.ValidateOrderSubmission(submission); err != nil {
		return domainlive.OrderAcknowledgement{}, err
	}
	req, err := bybitCreateOrderRequest(submission)
	if err != nil {
		return domainlive.OrderAcknowledgement{}, err
	}

	var result createOrderResult
	if err := c.postAuthenticated(ctx, "/v5/order/create", req, &result); err != nil {
		return domainlive.OrderAcknowledgement{}, err
	}

	return domainlive.NewOrderAcknowledgement(domainlive.OrderAcknowledgementInput{
		SubmissionID:    submission.SubmissionID,
		ClientOrderID:   result.OrderLinkID,
		Exchange:        exchangeName,
		ExchangeOrderID: result.OrderID,
		Status:          domainlive.OrderStatusAccepted,
		ReceivedAt:      c.now(),
	})
}

func (c *Client) GetOrderStatus(ctx context.Context, query domainlive.OrderStatusQuery) (domainlive.OrderStatusSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return domainlive.OrderStatusSnapshot{}, err
	}
	if c == nil {
		return domainlive.OrderStatusSnapshot{}, fmt.Errorf("bybit client is required")
	}
	if err := domainlive.ValidateOrderStatusQuery(query); err != nil {
		return domainlive.OrderStatusSnapshot{}, err
	}
	if query.Exchange != exchangeName {
		return domainlive.OrderStatusSnapshot{}, fmt.Errorf("bybit order status requires exchange %q", exchangeName)
	}

	values := url.Values{}
	values.Set("category", query.Category)
	values.Set("symbol", query.Symbol)
	values.Set("orderLinkId", query.ClientOrderID)
	values.Set("limit", "1")

	var result orderRealtimeResult
	if err := c.getAuthenticated(ctx, "/v5/order/realtime", values, &result); err != nil {
		return domainlive.OrderStatusSnapshot{}, err
	}
	if len(result.List) == 0 {
		return domainlive.OrderStatusSnapshot{}, fmt.Errorf("bybit order status not found for orderLinkId %q", query.ClientOrderID)
	}
	if len(result.List) > 1 {
		return domainlive.OrderStatusSnapshot{}, fmt.Errorf("bybit order status for orderLinkId %q is not unique", query.ClientOrderID)
	}
	return c.mapOrderStatus(query, result.Category, result.List[0])
}

func (c *Client) GetPositionSnapshot(ctx context.Context, query domainlive.PositionSnapshotQuery) (domainlive.PositionSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return domainlive.PositionSnapshot{}, err
	}
	if c == nil {
		return domainlive.PositionSnapshot{}, fmt.Errorf("bybit client is required")
	}
	if err := domainlive.ValidatePositionSnapshotQuery(query); err != nil {
		return domainlive.PositionSnapshot{}, err
	}
	if query.Exchange != exchangeName {
		return domainlive.PositionSnapshot{}, fmt.Errorf("bybit position snapshot requires exchange %q", exchangeName)
	}

	values := url.Values{}
	values.Set("category", query.Category)
	values.Set("symbol", query.Symbol)
	values.Set("limit", "20")

	var result positionListResult
	if err := c.getAuthenticated(ctx, "/v5/position/list", values, &result); err != nil {
		return domainlive.PositionSnapshot{}, err
	}
	if len(result.List) == 0 {
		return domainlive.PositionSnapshot{}, fmt.Errorf("bybit position snapshot not found for symbol %q", query.Symbol)
	}
	if len(result.List) > 1 {
		return domainlive.PositionSnapshot{}, fmt.Errorf("bybit position snapshot for symbol %q is not unique", query.Symbol)
	}
	return c.mapPositionSnapshot(query, result.Category, result.List[0])
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

func (c *Client) postAuthenticated(ctx context.Context, path string, payload any, result any) error {
	if strings.TrimSpace(c.apiKey) == "" || strings.TrimSpace(c.apiSecret) == "" {
		return fmt.Errorf("bybit private request requires API key and API secret")
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode bybit request body: %w", err)
	}

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

		err := c.postAuthenticatedOnce(ctx, path, body, result)
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

func (c *Client) getAuthenticated(ctx context.Context, path string, query url.Values, result any) error {
	if strings.TrimSpace(c.apiKey) == "" || strings.TrimSpace(c.apiSecret) == "" {
		return fmt.Errorf("bybit private request requires API key and API secret")
	}

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

		err := c.getAuthenticatedOnce(ctx, path, query, result)
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

func (c *Client) getAuthenticatedOnce(ctx context.Context, path string, query url.Values, result any) error {
	endpoint := *c.baseURL
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + path
	if query != nil {
		endpoint.RawQuery = query.Encode()
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return fmt.Errorf("create bybit request: %w", err)
	}
	timestamp := strconv.FormatInt(c.now().UnixMilli(), 10)
	recvWindow := strconv.FormatInt(c.recvWindow.Milliseconds(), 10)
	// Bybit V5 signs GET requests as timestamp + apiKey + recvWindow + raw query string.
	signature := signBybitHMAC(c.apiSecret, timestamp+c.apiKey+recvWindow+endpoint.RawQuery)

	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "Inquisitor/phase1")
	request.Header.Set("X-BAPI-API-KEY", c.apiKey)
	request.Header.Set("X-BAPI-TIMESTAMP", timestamp)
	request.Header.Set("X-BAPI-RECV-WINDOW", recvWindow)
	request.Header.Set("X-BAPI-SIGN", signature)

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

func (c *Client) postAuthenticatedOnce(ctx context.Context, path string, body []byte, result any) error {
	endpoint := *c.baseURL
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + path

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create bybit request: %w", err)
	}
	timestamp := strconv.FormatInt(c.now().UnixMilli(), 10)
	recvWindow := strconv.FormatInt(c.recvWindow.Milliseconds(), 10)
	// Bybit V5 signs POST requests as timestamp + apiKey + recvWindow + raw JSON body.
	signature := signBybitHMAC(c.apiSecret, timestamp+c.apiKey+recvWindow+string(body))

	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", "Inquisitor/phase1")
	request.Header.Set("X-BAPI-API-KEY", c.apiKey)
	request.Header.Set("X-BAPI-TIMESTAMP", timestamp)
	request.Header.Set("X-BAPI-RECV-WINDOW", recvWindow)
	request.Header.Set("X-BAPI-SIGN", signature)

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

func (c *Client) mapOrderStatus(query domainlive.OrderStatusQuery, category string, item orderRealtimeItem) (domainlive.OrderStatusSnapshot, error) {
	price, err := parseOptionalDecimal("price", item.Price)
	if err != nil {
		return domainlive.OrderStatusSnapshot{}, err
	}
	quantity, err := parseDecimal("qty", item.Qty)
	if err != nil {
		return domainlive.OrderStatusSnapshot{}, err
	}
	averagePrice, err := parseOptionalDecimal("avgPrice", item.AvgPrice)
	if err != nil {
		return domainlive.OrderStatusSnapshot{}, err
	}
	leavesQuantity, err := parseOptionalDecimal("leavesQty", item.LeavesQty)
	if err != nil {
		return domainlive.OrderStatusSnapshot{}, err
	}
	cumulativeExecutedQuantity, err := parseOptionalDecimal("cumExecQty", item.CumExecQty)
	if err != nil {
		return domainlive.OrderStatusSnapshot{}, err
	}
	cumulativeExecutedValue, err := parseOptionalDecimal("cumExecValue", item.CumExecValue)
	if err != nil {
		return domainlive.OrderStatusSnapshot{}, err
	}
	cumulativeFee, err := parseOptionalDecimal("cumExecFee", item.CumExecFee)
	if err != nil {
		return domainlive.OrderStatusSnapshot{}, err
	}
	exchangeCreatedAt, err := parseBybitUnixMilli("createdTime", item.CreatedTime)
	if err != nil {
		return domainlive.OrderStatusSnapshot{}, err
	}
	exchangeUpdatedAt, err := parseBybitUnixMilli("updatedTime", item.UpdatedTime)
	if err != nil {
		return domainlive.OrderStatusSnapshot{}, err
	}
	side, err := orderSideFromBybit(item.Side)
	if err != nil {
		return domainlive.OrderStatusSnapshot{}, err
	}
	orderType, err := orderTypeFromBybit(item.OrderType)
	if err != nil {
		return domainlive.OrderStatusSnapshot{}, err
	}
	timeInForce, err := timeInForceFromBybit(item.TimeInForce)
	if err != nil {
		return domainlive.OrderStatusSnapshot{}, err
	}
	status, err := orderStatusFromBybit(item.OrderStatus)
	if err != nil {
		return domainlive.OrderStatusSnapshot{}, err
	}

	return domainlive.NewOrderStatusSnapshot(domainlive.OrderStatusSnapshotInput{
		ClientOrderID:              item.OrderLinkID,
		ExchangeOrderID:            item.OrderID,
		Exchange:                   exchangeName,
		Category:                   firstNonEmpty(category, query.Category),
		Symbol:                     item.Symbol,
		Side:                       side,
		Type:                       orderType,
		TimeInForce:                timeInForce,
		ExchangeStatus:             status,
		RejectReason:               item.RejectReason,
		Quantity:                   quantity,
		Price:                      price,
		AveragePrice:               averagePrice,
		LeavesQuantity:             leavesQuantity,
		CumulativeExecutedQuantity: cumulativeExecutedQuantity,
		CumulativeExecutedValue:    cumulativeExecutedValue,
		CumulativeFee:              cumulativeFee,
		ReduceOnly:                 item.ReduceOnly,
		ExchangeCreatedAt:          exchangeCreatedAt,
		ExchangeUpdatedAt:          exchangeUpdatedAt,
		ObservedAt:                 c.now(),
	})
}

func (c *Client) mapPositionSnapshot(query domainlive.PositionSnapshotQuery, category string, item positionListItem) (domainlive.PositionSnapshot, error) {
	size, err := parseOptionalDecimal("size", item.Size)
	if err != nil {
		return domainlive.PositionSnapshot{}, err
	}
	averagePrice, err := parseOptionalDecimal("avgPrice", item.AvgPrice)
	if err != nil {
		return domainlive.PositionSnapshot{}, err
	}
	positionValue, err := parseOptionalDecimal("positionValue", item.PositionValue)
	if err != nil {
		return domainlive.PositionSnapshot{}, err
	}
	markPrice, err := parseOptionalDecimal("markPrice", item.MarkPrice)
	if err != nil {
		return domainlive.PositionSnapshot{}, err
	}
	liquidationPrice, err := parseOptionalDecimal("liqPrice", item.LiqPrice)
	if err != nil {
		return domainlive.PositionSnapshot{}, err
	}
	leverage, err := parseOptionalDecimal("leverage", item.Leverage)
	if err != nil {
		return domainlive.PositionSnapshot{}, err
	}
	unrealisedPnL, err := parseOptionalDecimal("unrealisedPnl", item.UnrealisedPnl)
	if err != nil {
		return domainlive.PositionSnapshot{}, err
	}
	currentRealisedPnL, err := parseOptionalDecimal("curRealisedPnl", item.CurRealisedPnl)
	if err != nil {
		return domainlive.PositionSnapshot{}, err
	}
	cumulativeRealisedPnL, err := parseOptionalDecimal("cumRealisedPnl", item.CumRealisedPnl)
	if err != nil {
		return domainlive.PositionSnapshot{}, err
	}
	exchangeCreatedAt, err := parseOptionalBybitUnixMilli("createdTime", item.CreatedTime)
	if err != nil {
		return domainlive.PositionSnapshot{}, err
	}
	exchangeUpdatedAt, err := parseOptionalBybitUnixMilli("updatedTime", item.UpdatedTime)
	if err != nil {
		return domainlive.PositionSnapshot{}, err
	}
	side, err := positionSideFromBybit(item.Side)
	if err != nil {
		return domainlive.PositionSnapshot{}, err
	}
	status, err := positionStatusFromBybit(item.PositionStatus)
	if err != nil {
		return domainlive.PositionSnapshot{}, err
	}

	return domainlive.NewPositionSnapshot(domainlive.PositionSnapshotInput{
		Exchange:              exchangeName,
		Category:              firstNonEmpty(category, query.Category),
		Symbol:                item.Symbol,
		Side:                  side,
		Size:                  size,
		AveragePrice:          averagePrice,
		PositionValue:         positionValue,
		MarkPrice:             markPrice,
		LiquidationPrice:      liquidationPrice,
		Leverage:              leverage,
		UnrealisedPnL:         unrealisedPnL,
		CurrentRealisedPnL:    currentRealisedPnL,
		CumulativeRealisedPnL: cumulativeRealisedPnL,
		ExchangeStatus:        status,
		PositionIndex:         item.PositionIdx,
		Sequence:              item.Seq,
		ExchangeReduceOnly:    item.IsReduceOnly,
		ExchangeCreatedAt:     exchangeCreatedAt,
		ExchangeUpdatedAt:     exchangeUpdatedAt,
		ObservedAt:            c.now(),
	})
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

func parseOptionalDecimal(field, value string) (decimal.Decimal, error) {
	if strings.TrimSpace(value) == "" {
		return decimal.Zero, nil
	}
	return parseDecimal(field, value)
}

func parseBybitUnixMilli(field, value string) (time.Time, error) {
	if strings.TrimSpace(value) == "" {
		return time.Time{}, fmt.Errorf("%s is empty", field)
	}
	milliseconds, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse %s as unix milliseconds: %w", field, err)
	}
	return time.UnixMilli(milliseconds).UTC(), nil
}

func parseOptionalBybitUnixMilli(field, value string) (time.Time, error) {
	if strings.TrimSpace(value) == "" {
		return time.Time{}, nil
	}
	return parseBybitUnixMilli(field, value)
}

func cloneValues(values url.Values) url.Values {
	cloned := make(url.Values, len(values))
	for key, items := range values {
		cloned[key] = append([]string(nil), items...)
	}
	return cloned
}

func bybitCreateOrderRequest(submission domainlive.OrderSubmission) (createOrderRequest, error) {
	side, err := bybitOrderSide(submission.Side, submission.ReduceOnly)
	if err != nil {
		return createOrderRequest{}, err
	}
	orderType, err := bybitOrderType(submission.Type)
	if err != nil {
		return createOrderRequest{}, err
	}
	timeInForce, err := bybitTimeInForce(submission.TimeInForce)
	if err != nil {
		return createOrderRequest{}, err
	}

	req := createOrderRequest{
		Category:    submission.Category,
		Symbol:      submission.Symbol,
		Side:        side,
		OrderType:   orderType,
		Qty:         submission.Quantity.String(),
		TimeInForce: timeInForce,
		PositionIdx: 0,
		OrderLinkID: submission.ClientOrderID,
		ReduceOnly:  submission.ReduceOnly,
	}
	if submission.Type == domainlive.OrderTypeLimit {
		req.Price = submission.LimitPrice.String()
	}
	if !submission.ReduceOnly {
		if submission.TakeProfit.IsPositive() {
			req.TakeProfit = submission.TakeProfit.String()
		}
		if submission.StopLoss.IsPositive() {
			req.StopLoss = submission.StopLoss.String()
		}
	}
	return req, nil
}

func bybitOrderSide(side domainlive.OrderSide, reduceOnly bool) (string, error) {
	switch side {
	case domainlive.OrderSideLong:
		if reduceOnly {
			return "Sell", nil
		}
		return "Buy", nil
	case domainlive.OrderSideShort:
		if reduceOnly {
			return "Buy", nil
		}
		return "Sell", nil
	default:
		return "", fmt.Errorf("unsupported live order side %q", side)
	}
}

func bybitOrderType(orderType domainlive.OrderType) (string, error) {
	switch orderType {
	case domainlive.OrderTypeMarket:
		return "Market", nil
	case domainlive.OrderTypeLimit:
		return "Limit", nil
	default:
		return "", fmt.Errorf("unsupported live order type %q", orderType)
	}
}

func bybitTimeInForce(timeInForce domainlive.TimeInForce) (string, error) {
	switch timeInForce {
	case domainlive.TimeInForceGTC:
		return "GTC", nil
	case domainlive.TimeInForceIOC:
		return "IOC", nil
	case domainlive.TimeInForceFOK:
		return "FOK", nil
	case domainlive.TimeInForcePostOnly:
		return "PostOnly", nil
	default:
		return "", fmt.Errorf("unsupported live time_in_force %q", timeInForce)
	}
}

func orderSideFromBybit(side string) (domainlive.OrderSide, error) {
	switch strings.TrimSpace(side) {
	case "Buy":
		return domainlive.OrderSideLong, nil
	case "Sell":
		return domainlive.OrderSideShort, nil
	default:
		return "", fmt.Errorf("unsupported bybit order side %q", side)
	}
}

func orderTypeFromBybit(orderType string) (domainlive.OrderType, error) {
	switch strings.TrimSpace(orderType) {
	case "Market":
		return domainlive.OrderTypeMarket, nil
	case "Limit":
		return domainlive.OrderTypeLimit, nil
	default:
		return "", fmt.Errorf("unsupported bybit order type %q", orderType)
	}
}

func timeInForceFromBybit(timeInForce string) (domainlive.TimeInForce, error) {
	switch strings.TrimSpace(timeInForce) {
	case "GTC":
		return domainlive.TimeInForceGTC, nil
	case "IOC":
		return domainlive.TimeInForceIOC, nil
	case "FOK":
		return domainlive.TimeInForceFOK, nil
	case "PostOnly":
		return domainlive.TimeInForcePostOnly, nil
	default:
		return "", fmt.Errorf("unsupported bybit timeInForce %q", timeInForce)
	}
}

func orderStatusFromBybit(status string) (domainlive.ExchangeOrderStatus, error) {
	switch strings.TrimSpace(status) {
	case "New":
		return domainlive.ExchangeOrderStatusNew, nil
	case "PartiallyFilled":
		return domainlive.ExchangeOrderStatusPartiallyFilled, nil
	case "Untriggered":
		return domainlive.ExchangeOrderStatusUntriggered, nil
	case "Rejected":
		return domainlive.ExchangeOrderStatusRejected, nil
	case "PartiallyFilledCanceled", "PartiallyFilledCancelled":
		return domainlive.ExchangeOrderStatusPartiallyFilledCancelled, nil
	case "Filled":
		return domainlive.ExchangeOrderStatusFilled, nil
	case "Cancelled", "Canceled":
		return domainlive.ExchangeOrderStatusCancelled, nil
	case "Triggered":
		return domainlive.ExchangeOrderStatusTriggered, nil
	case "Deactivated":
		return domainlive.ExchangeOrderStatusDeactivated, nil
	default:
		return "", fmt.Errorf("unsupported bybit orderStatus %q", status)
	}
}

func positionSideFromBybit(side string) (domainlive.OrderSide, error) {
	switch strings.TrimSpace(side) {
	case "":
		return "", nil
	case "Buy":
		return domainlive.OrderSideLong, nil
	case "Sell":
		return domainlive.OrderSideShort, nil
	default:
		return "", fmt.Errorf("unsupported bybit position side %q", side)
	}
}

func positionStatusFromBybit(status string) (domainlive.ExchangePositionStatus, error) {
	switch strings.TrimSpace(status) {
	case "":
		return "", nil
	case "Normal":
		return domainlive.ExchangePositionStatusNormal, nil
	case "Liq":
		return domainlive.ExchangePositionStatusLiq, nil
	case "Adl":
		return domainlive.ExchangePositionStatusAdl, nil
	default:
		return "", fmt.Errorf("unsupported bybit positionStatus %q", status)
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func signBybitHMAC(secret string, payload string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(payload))
	return hex.EncodeToString(mac.Sum(nil))
}
