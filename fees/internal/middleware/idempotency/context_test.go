package idempotency

import (
	"context"
	"testing"
)

func TestMetadataContextRoundTrip(t *testing.T) {
	t.Parallel()

	ctx := withMetadata(context.Background(), Metadata{
		Scope:       "POST:/v1/bills",
		Key:         "idem-1",
		Fingerprint: "fp-1",
	})

	got, ok := MetadataFromContext(ctx)
	if !ok {
		t.Fatal("expected metadata in context")
	}
	if got.Scope != "POST:/v1/bills" || got.Key != "idem-1" || got.Fingerprint != "fp-1" {
		t.Fatalf("MetadataFromContext() = %+v", got)
	}
}

func TestMetadataFromContextMissing(t *testing.T) {
	t.Parallel()

	if _, ok := MetadataFromContext(context.Background()); ok {
		t.Fatal("expected no metadata")
	}
}
