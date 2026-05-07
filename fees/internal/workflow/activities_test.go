package workflow_test

import (
	"context"
	"testing"

	"encore.app/fees/internal/domain"
	billingstore "encore.app/fees/internal/store/billing"
	"encore.app/fees/internal/testutil/mocks"
	workflow "encore.app/fees/internal/workflow"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestActivitiesTransitionBillStatusUsesUpdateBill(t *testing.T) {
	t.Parallel()

	store := mocks.NewActivityStore(t)
	activities := &workflow.Activities{Store: store}
	status := domain.BillStatusFinalizing

	store.On("UpdateBill", mock.Anything, mock.MatchedBy(func(params billingstore.UpdateBillParams) bool {
		return params.BillID == "bill-123" &&
			params.Status != nil &&
			*params.Status == status &&
			params.FailedAt == nil &&
			params.FailureReason == nil
	})).Return(nil).Once()

	err := activities.TransitionBillStatus(context.Background(), workflow.TransitionBillStatusActivityInput{
		BillID: "bill-123",
		Status: status,
	})
	require.NoError(t, err)
}

func TestActivitiesMarkBillFailedUsesUpdateBill(t *testing.T) {
	t.Parallel()

	store := mocks.NewActivityStore(t)
	activities := &workflow.Activities{Store: store}

	store.On("UpdateBill", mock.Anything, mock.MatchedBy(func(params billingstore.UpdateBillParams) bool {
		return params.BillID == "bill-123" &&
			params.Status != nil &&
			*params.Status == domain.BillStatusFailed &&
			params.SnapshotTotalAmountMinor != nil &&
			*params.SnapshotTotalAmountMinor == 275 &&
			params.FailedAt != nil &&
			params.FailureReason != nil &&
			*params.FailureReason == "finalize failed"
	})).Return(nil).Once()

	err := activities.MarkBillFailed(context.Background(), workflow.MarkBillFailedActivityInput{
		BillID:                   "bill-123",
		FailureReason:            "finalize failed",
		SnapshotTotalAmountMinor: 275,
	})
	require.NoError(t, err)
}

func TestActivitiesPersistLineItemsUsesStore(t *testing.T) {
	t.Parallel()

	store := mocks.NewActivityStore(t)
	activities := &workflow.Activities{Store: store}

	store.On("InsertLineItems", mock.Anything, mock.MatchedBy(func(params billingstore.InsertLineItemsParams) bool {
		return params.BillID == "bill-123" &&
			params.IdempotencyKey == "idem-1" &&
			params.CurrencyCode == domain.USD() &&
			len(params.LineItems) == 1 &&
			params.LineItems[0].ExternalReferenceID == "line-1" &&
			params.LineItems[0].AmountMinor == 30
	})).Return(&billingstore.InsertLineItemsResult{
		SuccessLineItemIDs: map[string]string{"line-1": "id-1"},
		BatchFailure:       &billingstore.BatchFailure{RecordReasons: map[string]string{"line-2": "duplicate"}},
		AmountDeltaMinor:   30,
	}, nil).Once()

	result, err := activities.PersistLineItems(context.Background(), workflow.PersistLineItemsActivityInput{
		BillID:         "bill-123",
		IdempotencyKey: "idem-1",
		CurrencyCode:   domain.USD(),
		LineItems: []domain.AddLineItemInput{{
			ExternalReferenceID: "line-1",
			AmountMinor:         30,
		}},
	})
	require.NoError(t, err)
	assert.Equal(t, int64(30), result.AmountDeltaMinor)
	assert.Equal(t, "id-1", result.SuccessLineItemIDsMap["line-1"])
	assert.Equal(t, "duplicate", result.FailedLineItemReasonsMap["line-2"])
}

func TestActivitiesFinalizeBillUsesStore(t *testing.T) {
	t.Parallel()

	store := mocks.NewActivityStore(t)
	activities := &workflow.Activities{Store: store}

	store.On("FinalizeBill", mock.Anything, "bill-123").Return(&billingstore.FinalizeBillResult{
		BillStatus:       domain.BillStatusClosed,
		TotalAmountMinor: 99,
		FailureReason:    "none",
	}, nil).Once()

	result, err := activities.FinalizeBill(context.Background(), workflow.FinalizeBillActivityInput{BillID: "bill-123"})
	require.NoError(t, err)
	assert.Equal(t, domain.BillStatusClosed, result.BillStatus)
	assert.Equal(t, int64(99), result.TotalAmountMinor)
	assert.Equal(t, "none", result.FailureReason)
}

func TestActivitiesRequireConfiguredStore(t *testing.T) {
	t.Parallel()

	activities := &workflow.Activities{}

	if err := activities.TransitionBillStatus(context.Background(), workflow.TransitionBillStatusActivityInput{}); err == nil {
		t.Fatal("expected TransitionBillStatus to fail without store")
	}
	if _, err := activities.PersistLineItems(context.Background(), workflow.PersistLineItemsActivityInput{}); err == nil {
		t.Fatal("expected PersistLineItems to fail without store")
	}
	if _, err := activities.FinalizeBill(context.Background(), workflow.FinalizeBillActivityInput{}); err == nil {
		t.Fatal("expected FinalizeBill to fail without store")
	}
	if err := activities.MarkBillFailed(context.Background(), workflow.MarkBillFailedActivityInput{}); err == nil {
		t.Fatal("expected MarkBillFailed to fail without store")
	}
}
