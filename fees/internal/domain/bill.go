package domain

import "time"

type BillStatus string

const (
	BillStatusScheduled  BillStatus = "SCHEDULED"
	BillStatusOpen       BillStatus = "OPEN"
	BillStatusFinalizing BillStatus = "FINALIZING"
	BillStatusClosed     BillStatus = "CLOSED"
	BillStatusFailed     BillStatus = "FAILED"
)

type Bill struct {
	ID                          string
	AccountID                   string
	ExternalReferenceID         string
	Status                      BillStatus
	CurrencyCode                CurrencyCode
	BillingPeriodStartAt        time.Time
	BillingPeriodEndAt          time.Time
	LineItemsSubmissionDeadline time.Time
	TotalAmountMinor            int64
	ClosedAt                    *time.Time
	FailedAt                    *time.Time
	FailureReason               *string
	CreatedAt                   time.Time
	UpdatedAt                   time.Time
}
