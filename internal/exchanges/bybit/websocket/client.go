package websocket

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"

	gorilla "github.com/gorilla/websocket"
)

type Client struct {
	endpoint *url.URL
	dialer   *gorilla.Dialer
	conn     *gorilla.Conn
	mu       sync.Mutex
}

type Option func(*Client)

func WithDialer(dialer *gorilla.Dialer) Option {
	return func(c *Client) {
		if dialer != nil {
			c.dialer = dialer
		}
	}
}

func NewClient(endpoint string, options ...Option) (*Client, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("parse bybit websocket url: %w", err)
	}
	if parsed.Scheme != "ws" && parsed.Scheme != "wss" {
		return nil, fmt.Errorf("bybit websocket url must use ws or wss")
	}
	if parsed.Host == "" {
		return nil, fmt.Errorf("bybit websocket url must include host")
	}

	client := &Client{
		endpoint: parsed,
		dialer:   gorilla.DefaultDialer,
	}
	for _, option := range options {
		option(client)
	}
	return client, nil
}

func (c *Client) Connect(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn != nil {
		return nil
	}

	conn, response, err := c.dialer.DialContext(ctx, c.endpoint.String(), nil)
	if err != nil {
		if response != nil {
			return fmt.Errorf("connect bybit websocket status %d: %w", response.StatusCode, err)
		}
		return fmt.Errorf("connect bybit websocket: %w", err)
	}
	if response != nil && response.StatusCode != http.StatusSwitchingProtocols {
		_ = conn.Close()
		return fmt.Errorf("connect bybit websocket unexpected status %d", response.StatusCode)
	}

	c.conn = conn
	return nil
}

func (c *Client) Subscribe(ctx context.Context, reqID string, topics []string) error {
	message, err := BuildSubscribeMessage(reqID, topics)
	if err != nil {
		return err
	}
	return c.writeText(ctx, message)
}

func (c *Client) Ping(ctx context.Context, reqID string) error {
	message := SubscribeRequest{
		ReqID: reqID,
		Op:    "ping",
	}
	return c.writeJSON(ctx, message)
}

func (c *Client) Read(ctx context.Context) ([]byte, error) {
	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()
	if conn == nil {
		return nil, fmt.Errorf("websocket is not connected")
	}

	if deadline, ok := ctx.Deadline(); ok {
		if err := conn.SetReadDeadline(deadline); err != nil {
			return nil, fmt.Errorf("set websocket read deadline: %w", err)
		}
	}

	messageType, payload, err := conn.ReadMessage()
	if err != nil {
		return nil, fmt.Errorf("read websocket message: %w", err)
	}
	if messageType != gorilla.TextMessage {
		return nil, fmt.Errorf("unexpected websocket message type %d", messageType)
	}
	return payload, nil
}

func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn == nil {
		return nil
	}

	deadline := time.Now().Add(time.Second)
	_ = c.conn.WriteControl(gorilla.CloseMessage, gorilla.FormatCloseMessage(gorilla.CloseNormalClosure, ""), deadline)
	err := c.conn.Close()
	c.conn = nil
	return err
}

func (c *Client) writeText(ctx context.Context, message []byte) error {
	return c.write(ctx, gorilla.TextMessage, message)
}

func (c *Client) writeJSON(ctx context.Context, value any) error {
	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()
	if conn == nil {
		return fmt.Errorf("websocket is not connected")
	}
	if deadline, ok := ctx.Deadline(); ok {
		if err := conn.SetWriteDeadline(deadline); err != nil {
			return fmt.Errorf("set websocket write deadline: %w", err)
		}
	}
	if err := conn.WriteJSON(value); err != nil {
		return fmt.Errorf("write websocket json: %w", err)
	}
	return nil
}

func (c *Client) write(ctx context.Context, messageType int, message []byte) error {
	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()
	if conn == nil {
		return fmt.Errorf("websocket is not connected")
	}
	if deadline, ok := ctx.Deadline(); ok {
		if err := conn.SetWriteDeadline(deadline); err != nil {
			return fmt.Errorf("set websocket write deadline: %w", err)
		}
	}
	if err := conn.WriteMessage(messageType, message); err != nil {
		return fmt.Errorf("write websocket message: %w", err)
	}
	return nil
}
