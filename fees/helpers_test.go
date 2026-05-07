package fees

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"testing"
	"time"

	"encore.dev/beta/errs"
	"encore.dev/storage/sqldb"
	"go.temporal.io/api/serviceerror"

	"encore.app/fees/internal/domain"
	"encore.app/fees/internal/pagination"
	billingstore "encore.app/fees/internal/store/billing"
	workflowpkg "encore.app/fees/internal/workflow"
)

func TestHealth(t *testing.T) {
	t.Parallel()

	svc := newServiceWithStores(nil, nil, nil, nil, fixedNow)
	resp, err := svc.Health(context.Background())
	if err != nil {
		t.Fatalf("Health() error = %v", err)
	}
	if resp.Service != "fees" {
		t.Fatalf("Health() = %#v", resp)
	}
}

func TestNewServiceWithStoresDefaultsClockAndShutdown(t *testing.T) {
	t.Parallel()

	svc := newServiceWithStores(nil, nil, nil, nil, nil)
	if svc.now == nil {
		t.Fatal("expected default clock")
	}
	if svc.now().Location() != time.UTC {
		t.Fatalf("default clock should return UTC time, got %v", svc.now().Location())
	}

	var nilSvc *Service
	nilSvc.Shutdown(context.Background())
	svc.Shutdown(context.Background())
}

func TestInitialBillStatusAndCloseInputMapping(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	if got := initialBillStatus(now, now.Add(time.Minute)); got != domain.BillStatusScheduled {
		t.Fatalf("initialBillStatus() = %q, want scheduled", got)
	}
	if got := initialBillStatus(now, now); got != domain.BillStatusOpen {
		t.Fatalf("initialBillStatus() = %q, want open", got)
	}

	if _, ok := reflect.TypeOf(CloseBillRequest{}).FieldByName("Reason"); ok {
		t.Fatal("CloseBillRequest should not expose a Reason field")
	}
	if _, ok := reflect.TypeOf(workflowpkg.CloseBillInput{}).FieldByName("Reason"); ok {
		t.Fatal("workflow CloseBillInput should not expose a Reason field")
	}

	input := domainCloseToWorkflow(&CloseBillRequest{IdempotencyKey: "idem-close"})
	if !reflect.DeepEqual(input, workflowpkg.CloseBillInput{}) {
		t.Fatalf("domainCloseToWorkflow() = %#v, want zero-value CloseBillInput", input)
	}
}

func TestHTTPStatusConstants(t *testing.T) {
	t.Parallel()

	if http.StatusCreated != 201 {
		t.Fatalf("http.StatusCreated = %d", http.StatusCreated)
	}
	if http.StatusOK != 200 {
		t.Fatalf("http.StatusOK = %d", http.StatusOK)
	}
	if http.StatusAccepted != 202 {
		t.Fatalf("http.StatusAccepted = %d", http.StatusAccepted)
	}
}

