package idempotency

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"encore.dev/storage/sqldb"
)

type PostgresStore struct {
	db *sqldb.Database
}

type queryer interface {
	Exec(ctx context.Context, query string, args ...any) (sqldb.ExecResult, error)
	QueryRow(ctx context.Context, query string, args ...any) *sqldb.Row
}

type recordRow struct {
	Key                string
	Scope              string
	RequestFingerprint sql.NullString
	ProcessingStatus   string
	InProgressExpiry   sql.NullTime
	ResponseHTTPStatus sql.NullInt64
	ResponseBody       []byte
	CreatedAt          sql.NullTime
	ExpiresAt          sql.NullTime
}

func NewPostgresStore(db *sqldb.Database) *PostgresStore {
	return &PostgresStore{db: db}
}

func (s *PostgresStore) Reserve(ctx context.Context, params ReserveParams) (*ReserveResult, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin reserve idempotency tx: %w", err)
	}
	defer rollbackUnlessCommitted(tx)

	inserted, err := insertRecord(ctx, tx, params)
	if err != nil {
		return nil, err
	}
	if inserted {
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("commit idempotency reserve insert: %w", err)
		}
		return &ReserveResult{Outcome: ReserveAcquired}, nil
	}

	record, err := getRecord(ctx, tx, params.Scope, params.Key)
	switch {
	case errors.Is(err, sqldb.ErrNoRows):
		return nil, fmt.Errorf("reserve idempotency record: record disappeared for key %q", params.Key)
	case err != nil:
		return nil, err
	}

	if !sameFingerprint(record.RequestFingerprint, params.Fingerprint) {
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("commit idempotency conflict read: %w", err)
		}
		return &ReserveResult{Outcome: ReserveConflict, Record: toRecord(record)}, nil
	}

	switch ProcessingStatus(record.ProcessingStatus) {
	case StatusCompleted:
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("commit idempotency replay read: %w", err)
		}
		return &ReserveResult{Outcome: ReserveReplay, Record: toRecord(record)}, nil
	case StatusInProgress:
		if record.InProgressExpiry.Valid && record.InProgressExpiry.Time.After(params.Now) {
			if err := tx.Commit(); err != nil {
				return nil, fmt.Errorf("commit idempotency in-progress read: %w", err)
			}
			return &ReserveResult{Outcome: ReserveInProgress, Record: toRecord(record)}, nil
		}
	}

	if err := refreshReservation(ctx, tx, params); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit idempotency reserve refresh: %w", err)
	}
	return &ReserveResult{Outcome: ReserveAcquired}, nil
}

func (s *PostgresStore) Complete(ctx context.Context, params CompleteParams) error {
	body, err := compactJSON(params.Response.Body)
	if err != nil {
		return fmt.Errorf("complete idempotency record: %w", err)
	}

	result, err := s.db.Exec(ctx, `
UPDATE idempotency_records
SET
	request_fingerprint = $3,
	processing_status = $4,
	in_progress_expires_at = NULL,
	response_http_status = $5,
	response_body_json = $6,
	expires_at = GREATEST(expires_at, now())
WHERE idempotency_key = $1
  AND operation_type = $2
  AND request_fingerprint IS NOT DISTINCT FROM $3
  AND processing_status = $7
`, params.Key, string(params.Scope), nullableString(params.Fingerprint), string(StatusCompleted), params.Response.HTTPStatus, body, string(StatusInProgress))
	if err != nil {
		return fmt.Errorf("complete idempotency record: %w", err)
	}
	if result.RowsAffected() == 0 {
		record, lookupErr := getRecord(ctx, s.db, params.Scope, params.Key)
		if lookupErr != nil {
			if errors.Is(lookupErr, sqldb.ErrNoRows) {
				return fmt.Errorf("complete idempotency record: stale or missing in-progress reservation for key %q", params.Key)
			}
			return fmt.Errorf("complete idempotency record: %w", lookupErr)
		}

		if ProcessingStatus(record.ProcessingStatus) == StatusCompleted &&
			sameFingerprint(record.RequestFingerprint, params.Fingerprint) &&
			sameStoredResponse(record, params.Response.HTTPStatus, body) {
			return nil
		}
		return fmt.Errorf("complete idempotency record: stale or missing in-progress reservation for key %q", params.Key)
	}
	return nil
}

