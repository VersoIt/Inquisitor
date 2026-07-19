package paper

import (
	"context"
	"fmt"
	"strings"

	domainpaper "github.com/VersoIt/Inquisitor/internal/paper"
)

type SettlePositionCloseRequest struct {
	EventID string
	Close   ClosePositionRequest
}

type SettlePositionCloseResult struct {
	Record      domainpaper.ValidationRecord
	Position    domainpaper.OpenPosition
	Close       domainpaper.PositionClose
	Event       domainpaper.EquityEvent
	CloseStats  domainpaper.PositionCloseStats
	EquityStats domainpaper.EquityEventStats
}

func (s *Service) SettlePositionClose(ctx context.Context, req SettlePositionCloseRequest) (SettlePositionCloseResult, error) {
	if err := ctx.Err(); err != nil {
		return SettlePositionCloseResult{}, err
	}
	if strings.TrimSpace(req.EventID) == "" {
		return SettlePositionCloseResult{}, fmt.Errorf("event_id is required")
	}

	closed, err := s.ClosePosition(ctx, req.Close)
	if err != nil {
		return SettlePositionCloseResult{}, err
	}
	accounted, err := s.AccountPositionClose(ctx, AccountPositionCloseRequest{
		ValidationID: closed.Record.ValidationID,
		EventID:      req.EventID,
		CloseID:      closed.Close.CloseID,
	})
	if err != nil {
		return SettlePositionCloseResult{}, err
	}
	if accounted.Close.CloseID != closed.Close.CloseID {
		return SettlePositionCloseResult{}, fmt.Errorf("paper settlement close/accounting mismatch")
	}
	return SettlePositionCloseResult{
		Record:      closed.Record,
		Position:    closed.Position,
		Close:       closed.Close,
		Event:       accounted.Event,
		CloseStats:  closed.Stats,
		EquityStats: accounted.Stats,
	}, nil
}
