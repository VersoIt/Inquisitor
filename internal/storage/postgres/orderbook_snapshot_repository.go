package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/VersoIt/Inquisitor/internal/marketdata"
	"github.com/VersoIt/Inquisitor/internal/marketdata/validator"
)

type OrderbookSnapshotRepository struct {
	db *sql.DB
}

func NewOrderbookSnapshotRepository(db *sql.DB) *OrderbookSnapshotRepository {
	return &OrderbookSnapshotRepository{db: db}
}

func (r *OrderbookSnapshotRepository) CreateOrderbookSnapshots(ctx context.Context, snapshots []marketdata.OrderbookSnapshot) (marketdata.WriteStats, error) {
	if len(snapshots) == 0 {
		return marketdata.WriteStats{}, nil
	}
	if err := validator.ValidateOrderbookSnapshots(snapshots); err != nil {
		return marketdata.WriteStats{}, err
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return marketdata.WriteStats{}, fmt.Errorf("begin orderbook snapshot transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	statement, err := tx.PrepareContext(ctx, `
		INSERT INTO orderbook_snapshots (
			exchange, category, symbol, depth, bids_json, asks_json,
			best_bid, best_ask, spread, spread_bps, update_id, sequence,
			exchange_time, matching_engine_time, created_at
		) VALUES (
			$1, $2, $3, $4, $5, $6,
			$7, $8, $9, $10, $11, $12,
			$13, $14, $15
		)
	`)
	if err != nil {
		return marketdata.WriteStats{}, fmt.Errorf("prepare orderbook snapshot insert: %w", err)
	}
	defer statement.Close()

	var stats marketdata.WriteStats
	for _, snapshot := range snapshots {
		bidsJSON, err := marshalOrderbookLevels(snapshot.Bids)
		if err != nil {
			return marketdata.WriteStats{}, fmt.Errorf("marshal orderbook bids %s: %w", snapshot.Symbol, err)
		}
		asksJSON, err := marshalOrderbookLevels(snapshot.Asks)
		if err != nil {
			return marketdata.WriteStats{}, fmt.Errorf("marshal orderbook asks %s: %w", snapshot.Symbol, err)
		}

		if _, err := statement.ExecContext(
			ctx,
			snapshot.Exchange,
			snapshot.Category,
			snapshot.Symbol,
			snapshot.Depth,
			bidsJSON,
			asksJSON,
			snapshot.BestBid.String(),
			snapshot.BestAsk.String(),
			snapshot.Spread.String(),
			snapshot.SpreadBPS.String(),
			snapshot.UpdateID,
			snapshot.Sequence,
			snapshot.ExchangeTime.UTC(),
			nullableTime(snapshot.MatchingEngineTime),
			snapshot.CreatedAt.UTC(),
		); err != nil {
			return marketdata.WriteStats{}, fmt.Errorf("insert orderbook snapshot %s %s: %w", snapshot.Symbol, snapshot.ExchangeTime.UTC().Format(timeFormatRFC3339Nano), err)
		}
		stats.Inserted++
	}

	if err := tx.Commit(); err != nil {
		return marketdata.WriteStats{}, fmt.Errorf("commit orderbook snapshot transaction: %w", err)
	}
	committed = true

	return stats, nil
}

func (r *OrderbookSnapshotRepository) ListOrderbookSnapshots(ctx context.Context, query marketdata.OrderbookSnapshotQuery) ([]marketdata.OrderbookSnapshot, error) {
	if err := validateOrderbookSnapshotQuery(query); err != nil {
		return nil, err
	}

	limit := query.Limit
	if limit <= 0 {
		limit = 1000
	}

	rows, err := r.db.QueryContext(ctx, `
		SELECT exchange, category, symbol, depth, bids_json::text, asks_json::text,
		       best_bid::text, best_ask::text, spread::text, spread_bps::text,
		       update_id, sequence, exchange_time, matching_engine_time, created_at
		FROM orderbook_snapshots
		WHERE exchange = $1
		  AND category = $2
		  AND symbol = $3
		  AND ($4::timestamptz IS NULL OR exchange_time >= $4)
		  AND ($5::timestamptz IS NULL OR exchange_time < $5)
		ORDER BY exchange_time ASC, id ASC
		LIMIT $6
	`, query.Exchange, query.Category, query.Symbol, nullableTime(query.Start), nullableTime(query.End), limit)
	if err != nil {
		return nil, fmt.Errorf("list orderbook snapshots: %w", err)
	}
	defer rows.Close()

	var snapshots []marketdata.OrderbookSnapshot
	for rows.Next() {
		snapshot, err := scanOrderbookSnapshot(rows)
		if err != nil {
			return nil, err
		}
		snapshots = append(snapshots, snapshot)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate orderbook snapshots: %w", err)
	}
	if err := validator.ValidateOrderbookSnapshots(snapshots); err != nil {
		return nil, err
	}

	return snapshots, nil
}

func validateOrderbookSnapshotQuery(query marketdata.OrderbookSnapshotQuery) error {
	if strings.TrimSpace(query.Exchange) == "" {
		return errors.New("exchange is required")
	}
	if strings.TrimSpace(query.Category) == "" {
		return errors.New("category is required")
	}
	if strings.TrimSpace(query.Symbol) == "" {
		return errors.New("symbol is required")
	}
	return nil
}

func scanOrderbookSnapshot(scanner interface {
	Scan(dest ...any) error
}) (marketdata.OrderbookSnapshot, error) {
	var snapshot marketdata.OrderbookSnapshot
	var bidsJSON, asksJSON string
	var bestBid, bestAsk, spread, spreadBPS string
	var matchingEngineTime sql.NullTime

	if err := scanner.Scan(
		&snapshot.Exchange,
		&snapshot.Category,
		&snapshot.Symbol,
		&snapshot.Depth,
		&bidsJSON,
		&asksJSON,
		&bestBid,
		&bestAsk,
		&spread,
		&spreadBPS,
		&snapshot.UpdateID,
		&snapshot.Sequence,
		&snapshot.ExchangeTime,
		&matchingEngineTime,
		&snapshot.CreatedAt,
	); err != nil {
		return marketdata.OrderbookSnapshot{}, fmt.Errorf("scan orderbook snapshot: %w", err)
	}

	var err error
	if snapshot.Bids, err = unmarshalOrderbookLevels(bidsJSON); err != nil {
		return marketdata.OrderbookSnapshot{}, fmt.Errorf("parse orderbook bids: %w", err)
	}
	if snapshot.Asks, err = unmarshalOrderbookLevels(asksJSON); err != nil {
		return marketdata.OrderbookSnapshot{}, fmt.Errorf("parse orderbook asks: %w", err)
	}
	if snapshot.BestBid, err = decimal.NewFromString(bestBid); err != nil {
		return marketdata.OrderbookSnapshot{}, fmt.Errorf("parse orderbook best_bid: %w", err)
	}
	if snapshot.BestAsk, err = decimal.NewFromString(bestAsk); err != nil {
		return marketdata.OrderbookSnapshot{}, fmt.Errorf("parse orderbook best_ask: %w", err)
	}
	if snapshot.Spread, err = decimal.NewFromString(spread); err != nil {
		return marketdata.OrderbookSnapshot{}, fmt.Errorf("parse orderbook spread: %w", err)
	}
	if snapshot.SpreadBPS, err = decimal.NewFromString(spreadBPS); err != nil {
		return marketdata.OrderbookSnapshot{}, fmt.Errorf("parse orderbook spread_bps: %w", err)
	}

	snapshot.ExchangeTime = snapshot.ExchangeTime.UTC()
	if matchingEngineTime.Valid {
		snapshot.MatchingEngineTime = matchingEngineTime.Time.UTC()
	}
	snapshot.CreatedAt = snapshot.CreatedAt.UTC()

	return snapshot, nil
}

type orderbookLevelJSON [2]string

func marshalOrderbookLevels(levels []marketdata.OrderbookLevel) (string, error) {
	payload := make([]orderbookLevelJSON, 0, len(levels))
	for _, level := range levels {
		payload = append(payload, orderbookLevelJSON{level.Price.String(), level.Quantity.String()})
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func unmarshalOrderbookLevels(raw string) ([]marketdata.OrderbookLevel, error) {
	var payload []orderbookLevelJSON
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return nil, err
	}

	levels := make([]marketdata.OrderbookLevel, 0, len(payload))
	for i, item := range payload {
		price, err := decimal.NewFromString(item[0])
		if err != nil {
			return nil, fmt.Errorf("level[%d] price: %w", i, err)
		}
		quantity, err := decimal.NewFromString(item[1])
		if err != nil {
			return nil, fmt.Errorf("level[%d] quantity: %w", i, err)
		}
		levels = append(levels, marketdata.OrderbookLevel{Price: price, Quantity: quantity})
	}
	return levels, nil
}

const timeFormatRFC3339Nano = "2006-01-02T15:04:05.999999999Z07:00"
