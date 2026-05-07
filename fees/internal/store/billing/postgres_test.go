package billing

import (
	"context"
	"fmt"
	"testing"
	"time"

	"encore.dev/et"
	"encore.dev/storage/sqldb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"encore.app/fees/internal/domain"
)

func TestInsertBillReturnsReplayForMatchingIdempotencyKey(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := newTestBillingStore(t)

	first, err := store.InsertBill(ctx, InsertBillParams{
		IdempotencyKey:              "idem-bill-1",
		AccountID:                   "acct_123",
		ExternalReferenceID:         "bill-001",
		CurrencyCode:                domain.USD(),
		Status:                      domain.BillStatusOpen,
		BillingPeriodStartAt:        time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
		BillingPeriodEndAt:          time.Date(2026, 5, 31, 23, 59, 59, 0, time.UTC),
		LineItemsSubmissionDeadline: time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC),
	})
	require.NoError(t, err)
	require.NotNil(t, first)
	assert.Equal(t, InsertBillCreated, first.Status)
	require.NotNil(t, first.Bill)

	second, err := store.InsertBill(ctx, InsertBillParams{
		IdempotencyKey:              "idem-bill-1",
		AccountID:                   "acct_123",
		ExternalReferenceID:         "bill-001",
		CurrencyCode:                domain.USD(),
		Status:                      domain.BillStatusOpen,
		BillingPeriodStartAt:        time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
		BillingPeriodEndAt:          time.Date(2026, 5, 31, 23, 59, 59, 0, time.UTC),
		LineItemsSubmissionDeadline: time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC),
	})
	require.NoError(t, err)
	require.NotNil(t, second)
	assert.Equal(t, InsertBillReplayed, second.Status)
	require.NotNil(t, second.Bill)
	assert.Equal(t, first.Bill.ID, second.Bill.ID)
}

func TestInsertBillReturnsConflictForDifferentIdempotencyKey(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := newTestBillingStore(t)

	first, err := store.InsertBill(ctx, InsertBillParams{
		IdempotencyKey:              "idem-bill-1",
		AccountID:                   "acct_123",
		ExternalReferenceID:         "bill-001",
		CurrencyCode:                domain.USD(),
		Status:                      domain.BillStatusOpen,
		BillingPeriodStartAt:        time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
		BillingPeriodEndAt:          time.Date(2026, 5, 31, 23, 59, 59, 0, time.UTC),
		LineItemsSubmissionDeadline: time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC),
	})
	require.NoError(t, err)
	require.NotNil(t, first)
	require.NotNil(t, first.Bill)

	second, err := store.InsertBill(ctx, InsertBillParams{
		IdempotencyKey:              "idem-bill-2",
		AccountID:                   "acct_123",
		ExternalReferenceID:         "bill-001",
		CurrencyCode:                domain.USD(),
		Status:                      domain.BillStatusOpen,
		BillingPeriodStartAt:        time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
		BillingPeriodEndAt:          time.Date(2026, 5, 31, 23, 59, 59, 0, time.UTC),
		LineItemsSubmissionDeadline: time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC),
	})
	require.NoError(t, err)
	require.NotNil(t, second)
	assert.Equal(t, InsertBillConflict, second.Status)
	require.NotNil(t, second.Bill)
	assert.Equal(t, first.Bill.ID, second.Bill.ID)
}

func TestBillingSchemaSupportsConflictTargetsAndIdempotencyColumns(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db, err := et.NewTestDatabase(ctx, "fees")
	require.NoError(t, err)

	assertSchemaHasColumn(t, ctx, db, "bills", "idempotency_key")
	assertSchemaHasColumn(t, ctx, db, "line_items", "idempotency_key")
	assertConstraintColumns(t, ctx, db, "bills", "UNIQUE", []string{"account_id", "external_reference_id"})
	assertConstraintColumns(t, ctx, db, "line_items", "UNIQUE", []string{"bill_id", "external_reference_id"})
}

func TestInsertLineItemsReturnsExistingRowIDOnSameRequestReplay(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := newTestBillingStore(t)
	bill := createTestBill(t, ctx, store, "acct_line_replay", "bill-line-replay", domain.USD())
	occurredAt := time.Date(2026, 5, 10, 10, 0, 0, 0, time.UTC)

	first, err := store.InsertLineItems(ctx, InsertLineItemsParams{
		BillID:         bill.ID,
		IdempotencyKey: "idem-line-1",
		RequestID:      "req-1",
		CurrencyCode:   domain.USD(),
		LineItems: []LineItemInsert{{
			ExternalReferenceID: "line-001",
			OccurredAt:          occurredAt,
			AmountMinor:         100,
		}},
	})
	require.NoError(t, err)
	require.NotNil(t, first)

	second, err := store.InsertLineItems(ctx, InsertLineItemsParams{
		BillID:         bill.ID,
		IdempotencyKey: "idem-line-1",
		RequestID:      "req-2",
		CurrencyCode:   domain.USD(),
		LineItems: []LineItemInsert{{
			ExternalReferenceID: "line-001",
			OccurredAt:          occurredAt,
			AmountMinor:         100,
		}},
	})
	require.NoError(t, err)
	require.NotNil(t, second)
	assert.Equal(t, int64(0), second.AmountDeltaMinor)
	assert.Equal(t, first.SuccessLineItemIDs["line-001"], second.SuccessLineItemIDs["line-001"])
	assert.Nil(t, second.BatchFailure)
}

