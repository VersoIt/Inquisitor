package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	domainpaper "github.com/VersoIt/Inquisitor/internal/paper"
	"github.com/shopspring/decimal"
)

type PaperOrderTicketRepository struct {
	db *sql.DB
}

func NewPaperOrderTicketRepository(db *sql.DB) *PaperOrderTicketRepository {
	return &PaperOrderTicketRepository{db: db}
}

func (r *PaperOrderTicketRepository) RecordOrderTicket(ctx context.Context, ticket domainpaper.OrderTicket) (domainpaper.OrderTicketStats, error) {
	if err := domainpaper.ValidateOrderTicket(ticket); err != nil {
		return domainpaper.OrderTicketStats{}, err
	}
	args := paperOrderTicketSQLArgs(ticket)
	result, err := r.db.ExecContext(ctx, `
		INSERT INTO paper_order_tickets (
			ticket_id, validation_id, decision_id, intent_id, exchange, category, symbol, interval,
			side, quantity, entry_price, stop_loss, take_profit, leverage, max_loss, confidence,
			reason, created_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8,
			$9, $10, $11, $12, $13, $14, $15, $16,
			$17, $18
		)
		ON CONFLICT (ticket_id) DO NOTHING
	`, args...)
	if err != nil {
		return domainpaper.OrderTicketStats{}, fmt.Errorf("insert paper order ticket %s: %w", ticket.TicketID, err)
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return domainpaper.OrderTicketStats{}, fmt.Errorf("read paper order ticket insert rows affected: %w", err)
	}
	if inserted == 1 {
		return domainpaper.OrderTicketStats{Inserted: 1}, nil
	}
	if err := r.assertExistingOrderTicketMatches(ctx, args); err != nil {
		return domainpaper.OrderTicketStats{}, err
	}
	return domainpaper.OrderTicketStats{Skipped: 1}, nil
}

func (r *PaperOrderTicketRepository) ListOrderTickets(ctx context.Context, query domainpaper.OrderTicketQuery) ([]domainpaper.OrderTicket, error) {
	if err := domainpaper.ValidateOrderTicketQuery(query); err != nil {
		return nil, err
	}
	limit := query.Limit
	if limit <= 0 {
		limit = 1000
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT ticket_id, validation_id, decision_id, intent_id, exchange, category, symbol, interval,
		       side, quantity::text, entry_price::text, stop_loss::text, take_profit::text,
		       leverage::text, max_loss::text, confidence, reason, created_at
		FROM paper_order_tickets
		WHERE ($1::text = '' OR ticket_id = $1)
		  AND ($2::text = '' OR validation_id = $2)
		  AND ($3::text = '' OR decision_id = $3)
		  AND ($4::text = '' OR intent_id = $4)
		  AND ($5::text = '' OR symbol = $5)
		  AND ($6::text = '' OR interval = $6)
		  AND ($7::timestamptz IS NULL OR created_at >= $7)
		  AND ($8::timestamptz IS NULL OR created_at < $8)
		ORDER BY created_at ASC, id ASC
		LIMIT $9
	`, strings.TrimSpace(query.TicketID), strings.TrimSpace(query.ValidationID), strings.TrimSpace(query.DecisionID), strings.TrimSpace(query.IntentID), strings.TrimSpace(query.Symbol), strings.TrimSpace(query.Interval), nullableTime(query.Start), nullableTime(query.End), limit)
	if err != nil {
		return nil, fmt.Errorf("list paper order tickets: %w", err)
	}
	defer rows.Close()

	var tickets []domainpaper.OrderTicket
	for rows.Next() {
		ticket, err := scanPaperOrderTicket(rows)
		if err != nil {
			return nil, err
		}
		tickets = append(tickets, ticket)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate paper order tickets: %w", err)
	}
	if err := domainpaper.ValidateOrderTickets(tickets); err != nil {
		return nil, err
	}
	return tickets, nil
}

func (r *PaperOrderTicketRepository) assertExistingOrderTicketMatches(ctx context.Context, args []any) error {
	var exists int
	if err := r.db.QueryRowContext(ctx, `
		SELECT 1
		FROM paper_order_tickets
		WHERE ticket_id = $1
		  AND validation_id = $2
		  AND decision_id = $3
		  AND intent_id = $4
		  AND exchange = $5
		  AND category = $6
		  AND symbol = $7
		  AND interval = $8
		  AND side = $9
		  AND quantity = $10::numeric
		  AND entry_price = $11::numeric
		  AND stop_loss = $12::numeric
		  AND take_profit = $13::numeric
		  AND leverage = $14::numeric
		  AND max_loss = $15::numeric
		  AND confidence = $16
		  AND reason = $17
		  AND created_at = $18
	`, args...).Scan(&exists); err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("paper order ticket %s already exists with different payload", args[0])
		}
		return fmt.Errorf("verify existing paper order ticket %s: %w", args[0], err)
	}
	return nil
}

func paperOrderTicketSQLArgs(ticket domainpaper.OrderTicket) []any {
	return []any{
		ticket.TicketID,
		ticket.ValidationID,
		ticket.DecisionID,
		ticket.IntentID,
		ticket.Exchange,
		ticket.Category,
		ticket.Symbol,
		ticket.Interval,
		string(ticket.Side),
		ticket.Quantity.String(),
		ticket.EntryPrice.String(),
		ticket.StopLoss.String(),
		ticket.TakeProfit.String(),
		ticket.Leverage.String(),
		ticket.MaxLoss.String(),
		ticket.Confidence,
		ticket.Reason,
		ticket.CreatedAt.UTC(),
	}
}

func scanPaperOrderTicket(scanner interface{ Scan(dest ...any) error }) (domainpaper.OrderTicket, error) {
	var ticket domainpaper.OrderTicket
	var side string
	var quantity, entryPrice, stopLoss, takeProfit, leverage, maxLoss string
	if err := scanner.Scan(
		&ticket.TicketID,
		&ticket.ValidationID,
		&ticket.DecisionID,
		&ticket.IntentID,
		&ticket.Exchange,
		&ticket.Category,
		&ticket.Symbol,
		&ticket.Interval,
		&side,
		&quantity,
		&entryPrice,
		&stopLoss,
		&takeProfit,
		&leverage,
		&maxLoss,
		&ticket.Confidence,
		&ticket.Reason,
		&ticket.CreatedAt,
	); err != nil {
		return domainpaper.OrderTicket{}, fmt.Errorf("scan paper order ticket: %w", err)
	}
	ticket.Side = domainpaper.OrderSide(side)
	values := []struct {
		name   string
		raw    string
		target *decimal.Decimal
	}{
		{"quantity", quantity, &ticket.Quantity},
		{"entry_price", entryPrice, &ticket.EntryPrice},
		{"stop_loss", stopLoss, &ticket.StopLoss},
		{"take_profit", takeProfit, &ticket.TakeProfit},
		{"leverage", leverage, &ticket.Leverage},
		{"max_loss", maxLoss, &ticket.MaxLoss},
	}
	for _, value := range values {
		parsed, err := decimal.NewFromString(value.raw)
		if err != nil {
			return domainpaper.OrderTicket{}, fmt.Errorf("parse paper order ticket %s: %w", value.name, err)
		}
		*value.target = parsed
	}
	ticket.CreatedAt = ticket.CreatedAt.UTC()
	if err := domainpaper.ValidateOrderTicket(ticket); err != nil {
		return domainpaper.OrderTicket{}, err
	}
	return ticket, nil
}
