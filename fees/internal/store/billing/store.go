package billing

import (
	"context"
	"errors"
	"time"

	"encore.app/fees/internal/domain"
)

type Store interface {
	InsertBill(ctx context.Context, params InsertBillParams) (*InsertBillResult, error)
	GetBill(ctx context.Context, billID string) (*domain.Bill, error)
	UpdateBill(ctx context.Context, params UpdateBillParams) error
	InsertLineItems(ctx context.Context, params InsertLineItemsParams) (*InsertLineItemsResult, error)
	ListLineItems(ctx context.Context, params ListLineItemsParams) (*ListLineItemsResult, error)
}

type InsertBillParams struct {
	IdempotencyKey              string
	AccountID                   string
	ExternalReferenceID         string
	CurrencyCode                domain.CurrencyCode
	Status                      domain.BillStatus
	BillingPeriodStartAt        time.Time
	BillingPeriodEndAt          time.Time
	LineItemsSubmissionDeadline time.Time
}

type InsertBillStatus string

const (
	InsertBillCreated  InsertBillStatus = "CREATED"
	InsertBillReplayed InsertBillStatus = "REPLAYED"
	InsertBillConflict InsertBillStatus = "CONFLICT"
)

type InsertBillResult struct {
	Bill   *domain.Bill
	Status InsertBillStatus
}

type UpdateBillParams struct {
	BillID                   string
	Status                   *domain.BillStatus
	SnapshotTotalAmountMinor *int64
	FinalTotalAmountMinor    *int64
	ClosedAt                 *time.Time
	FailedAt                 *time.Time
	FailureReason            *string
}

type InsertLineItemsParams struct {
	IdempotencyKey string
	BillID         string
	RequestID      string
	CurrencyCode   domain.CurrencyCode
	LineItems      []LineItemInsert
}

type LineItemInsert struct {
	ExternalReferenceID string
	OccurredAt          time.Time
	AmountMinor         int64
	Description         *string
}

type BatchFailure struct {
	RecordReasons map[string]string
}

type InsertLineItemsResult struct {
	SuccessLineItemIDs map[string]string
	BatchFailure       *BatchFailure
	AmountDeltaMinor   int64
}

type FinalizeBillResult struct {
	BillStatus       domain.BillStatus
	TotalAmountMinor int64
	FailureReason    string
}

type ListLineItemsParams struct {
	BillID          string
	OccurredAtAfter *time.Time
	LineItemIDAfter *string
	Limit           int
}

type ListLineItemsResult struct {
	Items   []LineItem
	HasMore bool
}

type LineItem struct {
	ID                  string
	BillID              string
	ExternalReferenceID string
	CurrencyCode        domain.CurrencyCode
	OccurredAt          time.Time
	AmountMinor         int64
	Description         *string
	CreatedAt           time.Time
}

var (
	ErrStoreUnavailable = errors.New("store unavailable")
)

type StoreUnavailableError struct {
	Op    string
	Cause error
}

func (e *StoreUnavailableError) Error() string {
	if e == nil {
		return ErrStoreUnavailable.Error()
	}
	if e.Op == "" {
		return ErrStoreUnavailable.Error()
	}
	return ErrStoreUnavailable.Error() + ": " + e.Op
}

func (e *StoreUnavailableError) Unwrap() error {
	if e == nil {
		return nil
	}
	if e.Cause != nil {
		return e.Cause
	}
	return ErrStoreUnavailable
}

func (e *StoreUnavailableError) Is(target error) bool {
	return target == ErrStoreUnavailable
}
