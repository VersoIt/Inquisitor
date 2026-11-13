package postgres

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/VersoIt/Inquisitor/internal/paper"
)

type PaperAccountRepository struct{ db *sql.DB }

func NewPaperAccountRepository(db *sql.DB) *PaperAccountRepository {
	return &PaperAccountRepository{db: db}
}

// All ledger components and inventory counts share one database snapshot.
func (r *PaperAccountRepository) LoadPaperAccountLedger(ctx context.Context, q paper.AccountLedgerQuery) (paper.AccountLedger, error) {
	if err := ctx.Err(); err != nil {
		return paper.AccountLedger{}, err
	}
	if r == nil || r.db == nil {
		return paper.AccountLedger{}, fmt.Errorf("paper account repository requires database")
	}
	if err := paper.ValidateAccountLedgerQuery(q); err != nil {
		return paper.AccountLedger{}, err
	}
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return paper.AccountLedger{}, err
	}
	defer tx.Rollback()
	var out paper.AccountLedger
	out.Validation, err = scanPaperValidationRecord(tx.QueryRowContext(ctx, `SELECT validation_id,run_id,status,status_reason,mode,initial_balance::text,minimum_days,reasons_json::text,planned_at,started_at,completed_at,cancelled_at FROM paper_validation_records WHERE validation_id=$1`, q.ValidationID))
	if err != nil {
		return paper.AccountLedger{}, fmt.Errorf("load account validation: %w", err)
	}
	out.Events, err = readAccountEvents(ctx, tx, q.ValidationID)
	if err != nil {
		return paper.AccountLedger{}, err
	}
	out.Closes, err = readAccountCloses(ctx, tx, q.ValidationID)
	if err != nil {
		return paper.AccountLedger{}, err
	}
	n := &out.Inventory
	err = tx.QueryRowContext(ctx, accountInventorySQL, q.ValidationID, q.AsOf).Scan(
		&n.Tickets, &n.Fills, &n.Positions, &n.Closes, &n.Events,
		&n.PendingTickets, &n.UnopenedFills, &n.ActivePositions,
		&n.LegacyTrades, &n.BrokenLinks, &n.FutureRecords,
	)
	if err != nil {
		return paper.AccountLedger{}, fmt.Errorf("read account inventory: %w", err)
	}
	if _, err := paper.BuildAccountSnapshot(out, q.AsOf); err != nil {
		return paper.AccountLedger{}, err
	}
	if err := tx.Commit(); err != nil {
		return paper.AccountLedger{}, err
	}
	return out, nil
}

