package billing

import "testing"

func TestInsertBillResultContractCompiles(t *testing.T) {
	t.Parallel()

	var _ InsertBillResult
	var _ Store
}
