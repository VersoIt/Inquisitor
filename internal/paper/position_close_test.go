package paper_test

import (
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/VersoIt/Inquisitor/internal/backtest"
	"github.com/VersoIt/Inquisitor/internal/paper"
)

func TestNewPositionCloseComputesLongPnLFeesAndReturn(t *testing.T) {
	position := validOpenPosition()
	closedAt := position.OpenedAt.Add(time.Hour)
	recordedAt := closedAt.Add(time.Second)

	got, err := paper.NewPositionClose(paper.PositionCloseInput{
		CloseID:      " paper_close_0001 ",
		Position:     position,
		Liquidity:    " taker ",
		ExitMidPrice: decimal.RequireFromString("101000"),
		ExitPrice:    decimal.RequireFromString("100950"),
		ExitFee:      decimal.RequireFromString("30.285"),
		ExitFeeBPS:   decimal.RequireFromString("6"),
		SpreadBPS:    decimal.RequireFromString("2"),
		SlippageBPS:  decimal.RequireFromString("3"),
		CloseReason:  " take_profit ",
		ClosedAt:     closedAt,
		RecordedAt:   recordedAt,
	})
	if err != nil {
		t.Fatalf("new position close: %v", err)
	}

	if got.CloseID != "paper_close_0001" || got.PositionID != position.PositionID || got.EntryFillID != position.FillID ||
		got.TicketID != position.TicketID || got.ValidationID != position.ValidationID {
		t.Fatalf("identity mismatch: %#v", got)
	}
	if got.Liquidity != backtest.LiquidityTaker || got.CloseReason != paper.PositionCloseReasonTakeProfit {
		t.Fatalf("execution metadata mismatch: %#v", got)
	}
	if !got.ExitNotional.Equal(decimal.RequireFromString("50475")) || !got.Fees.Equal(decimal.RequireFromString("60.3")) ||
		!got.GrossPnL.Equal(decimal.RequireFromString("450")) || !got.NetPnL.Equal(decimal.RequireFromString("389.7")) {
		t.Fatalf("pnl math mismatch: %#v", got)
	}
	if !got.Return.Equal(decimal.RequireFromString("0.0077901049475262")) {
		t.Fatalf("return mismatch: %s", got.Return)
	}
	if got.ClosedAt.Location() != time.UTC || got.RecordedAt.Location() != time.UTC {
		t.Fatalf("timestamps must be UTC: closed=%s recorded=%s", got.ClosedAt, got.RecordedAt)
	}
}

func TestNewPositionCloseComputesShortPnL(t *testing.T) {
	position := validOpenPosition()
	position.Side = paper.OrderSideShort
	position.EntryPrice = decimal.RequireFromString("99950")
	position.EntryNotional = position.EntryPrice.Mul(position.Quantity)
	position.EntryFee = decimal.RequireFromString("29.985")
	position.StopLoss = decimal.RequireFromString("101000")
	position.TakeProfit = decimal.RequireFromString("98000")
	position.OpenRisk = position.StopLoss.Sub(position.EntryPrice).Mul(position.Quantity)
	closedAt := position.OpenedAt.Add(time.Hour)

	got, err := paper.NewPositionClose(paper.PositionCloseInput{
		CloseID:      "paper_close_0001",
		Position:     position,
		Liquidity:    backtest.LiquidityTaker,
		ExitMidPrice: decimal.RequireFromString("99000"),
		ExitPrice:    decimal.RequireFromString("99050"),
		ExitFee:      decimal.RequireFromString("29.715"),
		ExitFeeBPS:   decimal.RequireFromString("6"),
		SpreadBPS:    decimal.RequireFromString("2"),
		SlippageBPS:  decimal.RequireFromString("3"),
		CloseReason:  paper.PositionCloseReasonTakeProfit,
		ClosedAt:     closedAt,
		RecordedAt:   closedAt.Add(time.Second),
	})
	if err != nil {
		t.Fatalf("new short position close: %v", err)
	}

	if !got.GrossPnL.Equal(decimal.RequireFromString("450")) || !got.NetPnL.Equal(decimal.RequireFromString("390.3")) {
		t.Fatalf("short pnl mismatch: gross=%s net=%s", got.GrossPnL, got.NetPnL)
	}
}

