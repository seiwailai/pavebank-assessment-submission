package billing

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"encore.dev/storage/sqldb"

	"encore.app/fees/internal/domain"
	"github.com/jackc/pgx/v5/pgconn"
)

const lineItemConflictReason = "conflict"
const lineItemDuplicateInRequestReason = "duplicate_in_request"
type PostgresStore struct {
	db *sqldb.Database
}

type queryer interface {
	Exec(ctx context.Context, query string, args ...any) (sqldb.ExecResult, error)
	Query(ctx context.Context, query string, args ...any) (*sqldb.Rows, error)
	QueryRow(ctx context.Context, query string, args ...any) *sqldb.Row
}

func NewPostgresStore(db *sqldb.Database) *PostgresStore {
	return &PostgresStore{db: db}
}

func (s *PostgresStore) InsertBill(ctx context.Context, params InsertBillParams) (*InsertBillResult, error) {
	row := s.db.QueryRow(ctx, `
WITH inserted AS (
	INSERT INTO bills (
		idempotency_key,
		account_id,
		external_reference_id,
		currency_code,
		bill_status,
		billing_period_start_at,
		billing_period_end_at,
		line_items_submission_deadline
	) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	ON CONFLICT (account_id, external_reference_id) DO NOTHING
	RETURNING
		bill_id,
		account_id,
		external_reference_id,
		currency_code,
		bill_status,
		billing_period_start_at,
		billing_period_end_at,
		line_items_submission_deadline,
		total_amount_minor,
		closed_at,
		failed_at,
		failure_reason,
		created_at,
		updated_at,
		'CREATED'::text AS insert_status
), existing AS (
	SELECT
		bill_id,
		account_id,
		external_reference_id,
		currency_code,
		bill_status,
		billing_period_start_at,
		billing_period_end_at,
		line_items_submission_deadline,
		total_amount_minor,
		closed_at,
		failed_at,
		failure_reason,
		created_at,
		updated_at,
		CASE
			WHEN idempotency_key = $1 THEN 'REPLAYED'::text
			ELSE 'CONFLICT'::text
		END AS insert_status
	FROM bills
	WHERE account_id = $2
	  AND external_reference_id = $3
	  AND NOT EXISTS (SELECT 1 FROM inserted)
)
SELECT
	bill_id,
	account_id,
	external_reference_id,
	currency_code,
	bill_status,
	billing_period_start_at,
	billing_period_end_at,
	line_items_submission_deadline,
	total_amount_minor,
	closed_at,
	failed_at,
	failure_reason,
	created_at,
	updated_at,
	insert_status
FROM inserted
UNION ALL
SELECT
	bill_id,
	account_id,
	external_reference_id,
	currency_code,
	bill_status,
	billing_period_start_at,
	billing_period_end_at,
	line_items_submission_deadline,
	total_amount_minor,
	closed_at,
	failed_at,
	failure_reason,
	created_at,
	updated_at,
	insert_status
FROM existing
`,
		params.IdempotencyKey,
		params.AccountID,
		params.ExternalReferenceID,
		string(params.CurrencyCode),
		string(params.Status),
		params.BillingPeriodStartAt,
		params.BillingPeriodEndAt,
		params.LineItemsSubmissionDeadline,
	)

	bill, status, err := scanBillWithInsertStatus(row)
	if err != nil {
		return nil, err
	}
	return &InsertBillResult{Bill: bill, Status: status}, nil
}

func (s *PostgresStore) GetBill(ctx context.Context, billID string) (*domain.Bill, error) {
	row := s.db.QueryRow(ctx, `
SELECT
	bill_id,
	account_id,
	external_reference_id,
	currency_code,
	bill_status,
	billing_period_start_at,
	billing_period_end_at,
	line_items_submission_deadline,
	total_amount_minor,
	closed_at,
	failed_at,
	failure_reason,
	created_at,
	updated_at
FROM bills
WHERE bill_id = $1
`, billID)
	return scanBill(row)
}

