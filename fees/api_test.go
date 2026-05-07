package fees

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"testing"
	"time"

	"encore.dev/beta/errs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"encore.app/fees/internal/domain"
	"encore.app/fees/internal/pagination"
	billingstore "encore.app/fees/internal/store/billing"
	"encore.app/fees/internal/testutil/mocks"
	workflowpkg "encore.app/fees/internal/workflow"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/temporal"
)

const (
	testBillID         = "11111111-1111-4111-8111-111111111111"
	testExistingBillID = "22222222-2222-4222-8222-222222222222"
)

func TestCreateBillCreatesBillAndStartsWorkflow(t *testing.T) {
	t.Parallel()

	now := fixedNow()
	billing := mocks.NewBillingStore(t)
	workflow := mocks.NewWorkflowClient(t)
	idempotency := mocks.NewIdempotencyStore(t)

	insertedBill := &domain.Bill{
		ID:                          testBillID,
		AccountID:                   "acct_123",
		ExternalReferenceID:         "bill-001",
		CurrencyCode:                domain.USD(),
		Status:                      domain.BillStatusOpen,
		BillingPeriodStartAt:        time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
		BillingPeriodEndAt:          time.Date(2026, 5, 31, 23, 59, 59, 0, time.UTC),
		LineItemsSubmissionDeadline: time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC),
	}
	billing.On("InsertBill", mock.Anything, mock.MatchedBy(func(params billingstore.InsertBillParams) bool {
		return params.IdempotencyKey == "idem-create-001" &&
			params.AccountID == "acct_123" &&
			params.ExternalReferenceID == "bill-001" &&
			params.CurrencyCode == domain.USD()
	})).Return(&billingstore.InsertBillResult{
		Bill:   insertedBill,
		Status: billingstore.InsertBillCreated,
	}, nil).Once()
	workflow.On("StartBillWorkflow", mock.Anything, mock.MatchedBy(func(input workflowpkg.StartBillWorkflowInput) bool {
		return input.BillID == testBillID && input.CurrencyCode == domain.USD()
	})).Return(nil).Once()

	svc := newServiceWithStores(billing, idempotency, workflow, nil, func() time.Time { return now })

	resp, err := svc.CreateBill(context.Background(), &CreateBillRequest{
		IdempotencyKey:              "idem-create-001",
		AccountID:                   "acct_123",
		ExternalReferenceID:         "bill-001",
		CurrencyCode:                "USD",
		BillingPeriodStartAt:        time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
		BillingPeriodEndAt:          time.Date(2026, 5, 31, 23, 59, 59, 0, time.UTC),
		LineItemsSubmissionDeadline: time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC),
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, testBillID, resp.BillID)
	assert.Equal(t, http.StatusCreated, resp.Status)
}

func TestCreateBillRejectsExistingBusinessReferenceWithNewIdempotencyKey(t *testing.T) {
	t.Parallel()

	billing := mocks.NewBillingStore(t)
	workflow := mocks.NewWorkflowClient(t)
	idempotency := mocks.NewIdempotencyStore(t)

	billing.On("InsertBill", mock.Anything, mock.Anything).Return(&billingstore.InsertBillResult{
		Bill:   &domain.Bill{ID: testExistingBillID},
		Status: billingstore.InsertBillConflict,
	}, nil).Once()

	svc := newServiceWithStores(billing, idempotency, workflow, nil, fixedNow)

	_, err := svc.CreateBill(context.Background(), &CreateBillRequest{
		IdempotencyKey:              "idem-create-002",
		AccountID:                   "acct_123",
		ExternalReferenceID:         "bill-001",
		CurrencyCode:                "USD",
		BillingPeriodStartAt:        time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
		BillingPeriodEndAt:          time.Date(2026, 5, 31, 23, 59, 59, 0, time.UTC),
		LineItemsSubmissionDeadline: time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC),
	})
	require.Error(t, err)
	assert.Equal(t, errs.Aborted, errs.Code(err))
}

