package workflow

import (
	"context"
	"errors"
	"time"

	"encore.app/fees/internal/domain"
	billingstore "encore.app/fees/internal/store/billing"
	"go.temporal.io/sdk/temporal"
)

type ActivityStore interface {
	UpdateBill(ctx context.Context, params billingstore.UpdateBillParams) error
	InsertLineItems(ctx context.Context, params billingstore.InsertLineItemsParams) (*billingstore.InsertLineItemsResult, error)
	FinalizeBill(ctx context.Context, billID string) (*billingstore.FinalizeBillResult, error)
}

type Activities struct {
	Store ActivityStore
}

func (a *Activities) TransitionBillStatus(ctx context.Context, input TransitionBillStatusActivityInput) error {
	if a == nil || a.Store == nil {
		return errors.New("workflow activity store is not configured")
	}
	status := input.Status
	return a.Store.UpdateBill(ctx, billingstore.UpdateBillParams{
		BillID: input.BillID,
		Status: &status,
	})
}

func (a *Activities) PersistLineItems(ctx context.Context, input PersistLineItemsActivityInput) (*PersistLineItemsActivityResult, error) {
	if a == nil || a.Store == nil {
		return nil, errors.New("workflow activity store is not configured")
	}
	lineItems := make([]billingstore.LineItemInsert, 0, len(input.LineItems))
	for _, item := range input.LineItems {
		lineItems = append(lineItems, billingstore.LineItemInsert{
			ExternalReferenceID: item.ExternalReferenceID,
			OccurredAt:          item.OccurredAt,
			AmountMinor:         item.AmountMinor,
			Description:         item.Description,
		})
	}
	result, err := a.Store.InsertLineItems(ctx, billingstore.InsertLineItemsParams{
		IdempotencyKey: input.IdempotencyKey,
		BillID:         input.BillID,
		CurrencyCode:   input.CurrencyCode,
		LineItems:      lineItems,
	})
	if err != nil {
		var unavailable *billingstore.StoreUnavailableError
		if errors.As(err, &unavailable) {
			return nil, temporal.NewApplicationError(
				"store unavailable",
				"StoreUnavailableError",
				unavailable.Error(),
			)
		}
		return nil, err
	}
	return &PersistLineItemsActivityResult{
		AmountDeltaMinor:         result.AmountDeltaMinor,
		SuccessLineItemIDsMap:    result.SuccessLineItemIDs,
		FailedLineItemReasonsMap: batchFailureReasons(result.BatchFailure),
	}, nil
}

func (a *Activities) FinalizeBill(ctx context.Context, input FinalizeBillActivityInput) (*FinalizeBillActivityResult, error) {
	if a == nil || a.Store == nil {
		return nil, errors.New("workflow activity store is not configured")
	}
	result, err := a.Store.FinalizeBill(ctx, input.BillID)
	if err != nil {
		return nil, err
	}
	return &FinalizeBillActivityResult{
		BillStatus:       result.BillStatus,
		TotalAmountMinor: result.TotalAmountMinor,
		FailureReason:    result.FailureReason,
	}, nil
}

func (a *Activities) MarkBillFailed(ctx context.Context, input MarkBillFailedActivityInput) error {
	if a == nil || a.Store == nil {
		return errors.New("workflow activity store is not configured")
	}
	status := domain.BillStatusFailed
	failedAt := time.Now().UTC()
	return a.Store.UpdateBill(ctx, billingstore.UpdateBillParams{
		BillID:                   input.BillID,
		Status:                   &status,
		SnapshotTotalAmountMinor: &input.SnapshotTotalAmountMinor,
		FailedAt:                 &failedAt,
		FailureReason:            &input.FailureReason,
	})
}

func batchFailureReasons(batch *billingstore.BatchFailure) map[string]string {
	if batch == nil {
		return map[string]string{}
	}
	return batch.RecordReasons
}
