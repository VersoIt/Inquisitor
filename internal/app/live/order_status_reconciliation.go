package live

import (
	"context"
	"fmt"
	"strings"

	domainlive "github.com/VersoIt/Inquisitor/internal/live"
)

type ReconcileSubmittedOrderStatusRequest struct {
	Submission      domainlive.OrderSubmission
	Acknowledgement domainlive.OrderAcknowledgement
}

type ReconcileSubmittedOrderStatusResult struct {
	Submission      domainlive.OrderSubmission
	Acknowledgement domainlive.OrderAcknowledgement
	Snapshot        domainlive.OrderStatusSnapshot
}

func (s *Service) ReconcileSubmittedOrderStatus(ctx context.Context, req ReconcileSubmittedOrderStatusRequest) (ReconcileSubmittedOrderStatusResult, error) {
	if err := ctx.Err(); err != nil {
		return ReconcileSubmittedOrderStatusResult{}, err
	}
	if s == nil || s.statusReader == nil {
		return ReconcileSubmittedOrderStatusResult{}, fmt.Errorf("live order status reconciliation requires order status reader")
	}
	if err := domainlive.ValidateOrderSubmission(req.Submission); err != nil {
		return ReconcileSubmittedOrderStatusResult{}, err
	}
	if orderAcknowledgementPresent(req.Acknowledgement) {
		if err := domainlive.ValidateOrderAcknowledgement(req.Acknowledgement); err != nil {
			return ReconcileSubmittedOrderStatusResult{}, err
		}
		if err := ensureAcknowledgementMatchesSubmission(req.Submission, req.Acknowledgement); err != nil {
			return ReconcileSubmittedOrderStatusResult{}, err
		}
	}

	snapshot, err := s.statusReader.GetOrderStatus(ctx, domainlive.OrderStatusQuery{
		Exchange:      req.Submission.Exchange,
		Category:      req.Submission.Category,
		Symbol:        req.Submission.Symbol,
		ClientOrderID: req.Submission.ClientOrderID,
	})
	if err != nil {
		return ReconcileSubmittedOrderStatusResult{}, fmt.Errorf("read live order status %q: %w", req.Submission.ClientOrderID, err)
	}
	if err := ensureOrderStatusMatchesSubmission(req.Submission, req.Acknowledgement, snapshot); err != nil {
		return ReconcileSubmittedOrderStatusResult{}, err
	}

	return ReconcileSubmittedOrderStatusResult{
		Submission:      req.Submission,
		Acknowledgement: req.Acknowledgement,
		Snapshot:        snapshot,
	}, nil
}

func ensureOrderStatusMatchesSubmission(
	submission domainlive.OrderSubmission,
	acknowledgement domainlive.OrderAcknowledgement,
	snapshot domainlive.OrderStatusSnapshot,
) error {
	if err := domainlive.ValidateOrderStatusSnapshot(snapshot); err != nil {
		return err
	}
	if snapshot.ClientOrderID != submission.ClientOrderID {
		return fmt.Errorf("live order reconciliation client_order_id %q does not match submission %q", snapshot.ClientOrderID, submission.ClientOrderID)
	}
	if snapshot.Exchange != submission.Exchange {
		return fmt.Errorf("live order reconciliation exchange %q does not match submission %q", snapshot.Exchange, submission.Exchange)
	}
	if snapshot.Category != submission.Category {
		return fmt.Errorf("live order reconciliation category %q does not match submission %q", snapshot.Category, submission.Category)
	}
	if snapshot.Symbol != submission.Symbol {
		return fmt.Errorf("live order reconciliation symbol %q does not match submission %q", snapshot.Symbol, submission.Symbol)
	}
	if snapshot.Side != submission.Side {
		return fmt.Errorf("live order reconciliation side %q does not match submission %q", snapshot.Side, submission.Side)
	}
	if snapshot.Type != submission.Type {
		return fmt.Errorf("live order reconciliation order type %q does not match submission %q", snapshot.Type, submission.Type)
	}
	if snapshot.TimeInForce != submission.TimeInForce {
		return fmt.Errorf("live order reconciliation time_in_force %q does not match submission %q", snapshot.TimeInForce, submission.TimeInForce)
	}
	if snapshot.ReduceOnly != submission.ReduceOnly {
		return fmt.Errorf("live order reconciliation reduce_only %t does not match submission %t", snapshot.ReduceOnly, submission.ReduceOnly)
	}
	if !snapshot.Quantity.Equal(submission.Quantity) {
		return fmt.Errorf("live order reconciliation quantity %s does not match submission %s", snapshot.Quantity, submission.Quantity)
	}
	if acknowledgement.ExchangeOrderID != "" && snapshot.ExchangeOrderID != acknowledgement.ExchangeOrderID {
		return fmt.Errorf("live order reconciliation exchange_order_id %q does not match acknowledgement %q", snapshot.ExchangeOrderID, acknowledgement.ExchangeOrderID)
	}
	return nil
}

func orderAcknowledgementPresent(acknowledgement domainlive.OrderAcknowledgement) bool {
	return strings.TrimSpace(acknowledgement.SubmissionID) != "" ||
		strings.TrimSpace(acknowledgement.ClientOrderID) != "" ||
		strings.TrimSpace(acknowledgement.Exchange) != "" ||
		strings.TrimSpace(acknowledgement.ExchangeOrderID) != "" ||
		strings.TrimSpace(string(acknowledgement.Status)) != "" ||
		strings.TrimSpace(acknowledgement.RejectReason) != "" ||
		!acknowledgement.ReceivedAt.IsZero()
}