func TestCreateBillTreatsReplayAsSuccessWhenWorkflowAlreadyStarted(t *testing.T) {
	t.Parallel()

	billing := mocks.NewBillingStore(t)
	workflow := mocks.NewWorkflowClient(t)
	idempotency := mocks.NewIdempotencyStore(t)

	billing.On("InsertBill", mock.Anything, mock.Anything).Return(&billingstore.InsertBillResult{
		Bill: &domain.Bill{
			ID:           testExistingBillID,
			CurrencyCode: domain.USD(),
			Status:       domain.BillStatusOpen,
		},
		Status: billingstore.InsertBillReplayed,
	}, nil).Once()
	workflow.On("StartBillWorkflow", mock.Anything, mock.Anything).Return(&serviceerror.WorkflowExecutionAlreadyStarted{}).Once()

	svc := newServiceWithStores(billing, idempotency, workflow, nil, fixedNow)

	resp, err := svc.CreateBill(context.Background(), &CreateBillRequest{
		IdempotencyKey:              "idem-create-001",
		AccountID:                   "acct_123",
		ExternalReferenceID:         "bill-001",
		CurrencyCode:                "USD",
		BillingPeriodStartAt:        time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
		BillingPeriodEndAt:          time.Date(2026, 5, 31, 23, 59, 59, 0, time.UTC),
		LineItemsSubmissionDeadline: time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC),
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, testExistingBillID, resp.BillID)
	assert.Equal(t, http.StatusCreated, resp.Status)
}

func TestAddLineItemsDelegatesToWorkflow(t *testing.T) {
	t.Parallel()

	billing := mocks.NewBillingStore(t)
	workflow := mocks.NewWorkflowClient(t)
	idempotency := mocks.NewIdempotencyStore(t)

	billing.On("GetBill", mock.Anything, testBillID).Return(&domain.Bill{
		ID:                   testBillID,
		CurrencyCode:         domain.USD(),
		Status:               domain.BillStatusOpen,
		BillingPeriodStartAt: time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
		BillingPeriodEndAt:   time.Date(2026, 5, 31, 23, 59, 59, 0, time.UTC),
	}, nil).Once()
	workflow.On("AddLineItems", mock.Anything, testBillID, mock.MatchedBy(func(input workflowpkg.AddLineItemsInput) bool {
		return input.BillID == testBillID && input.IdempotencyKey == "idem-add-001" && input.CurrencyCode == domain.USD()
	})).Return(&workflowpkg.AddLineItemsResult{
		BillID:                   testBillID,
		BillStatus:               domain.BillStatusOpen,
		SnapshotTotalAmountMinor: 125,
		SuccessLineItemIDsMap:    map[string]string{"line-001": "line-item-001"},
		FailedLineItemReasonsMap: map[string]string{},
	}, nil).Once()

	svc := newServiceWithStores(billing, idempotency, workflow, nil, fixedNow)

	resp, err := svc.AddLineItems(context.Background(), testBillID, &AddLineItemsRequest{
		IdempotencyKey: "idem-add-001",
		CurrencyCode:   "USD",
		LineItems: []AddLineItemRequest{{
			ExternalReferenceID: "line-001",
			OccurredAt:          time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC),
			AmountMinor:         125,
		}},
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, "line-item-001", resp.SuccessLineItemIDsMap["line-001"])
	assert.Empty(t, resp.FailedLineItemReasonsMap)
}

func TestAddLineItemsReturnsStructuredBatchFailure(t *testing.T) {
	t.Parallel()

	billing := mocks.NewBillingStore(t)
	workflow := mocks.NewWorkflowClient(t)
	idempotency := mocks.NewIdempotencyStore(t)

	billing.On("GetBill", mock.Anything, testBillID).Return(&domain.Bill{
		ID:                   testBillID,
		CurrencyCode:         domain.USD(),
		Status:               domain.BillStatusOpen,
		BillingPeriodStartAt: time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
		BillingPeriodEndAt:   time.Date(2026, 5, 31, 23, 59, 59, 0, time.UTC),
	}, nil).Once()
	workflow.On("AddLineItems", mock.Anything, testBillID, mock.Anything).Return((*workflowpkg.AddLineItemsResult)(nil), &workflowpkg.LineItemsBatchFailureError{
		RecordReasons: map[string]string{"line-001": "conflict"},
	}).Once()

	svc := newServiceWithStores(billing, idempotency, workflow, nil, fixedNow)

	_, err := svc.AddLineItems(context.Background(), testBillID, &AddLineItemsRequest{
		IdempotencyKey: "idem-add-002",
		CurrencyCode:   "USD",
		LineItems: []AddLineItemRequest{{
			ExternalReferenceID: "line-001",
			OccurredAt:          time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC),
			AmountMinor:         125,
		}},
	})
	require.Error(t, err)
	assert.Equal(t, errs.Aborted, errs.Code(err))
	details, ok := errs.Details(err).(addLineItemsFailureDetails)
	require.True(t, ok)
	assert.Equal(t, "conflict", details.RecordReasons["line-001"])
}

