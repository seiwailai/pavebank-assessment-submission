package idempotency

import "testing"

func TestReserveResultContractCompiles(t *testing.T) {
	t.Parallel()

	var _ ReserveResult
	var _ Store
}
