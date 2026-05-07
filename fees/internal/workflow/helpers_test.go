package workflow

import (
	"testing"

	"encore.app/fees/internal/domain"
)

func TestWorkflowValidationErrors(t *testing.T) {
	t.Parallel()

	if err := invalidBillIdentityErr("received", "expected"); err == nil || err.Error() == "" {
		t.Fatal("expected invalidBillIdentityErr to describe mismatch")
	}
	if err := invalidCurrencyErr(domain.GEL(), domain.USD()); err == nil || err.Error() == "" {
		t.Fatal("expected invalidCurrencyErr to describe mismatch")
	}
}