func TestAddLineItemsReturnsStructuredBatchFailureFromTemporalApplicationError(t *testing.T) {
	t.Parallel()

	billing := mocks.NewBillingStore(t)
	workflow := mocks.NewWorkflowClient(t)
	idempotency := mocks.NewIdempotencyStore(t)

	billing.On("GetBill", mock.Anything, testBillID).Return(&domain.Bill{
		ID:                   testBillID,
		CurrencyCode:         domain.USD(),
		Status:               domain.BillStatusOpen,
		BillingPeriodStartAt: time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
		BillingPeriodEndAt:   time.Date(2026, 5, 31, 23, 59, 59, 0, time.UTC),
	}, nil).Once()
	workflow.On("AddLineItems", mock.Anything, testBillID, mock.Anything).Return(
		(*workflowpkg.AddLineItemsResult)(nil),
		temporal.NewNonRetryableApplicationError(
			"line items batch rejected",
			"LineItemsBatchFailureError",
			nil,
			workflowpkg.LineItemsBatchFailureError{RecordReasons: map[string]string{"line-001": "conflict"}},
		),
	).Once()

	svc := newServiceWithStores(billing, idempotency, workflow, nil, fixedNow)

	_, err := svc.AddLineItems(context.Background(), testBillID, &AddLineItemsRequest{
		IdempotencyKey: "idem-add-002a",
		CurrencyCode:   "USD",
		LineItems: []AddLineItemRequest{{
			ExternalReferenceID: "line-001",
			OccurredAt:          time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC),
			AmountMinor:         125,
		}},
	})
	require.Error(t, err)
	assert.Equal(t, errs.Aborted, errs.Code(err))
	details, ok := errs.Details(err).(addLineItemsFailureDetails)
	require.True(t, ok)
	assert.Equal(t, "conflict", details.RecordReasons["line-001"])
}

func TestAddLineItemsRejectsOutOfPeriodLineItemsBeforeWorkflowCall(t *testing.T) {
	t.Parallel()

	billing := mocks.NewBillingStore(t)
	workflow := mocks.NewWorkflowClient(t)
	idempotency := mocks.NewIdempotencyStore(t)

	billing.On("GetBill", mock.Anything, testBillID).Return(&domain.Bill{
		ID:                   testBillID,
		CurrencyCode:         domain.USD(),
		Status:               domain.BillStatusOpen,
		BillingPeriodStartAt: time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
		BillingPeriodEndAt:   time.Date(2026, 5, 31, 23, 59, 59, 0, time.UTC),
	}, nil).Once()

	svc := newServiceWithStores(billing, idempotency, workflow, nil, fixedNow)

	_, err := svc.AddLineItems(context.Background(), testBillID, &AddLineItemsRequest{
		IdempotencyKey: "idem-add-out-of-period",
		CurrencyCode:   "USD",
		LineItems: []AddLineItemRequest{{
			ExternalReferenceID: "line-001",
			OccurredAt:          time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
			AmountMinor:         125,
		}},
	})
	require.Error(t, err)
	assert.Equal(t, errs.InvalidArgument, errs.Code(err))
	assert.Contains(t, err.Error(), "must fall within the bill billing period")
}

func TestAddLineItemsRejectsNonOpenBillBeforeWorkflowCall(t *testing.T) {
	t.Parallel()

	billing := mocks.NewBillingStore(t)
	workflow := mocks.NewWorkflowClient(t)
	idempotency := mocks.NewIdempotencyStore(t)

	billing.On("GetBill", mock.Anything, testBillID).Return(&domain.Bill{
		ID:                   testBillID,
		CurrencyCode:         domain.USD(),
		Status:               domain.BillStatusScheduled,
		BillingPeriodStartAt: time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
		BillingPeriodEndAt:   time.Date(2026, 5, 31, 23, 59, 59, 0, time.UTC),
	}, nil).Once()

	svc := newServiceWithStores(billing, idempotency, workflow, nil, fixedNow)

	_, err := svc.AddLineItems(context.Background(), testBillID, &AddLineItemsRequest{
		IdempotencyKey: "idem-add-scheduled",
		CurrencyCode:   "USD",
		LineItems: []AddLineItemRequest{{
			ExternalReferenceID: "line-001",
			OccurredAt:          time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC),
			AmountMinor:         125,
		}},
	})
	require.Error(t, err)
	assert.Equal(t, errs.Aborted, errs.Code(err))
	assert.Contains(t, err.Error(), "bill is not accepting new line items")
}

