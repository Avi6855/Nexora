package money

import (
	"errors"
	"fmt"
	"math"
)

var (
	ErrInvalidCurrency   = errors.New("invalid currency code")
	ErrDifferentCurrency = errors.New("cannot operate on different currencies")
	ErrOverflow          = errors.New("arithmetic overflow")
)

var validCurrencies = map[string]bool{
	"GBP": true, "USD": true, "EUR": true, "JPY": true,
	"AUD": true, "CAD": true, "CHF": true, "CNY": true,
	"INR": true, "BRL": true, "MXN": true, "KRW": true,
}

var currencyMinorUnits = map[string]int{
	"GBP": 2, "USD": 2, "EUR": 2, "JPY": 0,
	"AUD": 2, "CAD": 2, "CHF": 2, "CNY": 2,
	"INR": 2, "BRL": 2, "MXN": 2, "KRW": 0,
}

type Money struct {
	Amount   int64
	Currency string
}

func NewMoney(amount int64, currency string) (Money, error) {
	if !validCurrencies[currency] {
		return Money{}, fmt.Errorf("%w: %s", ErrInvalidCurrency, currency)
	}
	return Money{Amount: amount, Currency: currency}, nil
}

func MustNewMoney(amount int64, currency string) Money {
	m, err := NewMoney(amount, currency)
	if err != nil {
		panic(err)
	}
	return m
}

func Zero(currency string) Money {
	return Money{Amount: 0, Currency: currency}
}

func (m Money) Validate() error {
	if !validCurrencies[m.Currency] {
		return fmt.Errorf("%w: %s", ErrInvalidCurrency, m.Currency)
	}
	return nil
}

func (m Money) IsZero() bool {
	return m.Amount == 0
}

func (m Money) IsPositive() bool {
	return m.Amount > 0
}

func (m Money) IsNegative() bool {
	return m.Amount < 0
}

func (m Money) IsGreaterThanOrEqual(other Money) (bool, error) {
	if m.Currency != other.Currency {
		return false, ErrDifferentCurrency
	}
	return m.Amount >= other.Amount, nil
}

func (m Money) IsLessThan(other Money) (bool, error) {
	if m.Currency != other.Currency {
		return false, ErrDifferentCurrency
	}
	return m.Amount < other.Amount, nil
}

func (m Money) Compare(other Money) (int, error) {
	if m.Currency != other.Currency {
		return 0, ErrDifferentCurrency
	}
	if m.Amount > other.Amount {
		return 1, nil
	}
	if m.Amount < other.Amount {
		return -1, nil
	}
	return 0, nil
}

func (m Money) Add(other Money) (Money, error) {
	if m.Currency != other.Currency {
		return Money{}, ErrDifferentCurrency
	}
	if m.Amount > 0 && other.Amount > math.MaxInt64-m.Amount {
		return Money{}, ErrOverflow
	}
	if m.Amount < 0 && other.Amount < math.MinInt64-m.Amount {
		return Money{}, ErrOverflow
	}
	return Money{Amount: m.Amount + other.Amount, Currency: m.Currency}, nil
}

func (m Money) Subtract(other Money) (Money, error) {
	if m.Currency != other.Currency {
		return Money{}, ErrDifferentCurrency
	}
	return m.Add(Money{Amount: -other.Amount, Currency: other.Currency})
}

func (m Money) Negate() Money {
	return Money{Amount: -m.Amount, Currency: m.Currency}
}

func (m Money) Abs() Money {
	if m.Amount < 0 {
		return m.Negate()
	}
	return m
}

func (m Money) Multiply(factor int64) (Money, error) {
	if m.Amount > 0 && factor > 0 && m.Amount > math.MaxInt64/factor {
		return Money{}, ErrOverflow
	}
	if m.Amount > 0 && factor < 0 && factor < math.MinInt64/m.Amount {
		return Money{}, ErrOverflow
	}
	if m.Amount < 0 && factor > 0 && m.Amount < math.MinInt64/factor {
		return Money{}, ErrOverflow
	}
	if m.Amount < 0 && factor < 0 && -m.Amount > math.MaxInt64/(-factor) {
		return Money{}, ErrOverflow
	}
	return Money{Amount: m.Amount * factor, Currency: m.Currency}, nil
}

func (m Money) Split(n int) ([]Money, error) {
	if n <= 0 {
		return nil, fmt.Errorf("split count must be positive, got %d", n)
	}
	share := m.Amount / int64(n)
	remainder := m.Amount % int64(n)
	shares := make([]Money, n)
	for i := 0; i < n; i++ {
		amount := share
		if int64(i) < remainder {
			amount++
		}
		shares[i] = Money{Amount: amount, Currency: m.Currency}
	}
	return shares, nil
}

func (m Money) MinorUnits() int64 {
	return m.Amount
}

func (m Money) MajorUnits() float64 {
	decimals := currencyMinorUnits[m.Currency]
	divisor := math.Pow(10, float64(decimals))
	return float64(m.Amount) / divisor
}

func (m Money) String() string {
	decimals := currencyMinorUnits[m.Currency]
	divisor := int64(math.Pow(10, float64(decimals)))
	whole := m.Amount / divisor
	fraction := m.Amount % divisor
	if fraction < 0 {
		fraction = -fraction
	}
	if decimals > 0 {
		return fmt.Sprintf("%d.%0*d %s", whole, decimals, fraction, m.Currency)
	}
	return fmt.Sprintf("%d %s", whole, m.Currency)
}

func (m Money) MarshalJSON() ([]byte, error) {
	return []byte(fmt.Sprintf(`{"amount":%d,"currency":"%s"}`, m.Amount, m.Currency)), nil
}
