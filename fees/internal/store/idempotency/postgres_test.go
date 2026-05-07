package idempotency

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"encore.dev/et"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReserveUsesGenericScopeAndFingerprintMismatch(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := newTestStore(t)
	now := time.Date(2026, 5, 5, 10, 0, 0, 0, time.UTC)

	first, err := store.Reserve(ctx, ReserveParams{
		Scope:            Scope("POST:/v1/bills"),
		Key:              "idem-001",
		Fingerprint:      "fp-a",
		InProgressExpiry: now.Add(2 * time.Minute),
		RecordExpiry:     now.Add(24 * time.Hour),
		Now:              now,
	})
	require.NoError(t, err)
	require.NotNil(t, first)
	assert.Equal(t, ReserveAcquired, first.Outcome)

	second, err := store.Reserve(ctx, ReserveParams{
		Scope:            Scope("POST:/v1/bills"),
		Key:              "idem-001",
		Fingerprint:      "fp-b",
		InProgressExpiry: now.Add(2 * time.Minute),
		RecordExpiry:     now.Add(24 * time.Hour),
		Now:              now,
	})
	require.NoError(t, err)
	require.NotNil(t, second)
	assert.Equal(t, ReserveConflict, second.Outcome)
}

func TestReserveCompleteAndReplay(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := newTestStore(t)
	now := time.Date(2026, 5, 5, 10, 0, 0, 0, time.UTC)

	first, err := store.Reserve(ctx, ReserveParams{
		Scope:            Scope("POST:/v1/bills/bill-123/close"),
		Key:              "idem-002",
		Fingerprint:      "fp-close",
		InProgressExpiry: now.Add(2 * time.Minute),
		RecordExpiry:     now.Add(24 * time.Hour),
		Now:              now,
	})
	require.NoError(t, err)
	require.NotNil(t, first)
	assert.Equal(t, ReserveAcquired, first.Outcome)

	err = store.Complete(ctx, CompleteParams{
		Scope:       Scope("POST:/v1/bills/bill-123/close"),
		Key:         "idem-002",
		Fingerprint: "fp-close",
		Response: StoredResponse{
			HTTPStatus: 202,
			Body:       json.RawMessage(`{"bill_id":"bill-123"}`),
		},
	})
	require.NoError(t, err)

	replay, err := store.Reserve(ctx, ReserveParams{
		Scope:            Scope("POST:/v1/bills/bill-123/close"),
		Key:              "idem-002",
		Fingerprint:      "fp-close",
		InProgressExpiry: now.Add(2 * time.Minute),
		RecordExpiry:     now.Add(24 * time.Hour),
		Now:              now,
	})
	require.NoError(t, err)
	require.NotNil(t, replay)
	assert.Equal(t, ReserveReplay, replay.Outcome)
	require.NotNil(t, replay.Record)
	require.NotNil(t, replay.Record.Response)
	assert.Equal(t, 202, replay.Record.Response.HTTPStatus)
	assert.JSONEq(t, `{"bill_id":"bill-123"}`, string(replay.Record.Response.Body))
}

func TestCompleteIsIdempotentWhenAlreadyCompletedWithSameResponse(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := newTestStore(t)
	now := time.Date(2026, 5, 5, 10, 0, 0, 0, time.UTC)
	scope := Scope("POST:/v1/bills")
	key := "idem-complete-idempotent"
	fingerprint := "fp-same"
	response := StoredResponse{
		HTTPStatus: 201,
		Body:       json.RawMessage(`{"bill_id":"bill-1"}`),
	}

	first, err := store.Reserve(ctx, ReserveParams{
		Scope:            scope,
		Key:              key,
		Fingerprint:      fingerprint,
		InProgressExpiry: now.Add(2 * time.Minute),
		RecordExpiry:     now.Add(24 * time.Hour),
		Now:              now,
	})
	require.NoError(t, err)
	require.NotNil(t, first)
	assert.Equal(t, ReserveAcquired, first.Outcome)

	err = store.Complete(ctx, CompleteParams{
		Scope:       scope,
		Key:         key,
		Fingerprint: fingerprint,
		Response:    response,
	})
	require.NoError(t, err)

	err = store.Complete(ctx, CompleteParams{
		Scope:       scope,
		Key:         key,
		Fingerprint: fingerprint,
		Response:    response,
	})
	require.NoError(t, err)
}

func TestIdempotencyRecordsSchemaDropsScopeID(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db, err := et.NewTestDatabase(ctx, "fees")
	require.NoError(t, err)

	var scopeIDColumnCount int
	err = db.QueryRow(ctx, `
SELECT COUNT(*)
FROM information_schema.columns
WHERE table_name = 'idempotency_records'
  AND column_name = 'scope_id'
`).Scan(&scopeIDColumnCount)
	require.NoError(t, err)
	assert.Zero(t, scopeIDColumnCount)

	rows, err := db.Query(ctx, `
SELECT kcu.column_name
FROM information_schema.table_constraints tc
JOIN information_schema.key_column_usage kcu
  ON tc.constraint_name = kcu.constraint_name
 AND tc.table_schema = kcu.table_schema
WHERE tc.table_name = 'idempotency_records'
  AND tc.constraint_type = 'PRIMARY KEY'
ORDER BY kcu.ordinal_position
`)
	require.NoError(t, err)
	defer rows.Close()

	var pkColumns []string
	for rows.Next() {
		var columnName string
		require.NoError(t, rows.Scan(&columnName))
		pkColumns = append(pkColumns, columnName)
	}
	require.NoError(t, rows.Err())
	assert.Equal(t, []string{"idempotency_key", "operation_type"}, pkColumns)
}

func newTestStore(t *testing.T) *PostgresStore {
	t.Helper()

	db, err := et.NewTestDatabase(context.Background(), "fees")
	require.NoError(t, err)

	return NewPostgresStore(db)
}
