package idempotency

import "context"

type Metadata struct {
	Scope       string
	Key         string
	Fingerprint string
}

type contextKey struct{}

func withMetadata(ctx context.Context, metadata Metadata) context.Context {
	return context.WithValue(ctx, contextKey{}, metadata)
}

func MetadataFromContext(ctx context.Context) (Metadata, bool) {
	metadata, ok := ctx.Value(contextKey{}).(Metadata)
	return metadata, ok
}
