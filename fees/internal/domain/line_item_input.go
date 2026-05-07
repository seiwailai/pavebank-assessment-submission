package domain

import "time"

type AddLineItemInput struct {
	ExternalReferenceID string
	OccurredAt          time.Time
	AmountMinor         int64
	Description         *string
}
