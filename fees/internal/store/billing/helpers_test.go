package billing

import (
	"testing"
	"time"
)

func TestBuildLineItemBatchTracksDeltaAndDuplicates(t *testing.T) {
	t.Parallel()

	description := "hello"
	externalReferenceIDs, occurredAts, amountMinors, descriptions, delta, failures := buildLineItemBatch([]LineItemInsert{
		{ExternalReferenceID: "line-1", OccurredAt: time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC), AmountMinor: 10, Description: &description},
		{ExternalReferenceID: "line-2", OccurredAt: time.Date(2026, 5, 2, 0, 0, 0, 0, time.UTC), AmountMinor: 20},
		{ExternalReferenceID: "line-1", OccurredAt: time.Date(2026, 5, 3, 0, 0, 0, 0, time.UTC), AmountMinor: 30},
	})

	if len(externalReferenceIDs) != 2 || externalReferenceIDs[0] != "line-1" || externalReferenceIDs[1] != "line-2" {
		t.Fatalf("externalReferenceIDs = %#v", externalReferenceIDs)
	}
	if len(occurredAts) != 2 || !occurredAts[0].Equal(time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("occurredAts = %#v", occurredAts)
	}
	if len(amountMinors) != 2 || amountMinors[0] != 10 || amountMinors[1] != 20 {
		t.Fatalf("amountMinors = %#v", amountMinors)
	}
	if len(descriptions) != 2 || descriptions[0] != "hello" || descriptions[1] != "" {
		t.Fatalf("descriptions = %#v", descriptions)
	}
	if delta != 30 {
		t.Fatalf("delta = %d, want 30", delta)
	}
	if failures["line-1"] != lineItemDuplicateInRequestReason {
		t.Fatalf("failures = %#v", failures)
	}
}

func TestSameIdempotencyReplay(t *testing.T) {
	t.Parallel()

	successIDs, ok := sameIdempotencyReplay(map[string]existingLineItem{
		"line-1": {LineItemID: "id-1", IdempotencyKey: "idem-1"},
		"line-2": {LineItemID: "id-2", IdempotencyKey: "idem-1"},
	}, "idem-1", 2)
	if !ok {
		t.Fatal("expected replay to match")
	}
	if successIDs["line-1"] != "id-1" || successIDs["line-2"] != "id-2" {
		t.Fatalf("successIDs = %#v", successIDs)
	}

	if _, ok := sameIdempotencyReplay(map[string]existingLineItem{
		"line-1": {LineItemID: "id-1", IdempotencyKey: "idem-1"},
	}, "idem-1", 2); ok {
		t.Fatal("expected replay mismatch when counts differ")
	}

	if _, ok := sameIdempotencyReplay(map[string]existingLineItem{
		"line-1": {LineItemID: "id-1", IdempotencyKey: "idem-2"},
	}, "idem-1", 1); ok {
		t.Fatal("expected replay mismatch when idempotency key differs")
	}
}