func TestCloseBillRejectsClosedBillBeforeWorkflowCall(t *testing.T) {
	t.Parallel()

	billing := mocks.NewBillingStore(t)
	workflow := mocks.NewWorkflowClient(t)
	idempotency := mocks.NewIdempotencyStore(t)

	billing.On("GetBill", mock.Anything, testBillID).Return(&domain.Bill{
		ID:           testBillID,
		CurrencyCode: domain.USD(),
		Status:       domain.BillStatusClosed,
	}, nil).Once()

	svc := newServiceWithStores(billing, idempotency, workflow, nil, fixedNow)

	_, err := svc.CloseBill(context.Background(), testBillID, &CloseBillRequest{
		IdempotencyKey: "idem-close-closed",
	})
	require.Error(t, err)
	assert.Equal(t, errs.Aborted, errs.Code(err))
}

func TestBillEndpointsRejectInvalidBillID(t *testing.T) {
	t.Parallel()

	svc := newServiceWithStores(
		mocks.NewBillingStore(t),
		mocks.NewIdempotencyStore(t),
		mocks.NewWorkflowClient(t),
		nil,
		fixedNow,
	)

	tests := []struct {
		name string
		call func() error
	}{
		{
			name: "GetBill",
			call: func() error {
				_, err := svc.GetBill(context.Background(), "not-a-uuid")
				return err
			},
		},
		{
			name: "AddLineItems",
			call: func() error {
				_, err := svc.AddLineItems(context.Background(), "not-a-uuid", &AddLineItemsRequest{
					IdempotencyKey: "idem-add-001",
					CurrencyCode:   "USD",
					LineItems: []AddLineItemRequest{{
						ExternalReferenceID: "line-001",
						OccurredAt:          time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC),
						AmountMinor:         125,
					}},
				})
				return err
			},
		},
		{
			name: "CloseBill",
			call: func() error {
				_, err := svc.CloseBill(context.Background(), "not-a-uuid", &CloseBillRequest{
					IdempotencyKey: "idem-close-001",
				})
				return err
			},
		},
		{
			name: "ListBillLineItems",
			call: func() error {
				_, err := svc.ListBillLineItems(context.Background(), "not-a-uuid", &ListBillLineItemsParams{})
				return err
			},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := tc.call()
			require.Error(t, err)
			assert.Equal(t, errs.InvalidArgument, errs.Code(err))
			assert.Contains(t, err.Error(), "bill_id")
		})
	}
}

func TestBillEndpointsRejectNonV4BillID(t *testing.T) {
	t.Parallel()

	svc := newServiceWithStores(
		mocks.NewBillingStore(t),
		mocks.NewIdempotencyStore(t),
		mocks.NewWorkflowClient(t),
		nil,
		fixedNow,
	)

	billID := "6ba7b810-9dad-11d1-80b4-00c04fd430c8"
	tests := []struct {
		name string
		call func() error
	}{
		{
			name: "GetBill",
			call: func() error {
				_, err := svc.GetBill(context.Background(), billID)
				return err
			},
		},
		{
			name: "AddLineItems",
			call: func() error {
				_, err := svc.AddLineItems(context.Background(), billID, &AddLineItemsRequest{
					IdempotencyKey: "idem-add-001",
					CurrencyCode:   "USD",
					LineItems: []AddLineItemRequest{{
						ExternalReferenceID: "line-001",
						OccurredAt:          time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC),
						AmountMinor:         125,
					}},
				})
				return err
			},
		},
		{
			name: "CloseBill",
			call: func() error {
				_, err := svc.CloseBill(context.Background(), billID, &CloseBillRequest{
					IdempotencyKey: "idem-close-001",
				})
				return err
			},
		},
		{
			name: "ListBillLineItems",
			call: func() error {
				_, err := svc.ListBillLineItems(context.Background(), billID, &ListBillLineItemsParams{})
				return err
			},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := tc.call()
			require.Error(t, err)
			assert.Equal(t, errs.InvalidArgument, errs.Code(err))
			assert.Contains(t, err.Error(), "v4")
		})
	}
}