const accountInventorySQL = `SELECT
 (SELECT count(*) FROM paper_order_tickets WHERE validation_id=$1),
 (SELECT count(*) FROM paper_order_fills WHERE validation_id=$1),
 (SELECT count(*) FROM paper_open_positions WHERE validation_id=$1),
 (SELECT count(*) FROM paper_position_closes WHERE validation_id=$1),
 (SELECT count(*) FROM paper_equity_events WHERE validation_id=$1),
 (SELECT count(*) FROM paper_order_tickets t
  LEFT JOIN paper_order_fills f ON f.ticket_id=t.ticket_id
  WHERE t.validation_id=$1 AND f.fill_id IS NULL),
 (SELECT count(*) FROM paper_order_fills f
  LEFT JOIN paper_open_positions p ON p.fill_id=f.fill_id
  WHERE f.validation_id=$1 AND p.position_id IS NULL),
 (SELECT count(*) FROM paper_open_positions p
  LEFT JOIN paper_position_closes c ON c.position_id=p.position_id
  WHERE p.validation_id=$1 AND c.close_id IS NULL),
 (SELECT count(*) FROM paper_validation_trades WHERE validation_id=$1),
 (SELECT count(*) FROM paper_order_fills f
  JOIN paper_order_tickets t ON t.ticket_id=f.ticket_id
  WHERE (f.validation_id=$1 OR t.validation_id=$1) AND (
   f.validation_id<>t.validation_id OR f.decision_id<>t.decision_id OR f.intent_id<>t.intent_id
   OR f.exchange<>t.exchange OR f.category<>t.category OR f.symbol<>t.symbol
   OR f.interval<>t.interval OR f.side<>t.side OR f.quantity<>t.quantity))
 + (SELECT count(*) FROM paper_open_positions p
  JOIN paper_order_fills f ON f.fill_id=p.fill_id
  WHERE (p.validation_id=$1 OR f.validation_id=$1) AND (
   p.validation_id<>f.validation_id OR p.ticket_id<>f.ticket_id
   OR p.decision_id<>f.decision_id OR p.intent_id<>f.intent_id
   OR p.exchange<>f.exchange OR p.category<>f.category OR p.symbol<>f.symbol
   OR p.interval<>f.interval OR p.side<>f.side OR p.quantity<>f.quantity
   OR p.entry_price<>f.executed_price OR p.entry_fee<>f.fee OR p.opened_at<>f.filled_at))
 + (SELECT count(*) FROM paper_position_closes c
  JOIN paper_open_positions p ON p.position_id=c.position_id
  WHERE (c.validation_id=$1 OR p.validation_id=$1) AND (
   c.validation_id<>p.validation_id OR c.entry_fill_id<>p.fill_id OR c.ticket_id<>p.ticket_id
   OR c.decision_id<>p.decision_id OR c.intent_id<>p.intent_id
   OR c.exchange<>p.exchange OR c.category<>p.category OR c.symbol<>p.symbol
   OR c.interval<>p.interval OR c.side<>p.side OR c.quantity<>p.quantity
   OR c.entry_price<>p.entry_price OR c.entry_fee<>p.entry_fee OR c.opened_at<>p.opened_at)),
 (SELECT count(*) FROM paper_order_tickets WHERE validation_id=$1 AND created_at>$2)
 + (SELECT count(*) FROM paper_order_fills
    WHERE validation_id=$1 AND (filled_at>$2 OR recorded_at>$2))
 + (SELECT count(*) FROM paper_open_positions
    WHERE validation_id=$1 AND (opened_at>$2 OR recorded_at>$2))`

func readAccountEvents(ctx context.Context, tx *sql.Tx, id string) ([]paper.EquityEvent, error) {
	rows, err := tx.QueryContext(ctx, `SELECT event_id,validation_id,close_id,position_id,exchange,category,symbol,interval,sequence_number,net_pnl::text,fees::text,equity_before::text,equity_after::text,occurred_at,recorded_at FROM paper_equity_events WHERE validation_id=$1 ORDER BY sequence_number LIMIT $2`, id, paper.MaxAccountLedgerRows+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []paper.EquityEvent
	for rows.Next() {
		e, err := scanPaperEquityEvent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) > paper.MaxAccountLedgerRows {
		return nil, fmt.Errorf("account equity ledger exceeds safety limit")
	}
	return out, nil
}

func readAccountCloses(ctx context.Context, tx *sql.Tx, id string) ([]paper.PositionClose, error) {
	rows, err := tx.QueryContext(ctx, `SELECT close_id,position_id,entry_fill_id,ticket_id,validation_id,decision_id,intent_id,exchange,category,symbol,interval,side,liquidity,quantity::text,entry_price::text,exit_mid_price::text,exit_price::text,entry_notional::text,exit_notional::text,entry_fee::text,exit_fee::text,exit_fee_bps::text,spread_bps::text,slippage_bps::text,fees::text,gross_pnl::text,net_pnl::text,return_ratio::text,close_reason,opened_at,closed_at,recorded_at FROM paper_position_closes WHERE validation_id=$1 ORDER BY closed_at,close_id LIMIT $2`, id, paper.MaxAccountLedgerRows+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []paper.PositionClose
	for rows.Next() {
		c, err := scanPaperPositionClose(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) > paper.MaxAccountLedgerRows {
		return nil, fmt.Errorf("account close ledger exceeds safety limit")
	}
	return out, nil
}