func TestInsertLineItems_RollsBackWholeBatchOnExistingConflict(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := newTestBillingStore(t)
	bill := createTestBill(t, ctx, store, "acct_atomic", "bill-atomic", domain.USD())

	first, err := store.InsertLineItems(ctx, InsertLineItemsParams{
		BillID:         bill.ID,
		IdempotencyKey: "idem-line-1",
		RequestID:      "req-1",
		CurrencyCode:   domain.USD(),
		LineItems: []LineItemInsert{{
			ExternalReferenceID: "dup-1",
			OccurredAt:          time.Now().UTC(),
			AmountMinor:         100,
		}},
	})
	require.NoError(t, err)
	require.Len(t, first.SuccessLineItemIDs, 1)

	persistedBill, err := store.GetBill(ctx, bill.ID)
	require.NoError(t, err)
	require.Zero(t, persistedBill.TotalAmountMinor)

	result, err := store.InsertLineItems(ctx, InsertLineItemsParams{
		BillID:         bill.ID,
		IdempotencyKey: "idem-line-2",
		RequestID:      "req-2",
		CurrencyCode:   domain.USD(),
		LineItems: []LineItemInsert{
			{ExternalReferenceID: "dup-1", OccurredAt: time.Now().UTC(), AmountMinor: 100},
			{ExternalReferenceID: "new-2", OccurredAt: time.Now().UTC(), AmountMinor: 200},
		},
	})
	require.NoError(t, err)
	require.NotNil(t, result.BatchFailure)
	require.Equal(t, lineItemConflictReason, result.BatchFailure.RecordReasons["dup-1"])

	items := mustListAllLineItems(t, ctx, store, bill.ID)
	require.Len(t, items, 1)
}

func TestInsertLineItems_RollsBackPartialReplayWithSameIdempotencyKey(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := newTestBillingStore(t)
	bill := createTestBill(t, ctx, store, "acct_partial_replay", "bill-partial-replay", domain.USD())

	first, err := store.InsertLineItems(ctx, InsertLineItemsParams{
		BillID:         bill.ID,
		IdempotencyKey: "idem-line-1",
		RequestID:      "req-1",
		CurrencyCode:   domain.USD(),
		LineItems: []LineItemInsert{{
			ExternalReferenceID: "dup-1",
			OccurredAt:          time.Now().UTC(),
			AmountMinor:         100,
		}},
	})
	require.NoError(t, err)
	require.Len(t, first.SuccessLineItemIDs, 1)

	result, err := store.InsertLineItems(ctx, InsertLineItemsParams{
		BillID:         bill.ID,
		IdempotencyKey: "idem-line-1",
		RequestID:      "req-2",
		CurrencyCode:   domain.USD(),
		LineItems: []LineItemInsert{
			{ExternalReferenceID: "dup-1", OccurredAt: time.Now().UTC(), AmountMinor: 100},
			{ExternalReferenceID: "line-2", OccurredAt: time.Now().UTC(), AmountMinor: 200},
		},
	})
	require.NoError(t, err)
	require.NotNil(t, result.BatchFailure)
	require.Equal(t, lineItemConflictReason, result.BatchFailure.RecordReasons["dup-1"])

	items := mustListAllLineItems(t, ctx, store, bill.ID)
	require.Len(t, items, 1)
}

func TestInsertLineItems_RollsBackMixedConflictWithDifferentIdempotencyKey(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := newTestBillingStore(t)
	bill := createTestBill(t, ctx, store, "acct_mixed_conflict", "bill-mixed-conflict", domain.USD())

	first, err := store.InsertLineItems(ctx, InsertLineItemsParams{
		BillID:         bill.ID,
		IdempotencyKey: "idem-line-1",
		RequestID:      "req-1",
		CurrencyCode:   domain.USD(),
		LineItems: []LineItemInsert{{
			ExternalReferenceID: "dup-1",
			OccurredAt:          time.Now().UTC(),
			AmountMinor:         100,
		}},
	})
	require.NoError(t, err)
	require.Len(t, first.SuccessLineItemIDs, 1)

	result, err := store.InsertLineItems(ctx, InsertLineItemsParams{
		BillID:         bill.ID,
		IdempotencyKey: "idem-line-2",
		RequestID:      "req-2",
		CurrencyCode:   domain.USD(),
		LineItems: []LineItemInsert{
			{ExternalReferenceID: "dup-1", OccurredAt: time.Now().UTC(), AmountMinor: 100},
			{ExternalReferenceID: "line-2", OccurredAt: time.Now().UTC(), AmountMinor: 200},
		},
	})
	require.NoError(t, err)
	require.NotNil(t, result.BatchFailure)
	require.Equal(t, lineItemConflictReason, result.BatchFailure.RecordReasons["dup-1"])

	items := mustListAllLineItems(t, ctx, store, bill.ID)
	require.Len(t, items, 1)
}

