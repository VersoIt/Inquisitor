package realtime

import (
	"fmt"
	"sort"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/VersoIt/Inquisitor/internal/marketdata"
)

type OrderbookBook struct {
	current     marketdata.Orderbook
	initialized bool
}

func (b *OrderbookBook) Apply(update marketdata.Orderbook) (marketdata.Orderbook, error) {
	switch strings.ToLower(strings.TrimSpace(update.Type)) {
	case "snapshot":
		snapshot := cloneOrderbook(update)
		snapshot.Type = snapshotType
		if err := ValidateOrderbookSnapshot(snapshot); err != nil {
			return marketdata.Orderbook{}, err
		}
		b.current = snapshot
		b.initialized = true
		return cloneOrderbook(b.current), nil
	case "delta":
		if !b.initialized {
			return marketdata.Orderbook{}, fmt.Errorf("orderbook delta received before snapshot")
		}
		candidate := cloneOrderbook(b.current)
		if err := applyDelta(&candidate, update); err != nil {
			return marketdata.Orderbook{}, err
		}
		if err := ValidateOrderbookSnapshot(candidate); err != nil {
			return marketdata.Orderbook{}, err
		}
		b.current = candidate
		return cloneOrderbook(b.current), nil
	default:
		return marketdata.Orderbook{}, fmt.Errorf("unsupported orderbook update type %q", update.Type)
	}
}

func applyDelta(current *marketdata.Orderbook, update marketdata.Orderbook) error {
	if !sameOrderbookIdentity(*current, update) {
		return fmt.Errorf("orderbook delta identity mismatch")
	}

	current.Bids = applyLevelDeltas(current.Bids, update.Bids, true)
	current.Asks = applyLevelDeltas(current.Asks, update.Asks, false)
	current.UpdateID = update.UpdateID
	current.Sequence = update.Sequence
	current.ExchangeTime = update.ExchangeTime
	current.MatchingEngineTime = update.MatchingEngineTime
	current.Type = snapshotType
	return nil
}

func applyLevelDeltas(current, deltas []marketdata.OrderbookLevel, descending bool) []marketdata.OrderbookLevel {
	levels := make(map[string]marketdata.OrderbookLevel, len(current)+len(deltas))
	for _, level := range current {
		levels[level.Price.String()] = level
	}

	for _, delta := range deltas {
		key := delta.Price.String()
		if delta.Quantity.Equal(decimal.Zero) {
			delete(levels, key)
			continue
		}
		levels[key] = delta
	}

	merged := make([]marketdata.OrderbookLevel, 0, len(levels))
	for _, level := range levels {
		merged = append(merged, level)
	}
	sort.Slice(merged, func(i, j int) bool {
		if descending {
			return merged[i].Price.GreaterThan(merged[j].Price)
		}
		return merged[i].Price.LessThan(merged[j].Price)
	})
	return merged
}

func sameOrderbookIdentity(left, right marketdata.Orderbook) bool {
	return strings.EqualFold(strings.TrimSpace(left.Exchange), strings.TrimSpace(right.Exchange)) &&
		strings.EqualFold(strings.TrimSpace(left.Category), strings.TrimSpace(right.Category)) &&
		strings.EqualFold(strings.TrimSpace(left.Symbol), strings.TrimSpace(right.Symbol))
}

func cloneOrderbook(book marketdata.Orderbook) marketdata.Orderbook {
	cloned := book
	cloned.Bids = cloneLevels(book.Bids)
	cloned.Asks = cloneLevels(book.Asks)
	return cloned
}

func cloneLevels(levels []marketdata.OrderbookLevel) []marketdata.OrderbookLevel {
	if len(levels) == 0 {
		return nil
	}
	cloned := make([]marketdata.OrderbookLevel, len(levels))
	copy(cloned, levels)
	return cloned
}