func TestGetBillReturnsInternalWhenWorkflowStateIsMissing(t *testing.T) {
	t.Parallel()

	billing := mocks.NewBillingStore(t)
	workflow := mocks.NewWorkflowClient(t)
	idempotency := mocks.NewIdempotencyStore(t)

	billing.On("GetBill", mock.Anything, testBillID).Return(&domain.Bill{
		ID:                   testBillID,
		CurrencyCode:         domain.USD(),
		Status:               domain.BillStatusOpen,
		BillingPeriodStartAt: time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
		BillingPeriodEndAt:   time.Date(2026, 5, 31, 23, 59, 59, 0, time.UTC),
	}, nil).Once()
	workflow.On("GetState", mock.Anything, testBillID).Return((*workflowpkg.State)(nil), &serviceerror.NotFound{}).Once()

	svc := newServiceWithStores(billing, idempotency, workflow, nil, fixedNow)

	_, err := svc.GetBill(context.Background(), testBillID)
	require.Error(t, err)
	assert.Equal(t, errs.Internal, errs.Code(err))
}

func TestGetBillMapsClosedTotal(t *testing.T) {
	t.Parallel()

	closedAt := time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)
	billing := mocks.NewBillingStore(t)
	workflow := mocks.NewWorkflowClient(t)
	idempotency := mocks.NewIdempotencyStore(t)

	billing.On("GetBill", mock.Anything, testBillID).Return(&domain.Bill{
		ID:                          testBillID,
		AccountID:                   "acct_123",
		ExternalReferenceID:         "bill-001",
		CurrencyCode:                domain.USD(),
		Status:                      domain.BillStatusClosed,
		BillingPeriodStartAt:        time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
		BillingPeriodEndAt:          time.Date(2026, 5, 31, 23, 59, 59, 0, time.UTC),
		LineItemsSubmissionDeadline: time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC),
		TotalAmountMinor:            250,
		ClosedAt:                    &closedAt,
		CreatedAt:                   time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
		UpdatedAt:                   closedAt,
	}, nil).Once()

	resp, err := newServiceWithStores(billing, idempotency, workflow, nil, fixedNow).GetBill(context.Background(), testBillID)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.NotNil(t, resp.FinalTotalAmountMinor)
	assert.Equal(t, int64(250), *resp.FinalTotalAmountMinor)
	assert.Nil(t, resp.SnapshotTotalAmountMinor)
}

func TestGetBillUsesWorkflowSnapshotForOpenBill(t *testing.T) {
	t.Parallel()

	billing := mocks.NewBillingStore(t)
	workflow := mocks.NewWorkflowClient(t)
	idempotency := mocks.NewIdempotencyStore(t)

	billing.On("GetBill", mock.Anything, testBillID).Return(&domain.Bill{
		ID:                          testBillID,
		AccountID:                   "acct_123",
		ExternalReferenceID:         "bill-001",
		CurrencyCode:                domain.USD(),
		Status:                      domain.BillStatusOpen,
		BillingPeriodStartAt:        time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
		BillingPeriodEndAt:          time.Date(2026, 5, 31, 23, 59, 59, 0, time.UTC),
		LineItemsSubmissionDeadline: time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC),
		TotalAmountMinor:            0,
		CreatedAt:                   time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
		UpdatedAt:                   time.Date(2026, 5, 2, 0, 0, 0, 0, time.UTC),
	}, nil).Once()
	workflow.On("GetState", mock.Anything, testBillID).Return(&workflowpkg.State{
		BillID:                   testBillID,
		CurrencyCode:             domain.USD(),
		Status:                   domain.BillStatusOpen,
		SnapshotTotalAmountMinor: 900,
	}, nil).Once()

	resp, err := newServiceWithStores(billing, idempotency, workflow, nil, fixedNow).GetBill(context.Background(), testBillID)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.NotNil(t, resp.SnapshotTotalAmountMinor)
	assert.Equal(t, int64(900), *resp.SnapshotTotalAmountMinor)
	assert.Nil(t, resp.FinalTotalAmountMinor)
}

