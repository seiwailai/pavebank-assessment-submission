package idempotency

import (
	"context"
	"encoding/json"
	"time"
)

type Store interface {
	Reserve(ctx context.Context, params ReserveParams) (*ReserveResult, error)
	Complete(ctx context.Context, params CompleteParams) error
}

type Scope string

type ProcessingStatus string

const (
	StatusInProgress ProcessingStatus = "IN_PROGRESS"
	StatusCompleted  ProcessingStatus = "COMPLETED"
)

type ReserveOutcome string

const (
	ReserveAcquired   ReserveOutcome = "ACQUIRED"
	ReserveReplay     ReserveOutcome = "REPLAY"
	ReserveConflict   ReserveOutcome = "CONFLICT"
	ReserveInProgress ReserveOutcome = "IN_PROGRESS"
)

type ReserveParams struct {
	Scope        Scope
	Key          string
	Fingerprint  string
	InProgressExpiry time.Time
	RecordExpiry time.Time
	Now          time.Time
}

type CompleteParams struct {
	Scope       Scope
	Key         string
	Fingerprint string
	Response    StoredResponse
}

type StoredResponse struct {
	HTTPStatus int
	Body       json.RawMessage
}

type Record struct {
	Scope            Scope
	Key              string
	Fingerprint      string
	Status           ProcessingStatus
	InProgressExpiry *time.Time
	Response         *StoredResponse
	CreatedAt        time.Time
	ExpiresAt        time.Time
}

type ReserveResult struct {
	Outcome ReserveOutcome
	Record  *Record
}
