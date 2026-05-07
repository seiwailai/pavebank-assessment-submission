package fees

import (
	"context"
	"fmt"
	"testing"
	"time"

	"encore.dev/beta/errs"
	"encore.dev/et"

	"encore.app/fees/internal/domain"
	billingstore "encore.app/fees/internal/store/billing"
	idempotencystore "encore.app/fees/internal/store/idempotency"
	workflowpkg "encore.app/fees/internal/workflow"
)

func TestBillLifecycle(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc := newIntegrationService(t)

	createResp, err := svc.CreateBill(ctx, &CreateBillRequest{
		IdempotencyKey:              "idem-create-lifecycle",
		AccountID:                   "acct_123",
		ExternalReferenceID:         "bill-001",
		CurrencyCode:                "USD",
		BillingPeriodStartAt:        time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
		BillingPeriodEndAt:          time.Date(2026, 5, 31, 23, 59, 59, 0, time.UTC),
		LineItemsSubmissionDeadline: time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("CreateBill() error = %v", err)
	}
	if createResp.BillID == "" {
		t.Fatal("expected bill id")
	}

	addResp, err := svc.AddLineItems(ctx, createResp.BillID, &AddLineItemsRequest{
		IdempotencyKey: "idem-add-lifecycle",
		CurrencyCode:   "USD",
		LineItems: []AddLineItemRequest{
			{
				ExternalReferenceID: "line-001",
				OccurredAt:          time.Date(2026, 5, 10, 10, 0, 0, 0, time.UTC),
				AmountMinor:         120,
			},
			{
				ExternalReferenceID: "line-002",
				OccurredAt:          time.Date(2026, 5, 11, 11, 0, 0, 0, time.UTC),
				AmountMinor:         80,
			},
		},
	})
	if err != nil {
		t.Fatalf("AddLineItems() error = %v", err)
	}
	if len(addResp.SuccessLineItemIDsMap) != 2 {
		t.Fatalf("len(addResp.SuccessLineItemIDsMap) = %d, want %d", len(addResp.SuccessLineItemIDsMap), 2)
	}

	billResp, err := svc.GetBill(ctx, createResp.BillID)
	if err != nil {
		t.Fatalf("GetBill() before close error = %v", err)
	}
	if billResp.SnapshotTotalAmountMinor == nil || *billResp.SnapshotTotalAmountMinor != 200 {
		t.Fatalf("billResp.SnapshotTotalAmountMinor = %v, want %d", billResp.SnapshotTotalAmountMinor, 200)
	}

	lineItemsResp, err := svc.ListBillLineItems(ctx, createResp.BillID, &ListBillLineItemsParams{PageSize: 1})
	if err != nil {
		t.Fatalf("ListBillLineItems() first page error = %v", err)
	}
	if len(lineItemsResp.Items) != 1 {
		t.Fatalf("len(lineItemsResp.Items) = %d, want %d", len(lineItemsResp.Items), 1)
	}
	if lineItemsResp.NextPageToken == nil {
		t.Fatal("expected next_page_token on first page")
	}

	secondPageResp, err := svc.ListBillLineItems(ctx, createResp.BillID, &ListBillLineItemsParams{
		PageSize:  1,
		PageToken: *lineItemsResp.NextPageToken,
	})
	if err != nil {
		t.Fatalf("ListBillLineItems() second page error = %v", err)
	}
	if len(secondPageResp.Items) != 1 {
		t.Fatalf("len(secondPageResp.Items) = %d, want %d", len(secondPageResp.Items), 1)
	}

	closeResp, err := svc.CloseBill(ctx, createResp.BillID, &CloseBillRequest{
		IdempotencyKey: "idem-close-lifecycle",
	})
	if err != nil {
		t.Fatalf("CloseBill() error = %v", err)
	}
	if closeResp.BillStatus != string(domain.BillStatusOpen) {
		t.Fatalf("closeResp.BillStatus = %q, want %q", closeResp.BillStatus, domain.BillStatusOpen)
	}

	closedBillResp, err := svc.GetBill(ctx, createResp.BillID)
	if err != nil {
		t.Fatalf("GetBill() after close error = %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for closedBillResp.BillStatus != string(domain.BillStatusClosed) && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
		closedBillResp, err = svc.GetBill(ctx, createResp.BillID)
		if err != nil {
			t.Fatalf("GetBill() polling after close error = %v", err)
		}
	}
	if closedBillResp.BillStatus != string(domain.BillStatusClosed) {
		t.Fatalf("closedBillResp.BillStatus = %q, want %q", closedBillResp.BillStatus, domain.BillStatusClosed)
	}
	if closedBillResp.FinalTotalAmountMinor == nil || *closedBillResp.FinalTotalAmountMinor != 200 {
		t.Fatalf("closedBillResp.FinalTotalAmountMinor = %v, want %d", closedBillResp.FinalTotalAmountMinor, 200)
	}

	_, err = svc.AddLineItems(ctx, createResp.BillID, &AddLineItemsRequest{
		IdempotencyKey: "idem-add-after-close",
		CurrencyCode:   "USD",
		LineItems: []AddLineItemRequest{{
			ExternalReferenceID: "line-003",
			OccurredAt:          time.Date(2026, 5, 12, 12, 0, 0, 0, time.UTC),
			AmountMinor:         50,
		}},
	})
	if errs.Code(err) != errs.Aborted {
		t.Fatalf("errs.Code(err) = %q, want %q", errs.Code(err), errs.Aborted)
	}
}

func newIntegrationService(t *testing.T) *Service {
	t.Helper()

	db, err := et.NewTestDatabase(context.Background(), "fees")
	if err != nil {
		t.Fatalf("et.NewTestDatabase() error = %v", err)
	}

	billingStore := billingstore.NewPostgresStore(db)
	idempotencyStore := idempotencystore.NewPostgresStore(db)
	wf := &integrationWorkflowClient{store: billingStore, states: make(map[string]*workflowpkg.State)}
	return newServiceWithStores(billingStore, idempotencyStore, wf, nil, func() time.Time { return time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC) })
}

type integrationWorkflowClient struct {
	store  *billingstore.PostgresStore
	states map[string]*workflowpkg.State
}

func (c *integrationWorkflowClient) StartBillWorkflow(_ context.Context, input workflowpkg.StartBillWorkflowInput) error {
	if c.states == nil {
		c.states = make(map[string]*workflowpkg.State)
	}
	c.states[input.BillID] = &workflowpkg.State{
		BillID:                      input.BillID,
		CurrencyCode:                input.CurrencyCode,
		Status:                      input.InitialStatus,
		BillingPeriodStartAt:        input.BillingPeriodStartAt,
		LineItemsSubmissionDeadline: input.LineItemsSubmissionDeadline,
		SnapshotTotalAmountMinor:    input.SnapshotTotalAmountMinor,
	}
	return nil
}

func (c *integrationWorkflowClient) AddLineItems(ctx context.Context, billID string, input workflowpkg.AddLineItemsInput) (*workflowpkg.AddLineItemsResult, error) {
	state, ok := c.states[billID]
	if !ok {
		return nil, fmt.Errorf("workflow state not found")
	}
	if state.Status != domain.BillStatusOpen {
		return nil, workflowpkg.ErrBillNotAcceptingLineItems
	}
	if input.CurrencyCode != state.CurrencyCode {
		return nil, &workflowpkg.LineItemsBatchFailureError{RecordReasons: map[string]string{"currency_code": "currency_code does not match workflow currency"}}
	}

	requestID := "it-add-" + input.LineItems[0].ExternalReferenceID
	lineItems := make([]billingstore.LineItemInsert, 0, len(input.LineItems))
	for _, item := range input.LineItems {
		lineItems = append(lineItems, billingstore.LineItemInsert{
			ExternalReferenceID: item.ExternalReferenceID,
			OccurredAt:          item.OccurredAt,
			AmountMinor:         item.AmountMinor,
			Description:         item.Description,
		})
	}
	storeResult, err := c.store.InsertLineItems(ctx, billingstore.InsertLineItemsParams{
		BillID:         billID,
		RequestID:      requestID,
		IdempotencyKey: input.IdempotencyKey,
		CurrencyCode:   input.CurrencyCode,
		LineItems:      lineItems,
	})
	if err != nil {
		return nil, err
	}
	if storeResult.BatchFailure != nil {
		return nil, &workflowpkg.LineItemsBatchFailureError{RecordReasons: storeResult.BatchFailure.RecordReasons}
	}

	state.SnapshotTotalAmountMinor += storeResult.AmountDeltaMinor
	return &workflowpkg.AddLineItemsResult{
		BillID:                   billID,
		BillStatus:               state.Status,
		SnapshotTotalAmountMinor: state.SnapshotTotalAmountMinor,
		AcceptedRequestID:        requestID,
		SuccessLineItemIDsMap:    storeResult.SuccessLineItemIDs,
		FailedLineItemReasonsMap: map[string]string{},
	}, nil
}

func (c *integrationWorkflowClient) CloseBill(ctx context.Context, billID string, input workflowpkg.CloseBillInput) (*workflowpkg.CloseBillResult, error) {
	bill, err := c.store.GetBill(ctx, billID)
	if err != nil {
		return nil, err
	}
	if bill.Status != domain.BillStatusOpen {
		return nil, workflowpkg.ErrBillInvalidState
	}
	if state, ok := c.states[billID]; ok {
		state.Status = domain.BillStatusFinalizing
	}

	status := domain.BillStatusFinalizing
	if err := c.store.UpdateBill(ctx, billingstore.UpdateBillParams{
		BillID: billID,
		Status: &status,
	}); err != nil {
		return nil, err
	}
	finalized, err := c.store.FinalizeBill(ctx, billID)
	if err != nil {
		return nil, err
	}
	if state, ok := c.states[billID]; ok {
		state.Status = finalized.BillStatus
		state.SnapshotTotalAmountMinor = finalized.TotalAmountMinor
	}
	return &workflowpkg.CloseBillResult{
		BillID:                   billID,
		BillStatus:               domain.BillStatusOpen,
		SnapshotTotalAmountMinor: finalized.TotalAmountMinor,
	}, nil
}

func (c *integrationWorkflowClient) GetState(_ context.Context, billID string) (*workflowpkg.State, error) {
	state, ok := c.states[billID]
	if !ok {
		return nil, fmt.Errorf("workflow state not found")
	}
	cloned := *state
	return &cloned, nil
}