func TestListBillLineItemsReturnsNextPageToken(t *testing.T) {
	t.Parallel()

	billing := mocks.NewBillingStore(t)
	workflow := mocks.NewWorkflowClient(t)
	idempotency := mocks.NewIdempotencyStore(t)

	billing.On("GetBill", mock.Anything, testBillID).Return(&domain.Bill{
		ID:           testBillID,
		CurrencyCode: domain.USD(),
	}, nil).Once()
	billing.On("ListLineItems", mock.Anything, mock.Anything).Return(&billingstore.ListLineItemsResult{
		Items: []billingstore.LineItem{{
			ID:                  "33333333-3333-4333-8333-333333333333",
			BillID:              testBillID,
			ExternalReferenceID: "line-001",
			CurrencyCode:        domain.USD(),
			OccurredAt:          time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC),
			AmountMinor:         125,
			CreatedAt:           time.Date(2026, 5, 15, 12, 1, 0, 0, time.UTC),
		}},
		HasMore: true,
	}, nil).Once()

	resp, err := newServiceWithStores(billing, idempotency, workflow, nil, fixedNow).ListBillLineItems(context.Background(), testBillID, &ListBillLineItemsParams{PageSize: 1})
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Len(t, resp.Items, 1)
	require.NotNil(t, resp.NextPageToken)
	_, decodeErr := base64.RawURLEncoding.DecodeString(*resp.NextPageToken)
	require.NoError(t, decodeErr)
}

func TestCreateBillMapsUnexpectedStoreFailureToInternalError(t *testing.T) {
	t.Parallel()

	billing := mocks.NewBillingStore(t)
	workflow := mocks.NewWorkflowClient(t)
	idempotency := mocks.NewIdempotencyStore(t)

	billing.On("InsertBill", mock.Anything, mock.Anything).Return((*billingstore.InsertBillResult)(nil), errors.New("dial tcp 10.0.0.1:5432: i/o timeout")).Once()

	_, err := newServiceWithStores(billing, idempotency, workflow, nil, fixedNow).CreateBill(context.Background(), validCreateBillRequest())
	require.Error(t, err)
	assert.Equal(t, errs.Internal, errs.Code(err))
}

func TestCreateBillRequestValidateRejectsMissingIdempotencyKey(t *testing.T) {
	t.Parallel()

	req := validCreateBillRequest()
	req.IdempotencyKey = ""

	err := req.Validate()
	require.Error(t, err)
	assert.Equal(t, errs.InvalidArgument, errs.Code(err))
	assert.Contains(t, err.Error(), "Idempotency-Key")
}

func TestCreateBillRequestValidate_AllowsSubmissionDeadlineEqualToBillingPeriodEnd(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	req := &CreateBillRequest{
		IdempotencyKey:              "idem-1",
		AccountID:                   "acct-1",
		ExternalReferenceID:         "bill-ext-1",
		CurrencyCode:                "USD",
		BillingPeriodStartAt:        now.Add(-2 * time.Hour),
		BillingPeriodEndAt:          now.Add(10 * time.Minute),
		LineItemsSubmissionDeadline: now.Add(10 * time.Minute),
	}

	err := req.Validate()
	require.NoError(t, err)
}

func TestCreateBillRequestValidate_AllowsFlexibleClientBufferAfterBillingPeriodEnd(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	req := &CreateBillRequest{
		IdempotencyKey:              "idem-1",
		AccountID:                   "acct-1",
		ExternalReferenceID:         "bill-ext-1",
		CurrencyCode:                "USD",
		BillingPeriodStartAt:        now.Add(-2 * time.Hour),
		BillingPeriodEndAt:          now.Add(-2 * time.Hour),
		LineItemsSubmissionDeadline: now.Add(-90 * time.Minute),
	}

	err := req.Validate()
	require.NoError(t, err)
}

func TestCreateBillRequestValidateRejectsUnsupportedCurrency(t *testing.T) {
	t.Parallel()

	req := validCreateBillRequest()
	req.CurrencyCode = "EUR"

	err := req.Validate()
	require.Error(t, err)
	assert.Equal(t, errs.InvalidArgument, errs.Code(err))
	assert.Contains(t, err.Error(), "currency_code")
}

func TestCreateBillRequestValidateRejectsBillingPeriodEndBeforeStart(t *testing.T) {
	t.Parallel()

	req := validCreateBillRequest()
	req.BillingPeriodStartAt = time.Date(2026, 5, 31, 23, 59, 59, 0, time.UTC)
	req.BillingPeriodEndAt = time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)

	err := req.Validate()
	require.Error(t, err)
	assert.Equal(t, errs.InvalidArgument, errs.Code(err))
	assert.Contains(t, err.Error(), "billing_period_end_at")
}

