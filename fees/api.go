package fees

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"encore.dev/beta/errs"
	"encore.dev/rlog"
	"encore.dev/storage/sqldb"

	"encore.app/fees/internal/domain"
	billingstore "encore.app/fees/internal/store/billing"
	workflowpkg "encore.app/fees/internal/workflow"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/temporal"
)

//encore:api public method=GET path=/v1/health/fees
func (s *Service) Health(ctx context.Context) (*HealthResponse, error) {
	return &HealthResponse{Service: "fees"}, nil
}

//encore:api public method=POST path=/v1/bills tag:idempotent
func (s *Service) CreateBill(ctx context.Context, req *CreateBillRequest) (*CreateBillResponse, error) {
	now := s.now()

	result, err := s.billingStore.InsertBill(ctx, billingstore.InsertBillParams{
		IdempotencyKey:              req.IdempotencyKey,
		AccountID:                   req.AccountID,
		ExternalReferenceID:         req.ExternalReferenceID,
		CurrencyCode:                domain.CurrencyCode(req.CurrencyCode),
		Status:                      initialBillStatus(now, req.BillingPeriodStartAt),
		BillingPeriodStartAt:        req.BillingPeriodStartAt,
		BillingPeriodEndAt:          req.BillingPeriodEndAt,
		LineItemsSubmissionDeadline: req.LineItemsSubmissionDeadline,
	})
	if err != nil {
		return nil, internalError("internal error")
	}
	if result == nil || result.Bill == nil {
		return nil, internalError("internal error")
	}
	if result.Status == billingstore.InsertBillConflict {
		return nil, conflictError("bill already exists for account_id and external_reference_id")
	}
	if err := s.wf.StartBillWorkflow(ctx, req.toWorkflowInput(result.Bill)); err != nil && !isWorkflowAlreadyStarted(err) {
		return nil, internalError("internal error")
	}
	return &CreateBillResponse{
		BillID: result.Bill.ID,
		Status: http.StatusCreated,
	}, nil
}

//encore:api public method=POST path=/v1/bills/:billID/line-items tag:idempotent
func (s *Service) AddLineItems(ctx context.Context, billID string, req *AddLineItemsRequest) (*AddLineItemsResponse, error) {
	if err := validateBillID(billID); err != nil {
		return nil, err
	}
	bill, err := s.billingStore.GetBill(ctx, billID)
	if err != nil {
		rlog.Error("fees.AddLineItems lookup failed", "bill_id", billID, "err_type", errorType(err), "err", err.Error())
		return nil, mapBillLookupError(err)
	}
	if bill.Status != domain.BillStatusOpen {
		rlog.Info("fees.AddLineItems rejected non-open bill", "bill_id", billID, "status", string(bill.Status))
		return nil, conflictError("bill is not accepting new line items")
	}
	if err := req.ValidateBusinessRules(bill); err != nil {
		rlog.Info("fees.AddLineItems business validation failed", "bill_id", billID, "err_type", errorType(err), "err", err.Error())
		return nil, err
	}
	result, err := s.wf.AddLineItems(ctx, billID, req.toWorkflowInput(billID))
	if err != nil {
		rlog.Error("fees.AddLineItems workflow update failed", "bill_id", billID, "err_type", errorType(err), "err", err.Error(), "err_chain", errorChain(err))
		return nil, mapWorkflowMutationError(err)
	}
	return &AddLineItemsResponse{
		SuccessLineItemIDsMap:    result.SuccessLineItemIDsMap,
		FailedLineItemReasonsMap: map[string]string{},
		Status:                   http.StatusOK,
	}, nil
}

//encore:api public method=POST path=/v1/bills/:billID/close tag:idempotent
func (s *Service) CloseBill(ctx context.Context, billID string, req *CloseBillRequest) (*CloseBillResponse, error) {
	if err := validateBillID(billID); err != nil {
		return nil, err
	}
	bill, err := s.billingStore.GetBill(ctx, billID)
	if err != nil {
		return nil, mapBillLookupError(err)
	}
	if bill.Status != domain.BillStatusOpen {
		return nil, conflictError("bill cannot be closed from its current status")
	}
	result, err := s.wf.CloseBill(ctx, billID, domainCloseToWorkflow(req))
	if err != nil {
		return nil, mapWorkflowMutationError(err)
	}
	return &CloseBillResponse{
		BillID:           result.BillID,
		BillStatus:       string(result.BillStatus),
		TotalAmountMinor: result.SnapshotTotalAmountMinor,
		Status:           http.StatusAccepted,
	}, nil
}

//encore:api public method=GET path=/v1/bills/:billID
func (s *Service) GetBill(ctx context.Context, billID string) (*GetBillResponse, error) {
	if err := validateBillID(billID); err != nil {
		return nil, err
	}
	bill, err := s.billingStore.GetBill(ctx, billID)
	if err != nil {
		return nil, mapBillLookupError(err)
	}
	if bill.Status == domain.BillStatusOpen || bill.Status == domain.BillStatusFinalizing {
		state, err := s.wf.GetState(ctx, billID)
		if err != nil {
			return nil, mapWorkflowQueryError(err)
		}
		return mapGetBillResponseWithWorkflowSnapshot(bill, state), nil
	}
	return mapGetBillResponse(bill), nil
}

