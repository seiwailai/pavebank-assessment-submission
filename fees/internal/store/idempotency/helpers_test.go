package idempotency

import (
	"database/sql"
	"testing"
	"time"
)

func TestSameFingerprint(t *testing.T) {
	t.Parallel()

	if !sameFingerprint(sql.NullString{}, "") {
		t.Fatal("empty requested fingerprint should match NULL")
	}
	if sameFingerprint(sql.NullString{Valid: true, String: "fp-a"}, "") {
		t.Fatal("empty requested fingerprint should not match non-empty existing fingerprint")
	}
	if !sameFingerprint(sql.NullString{Valid: true, String: "fp-a"}, "fp-a") {
		t.Fatal("matching fingerprints should match")
	}
	if sameFingerprint(sql.NullString{Valid: true, String: "fp-a"}, "fp-b") {
		t.Fatal("different fingerprints should not match")
	}
}

func TestNullableString(t *testing.T) {
	t.Parallel()

	if nullableString("") != nil {
		t.Fatal("empty string should become nil")
	}
	if got := nullableString("value"); got != "value" {
		t.Fatalf("nullableString() = %#v", got)
	}
}

func TestCompactJSON(t *testing.T) {
	t.Parallel()

	compacted, err := compactJSON([]byte("{\n  \"a\": 1\n}"))
	if err != nil {
		t.Fatalf("compactJSON() error = %v", err)
	}
	if string(compacted) != "{\"a\":1}" {
		t.Fatalf("compactJSON() = %s", compacted)
	}

	unchanged, err := compactJSON(nil)
	if err != nil {
		t.Fatalf("compactJSON(nil) error = %v", err)
	}
	if len(unchanged) != 0 {
		t.Fatalf("compactJSON(nil) = %#v", unchanged)
	}

	if _, err := compactJSON([]byte("{")); err == nil {
		t.Fatal("expected compactJSON to reject invalid JSON")
	}
}

func TestToRecord(t *testing.T) {
	t.Parallel()

	createdAt := time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)
	expiresAt := createdAt.Add(24 * time.Hour)
	inProgressExpiry := createdAt.Add(2 * time.Minute)

	record := toRecord(&recordRow{
		Key:                "idem-1",
		Scope:              "POST:/v1/bills",
		RequestFingerprint: sql.NullString{Valid: true, String: "fp-1"},
		ProcessingStatus:   string(StatusCompleted),
		InProgressExpiry:   sql.NullTime{Valid: true, Time: inProgressExpiry},
		ResponseHTTPStatus: sql.NullInt64{Valid: true, Int64: 202},
		ResponseBody:       []byte("{\n  \"bill_id\": \"bill-1\"\n}"),
		CreatedAt:          sql.NullTime{Valid: true, Time: createdAt},
		ExpiresAt:          sql.NullTime{Valid: true, Time: expiresAt},
	})

	if record == nil {
		t.Fatal("expected record")
	}
	if record.Scope != Scope("POST:/v1/bills") || record.Key != "idem-1" || record.Fingerprint != "fp-1" {
		t.Fatalf("record = %+v", record)
	}
	if record.Status != StatusCompleted {
		t.Fatalf("record.Status = %q", record.Status)
	}
	if record.InProgressExpiry == nil || !record.InProgressExpiry.Equal(inProgressExpiry) {
		t.Fatalf("record.InProgressExpiry = %#v", record.InProgressExpiry)
	}
	if record.Response == nil || record.Response.HTTPStatus != 202 || string(record.Response.Body) != "{\"bill_id\":\"bill-1\"}" {
		t.Fatalf("record.Response = %#v", record.Response)
	}

	if toRecord(nil) != nil {
		t.Fatal("toRecord(nil) should be nil")
	}
}
