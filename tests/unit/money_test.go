package unit

import (
	"math"
	"testing"

	"github.com/nexora/nexora/shared/money"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewMoney(t *testing.T) {
	tests := []struct {
		name     string
		amount   int64
		currency string
		wantErr  bool
	}{
		{"valid GBP", 1000, "GBP", false},
		{"valid USD", 5050, "USD", false},
		{"valid EUR", 100, "EUR", false},
		{"valid JPY", 1000, "JPY", false},
		{"valid AUD", 100, "AUD", false},
		{"valid CAD", 100, "CAD", false},
		{"valid CHF", 100, "CHF", false},
		{"valid CNY", 100, "CNY", false},
		{"valid INR", 100, "INR", false},
		{"valid BRL", 100, "BRL", false},
		{"valid MXN", 100, "MXN", false},
		{"valid KRW", 100, "KRW", false},
		{"invalid currency XYZ", 100, "XYZ", true},
		{"empty currency", 100, "", true},
		{"invalid currency AAA", 100, "AAA", true},
		{"zero amount valid", 0, "GBP", false},
		{"negative amount valid", -500, "GBP", false},
		{"max int64 amount", math.MaxInt64, "GBP", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, err := money.NewMoney(tt.amount, tt.currency)
			if tt.wantErr {
				assert.Error(t, err)
				assert.ErrorIs(t, err, money.ErrInvalidCurrency)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.amount, m.Amount)
				assert.Equal(t, tt.currency, m.Currency)
			}
		})
	}
}

func TestMustNewMoney(t *testing.T) {
	m := money.MustNewMoney(1000, "GBP")
	assert.Equal(t, int64(1000), m.Amount)
	assert.Equal(t, "GBP", m.Currency)

	assert.Panics(t, func() {
		money.MustNewMoney(100, "INVALID")
	})
}

func TestMoneyAdd(t *testing.T) {
	tests := []struct {
		name    string
		a       int64
		b       int64
		currency string
		want    int64
		wantErr bool
	}{
		{"positive add", 1000, 500, "GBP", 1500, false},
		{"zero add", 1000, 0, "GBP", 1000, false},
		{"negative add", 1000, -300, "GBP", 700, false},
		{"both negative", -500, -300, "GBP", -800, false},
		{"result zero", 500, -500, "GBP", 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := money.MustNewMoney(tt.a, tt.currency)
			b := money.MustNewMoney(tt.b, tt.currency)
			result, err := a.Add(b)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.want, result.Amount)
				assert.Equal(t, tt.currency, result.Currency)
			}
		})
	}

	a := money.MustNewMoney(1000, "GBP")
	b := money.MustNewMoney(100, "USD")
	_, err := a.Add(b)
	assert.ErrorIs(t, err, money.ErrDifferentCurrency)
}

func TestMoneyAddOverflow(t *testing.T) {
	a := money.MustNewMoney(math.MaxInt64, "GBP")
	b := money.MustNewMoney(1, "GBP")
	_, err := a.Add(b)
	assert.ErrorIs(t, err, money.ErrOverflow)

	negA := money.MustNewMoney(math.MinInt64, "GBP")
	negB := money.MustNewMoney(-1, "GBP")
	_, err = negA.Add(negB)
	assert.ErrorIs(t, err, money.ErrOverflow)
}

func TestMoneySubtract(t *testing.T) {
	a := money.MustNewMoney(1000, "GBP")
	b := money.MustNewMoney(300, "GBP")
	result, err := a.Subtract(b)
	require.NoError(t, err)
	assert.Equal(t, int64(700), result.Amount)

	c := money.MustNewMoney(100, "USD")
	_, err = a.Subtract(c)
	assert.ErrorIs(t, err, money.ErrDifferentCurrency)
}

func TestMoneyNegate(t *testing.T) {
	a := money.MustNewMoney(500, "GBP")
	neg := a.Negate()
	assert.Equal(t, int64(-500), neg.Amount)
	assert.Equal(t, "GBP", neg.Currency)

	pos := neg.Negate()
	assert.Equal(t, int64(500), pos.Amount)
}

func TestMoneyAbs(t *testing.T) {
	a := money.MustNewMoney(-500, "GBP")
	assert.Equal(t, int64(500), a.Abs().Amount)

	b := money.MustNewMoney(500, "GBP")
	assert.Equal(t, int64(500), b.Abs().Amount)

	c := money.Zero("GBP")
	assert.Equal(t, int64(0), c.Abs().Amount)
}