func (s *PostgresStore) UpdateBill(ctx context.Context, params UpdateBillParams) error {
	sets := make([]string, 0, 6)
	args := make([]any, 0, 8)
	next := 1

	if params.Status != nil {
		sets = append(sets, fmt.Sprintf("bill_status = $%d", next))
		args = append(args, string(*params.Status))
		next++
	}
	if params.SnapshotTotalAmountMinor != nil {
		sets = append(sets, fmt.Sprintf("total_amount_minor = $%d", next))
		args = append(args, *params.SnapshotTotalAmountMinor)
		next++
	}
	if params.FinalTotalAmountMinor != nil {
		sets = append(sets, fmt.Sprintf("total_amount_minor = $%d", next))
		args = append(args, *params.FinalTotalAmountMinor)
		next++
	}
	if params.ClosedAt != nil {
		sets = append(sets, fmt.Sprintf("closed_at = $%d", next))
		args = append(args, *params.ClosedAt)
		next++
	}
	if params.FailedAt != nil {
		sets = append(sets, fmt.Sprintf("failed_at = $%d", next))
		args = append(args, *params.FailedAt)
		next++
	}
	if params.FailureReason != nil {
		sets = append(sets, fmt.Sprintf("failure_reason = $%d", next))
		args = append(args, *params.FailureReason)
		next++
	}
	if len(sets) == 0 {
		return nil
	}

	args = append(args, params.BillID)
	query := fmt.Sprintf("UPDATE bills SET %s WHERE bill_id = $%d", strings.Join(sets, ", "), next)
	if _, err := s.db.Exec(ctx, query, args...); err != nil {
		return fmt.Errorf("update bill: %w", err)
	}
	return nil
}

func (s *PostgresStore) InsertLineItems(ctx context.Context, params InsertLineItemsParams) (*InsertLineItemsResult, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin insert line items tx: %w", err)
	}
	defer rollbackUnlessCommitted(tx)

	externalReferenceIDs, occurredAts, amountMinors, descriptions, amountDelta, duplicateFailures := buildLineItemBatch(params.LineItems)
	if len(duplicateFailures) > 0 {
		return &InsertLineItemsResult{
			SuccessLineItemIDs: map[string]string{},
			BatchFailure:       &BatchFailure{RecordReasons: duplicateFailures},
			AmountDeltaMinor:   0,
		}, nil
	}

	existingRows, err := fetchExistingLineItems(ctx, tx, params.BillID, externalReferenceIDs)
	if err != nil {
		return nil, err
	}
	if len(existingRows) > 0 {
		successReplay, replayOK := sameIdempotencyReplay(existingRows, params.IdempotencyKey, len(externalReferenceIDs))
		if replayOK {
			return &InsertLineItemsResult{
				SuccessLineItemIDs: successReplay,
				BatchFailure:       nil,
				AmountDeltaMinor:   0,
			}, nil
		}

		conflicts := make(map[string]string, len(existingRows))
		for externalReferenceID := range existingRows {
			conflicts[externalReferenceID] = lineItemConflictReason
		}
		return &InsertLineItemsResult{
			SuccessLineItemIDs: map[string]string{},
			BatchFailure:       &BatchFailure{RecordReasons: conflicts},
			AmountDeltaMinor:   0,
		}, nil
	}

	successIDs, err := bulkInsertLineItems(ctx, tx, params, externalReferenceIDs, occurredAts, amountMinors, descriptions)
	if err != nil {
		var conflictErr *lineItemsConflictError
		if errors.As(err, &conflictErr) {
			return &InsertLineItemsResult{
				SuccessLineItemIDs: map[string]string{},
				BatchFailure:       &BatchFailure{RecordReasons: conflictErr.RecordReasons},
				AmountDeltaMinor:   0,
			}, nil
		}
		if unavailable, ok := asStoreUnavailable(err, "insert line items"); ok {
			return nil, unavailable
		}
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit insert line items tx: %w", err)
	}
	return &InsertLineItemsResult{
		SuccessLineItemIDs: successIDs,
		BatchFailure:       nil,
		AmountDeltaMinor:   amountDelta,
	}, nil
}

