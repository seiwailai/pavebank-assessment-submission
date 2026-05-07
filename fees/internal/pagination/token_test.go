package pagination

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPageTokenRoundTrip(t *testing.T) {
	t.Parallel()

	token, err := Encode(&LineItemPaginationToken{
		OccurredAt: time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC),
		LineItemID: "11111111-1111-4111-8111-111111111111",
	})
	require.NoError(t, err)

	var got LineItemPaginationToken
	err = Decode(token, &got)
	require.NoError(t, err)

	assert.Equal(t, "11111111-1111-4111-8111-111111111111", got.LineItemID)
	assert.True(t, got.OccurredAt.Equal(time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)))
}

func TestDecodeRejectsNilTarget(t *testing.T) {
	t.Parallel()

	err := Decode("abc", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "target")
}

func TestLineItemPaginationTokenValidateRejectsNonUUIDLineItemID(t *testing.T) {
	t.Parallel()

	err := (&LineItemPaginationToken{
		OccurredAt: time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC),
		LineItemID: "not-a-uuid",
	}).Validate()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "uuid")
}

func TestLineItemPaginationTokenValidateRejectsNonV4UUIDLineItemID(t *testing.T) {
	t.Parallel()

	err := (&LineItemPaginationToken{
		OccurredAt: time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC),
		LineItemID: "6ba7b810-9dad-11d1-80b4-00c04fd430c8",
	}).Validate()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "v4")
}
