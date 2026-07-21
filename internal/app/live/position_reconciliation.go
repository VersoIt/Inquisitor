package live

import (
	"context"
	"fmt"

	"github.com/shopspring/decimal"

	domainlive "github.com/VersoIt/Inquisitor/internal/live"
)

type ReconcileSubmittedOrderPositionRequest struct {
	Submission  domainlive.OrderSubmission
	OrderStatus domainlive.OrderStatusSnapshot
}

type ReconcileSubmittedOrderPositionResult struct {
	Submission    domainlive.OrderSubmission
	OrderStatus   domainlive.OrderStatusSnapshot
	Snapshot      domainlive.PositionSnapshot
	SnapshotStats domainlive.PositionSnapshotStats
}

func (s *Service) ReconcileSubmittedOrderPosition(ctx context.Context, req ReconcileSubmittedOrderPositionRequest) (ReconcileSubmittedOrderPositionResult, error) {
	if err := ctx.Err(); err != nil {
		return ReconcileSubmittedOrderPositionResult{}, err
	}
	if s == nil || s.positionReader == nil {
		return ReconcileSubmittedOrderPositionResult{}, fmt.Errorf("live position reconciliation requires position snapshot reader")
	}
	if s.positionJournal == nil {
		return ReconcileSubmittedOrderPositionResult{}, fmt.Errorf("live position reconciliation requires position snapshot journal")
	}
	if err := domainlive.ValidateOrderSubmission(req.Submission); err != nil {
		return ReconcileSubmittedOrderPositionResult{}, err
	}
	if err := ensureOrderStatusMatchesSubmission(req.Submission, domainlive.OrderAcknowledgement{}, req.OrderStatus); err != nil {
		return ReconcileSubmittedOrderPositionResult{}, err
	}

	snapshot, err := s.positionReader.GetPositionSnapshot(ctx, domainlive.PositionSnapshotQuery{
		Exchange: req.Submission.Exchange,
		Category: req.Submission.Category,
		Symbol:   req.Submission.Symbol,
	})
	if err != nil {
		return ReconcileSubmittedOrderPositionResult{}, fmt.Errorf("read live position snapshot %q: %w", req.Submission.Symbol, err)
	}
	if err := ensurePositionMatchesSubmittedOrder(req.Submission, req.OrderStatus, snapshot); err != nil {
		return ReconcileSubmittedOrderPositionResult{}, err
	}
	stats, err := s.positionJournal.RecordPositionSnapshot(ctx, snapshot)
	if err != nil {
		return ReconcileSubmittedOrderPositionResult{}, fmt.Errorf("record live position snapshot %q: %w", snapshot.Symbol, err)
	}
	if stats.Total() == 0 {
		return ReconcileSubmittedOrderPositionResult{}, fmt.Errorf("live position snapshot journal did not record %q", snapshot.Symbol)
	}

	return ReconcileSubmittedOrderPositionResult{
		Submission:    req.Submission,
		OrderStatus:   req.OrderStatus,
		Snapshot:      snapshot,
		SnapshotStats: stats,
	}, nil
}

func ensurePositionMatchesSubmittedOrder(
	submission domainlive.OrderSubmission,
	orderStatus domainlive.OrderStatusSnapshot,
	position domainlive.PositionSnapshot,
) error {
	if err := domainlive.ValidatePositionSnapshot(position); err != nil {
		return err
	}
	if position.Exchange != submission.Exchange {
		return fmt.Errorf("live position reconciliation exchange %q does not match submission %q", position.Exchange, submission.Exchange)
	}
	if position.Category != submission.Category {
		return fmt.Errorf("live position reconciliation category %q does not match submission %q", position.Category, submission.Category)
	}
	if position.Symbol != submission.Symbol {
		return fmt.Errorf("live position reconciliation symbol %q does not match submission %q", position.Symbol, submission.Symbol)
	}

	executedQuantity := orderStatus.CumulativeExecutedQuantity
	if executedQuantity.IsNegative() {
		return fmt.Errorf("live position reconciliation executed quantity must be non-negative")
	}
	if executedQuantity.IsZero() {
		if position.Open {
			return fmt.Errorf("live position reconciliation found open position size %s but submitted order has no executed quantity", position.Size)
		}
		return nil
	}
	if !position.Open {
		return fmt.Errorf("live position reconciliation expected open position size %s but exchange position is flat", executedQuantity)
	}
	if position.Side != submission.Side {
		return fmt.Errorf("live position reconciliation side %q does not match submission %q", position.Side, submission.Side)
	}
	if !position.Size.Equal(executedQuantity) {
		return fmt.Errorf("live position reconciliation size %s does not match executed quantity %s", position.Size, executedQuantity)
	}
	if submission.ReduceOnly && position.Size.GreaterThan(decimal.Zero) {
		return fmt.Errorf("live position reconciliation does not support reduce_only position expansion")
	}
	return nil
}
