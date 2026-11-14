package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"slices"

	"github.com/VersoIt/Inquisitor/internal/strategy"
)

type PaperMarketRepository struct{ db *sql.DB }

func NewPaperMarketRepository(db *sql.DB) *PaperMarketRepository {
	return &PaperMarketRepository{db: db}
}

func (r *PaperMarketRepository) LoadPaperMarket(ctx context.Context, q strategy.PaperMarketQuery) (strategy.PaperMarketFacts, error) {
	if err := ctx.Err(); err != nil {
		return strategy.PaperMarketFacts{}, err
	}
	if r == nil || r.db == nil {
		return strategy.PaperMarketFacts{}, fmt.Errorf("paper market repository requires database")
	}
	if err := strategy.ValidatePaperMarketQuery(q); err != nil {
		return strategy.PaperMarketFacts{}, err
	}
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return strategy.PaperMarketFacts{}, err
	}
	defer tx.Rollback()
	s := q.Scope
	var out strategy.PaperMarketFacts
	rows, err := tx.QueryContext(ctx, `SELECT exchange,category,symbol,interval,open_time,close_time,
 open::text,high::text,low::text,close::text,volume::text,turnover::text,is_closed
 FROM candles WHERE exchange=$1 AND category=$2 AND symbol=$3 AND interval=$4 AND is_closed
 ORDER BY open_time DESC LIMIT $5`, s.Exchange, s.Category, s.Symbol, s.Interval, strategy.PaperMarketCandleCount)
	if err != nil {
		return strategy.PaperMarketFacts{}, err
	}
	for rows.Next() {
		c, err := scanCandle(rows)
		if err != nil {
			rows.Close()
			return strategy.PaperMarketFacts{}, err
		}
		out.Candles = append(out.Candles, c)
	}
	iterationErr := rows.Err()
	closeErr := rows.Close()
	if iterationErr != nil {
		return strategy.PaperMarketFacts{}, iterationErr
	}
	if closeErr != nil {
		return strategy.PaperMarketFacts{}, closeErr
	}
	if len(out.Candles) != strategy.PaperMarketCandleCount {
		return strategy.PaperMarketFacts{}, fmt.Errorf("paper market candle history is incomplete")
	}
	slices.Reverse(out.Candles)
	out.Regime, err = scanRegimeState(tx.QueryRowContext(ctx, `SELECT exchange,category,symbol,interval,open_time,close_time,calculated_at,
 regime,candidate_regime,confidence,no_trade,reasons_json::text FROM regime_states
 WHERE exchange=$1 AND category=$2 AND symbol=$3 AND interval=$4 ORDER BY close_time DESC,id DESC LIMIT 1`, s.Exchange, s.Category, s.Symbol, s.Interval))
	if err != nil {
		return strategy.PaperMarketFacts{}, fmt.Errorf("load latest paper regime: %w", err)
	}
	out.Book, err = scanOrderbookSnapshot(tx.QueryRowContext(ctx, `SELECT exchange,category,symbol,depth,
 CASE WHEN octet_length(bids_json::text)<=$4 THEN bids_json::text END,
 CASE WHEN octet_length(asks_json::text)<=$4 THEN asks_json::text END,
 best_bid::text,best_ask::text,spread::text,spread_bps::text,update_id,sequence,exchange_time,matching_engine_time,created_at
 FROM orderbook_snapshots WHERE exchange=$1 AND category=$2 AND symbol=$3 ORDER BY exchange_time DESC,id DESC LIMIT 1`, s.Exchange, s.Category, s.Symbol, strategy.MaxPaperBookJSONBytes))
	if err != nil {
		return strategy.PaperMarketFacts{}, fmt.Errorf("load latest paper book: %w", err)
	}
	out.Instrument, err = scanInstrument(tx.QueryRowContext(ctx, `SELECT exchange,category,symbol,base_coin,quote_coin,status,
 tick_size::text,qty_step::text,min_order_qty::text,max_order_qty::text,max_market_order_qty::text,min_notional_value::text,
 price_scale,leverage_filter_json::text,price_filter_json::text,lot_size_filter_json::text,raw_json::text,updated_at
 FROM instruments WHERE exchange=$1 AND category=$2 AND symbol=$3`, s.Exchange, s.Category, s.Symbol))
	if err != nil {
		return strategy.PaperMarketFacts{}, fmt.Errorf("load paper instrument: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return strategy.PaperMarketFacts{}, err
	}
	return out, nil
}