func TestUpdateBillCanCloseBillAndSetFinalTotal(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := newTestBillingStore(t)
	bill := createTestBill(t, ctx, store, "acct_update", "bill-update", domain.USD())
	closedAt := time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)
	status := domain.BillStatusClosed
	finalTotal := int64(450)

	err := store.UpdateBill(ctx, UpdateBillParams{
		BillID:                bill.ID,
		Status:                &status,
		FinalTotalAmountMinor: &finalTotal,
		ClosedAt:              &closedAt,
	})
	require.NoError(t, err)

	got, err := store.GetBill(ctx, bill.ID)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, domain.BillStatusClosed, got.Status)
	assert.Equal(t, finalTotal, got.TotalAmountMinor)
	require.NotNil(t, got.ClosedAt)
	assert.True(t, got.ClosedAt.Equal(closedAt))
}

func newTestBillingStore(t *testing.T) *PostgresStore {
	t.Helper()

	db, err := et.NewTestDatabase(context.Background(), "fees")
	require.NoError(t, err)

	return NewPostgresStore(db)
}

func createTestBill(t *testing.T, ctx context.Context, store *PostgresStore, accountID, externalRef string, currency domain.CurrencyCode) *domain.Bill {
	t.Helper()

	result, err := store.InsertBill(ctx, InsertBillParams{
		IdempotencyKey:              "idem-" + externalRef,
		AccountID:                   accountID,
		ExternalReferenceID:         externalRef,
		CurrencyCode:                currency,
		Status:                      domain.BillStatusOpen,
		BillingPeriodStartAt:        time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
		BillingPeriodEndAt:          time.Date(2026, 5, 31, 23, 59, 59, 0, time.UTC),
		LineItemsSubmissionDeadline: time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC),
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, result.Bill)
	return result.Bill
}

func mustListAllLineItems(t *testing.T, ctx context.Context, store *PostgresStore, billID string) []LineItem {
	t.Helper()

	result, err := store.ListLineItems(ctx, ListLineItemsParams{
		BillID: billID,
		Limit:  100,
	})
	require.NoError(t, err)
	require.False(t, result.HasMore)
	return result.Items
}

func assertSchemaHasColumn(t *testing.T, ctx context.Context, db interface {
	QueryRow(context.Context, string, ...any) *sqldb.Row
}, tableName, columnName string) {
	t.Helper()

	var count int
	err := db.QueryRow(ctx, `
SELECT COUNT(*)
FROM information_schema.columns
WHERE table_name = $1
  AND column_name = $2
`, tableName, columnName).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count, fmt.Sprintf("expected %s.%s column", tableName, columnName))
}

func assertConstraintColumns(t *testing.T, ctx context.Context, db interface {
	Query(context.Context, string, ...any) (*sqldb.Rows, error)
}, tableName, constraintType string, expectedColumns []string) {
	t.Helper()

	rows, err := db.Query(ctx, `
SELECT tc.constraint_name, kcu.column_name, kcu.ordinal_position
FROM information_schema.table_constraints tc
JOIN information_schema.key_column_usage kcu
  ON tc.constraint_name = kcu.constraint_name
 AND tc.table_schema = kcu.table_schema
WHERE tc.table_name = $1
  AND tc.constraint_type = $2
ORDER BY tc.constraint_name, kcu.ordinal_position
`, tableName, constraintType)
	require.NoError(t, err)
	defer rows.Close()

	byConstraint := make(map[string][]string)
	for rows.Next() {
		var constraintName string
		var columnName string
		var ordinal int
		require.NoError(t, rows.Scan(&constraintName, &columnName, &ordinal))
		byConstraint[constraintName] = append(byConstraint[constraintName], columnName)
	}
	require.NoError(t, rows.Err())

	for _, columns := range byConstraint {
		if assert.ObjectsAreEqual(expectedColumns, columns) {
			return
		}
	}

	require.Failf(t, "missing constraint", "expected %s constraint on %s with columns %v, got %v", constraintType, tableName, expectedColumns, byConstraint)
}