func TestValidatePositionCloseRejectsInvalidInputsTableDriven(t *testing.T) {
	valid := validPositionClose()
	tests := []struct {
		name       string
		mutate     func(*paper.PositionClose)
		wantErrSub string
	}{
		{"missing close id", func(c *paper.PositionClose) { c.CloseID = " " }, "close_id"},
		{"missing position id", func(c *paper.PositionClose) { c.PositionID = "" }, "position_id"},
		{"missing entry fill id", func(c *paper.PositionClose) { c.EntryFillID = "" }, "entry_fill_id"},
		{"uppercase exchange", func(c *paper.PositionClose) { c.Exchange = "BYBIT" }, "exchange"},
		{"lowercase symbol", func(c *paper.PositionClose) { c.Symbol = "btcusdt" }, "symbol"},
		{"unsupported interval", func(c *paper.PositionClose) { c.Interval = "2" }, "interval"},
		{"unknown side", func(c *paper.PositionClose) { c.Side = "BUY" }, "side"},
		{"unknown liquidity", func(c *paper.PositionClose) { c.Liquidity = "POST_ONLY" }, "liquidity"},
		{"unknown close reason", func(c *paper.PositionClose) { c.CloseReason = "UNKNOWN" }, "close_reason"},
		{"zero quantity", func(c *paper.PositionClose) { c.Quantity = decimal.Zero }, "quantity"},
		{"zero entry price", func(c *paper.PositionClose) { c.EntryPrice = decimal.Zero }, "entry_price"},
		{"zero exit mid price", func(c *paper.PositionClose) { c.ExitMidPrice = decimal.Zero }, "exit_mid_price"},
		{"zero exit price", func(c *paper.PositionClose) { c.ExitPrice = decimal.Zero }, "exit_price"},
		{"entry notional mismatch", func(c *paper.PositionClose) { c.EntryNotional = decimal.RequireFromString("1") }, "entry_notional"},
		{"exit notional mismatch", func(c *paper.PositionClose) { c.ExitNotional = decimal.RequireFromString("1") }, "exit_notional"},
		{"negative entry fee", func(c *paper.PositionClose) { c.EntryFee = decimal.RequireFromString("-1") }, "entry_fee"},
		{"negative exit fee", func(c *paper.PositionClose) { c.ExitFee = decimal.RequireFromString("-1") }, "exit_fee"},
		{"negative exit fee bps", func(c *paper.PositionClose) { c.ExitFeeBPS = decimal.RequireFromString("-1") }, "exit_fee_bps"},
		{"exit fee math mismatch", func(c *paper.PositionClose) { c.ExitFee = decimal.RequireFromString("1") }, "exit_fee"},
		{"fees mismatch", func(c *paper.PositionClose) { c.Fees = decimal.RequireFromString("1") }, "fees"},
		{"gross pnl mismatch", func(c *paper.PositionClose) { c.GrossPnL = decimal.RequireFromString("1") }, "gross_pnl"},
		{"net pnl mismatch", func(c *paper.PositionClose) { c.NetPnL = decimal.RequireFromString("1") }, "net_pnl"},
		{"return mismatch", func(c *paper.PositionClose) { c.Return = decimal.RequireFromString("1") }, "return"},
		{"long favorable exit price", func(c *paper.PositionClose) {
			c.ExitPrice = decimal.RequireFromString("101050")
			c.ExitNotional = c.ExitPrice.Mul(c.Quantity)
			c.ExitFee = c.ExitNotional.Mul(c.ExitFeeBPS).Div(decimal.RequireFromString("10000"))
			recomputePositionClose(c)
		}, "LONG"},
		{"short favorable exit price", func(c *paper.PositionClose) {
			c.Side = paper.OrderSideShort
			c.ExitPrice = decimal.RequireFromString("98950")
			c.ExitNotional = c.ExitPrice.Mul(c.Quantity)
			c.ExitFee = c.ExitNotional.Mul(c.ExitFeeBPS).Div(decimal.RequireFromString("10000"))
			recomputePositionClose(c)
		}, "SHORT"},
		{"closed at not after opened", func(c *paper.PositionClose) { c.ClosedAt = c.OpenedAt }, "closed_at"},
		{"recorded before close", func(c *paper.PositionClose) { c.RecordedAt = c.ClosedAt.Add(-time.Nanosecond) }, "recorded_at"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			close := valid
			tt.mutate(&close)

			err := paper.ValidatePositionClose(close)
			if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
		})
	}
}