//encore:api public method=GET path=/v1/bills/:billID/line-items
func (s *Service) ListBillLineItems(ctx context.Context, billID string, params *ListBillLineItemsParams) (*ListBillLineItemsResponse, error) {
	if err := validateBillID(billID); err != nil {
		return nil, err
	}
	if params == nil {
		params = &ListBillLineItemsParams{}
	}

	bill, err := s.billingStore.GetBill(ctx, billID)
	if err != nil {
		return nil, mapBillLookupError(err)
	}

	repoParams, err := params.toRepositoryParams(billID)
	if err != nil {
		return nil, err
	}
	result, err := s.billingStore.ListLineItems(ctx, repoParams)
	if err != nil {
		return nil, internalError("internal error")
	}
	return mapListBillLineItemsResponse(bill, result)
}

func initialBillStatus(now, billingPeriodStartAt time.Time) domain.BillStatus {
	if now.Before(billingPeriodStartAt) {
		return domain.BillStatusScheduled
	}
	return domain.BillStatusOpen
}

func domainCloseToWorkflow(req *CloseBillRequest) workflowpkg.CloseBillInput {
	return workflowpkg.CloseBillInput{}
}

func mapBillLookupError(err error) error {
	if errors.Is(err, sqldb.ErrNoRows) {
		return &errs.Error{Code: errs.NotFound, Message: "bill not found"}
	}
	return internalError("internal error")
}

func mapWorkflowMutationError(err error) error {
	var batchFailure *workflowpkg.LineItemsBatchFailureError
	var notFound *serviceerror.NotFound
	var appErr *temporal.ApplicationError

	if errors.As(err, &batchFailure) {
		rlog.Info("fees.mapWorkflowMutationError mapped batch failure", "err_type", errorType(err), "err", err.Error(), "err_chain", errorChain(err))
		return lineItemsBatchFailureError(batchFailure.RecordReasons)
	}

	if errors.As(err, &appErr) {
		var details workflowpkg.LineItemsBatchFailureError
		if appErr.Details(&details) == nil && len(details.RecordReasons) > 0 {
			return lineItemsBatchFailureError(details.RecordReasons)
		}

		switch appErr.Type() {
		case "LineItemsBatchFailureError":
			return lineItemsBatchFailureError(map[string]string{})
		case "StoreUnavailableError":
			return unavailableError("service temporarily unavailable")
		case "BillNotAcceptingLineItemsError":
			rlog.Info("fees.mapWorkflowMutationError mapped to 409 line-items-not-accepting", "err_type", errorType(err), "err", err.Error(), "err_chain", errorChain(err))
			return conflictError("bill is not accepting new line items")
		case "BillClosingInProgressError", "BillInvalidStateError":
			rlog.Info("fees.mapWorkflowMutationError mapped to 409 close-state-conflict", "err_type", errorType(err), "err", err.Error(), "err_chain", errorChain(err))
			return conflictError("bill cannot be closed from its current status")
		}
	}

	switch {
	case errors.As(err, &notFound):
		rlog.Error("fees.mapWorkflowMutationError mapped temporal not found to internal", "err_type", errorType(err), "err", err.Error(), "err_chain", errorChain(err))
		return errs.WrapCode(err, errs.Internal, "internal error")
	case errors.Is(err, workflowpkg.ErrBillNotAcceptingLineItems):
		return conflictError("bill is not accepting new line items")
	case errors.Is(err, workflowpkg.ErrBillClosingInProgress):
		return conflictError("bill cannot be closed from its current status")
	default:
		rlog.Error("fees.mapWorkflowMutationError fallback internal", "err_type", errorType(err), "err", err.Error(), "err_chain", errorChain(err))
		return internalError("internal error: " + err.Error())
	}
}

func errorChain(err error) string {
	if err == nil {
		return "<nil>"
	}
	parts := make([]string, 0, 8)
	for current := err; current != nil; current = errors.Unwrap(current) {
		parts = append(parts, current.Error())
	}
	return strings.Join(parts, " | ")
}

func errorType(err error) string {
	if err == nil {
		return "<nil>"
	}
	return fmt.Sprintf("%T", err)
}

func mapWorkflowQueryError(err error) error {
	var notFound *serviceerror.NotFound
	if errors.As(err, &notFound) {
		return errs.WrapCode(err, errs.Internal, "internal error")
	}
	return internalError("internal error")
}

func isWorkflowAlreadyStarted(err error) bool {
	var alreadyStarted *serviceerror.WorkflowExecutionAlreadyStarted
	return errors.As(err, &alreadyStarted)
}

func invalidArgument(message string) error {
	return &errs.Error{Code: errs.InvalidArgument, Message: message}
}

func conflictError(message string) error {
	return &errs.Error{Code: errs.Aborted, Message: message}
}

func internalError(message string) error {
	return &errs.Error{Code: errs.Internal, Message: message}
}

func unavailableError(message string) error {
	return &errs.Error{Code: errs.Unavailable, Message: message}
}

func validateBillID(billID string) error {
	return validateUUIDv4Field("bill_id", billID)
}
