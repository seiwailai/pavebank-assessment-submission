package domain

type CurrencyCode string

const (
	currencyUSD CurrencyCode = "USD"
	currencyGEL CurrencyCode = "GEL"
)

func USD() CurrencyCode {
	return currencyUSD
}

func GEL() CurrencyCode {
	return currencyGEL
}

func IsSupportedCurrency(code CurrencyCode) bool {
	switch code {
	case currencyUSD, currencyGEL:
		return true
	default:
		return false
	}
}