func TestMoneyMultiply(t *testing.T) {
	tests := []struct {
		name   string
		amount int64
		factor int64
		want   int64
	}{
		{"positive factor", 100, 5, 500},
		{"negative factor", 100, -2, -200},
		{"zero factor", 100, 0, 0},
		{"one factor", 100, 1, 100},
		{"negative amount positive factor", -100, 5, -500},
		{"negative amount negative factor", -100, -5, 500},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := money.MustNewMoney(tt.amount, "GBP")
			result, err := a.Multiply(tt.factor)
			require.NoError(t, err)
			assert.Equal(t, tt.want, result.Amount)
		})
	}
}

func TestMoneyMultiplyOverflow(t *testing.T) {
	a := money.MustNewMoney(math.MaxInt64, "GBP")
	_, err := a.Multiply(2)
	assert.ErrorIs(t, err, money.ErrOverflow)

	b := money.MustNewMoney(math.MinInt64, "GBP")
	_, err = b.Multiply(2)
	assert.ErrorIs(t, err, money.ErrOverflow)
}

func TestMoneySplit(t *testing.T) {
	tests := []struct {
		name    string
		amount  int64
		n       int
		wantMin int64
		wantMax int64
	}{
		{"split 100 by 3", 100, 3, 33, 34},
		{"split 100 by 2", 100, 2, 50, 50},
		{"split 100 by 5", 100, 5, 20, 20},
		{"split 1 by 1", 1, 1, 1, 1},
		{"split 10 by 3", 10, 3, 3, 4},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := money.MustNewMoney(tt.amount, "GBP")
			shares, err := a.Split(tt.n)
			require.NoError(t, err)
			require.Len(t, shares, tt.n)

			total := int64(0)
			for _, s := range shares {
				assert.Equal(t, "GBP", s.Currency)
				assert.GreaterOrEqual(t, s.Amount, tt.wantMin)
				assert.LessOrEqual(t, s.Amount, tt.wantMax)
				total += s.Amount
			}
			assert.Equal(t, tt.amount, total)
		})
	}
}

func TestMoneySplitInvalid(t *testing.T) {
	a := money.MustNewMoney(100, "GBP")

	_, err := a.Split(0)
	assert.Error(t, err)

	_, err = a.Split(-1)
	assert.Error(t, err)
}

func TestMoneyString(t *testing.T) {
	tests := []struct {
		name   string
		amount int64
		currency string
		want   string
	}{
		{"GBP positive", 1050, "GBP", "10.50 GBP"},
		{"GBP zero", 0, "GBP", "0.00 GBP"},
		{"USD", 1050, "USD", "10.50 USD"},
		{"EUR", 1050, "EUR", "10.50 EUR"},
		{"JPY no decimals", 1000, "JPY", "1000 JPY"},
		{"KRW no decimals", 5000, "KRW", "5000 KRW"},
		{"negative GBP", -1050, "GBP", "-10.50 GBP"},
		{"GBP whole", 1000, "GBP", "10.00 GBP"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := money.MustNewMoney(tt.amount, tt.currency)
			assert.Equal(t, tt.want, m.String())
		})
	}
}

func TestMoneyIsZero(t *testing.T) {
	assert.True(t, money.Zero("GBP").IsZero())
	assert.True(t, money.MustNewMoney(0, "GBP").IsZero())
	assert.False(t, money.MustNewMoney(100, "GBP").IsZero())
	assert.False(t, money.MustNewMoney(-100, "GBP").IsZero())
}

func TestMoneyIsPositive(t *testing.T) {
	assert.True(t, money.MustNewMoney(100, "GBP").IsPositive())
	assert.False(t, money.MustNewMoney(-100, "GBP").IsPositive())
	assert.False(t, money.Zero("GBP").IsPositive())
}

func TestMoneyIsNegative(t *testing.T) {
	assert.True(t, money.MustNewMoney(-100, "GBP").IsNegative())
	assert.False(t, money.MustNewMoney(100, "GBP").IsNegative())
	assert.False(t, money.Zero("GBP").IsNegative())
}

func TestMoneyCompare(t *testing.T) {
	a := money.MustNewMoney(1000, "GBP")
	b := money.MustNewMoney(500, "GBP")
	c := money.MustNewMoney(1000, "GBP")

	cmp, err := a.Compare(b)
	require.NoError(t, err)
	assert.Equal(t, 1, cmp)

	cmp, err = b.Compare(a)
	require.NoError(t, err)
	assert.Equal(t, -1, cmp)

	cmp, err = a.Compare(c)
	require.NoError(t, err)
	assert.Equal(t, 0, cmp)

	_, err = a.Compare(money.MustNewMoney(100, "USD"))
	assert.ErrorIs(t, err, money.ErrDifferentCurrency)
}

