package domain

import "testing"

func TestCurrencyHelpers(t *testing.T) {
	t.Parallel()

	if USD() != CurrencyCode("USD") {
		t.Fatalf("USD() = %q, want %q", USD(), "USD")
	}
	if GEL() != CurrencyCode("GEL") {
		t.Fatalf("GEL() = %q, want %q", GEL(), "GEL")
	}
	if !IsSupportedCurrency(USD()) {
		t.Fatal("USD should be supported")
	}
	if !IsSupportedCurrency(GEL()) {
		t.Fatal("GEL should be supported")
	}
	if IsSupportedCurrency(CurrencyCode("EUR")) {
		t.Fatal("EUR should not be supported")
	}
	if IsSupportedCurrency("") {
		t.Fatal("empty currency should not be supported")
	}
}
