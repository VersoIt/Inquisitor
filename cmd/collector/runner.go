package main

import (
	"context"
	"fmt"
	"time"
)

type collectorWebSocket interface {
	Connect(context.Context) error
	Subscribe(context.Context, string, []string) error
	Ping(context.Context, string) error
	Read(context.Context) ([]byte, error)
	Close() error
}

type collectorLogger interface {
	Info(msg string, args ...any)
	Warn(msg string, args ...any)
}

type collectorRunner struct {
	client            collectorWebSocket
	log               collectorLogger
	topics            []string
	reqID             string
	messages          int
	readTimeout       time.Duration
	pingInterval      time.Duration
	reconnectAttempts int
	reconnectBackoff  time.Duration
	now               func() time.Time
	sleep             func(context.Context, time.Duration) error
	handlePayload     func(context.Context, []byte)
}

func (r collectorRunner) Run(ctx context.Context) (int, error) {
	if r.client == nil {
		return 0, fmt.Errorf("websocket client is required")
	}
	if r.messages <= 0 {
		return 0, fmt.Errorf("messages must be positive")
	}
	if r.reqID == "" {
		return 0, fmt.Errorf("request id is required")
	}
	if r.readTimeout <= 0 {
		return 0, fmt.Errorf("read timeout must be positive")
	}
	if r.pingInterval < 0 {
		return 0, fmt.Errorf("ping interval must be greater than or equal to zero")
	}
	if r.reconnectAttempts < 0 {
		return 0, fmt.Errorf("reconnect attempts must be greater than or equal to zero")
	}
	if r.reconnectBackoff <= 0 {
		return 0, fmt.Errorf("reconnect backoff must be positive")
	}
	if r.sleep == nil {
		r.sleep = sleepContext
	}
	if r.now == nil {
		r.now = time.Now
	}
	if r.handlePayload == nil {
		r.handlePayload = func(context.Context, []byte) {}
	}

	if err := r.connectAndSubscribe(ctx); err != nil {
		return 0, err
	}
	if r.log != nil {
		r.log.Info("collector subscribed", "topics", r.topics, "messages", r.messages)
	}

	messagesRead := 0
	reconnectsUsed := 0
	lastPing := r.now()
	for messagesRead < r.messages {
		if r.pingInterval > 0 && !r.now().Before(lastPing.Add(r.pingInterval)) {
			if err := r.client.Ping(ctx, r.reqID+"-ping"); err != nil {
				if reconnectsUsed >= r.reconnectAttempts {
					return messagesRead, fmt.Errorf("ping websocket after %d reconnect attempts: %w", reconnectsUsed, err)
				}
				reconnectsUsed++
				if r.log != nil {
					r.log.Warn("websocket ping failed; reconnecting", "error", err, "attempt", reconnectsUsed, "max_attempts", r.reconnectAttempts)
				}
				if err := r.reconnect(ctx, reconnectsUsed); err != nil {
					return messagesRead, err
				}
			}
			lastPing = r.now()
		}

		readCtx, cancelRead := context.WithTimeout(ctx, r.readTimeout)
		payload, err := r.client.Read(readCtx)
		cancelRead()
		if err == nil {
			r.handlePayload(ctx, payload)
			messagesRead++
			continue
		}

		if reconnectsUsed >= r.reconnectAttempts {
			return messagesRead, fmt.Errorf("read websocket message after %d reconnect attempts: %w", reconnectsUsed, err)
		}
		reconnectsUsed++
		if r.log != nil {
			r.log.Warn("websocket read failed; reconnecting", "error", err, "attempt", reconnectsUsed, "max_attempts", r.reconnectAttempts)
		}
		if err := r.reconnect(ctx, reconnectsUsed); err != nil {
			return messagesRead, err
		}
		lastPing = r.now()
	}

	return messagesRead, nil
}

func (r collectorRunner) reconnect(ctx context.Context, attempt int) error {
	_ = r.client.Close()

	if err := r.sleep(ctx, r.reconnectBackoff); err != nil {
		return fmt.Errorf("wait before websocket reconnect: %w", err)
	}
	if err := r.connectAndSubscribe(ctx); err != nil {
		return fmt.Errorf("reconnect websocket attempt %d: %w", attempt, err)
	}
	return nil
}

func (r collectorRunner) connectAndSubscribe(ctx context.Context) error {
	if err := r.client.Connect(ctx); err != nil {
		return fmt.Errorf("connect websocket: %w", err)
	}
	if err := r.client.Subscribe(ctx, r.reqID, r.topics); err != nil {
		return fmt.Errorf("subscribe websocket topics: %w", err)
	}
	return nil
}

func sleepContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