func TestValidatePositionCloseQueryRejectsInvalidInputsTableDriven(t *testing.T) {
	start := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name       string
		query      paper.PositionCloseQuery
		wantErrSub string
	}{
		{"valid empty query", paper.PositionCloseQuery{}, ""},
		{"valid filtered query", paper.PositionCloseQuery{Symbol: "BTCUSDT", Interval: "1", Start: start, End: start.Add(time.Hour), Limit: 10}, ""},
		{"lowercase symbol", paper.PositionCloseQuery{Symbol: "btcusdt"}, "symbol"},
		{"unsupported interval", paper.PositionCloseQuery{Interval: "2"}, "interval"},
		{"end before start", paper.PositionCloseQuery{Start: start, End: start}, "end"},
		{"negative limit", paper.PositionCloseQuery{Limit: -1}, "limit"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := paper.ValidatePositionCloseQuery(tt.query)
			if tt.wantErrSub == "" {
				if err != nil {
					t.Fatalf("validate query: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
		})
	}
}

func validPositionClose() paper.PositionClose {
	position := validOpenPosition()
	closedAt := position.OpenedAt.Add(time.Hour)
	return paper.PositionClose{
		CloseID:       "paper_close_0001",
		PositionID:    position.PositionID,
		EntryFillID:   position.FillID,
		TicketID:      position.TicketID,
		ValidationID:  position.ValidationID,
		DecisionID:    position.DecisionID,
		IntentID:      position.IntentID,
		Exchange:      position.Exchange,
		Category:      position.Category,
		Symbol:        position.Symbol,
		Interval:      position.Interval,
		Side:          position.Side,
		Liquidity:     backtest.LiquidityTaker,
		Quantity:      position.Quantity,
		EntryPrice:    position.EntryPrice,
		ExitMidPrice:  decimal.RequireFromString("101000"),
		ExitPrice:     decimal.RequireFromString("100950"),
		EntryNotional: position.EntryNotional,
		ExitNotional:  decimal.RequireFromString("50475"),
		EntryFee:      position.EntryFee,
		ExitFee:       decimal.RequireFromString("30.285"),
		ExitFeeBPS:    decimal.RequireFromString("6"),
		SpreadBPS:     decimal.RequireFromString("2"),
		SlippageBPS:   decimal.RequireFromString("3"),
		Fees:          decimal.RequireFromString("60.3"),
		GrossPnL:      decimal.RequireFromString("450"),
		NetPnL:        decimal.RequireFromString("389.7"),
		Return:        decimal.RequireFromString("0.0077901049475262"),
		CloseReason:   paper.PositionCloseReasonTakeProfit,
		OpenedAt:      position.OpenedAt,
		ClosedAt:      closedAt,
		RecordedAt:    closedAt.Add(time.Second),
	}
}

func recomputePositionClose(close *paper.PositionClose) {
	close.Fees = close.EntryFee.Add(close.ExitFee)
	switch close.Side {
	case paper.OrderSideLong:
		close.GrossPnL = close.ExitPrice.Sub(close.EntryPrice).Mul(close.Quantity)
	case paper.OrderSideShort:
		close.GrossPnL = close.EntryPrice.Sub(close.ExitPrice).Mul(close.Quantity)
	}
	close.NetPnL = close.GrossPnL.Sub(close.Fees)
	close.Return = close.NetPnL.Div(close.EntryNotional)
}