func TestAPIErrorMappingHelpers(t *testing.T) {
	t.Parallel()

	if errs.Code(mapBillLookupError(sqldb.ErrNoRows)) != errs.NotFound {
		t.Fatal("expected bill lookup no rows to map to not_found")
	}
	if errs.Code(mapBillLookupError(errors.New("boom"))) != errs.Internal {
		t.Fatal("expected generic bill lookup failure to map to internal")
	}

	batchErr := mapWorkflowMutationError(&workflowpkg.LineItemsBatchFailureError{RecordReasons: map[string]string{"line-1": "duplicate"}})
	if errs.Code(batchErr) != errs.Aborted {
		t.Fatalf("batch mutation error code = %q", errs.Code(batchErr))
	}
	if details, ok := errs.Details(batchErr).(addLineItemsFailureDetails); !ok || details.RecordReasons["line-1"] != "duplicate" {
		t.Fatalf("batch mutation details = %#v", errs.Details(batchErr))
	}
	if errs.Code(mapWorkflowMutationError(workflowpkg.ErrBillNotAcceptingLineItems)) != errs.Aborted {
		t.Fatal("line item state conflict should map to aborted")
	}
	if errs.Code(mapWorkflowMutationError(workflowpkg.ErrBillClosingInProgress)) != errs.Aborted {
		t.Fatal("close state conflict should map to aborted")
	}
	if errs.Code(mapWorkflowMutationError(&serviceerror.NotFound{})) != errs.Internal {
		t.Fatal("workflow mutation not found should map to internal")
	}
	if errs.Code(mapWorkflowMutationError(errors.New("boom"))) != errs.Internal {
		t.Fatal("unknown workflow mutation error should map to internal")
	}

	if errs.Code(mapWorkflowQueryError(&serviceerror.NotFound{})) != errs.Internal {
		t.Fatal("workflow query not found should map to internal")
	}
	if errs.Code(mapWorkflowQueryError(errors.New("boom"))) != errs.Internal {
		t.Fatal("workflow query generic failure should map to internal")
	}

	if !isWorkflowAlreadyStarted(&serviceerror.WorkflowExecutionAlreadyStarted{}) {
		t.Fatal("expected already-started detection")
	}
	if isWorkflowAlreadyStarted(errors.New("nope")) {
		t.Fatal("unexpected already-started detection")
	}
	if errs.Code(invalidArgument("bad")) != errs.InvalidArgument {
		t.Fatal("invalidArgument should produce invalid_argument")
	}
	if errs.Code(conflictError("conflict")) != errs.Aborted {
		t.Fatal("conflictError should produce aborted")
	}
	if errs.Code(internalError("internal")) != errs.Internal {
		t.Fatal("internalError should produce internal")
	}
}

func TestTypesHelpersAndMappers(t *testing.T) {
	t.Parallel()

	bill := &domain.Bill{
		ID:                          "bill-1",
		AccountID:                   "acct-1",
		ExternalReferenceID:         "ext-1",
		Status:                      domain.BillStatusFailed,
		CurrencyCode:                domain.USD(),
		BillingPeriodStartAt:        time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
		BillingPeriodEndAt:          time.Date(2026, 5, 31, 0, 0, 0, 0, time.UTC),
		LineItemsSubmissionDeadline: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		TotalAmountMinor:            125,
		CreatedAt:                   time.Date(2026, 5, 1, 1, 0, 0, 0, time.UTC),
		UpdatedAt:                   time.Date(2026, 5, 1, 2, 0, 0, 0, time.UTC),
	}

	createInput := validCreateBillRequest().toWorkflowInput(bill)
	if createInput.BillID != "bill-1" || createInput.CurrencyCode != domain.USD() || createInput.InitialStatus != domain.BillStatusFailed {
		t.Fatalf("createInput = %#v", createInput)
	}

	addReq := &AddLineItemsRequest{
		IdempotencyKey: "idem-add",
		CurrencyCode:   "USD",
		LineItems: []AddLineItemRequest{{
			ExternalReferenceID: "line-1",
			OccurredAt:          time.Date(2026, 5, 2, 0, 0, 0, 0, time.UTC),
			AmountMinor:         10,
		}},
	}
	addInput := addReq.toWorkflowInput("bill-1")
	if addInput.BillID != "bill-1" || addInput.IdempotencyKey != "idem-add" || len(addInput.LineItems) != 1 {
		t.Fatalf("addInput = %#v", addInput)
	}

	if got := mapGetBillResponse(nil); got != nil {
		t.Fatalf("mapGetBillResponse(nil) = %#v", got)
	}
	resp := mapGetBillResponse(bill)
	if resp.SnapshotTotalAmountMinor == nil || *resp.SnapshotTotalAmountMinor != 125 {
		t.Fatalf("mapGetBillResponse() = %#v", resp)
	}
	closedBill := *bill
	closedBill.Status = domain.BillStatusClosed
	closedResp := mapGetBillResponse(&closedBill)
	if closedResp.FinalTotalAmountMinor == nil || closedResp.SnapshotTotalAmountMinor != nil {
		t.Fatalf("closedResp = %#v", closedResp)
	}

	workflowState := &workflowpkg.State{
		Status:                   domain.BillStatusFinalizing,
		CurrencyCode:             "",
		SnapshotTotalAmountMinor: 500,
	}
	snapshotResp := mapGetBillResponseWithWorkflowSnapshot(bill, workflowState)
	if snapshotResp.BillStatus != string(domain.BillStatusFinalizing) || snapshotResp.FinalTotalAmountMinor != nil {
		t.Fatalf("snapshotResp = %#v", snapshotResp)
	}
	if snapshotResp.SnapshotTotalAmountMinor == nil || *snapshotResp.SnapshotTotalAmountMinor != 500 {
		t.Fatalf("snapshotResp = %#v", snapshotResp)
	}
	if snapshotResp.CurrencyCode != "USD" {
		t.Fatalf("snapshotResp.CurrencyCode = %q", snapshotResp.CurrencyCode)
	}
	if got := mapGetBillResponseWithWorkflowSnapshot(nil, workflowState); got != nil {
		t.Fatalf("mapGetBillResponseWithWorkflowSnapshot(nil, state) = %#v", got)
	}

	if firstNonZeroCurrency("", domain.GEL()) != domain.GEL() {
		t.Fatal("expected fallback currency")
	}
	if firstNonZeroCurrency(domain.USD(), domain.GEL()) != domain.USD() {
		t.Fatal("expected primary currency")
	}
}

