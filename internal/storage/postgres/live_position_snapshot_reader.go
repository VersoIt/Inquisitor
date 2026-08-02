package postgres

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/shopspring/decimal"

	domainlive "github.com/VersoIt/Inquisitor/internal/live"
)

func (r *LiveOrderJournalRepository) GetLatestPositionSnapshot(
	ctx context.Context,
	query domainlive.PositionSnapshotQuery,
) (domainlive.PositionSnapshot, bool, error) {
	if r == nil || r.db == nil {
		return domainlive.PositionSnapshot{}, false, fmt.Errorf("live position snapshot reader requires db")
	}
	if err := domainlive.ValidatePositionSnapshotQuery(query); err != nil {
		return domainlive.PositionSnapshot{}, false, err
	}
	row := r.db.QueryRowContext(ctx, `
		SELECT
			exchange, category, symbol, open, side, size, average_price, position_value,
			mark_price, liquidation_price, leverage, unrealised_pnl, current_realised_pnl,
			cumulative_realised_pnl, exchange_status, position_index, sequence,
			exchange_reduce_only, exchange_created_at, exchange_updated_at, observed_at
		FROM live_position_snapshots
		WHERE exchange = $1
		  AND category = $2
		  AND symbol = $3
		ORDER BY observed_at DESC, id DESC
		LIMIT 1
	`, query.Exchange, query.Category, query.Symbol)
	snapshot, err := scanLivePositionSnapshotRow(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return domainlive.PositionSnapshot{}, false, nil
		}
		return domainlive.PositionSnapshot{}, false, fmt.Errorf("read latest live position snapshot %s: %w", query.Symbol, err)
	}
	return snapshot, true, nil
}

func scanLivePositionSnapshotRow(scanner interface{ Scan(dest ...any) error }) (domainlive.PositionSnapshot, error) {
	var (
		snapshot              domainlive.PositionSnapshot
		side                  string
		size                  string
		averagePrice          string
		positionValue         string
		markPrice             string
		liquidationPrice      string
		leverage              string
		unrealisedPnL         string
		currentRealisedPnL    string
		cumulativeRealisedPnL string
		exchangeStatus        string
		exchangeCreatedAt     sql.NullTime
		exchangeUpdatedAt     sql.NullTime
	)
	if err := scanner.Scan(
		&snapshot.Exchange,
		&snapshot.Category,
		&snapshot.Symbol,
		&snapshot.Open,
		&side,
		&size,
		&averagePrice,
		&positionValue,
		&markPrice,
		&liquidationPrice,
		&leverage,
		&unrealisedPnL,
		&currentRealisedPnL,
		&cumulativeRealisedPnL,
		&exchangeStatus,
		&snapshot.PositionIndex,
		&snapshot.Sequence,
		&snapshot.ExchangeReduceOnly,
		&exchangeCreatedAt,
		&exchangeUpdatedAt,
		&snapshot.ObservedAt,
	); err != nil {
		return domainlive.PositionSnapshot{}, err
	}
	parsed, err := parseLivePositionSnapshotDecimals(
		size,
		averagePrice,
		positionValue,
		markPrice,
		liquidationPrice,
		leverage,
		unrealisedPnL,
		currentRealisedPnL,
		cumulativeRealisedPnL,
	)
	if err != nil {
		return domainlive.PositionSnapshot{}, err
	}
	snapshot.Side = domainlive.OrderSide(side)
	snapshot.Size = parsed.size
	snapshot.AveragePrice = parsed.averagePrice
	snapshot.PositionValue = parsed.positionValue
	snapshot.MarkPrice = parsed.markPrice
	snapshot.LiquidationPrice = parsed.liquidationPrice
	snapshot.Leverage = parsed.leverage
	snapshot.UnrealisedPnL = parsed.unrealisedPnL
	snapshot.CurrentRealisedPnL = parsed.currentRealisedPnL
	snapshot.CumulativeRealisedPnL = parsed.cumulativeRealisedPnL
	snapshot.ExchangeStatus = domainlive.ExchangePositionStatus(exchangeStatus)
	if exchangeCreatedAt.Valid {
		snapshot.ExchangeCreatedAt = exchangeCreatedAt.Time.UTC()
	}
	if exchangeUpdatedAt.Valid {
		snapshot.ExchangeUpdatedAt = exchangeUpdatedAt.Time.UTC()
	}
	snapshot.ObservedAt = snapshot.ObservedAt.UTC()
	if err := domainlive.ValidatePositionSnapshot(snapshot); err != nil {
		return domainlive.PositionSnapshot{}, err
	}
	return snapshot, nil
}

type livePositionSnapshotDecimalFields struct {
	size                  decimal.Decimal
	averagePrice          decimal.Decimal
	positionValue         decimal.Decimal
	markPrice             decimal.Decimal
	liquidationPrice      decimal.Decimal
	leverage              decimal.Decimal
	unrealisedPnL         decimal.Decimal
	currentRealisedPnL    decimal.Decimal
	cumulativeRealisedPnL decimal.Decimal
}

func parseLivePositionSnapshotDecimals(
	size string,
	averagePrice string,
	positionValue string,
	markPrice string,
	liquidationPrice string,
	leverage string,
	unrealisedPnL string,
	currentRealisedPnL string,
	cumulativeRealisedPnL string,
) (livePositionSnapshotDecimalFields, error) {
	var fields livePositionSnapshotDecimalFields
	for _, item := range []struct {
		name   string
		value  string
		target *decimal.Decimal
	}{
		{"size", size, &fields.size},
		{"average_price", averagePrice, &fields.averagePrice},
		{"position_value", positionValue, &fields.positionValue},
		{"mark_price", markPrice, &fields.markPrice},
		{"liquidation_price", liquidationPrice, &fields.liquidationPrice},
		{"leverage", leverage, &fields.leverage},
		{"unrealised_pnl", unrealisedPnL, &fields.unrealisedPnL},
		{"current_realised_pnl", currentRealisedPnL, &fields.currentRealisedPnL},
		{"cumulative_realised_pnl", cumulativeRealisedPnL, &fields.cumulativeRealisedPnL},
	} {
		parsed, err := decimal.NewFromString(item.value)
		if err != nil {
			return livePositionSnapshotDecimalFields{}, fmt.Errorf("scan live position snapshot decimal %s=%q: %w", item.name, item.value, err)
		}
		*item.target = parsed
	}
	return fields, nil
}