func (s *PostgresStore) ListLineItems(ctx context.Context, params ListLineItemsParams) (*ListLineItemsResult, error) {
	limit := params.Limit
	if limit <= 0 {
		limit = 50
	}

	rows, err := s.db.Query(ctx, `
SELECT
	line_item_id,
	bill_id,
	external_reference_id,
	currency_code,
	occurred_at,
	amount_minor,
	description,
	created_at
FROM line_items
WHERE bill_id = $1
  AND (
	$2::timestamptz IS NULL OR
	occurred_at > $2 OR
	($3::uuid IS NOT NULL AND occurred_at = $2 AND line_item_id > $3)
  )
ORDER BY occurred_at ASC, line_item_id ASC
LIMIT $4
`, params.BillID, params.OccurredAtAfter, params.LineItemIDAfter, limit+1)
	if err != nil {
		return nil, fmt.Errorf("list line items: %w", err)
	}
	defer rows.Close()

	result := &ListLineItemsResult{Items: make([]LineItem, 0, limit)}
	for rows.Next() {
		var item LineItem
		var currency string
		var description sql.NullString

		if err := rows.Scan(
			&item.ID,
			&item.BillID,
			&item.ExternalReferenceID,
			&currency,
			&item.OccurredAt,
			&item.AmountMinor,
			&description,
			&item.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan line item: %w", err)
		}

		item.CurrencyCode = domain.CurrencyCode(currency)
		if description.Valid {
			desc := description.String
			item.Description = &desc
		}
		result.Items = append(result.Items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate line items: %w", err)
	}
	if len(result.Items) > limit {
		result.Items = result.Items[:limit]
		result.HasMore = true
	}
	return result, nil
}

func (s *PostgresStore) FinalizeBill(ctx context.Context, billID string) (*FinalizeBillResult, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin finalize bill tx: %w", err)
	}
	defer rollbackUnlessCommitted(tx)

	var total int64
	if err := tx.QueryRow(ctx, `
SELECT COALESCE(SUM(amount_minor), 0)
FROM line_items
WHERE bill_id = $1
`, billID).Scan(&total); err != nil {
		return nil, fmt.Errorf("sum line items: %w", err)
	}

	if _, err := tx.Exec(ctx, `
UPDATE bills
SET
	bill_status = $2,
	total_amount_minor = $3,
	closed_at = now(),
	failed_at = NULL,
	failure_reason = NULL
WHERE bill_id = $1
`, billID, string(domain.BillStatusClosed), total); err != nil {
		return nil, fmt.Errorf("update finalized bill: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit finalize bill tx: %w", err)
	}

	return &FinalizeBillResult{
		BillStatus:       domain.BillStatusClosed,
		TotalAmountMinor: total,
	}, nil
}

func scanBillWithInsertStatus(row *sqldb.Row) (*domain.Bill, InsertBillStatus, error) {
	var bill domain.Bill
	var currencyCode string
	var status string
	var closedAt sql.NullTime
	var failedAt sql.NullTime
	var failureReason sql.NullString
	var insertStatus string

	if err := row.Scan(
		&bill.ID,
		&bill.AccountID,
		&bill.ExternalReferenceID,
		&currencyCode,
		&status,
		&bill.BillingPeriodStartAt,
		&bill.BillingPeriodEndAt,
		&bill.LineItemsSubmissionDeadline,
		&bill.TotalAmountMinor,
		&closedAt,
		&failedAt,
		&failureReason,
		&bill.CreatedAt,
		&bill.UpdatedAt,
		&insertStatus,
	); err != nil {
		return nil, "", err
	}

	bill.CurrencyCode = domain.CurrencyCode(currencyCode)
	bill.Status = domain.BillStatus(status)
	if closedAt.Valid {
		t := closedAt.Time
		bill.ClosedAt = &t
	}
	if failedAt.Valid {
		t := failedAt.Time
		bill.FailedAt = &t
	}
	if failureReason.Valid {
		s := failureReason.String
		bill.FailureReason = &s
	}

	return &bill, InsertBillStatus(insertStatus), nil
}

