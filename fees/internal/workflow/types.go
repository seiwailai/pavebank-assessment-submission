package workflow

import (
	"time"

	"encore.app/fees/internal/domain"
)

const (
	DefaultTaskQueue = "fees-bill-lifecycle"

	UpdateNameAddLineItems = "add-line-items"
	UpdateNameCloseBill    = "close-bill"
	QueryNameState         = "state"

	ActivityNameTransitionBillStatus = "fees.transition-bill-status"
	ActivityNamePersistLineItems     = "fees.persist-line-items"
	ActivityNameFinalizeBill         = "fees.finalize-bill"
	ActivityNameMarkBillFailed       = "fees.mark-bill-failed"
)

type StartBillWorkflowInput struct {
	BillID                      string
	CurrencyCode                domain.CurrencyCode
	InitialStatus               domain.BillStatus
	BillingPeriodStartAt        time.Time
	LineItemsSubmissionDeadline time.Time
	SnapshotTotalAmountMinor    int64
	CloseRequested              bool
}

type State struct {
	BillID                      string
	CurrencyCode                domain.CurrencyCode
	Status                      domain.BillStatus
	BillingPeriodStartAt        time.Time
	LineItemsSubmissionDeadline time.Time
	SnapshotTotalAmountMinor    int64
}

type AddLineItemsInput struct {
	BillID         string
	IdempotencyKey string
	CurrencyCode   domain.CurrencyCode
	LineItems      []domain.AddLineItemInput
}

type AddLineItemsResult struct {
	BillID                   string
	BillStatus               domain.BillStatus
	SnapshotTotalAmountMinor int64
	AcceptedRequestID        string
	SuccessLineItemIDsMap    map[string]string
	FailedLineItemReasonsMap map[string]string
}

type CloseBillInput struct{}

type CloseBillResult struct {
	BillID                   string
	BillStatus               domain.BillStatus
	SnapshotTotalAmountMinor int64
}

type BillWorkflowResult struct {
	BillID           string
	BillStatus       domain.BillStatus
	TotalAmountMinor int64
	FailureReason    string
}

type TransitionBillStatusActivityInput struct {
	BillID string
	Status domain.BillStatus
}

type PersistLineItemsActivityInput struct {
	BillID         string
	IdempotencyKey string
	CurrencyCode   domain.CurrencyCode
	LineItems      []domain.AddLineItemInput
}

type PersistLineItemsActivityResult struct {
	AmountDeltaMinor         int64
	SuccessLineItemIDsMap    map[string]string
	FailedLineItemReasonsMap map[string]string
}

type FinalizeBillActivityInput struct {
	BillID string
}

type FinalizeBillActivityResult struct {
	BillStatus       domain.BillStatus
	TotalAmountMinor int64
	FailureReason    string
}

type MarkBillFailedActivityInput struct {
	BillID                   string
	FailureReason            string
	SnapshotTotalAmountMinor int64
}

type LineItemsBatchFailureError struct {
	RecordReasons map[string]string
}

func (e *LineItemsBatchFailureError) Error() string {
	return "line items batch rejected"
}

func WorkflowID(billID string) string {
	return "bill-workflow-id-" + billID
}