func TestMoneyIsGreaterThanOrEqual(t *testing.T) {
	a := money.MustNewMoney(1000, "GBP")
	b := money.MustNewMoney(500, "GBP")
	c := money.MustNewMoney(1000, "GBP")

	gte, err := a.IsGreaterThanOrEqual(b)
	require.NoError(t, err)
	assert.True(t, gte)

	gte, err = a.IsGreaterThanOrEqual(c)
	require.NoError(t, err)
	assert.True(t, gte)

	gte, err = b.IsGreaterThanOrEqual(a)
	require.NoError(t, err)
	assert.False(t, gte)

	_, err = a.IsGreaterThanOrEqual(money.MustNewMoney(100, "USD"))
	assert.ErrorIs(t, err, money.ErrDifferentCurrency)
}

func TestMoneyIsLessThan(t *testing.T) {
	a := money.MustNewMoney(1000, "GBP")
	b := money.MustNewMoney(500, "GBP")
	c := money.MustNewMoney(1000, "GBP")

	lt, err := b.IsLessThan(a)
	require.NoError(t, err)
	assert.True(t, lt)

	lt, err = a.IsLessThan(b)
	require.NoError(t, err)
	assert.False(t, lt)

	lt, err = a.IsLessThan(c)
	require.NoError(t, err)
	assert.False(t, lt)

	_, err = a.IsLessThan(money.MustNewMoney(100, "USD"))
	assert.ErrorIs(t, err, money.ErrDifferentCurrency)
}

func TestMoneyMinorUnits(t *testing.T) {
	gbp := money.MustNewMoney(1050, "GBP")
	assert.Equal(t, int64(1050), gbp.MinorUnits())

	jpy := money.MustNewMoney(1000, "JPY")
	assert.Equal(t, int64(1000), jpy.MinorUnits())
}

func TestMoneyMajorUnits(t *testing.T) {
	gbp := money.MustNewMoney(1050, "GBP")
	assert.InDelta(t, 10.50, gbp.MajorUnits(), 0.001)

	jpy := money.MustNewMoney(1000, "JPY")
	assert.InDelta(t, 1000.0, jpy.MajorUnits(), 0.001)

	usd := money.MustNewMoney(100, "USD")
	assert.InDelta(t, 1.0, usd.MajorUnits(), 0.001)
}

func TestMoneyMarshalJSON(t *testing.T) {
	m := money.MustNewMoney(1050, "GBP")
	jsonBytes, err := m.MarshalJSON()
	require.NoError(t, err)
	assert.Contains(t, string(jsonBytes), `"amount":1050`)
	assert.Contains(t, string(jsonBytes), `"currency":"GBP"`)
}

func TestMoneyValidate(t *testing.T) {
	m := money.MustNewMoney(100, "GBP")
	assert.NoError(t, m.Validate())

	invalid := money.Money{Amount: 100, Currency: "XYZ"}
	assert.Error(t, invalid.Validate())

	empty := money.Money{Amount: 100, Currency: ""}
	assert.Error(t, empty.Validate())
}

func TestZero(t *testing.T) {
	z := money.Zero("GBP")
	assert.Equal(t, int64(0), z.Amount)
	assert.Equal(t, "GBP", z.Currency)

	zUSD := money.Zero("USD")
	assert.Equal(t, int64(0), zUSD.Amount)
	assert.Equal(t, "USD", zUSD.Currency)
}

func TestMoneyCurrencyMinorUnits(t *testing.T) {
	currencies := map[string]int{
		"GBP": 2, "USD": 2, "EUR": 2, "JPY": 0,
		"AUD": 2, "CAD": 2, "CHF": 2, "CNY": 2,
		"INR": 2, "BRL": 2, "MXN": 2, "KRW": 0,
	}

	for curr, expectedDecimals := range currencies {
		t.Run(curr, func(t *testing.T) {
			m := money.MustNewMoney(100, curr)
			assert.NoError(t, m.Validate())

			if expectedDecimals == 0 {
				assert.Equal(t, "100 "+curr, m.String())
			} else {
				assert.Equal(t, "1.00 "+curr, m.String())
			}
		})
	}
}