func scanBill(row *sqldb.Row) (*domain.Bill, error) {
	var bill domain.Bill
	var currencyCode string
	var status string
	var closedAt sql.NullTime
	var failedAt sql.NullTime
	var failureReason sql.NullString

	if err := row.Scan(
		&bill.ID,
		&bill.AccountID,
		&bill.ExternalReferenceID,
		&currencyCode,
		&status,
		&bill.BillingPeriodStartAt,
		&bill.BillingPeriodEndAt,
		&bill.LineItemsSubmissionDeadline,
		&bill.TotalAmountMinor,
		&closedAt,
		&failedAt,
		&failureReason,
		&bill.CreatedAt,
		&bill.UpdatedAt,
	); err != nil {
		return nil, err
	}

	bill.CurrencyCode = domain.CurrencyCode(currencyCode)
	bill.Status = domain.BillStatus(status)
	if closedAt.Valid {
		t := closedAt.Time
		bill.ClosedAt = &t
	}
	if failedAt.Valid {
		t := failedAt.Time
		bill.FailedAt = &t
	}
	if failureReason.Valid {
		s := failureReason.String
		bill.FailureReason = &s
	}
	return &bill, nil
}

type existingLineItem struct {
	LineItemID     string
	IdempotencyKey string
}

func buildLineItemBatch(lineItems []LineItemInsert) ([]string, []time.Time, []int64, []string, int64, map[string]string) {
	externalReferenceIDs := make([]string, 0, len(lineItems))
	occurredAts := make([]time.Time, 0, len(lineItems))
	amountMinors := make([]int64, 0, len(lineItems))
	descriptions := make([]string, 0, len(lineItems))
	seen := make(map[string]struct{}, len(lineItems))
	duplicateFailures := make(map[string]string)
	var amountDelta int64

	for _, item := range lineItems {
		if _, exists := seen[item.ExternalReferenceID]; exists {
			duplicateFailures[item.ExternalReferenceID] = lineItemDuplicateInRequestReason
			continue
		}
		seen[item.ExternalReferenceID] = struct{}{}

		externalReferenceIDs = append(externalReferenceIDs, item.ExternalReferenceID)
		occurredAts = append(occurredAts, item.OccurredAt)
		amountMinors = append(amountMinors, item.AmountMinor)
		if item.Description != nil {
			descriptions = append(descriptions, *item.Description)
		} else {
			descriptions = append(descriptions, "")
		}
		amountDelta += item.AmountMinor
	}

	return externalReferenceIDs, occurredAts, amountMinors, descriptions, amountDelta, duplicateFailures
}

func fetchExistingLineItems(ctx context.Context, q queryer, billID string, externalReferenceIDs []string) (map[string]existingLineItem, error) {
	if len(externalReferenceIDs) == 0 {
		return map[string]existingLineItem{}, nil
	}

	rows, err := q.Query(ctx, `
WITH input_rows AS (
	SELECT *
	FROM unnest($2::text[]) AS r(external_reference_id)
)
SELECT
	li.external_reference_id,
	li.line_item_id,
	li.idempotency_key
FROM line_items li
JOIN input_rows ir
  ON ir.external_reference_id = li.external_reference_id
WHERE li.bill_id = $1
`, billID, externalReferenceIDs)
	if err != nil {
		return nil, fmt.Errorf("fetch existing line items: %w", err)
	}
	defer rows.Close()

	existingRows := make(map[string]existingLineItem)
	for rows.Next() {
		var externalReferenceID string
		var row existingLineItem
		if err := rows.Scan(&externalReferenceID, &row.LineItemID, &row.IdempotencyKey); err != nil {
			return nil, fmt.Errorf("scan existing line item: %w", err)
		}
		existingRows[externalReferenceID] = row
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate existing line items: %w", err)
	}
	return existingRows, nil
}