func sameStoredResponse(record *recordRow, expectedStatus int, expectedBody []byte) bool {
	if record == nil || !record.ResponseHTTPStatus.Valid {
		return false
	}
	if int(record.ResponseHTTPStatus.Int64) != expectedStatus {
		return false
	}

	actualBody, err := compactJSON(record.ResponseBody)
	if err != nil {
		actualBody = record.ResponseBody
	}
	return slices.Equal(actualBody, expectedBody)
}

func getRecord(ctx context.Context, q queryer, scope Scope, key string) (*recordRow, error) {
	row := q.QueryRow(ctx, `
SELECT
	idempotency_key,
	operation_type,
	request_fingerprint,
	processing_status,
	in_progress_expires_at,
	response_http_status,
	response_body_json,
	created_at,
	expires_at
FROM idempotency_records
WHERE idempotency_key = $1
  AND operation_type = $2
FOR UPDATE
`, key, string(scope))

	var record recordRow
	if err := row.Scan(
		&record.Key,
		&record.Scope,
		&record.RequestFingerprint,
		&record.ProcessingStatus,
		&record.InProgressExpiry,
		&record.ResponseHTTPStatus,
		&record.ResponseBody,
		&record.CreatedAt,
		&record.ExpiresAt,
	); err != nil {
		return nil, err
	}
	return &record, nil
}

func insertRecord(ctx context.Context, q queryer, params ReserveParams) (bool, error) {
	result, err := q.Exec(ctx, `
INSERT INTO idempotency_records (
	idempotency_key,
	operation_type,
	request_fingerprint,
	processing_status,
	in_progress_expires_at,
	expires_at
) VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (idempotency_key, operation_type) DO NOTHING
`, params.Key, string(params.Scope), nullableString(params.Fingerprint), string(StatusInProgress), params.InProgressExpiry, params.RecordExpiry)
	if err != nil {
		return false, fmt.Errorf("insert idempotency record: %w", err)
	}
	return result.RowsAffected() == 1, nil
}

func refreshReservation(ctx context.Context, q queryer, params ReserveParams) error {
	if _, err := q.Exec(ctx, `
UPDATE idempotency_records
SET
	request_fingerprint = $3,
	processing_status = $4,
	in_progress_expires_at = $5,
	response_http_status = NULL,
	response_body_json = NULL,
	expires_at = $6
WHERE idempotency_key = $1
  AND operation_type = $2
`, params.Key, string(params.Scope), nullableString(params.Fingerprint), string(StatusInProgress), params.InProgressExpiry, params.RecordExpiry); err != nil {
		return fmt.Errorf("refresh idempotency reservation: %w", err)
	}
	return nil
}

func toRecord(row *recordRow) *Record {
	if row == nil {
		return nil
	}

	record := &Record{
		Scope:       Scope(row.Scope),
		Key:         row.Key,
		Fingerprint: row.RequestFingerprint.String,
		Status:      ProcessingStatus(row.ProcessingStatus),
	}
	if row.CreatedAt.Valid {
		record.CreatedAt = row.CreatedAt.Time
	}
	if row.ExpiresAt.Valid {
		record.ExpiresAt = row.ExpiresAt.Time
	}
	if row.InProgressExpiry.Valid {
		expiry := row.InProgressExpiry.Time
		record.InProgressExpiry = &expiry
	}
	if row.ResponseHTTPStatus.Valid {
		body, err := compactJSON(row.ResponseBody)
		if err != nil {
			body = row.ResponseBody
		}
		record.Response = &StoredResponse{
			HTTPStatus: int(row.ResponseHTTPStatus.Int64),
			Body:       body,
		}
	}
	return record
}

func sameFingerprint(existing sql.NullString, requested string) bool {
	if requested == "" {
		return !existing.Valid || existing.String == ""
	}
	return existing.Valid && existing.String == requested
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func compactJSON(input []byte) ([]byte, error) {
	if len(input) == 0 {
		return input, nil
	}

	var out bytes.Buffer
	if err := json.Compact(&out, input); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func rollbackUnlessCommitted(tx *sqldb.Tx) {
	_ = tx.Rollback()
}