func TestLineItemsFailureAndPaginationHelpers(t *testing.T) {
	t.Parallel()

	if _, ok := interface{}(addLineItemsFailureDetails{}).(interface{ ErrDetails() }); !ok {
		t.Fatal("addLineItemsFailureDetails should satisfy ErrDetails")
	}

	err := lineItemsBatchFailureError(map[string]string{"line-1": "duplicate"})
	if errs.Code(err) != errs.Aborted {
		t.Fatalf("lineItemsBatchFailureError code = %q", errs.Code(err))
	}

	req := &AddLineItemsRequest{
		LineItems: []AddLineItemRequest{
			{OccurredAt: time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)},
			{OccurredAt: time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC)},
		},
	}
	bill := &domain.Bill{
		BillingPeriodStartAt: time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
		BillingPeriodEndAt:   time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC),
	}
	if err := req.ValidateBusinessRules(bill); err != nil {
		t.Fatalf("ValidateBusinessRules() error = %v", err)
	}

	params, err := (&ListBillLineItemsParams{}).toRepositoryParams("bill-1")
	if err != nil {
		t.Fatalf("toRepositoryParams() error = %v", err)
	}
	if params.Limit != 50 || params.BillID != "bill-1" {
		t.Fatalf("params = %#v", params)
	}

	token, err := pagination.Encode(&pagination.LineItemPaginationToken{
		OccurredAt: time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
		LineItemID: "11111111-1111-4111-8111-111111111111",
	})
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
	params, err = (&ListBillLineItemsParams{PageSize: 10, PageToken: token}).toRepositoryParams("bill-1")
	if err != nil {
		t.Fatalf("toRepositoryParams(token) error = %v", err)
	}
	if params.Limit != 10 || params.OccurredAtAfter == nil || params.LineItemIDAfter == nil {
		t.Fatalf("params = %#v", params)
	}

	if _, err := (&ListBillLineItemsParams{PageSize: 101}).toRepositoryParams("bill-1"); err == nil {
		t.Fatal("expected page size validation error")
	}
	if _, err := (&ListBillLineItemsParams{PageToken: "not-base64"}).toRepositoryParams("bill-1"); err == nil {
		t.Fatal("expected invalid token error")
	}
	if _, err := pagination.Encode(&pagination.LineItemPaginationToken{
		OccurredAt: time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
		LineItemID: "not-a-uuid",
	}); err == nil {
		t.Fatal("expected invalid UUID token encoding to fail")
	}

	listResp, err := mapListBillLineItemsResponse(&domain.Bill{ID: "bill-1", CurrencyCode: domain.USD()}, &billingstore.ListLineItemsResult{
		Items: []billingstore.LineItem{{
			ID:                  "11111111-1111-4111-8111-111111111111",
			ExternalReferenceID: "ext-1",
			OccurredAt:          time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
			AmountMinor:         10,
			CreatedAt:           time.Date(2026, 5, 1, 1, 0, 0, 0, time.UTC),
		}},
		HasMore: true,
	})
	if err != nil {
		t.Fatalf("mapListBillLineItemsResponse() error = %v", err)
	}
	if listResp.NextPageToken == nil || len(listResp.Items) != 1 {
		t.Fatalf("listResp = %#v", listResp)
	}

	if _, err := mapListBillLineItemsResponse(&domain.Bill{ID: "bill-1", CurrencyCode: domain.USD()}, &billingstore.ListLineItemsResult{
		Items:   []billingstore.LineItem{{}},
		HasMore: true,
	}); err == nil || errs.Code(err) != errs.Internal {
		t.Fatalf("expected internal error from invalid next page token, got %v", err)
	}
}