func sameIdempotencyReplay(existingRows map[string]existingLineItem, idempotencyKey string, expectedCount int) (map[string]string, bool) {
	if len(existingRows) != expectedCount {
		return nil, false
	}

	successIDs := make(map[string]string, len(existingRows))
	for externalReferenceID, row := range existingRows {
		if row.IdempotencyKey != idempotencyKey {
			return nil, false
		}
		successIDs[externalReferenceID] = row.LineItemID
	}
	return successIDs, true
}

func bulkInsertLineItems(
	ctx context.Context,
	q queryer,
	params InsertLineItemsParams,
	externalReferenceIDs []string,
	occurredAts []time.Time,
	amountMinors []int64,
	descriptions []string,
) (map[string]string, error) {
	rows, err := q.Query(ctx, `
INSERT INTO line_items (
	bill_id,
	request_id,
	idempotency_key,
	external_reference_id,
	currency_code,
	occurred_at,
	amount_minor,
	description
)
SELECT
	$1,
	$2,
	$3,
	input_rows.external_reference_id,
	$4,
	input_rows.occurred_at,
	input_rows.amount_minor,
	NULLIF(input_rows.description, '')
FROM unnest($5::text[], $6::timestamptz[], $7::bigint[], $8::text[]) AS input_rows(
	external_reference_id,
	occurred_at,
	amount_minor,
	description
)
RETURNING line_item_id, external_reference_id
`, params.BillID, params.RequestID, params.IdempotencyKey, string(params.CurrencyCode), externalReferenceIDs, occurredAts, amountMinors, descriptions)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			switch pgErr.Code {
			case "23505":
				// Deterministic race-condition handling:
				// unique_violation means the line item already exists, so surface
				// this as a business conflict (batch failure), not internal error.
				conflicts := make(map[string]string, len(externalReferenceIDs))
				for _, externalReferenceID := range externalReferenceIDs {
					conflicts[externalReferenceID] = lineItemConflictReason
				}
				return nil, &lineItemsConflictError{RecordReasons: conflicts}
			case "40P01", "40001":
				return nil, &StoreUnavailableError{Op: "bulk insert line items", Cause: err}
			}
		}
		if errors.Is(err, io.EOF) || strings.Contains(strings.ToLower(err.Error()), "proxy") {
			return nil, &StoreUnavailableError{Op: "bulk insert line items", Cause: err}
		}
		return nil, fmt.Errorf("bulk insert line items: %w", err)
	}
	defer rows.Close()

	successIDs := make(map[string]string, len(externalReferenceIDs))
	for rows.Next() {
		var lineItemID string
		var externalReferenceID string
		if err := rows.Scan(&lineItemID, &externalReferenceID); err != nil {
			return nil, fmt.Errorf("scan inserted line item: %w", err)
		}
		successIDs[externalReferenceID] = lineItemID
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate inserted line items: %w", err)
	}
	return successIDs, nil
}

func rollbackUnlessCommitted(tx *sqldb.Tx) {
	_ = tx.Rollback()
}

func asStoreUnavailable(err error, op string) (*StoreUnavailableError, bool) {
	_ = op
	if err == nil {
		return nil, false
	}
	var unavailable *StoreUnavailableError
	if errors.As(err, &unavailable) {
		return unavailable, true
	}
	return nil, false
}

type lineItemsConflictError struct {
	RecordReasons map[string]string
}

func (e *lineItemsConflictError) Error() string {
	return "line items batch rejected"
}
