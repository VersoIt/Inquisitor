package paper

import (
	"context"
	"fmt"
	"time"

	"github.com/VersoIt/Inquisitor/internal/clock"
	domain "github.com/VersoIt/Inquisitor/internal/paper"
)

type AccountSnapshotService struct {
	reader domain.AccountLedgerReader
	clock  clock.Clock
}

func NewAccountSnapshotService(reader domain.AccountLedgerReader, value clock.Clock) *AccountSnapshotService {
	return &AccountSnapshotService{reader: reader, clock: value}
}

func (s *AccountSnapshotService) Snapshot(ctx context.Context, validationID string) (domain.AccountSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return domain.AccountSnapshot{}, err
	}
	if s == nil || s.reader == nil || s.clock == nil {
		return domain.AccountSnapshot{}, fmt.Errorf("paper account requires ledger reader and clock")
	}
	q := domain.AccountLedgerQuery{ValidationID: validationID, AsOf: s.clock.Now().UTC().Truncate(time.Microsecond)}
	if err := domain.ValidateAccountLedgerQuery(q); err != nil {
		return domain.AccountSnapshot{}, err
	}
	ledger, err := s.reader.LoadPaperAccountLedger(ctx, q)
	if err != nil {
		return domain.AccountSnapshot{}, fmt.Errorf("load paper account ledger: %w", err)
	}
	if ledger.Validation.ValidationID != q.ValidationID {
		return domain.AccountSnapshot{}, fmt.Errorf("paper account source validation mismatch")
	}
	if err := ctx.Err(); err != nil {
		return domain.AccountSnapshot{}, err
	}
	snapshot, err := domain.BuildAccountSnapshot(ledger, q.AsOf)
	if err != nil {
		return domain.AccountSnapshot{}, err
	}
	if err := ctx.Err(); err != nil {
		return domain.AccountSnapshot{}, err
	}
	return snapshot, nil
}