func TestAdditionalValidationEdges(t *testing.T) {
	t.Parallel()

	req := validCreateBillRequest()
	req.AccountID = ""
	if err := req.Validate(); err == nil {
		t.Fatal("expected missing account_id validation error")
	}
	req = validCreateBillRequest()
	req.ExternalReferenceID = ""
	if err := req.Validate(); err == nil {
		t.Fatal("expected missing external_reference_id validation error")
	}
	req = validCreateBillRequest()
	req.BillingPeriodStartAt = time.Time{}
	if err := req.Validate(); err == nil {
		t.Fatal("expected missing start time validation error")
	}
	req = validCreateBillRequest()
	req.LineItemsSubmissionDeadline = time.Time{}
	if err := req.Validate(); err == nil {
		t.Fatal("expected missing submission deadline validation error")
	}

	if err := (&CreateBillRequest{}).Validate(); err == nil {
		t.Fatal("expected missing fields validation error")
	}
	if err := (*CreateBillRequest)(nil).Validate(); err == nil {
		t.Fatal("expected nil create request validation error")
	}
	if err := (*CloseBillRequest)(nil).Validate(); err == nil {
		t.Fatal("expected nil close request validation error")
	}

	addReq := &AddLineItemsRequest{IdempotencyKey: "idem", CurrencyCode: "USD"}
	if err := addReq.Validate(); err == nil {
		t.Fatal("expected empty line_items validation error")
	}
	addReq = &AddLineItemsRequest{
		IdempotencyKey: "idem",
		CurrencyCode:   "EUR",
		LineItems:      []AddLineItemRequest{{ExternalReferenceID: "line-1", OccurredAt: time.Now().UTC(), AmountMinor: 1}},
	}
	if err := addReq.Validate(); err == nil {
		t.Fatal("expected unsupported currency validation error")
	}
	addReq = &AddLineItemsRequest{
		IdempotencyKey: "idem",
		CurrencyCode:   "USD",
		LineItems:      []AddLineItemRequest{{OccurredAt: time.Now().UTC(), AmountMinor: 1}},
	}
	if err := addReq.Validate(); err == nil {
		t.Fatal("expected missing external_reference_id validation error")
	}
	addReq = &AddLineItemsRequest{
		IdempotencyKey: "idem",
		CurrencyCode:   "USD",
		LineItems:      []AddLineItemRequest{{ExternalReferenceID: "line-1", AmountMinor: 1}},
	}
	if err := addReq.Validate(); err == nil {
		t.Fatal("expected missing occurred_at validation error")
	}
	addReq = &AddLineItemsRequest{
		IdempotencyKey: "idem",
		CurrencyCode:   "USD",
		LineItems:      []AddLineItemRequest{{ExternalReferenceID: "line-1", OccurredAt: time.Now().UTC(), AmountMinor: 0}},
	}
	if err := addReq.Validate(); err == nil {
		t.Fatal("expected non-positive amount validation error")
	}

	tooMany := make([]AddLineItemRequest, 101)
	for i := range tooMany {
		tooMany[i] = AddLineItemRequest{
			ExternalReferenceID: "line",
			OccurredAt:          time.Now().UTC(),
			AmountMinor:         1,
		}
		tooMany[i].ExternalReferenceID = tooMany[i].ExternalReferenceID + string(rune(i))
	}
	addReq = &AddLineItemsRequest{IdempotencyKey: "idem", CurrencyCode: "USD", LineItems: tooMany}
	if err := addReq.Validate(); err == nil {
		t.Fatal("expected too-many line items validation error")
	}
}