func TestCreateBillRequestValidateRejectsSubmissionDeadlineBeforeBillingPeriodEnd(t *testing.T) {
	t.Parallel()

	req := validCreateBillRequest()
	req.LineItemsSubmissionDeadline = req.BillingPeriodEndAt.Add(-time.Minute)

	err := req.Validate()
	require.Error(t, err)
	assert.Equal(t, errs.InvalidArgument, errs.Code(err))
	assert.Contains(t, err.Error(), "line_items_submission_deadline")
}

func TestAddLineItemsRequestValidate_RejectsDuplicateExternalReferenceIDs(t *testing.T) {
	t.Parallel()

	req := &AddLineItemsRequest{
		IdempotencyKey: "idem-duplicates",
		CurrencyCode:   "USD",
		LineItems: []AddLineItemRequest{
			{
				ExternalReferenceID: "dup-1",
				OccurredAt:          time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC),
				AmountMinor:         100,
			},
			{
				ExternalReferenceID: "dup-1",
				OccurredAt:          time.Date(2026, 5, 15, 13, 0, 0, 0, time.UTC),
				AmountMinor:         200,
			},
		},
	}

	err := req.Validate()
	require.Error(t, err)
	assert.Equal(t, errs.InvalidArgument, errs.Code(err))
	assert.Contains(t, err.Error(), "duplicate external_reference_id")
}

func TestAddLineItemsRequestValidateRejectsMissingIdempotencyKey(t *testing.T) {
	t.Parallel()

	req := &AddLineItemsRequest{
		CurrencyCode: "USD",
		LineItems: []AddLineItemRequest{{
			ExternalReferenceID: "line-001",
			OccurredAt:          time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC),
			AmountMinor:         100,
		}},
	}

	err := req.Validate()
	require.Error(t, err)
	assert.Equal(t, errs.InvalidArgument, errs.Code(err))
	assert.Contains(t, err.Error(), "Idempotency-Key")
}

func TestCloseBillRequestValidateRejectsMissingIdempotencyKey(t *testing.T) {
	t.Parallel()

	req := &CloseBillRequest{}

	err := req.Validate()
	require.Error(t, err)
	assert.Equal(t, errs.InvalidArgument, errs.Code(err))
	assert.Contains(t, err.Error(), "Idempotency-Key")
}

func TestListBillLineItemsParamsValidateRejectsPageSizeOverLimit(t *testing.T) {
	t.Parallel()

	params := &ListBillLineItemsParams{PageSize: 101}

	err := params.Validate()
	require.Error(t, err)
	assert.Equal(t, errs.InvalidArgument, errs.Code(err))
	assert.Contains(t, err.Error(), "page_size")
}

func TestListBillLineItemsParamsValidateRejectsInvalidPageToken(t *testing.T) {
	t.Parallel()

	token, err := pagination.Encode(&pagination.LineItemPaginationToken{
		OccurredAt: time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
		LineItemID: "11111111-1111-4111-8111-111111111111",
	})
	require.NoError(t, err)

	params := &ListBillLineItemsParams{PageToken: token[:len(token)-1] + "!"}
	err = params.Validate()
	require.Error(t, err)
	assert.Equal(t, errs.InvalidArgument, errs.Code(err))
	assert.Contains(t, err.Error(), "page_token")
}

func TestListBillLineItemsParamsValidateRejectsNonV4PageTokenCursor(t *testing.T) {
	t.Parallel()

	nonV4Token := base64.RawURLEncoding.EncodeToString([]byte(`{"occurred_at":"2026-05-01T00:00:00Z","line_item_id":"6ba7b810-9dad-11d1-80b4-00c04fd430c8"}`))
	params := &ListBillLineItemsParams{PageToken: nonV4Token}

	err := params.Validate()
	require.Error(t, err)
	assert.Equal(t, errs.InvalidArgument, errs.Code(err))
	assert.Contains(t, err.Error(), "page_token")
}

func fixedNow() time.Time {
	return time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
}

func validCreateBillRequest() *CreateBillRequest {
	return &CreateBillRequest{
		IdempotencyKey:              "idem-create-001",
		AccountID:                   "acct_123",
		ExternalReferenceID:         "bill-001",
		CurrencyCode:                "USD",
		BillingPeriodStartAt:        time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
		BillingPeriodEndAt:          time.Date(2026, 5, 31, 23, 59, 59, 0, time.UTC),
		LineItemsSubmissionDeadline: time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC),
	}
}
