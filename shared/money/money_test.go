package money

import (
	"testing"

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
		{"valid JPY", 1000, "JPY", false},
		{"invalid currency", 100, "XYZ", true},
		{"empty currency", 100, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, err := NewMoney(tt.amount, tt.currency)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.amount, m.Amount)
				assert.Equal(t, tt.currency, m.Currency)
			}
		})
	}
}

func TestZero(t *testing.T) {
	z := Zero("GBP")
	assert.Equal(t, int64(0), z.Amount)
	assert.Equal(t, "GBP", z.Currency)
}

func TestMoneyAdd(t *testing.T) {
	a := MustNewMoney(1000, "GBP")
	b := MustNewMoney(500, "GBP")
	result, err := a.Add(b)
	require.NoError(t, err)
	assert.Equal(t, int64(1500), result.Amount)

	_, err = a.Add(MustNewMoney(100, "USD"))
	assert.ErrorIs(t, err, ErrDifferentCurrency)
}

func TestMoneySubtract(t *testing.T) {
	a := MustNewMoney(1000, "GBP")
	b := MustNewMoney(300, "GBP")
	result, err := a.Subtract(b)
	require.NoError(t, err)
	assert.Equal(t, int64(700), result.Amount)
}

func TestMoneyNegate(t *testing.T) {
	a := MustNewMoney(500, "GBP")
	neg := a.Negate()
	assert.Equal(t, int64(-500), neg.Amount)

	pos := neg.Negate()
	assert.Equal(t, int64(500), pos.Amount)
}

func TestMoneyAbs(t *testing.T) {
	a := MustNewMoney(-500, "GBP")
	assert.Equal(t, int64(500), a.Abs().Amount)

	b := MustNewMoney(500, "GBP")
	assert.Equal(t, int64(500), b.Abs().Amount)
}

func TestMoneyCompare(t *testing.T) {
	a := MustNewMoney(1000, "GBP")
	b := MustNewMoney(500, "GBP")
	c := MustNewMoney(1000, "GBP")

	cmp, err := a.Compare(b)
	require.NoError(t, err)
	assert.Equal(t, 1, cmp)

	cmp, err = b.Compare(a)
	require.NoError(t, err)
	assert.Equal(t, -1, cmp)

	cmp, err = a.Compare(c)
	require.NoError(t, err)
	assert.Equal(t, 0, cmp)

	_, err = a.Compare(MustNewMoney(100, "USD"))
	assert.ErrorIs(t, err, ErrDifferentCurrency)
}

func TestMoneyCompareMethods(t *testing.T) {
	a := MustNewMoney(1000, "GBP")
	b := MustNewMoney(500, "GBP")

	gte, err := a.IsGreaterThanOrEqual(b)
	require.NoError(t, err)
	assert.True(t, gte)

	lt, err := b.IsLessThan(a)
	require.NoError(t, err)
	assert.True(t, lt)
}

func TestMoneyMultiply(t *testing.T) {
	a := MustNewMoney(100, "GBP")
	result, err := a.Multiply(5)
	require.NoError(t, err)
	assert.Equal(t, int64(500), result.Amount)

	result, err = a.Multiply(-2)
	require.NoError(t, err)
	assert.Equal(t, int64(-200), result.Amount)
}

func TestMoneySplit(t *testing.T) {
	a := MustNewMoney(100, "GBP")
	shares, err := a.Split(3)
	require.NoError(t, err)
	require.Len(t, shares, 3)

	total := int64(0)
	for _, s := range shares {
		assert.Equal(t, "GBP", s.Currency)
		total += s.Amount
	}
	assert.Equal(t, int64(100), total)
}

func TestMoneySplitInvalid(t *testing.T) {
	a := MustNewMoney(100, "GBP")
	_, err := a.Split(0)
	assert.Error(t, err)
}

func TestMoneyString(t *testing.T) {
	gbp := MustNewMoney(1050, "GBP")
	assert.Equal(t, "10.50 GBP", gbp.String())

	jpy := MustNewMoney(1000, "JPY")
	assert.Equal(t, "1000 JPY", jpy.String())

	zero := Zero("USD")
	assert.Equal(t, "0.00 USD", zero.String())
}

func TestMoneyIsZero(t *testing.T) {
	assert.True(t, Zero("GBP").IsZero())
	assert.False(t, MustNewMoney(100, "GBP").IsZero())
}

func TestMoneyIsPositive(t *testing.T) {
	assert.True(t, MustNewMoney(100, "GBP").IsPositive())
	assert.False(t, MustNewMoney(-100, "GBP").IsPositive())
	assert.False(t, Zero("GBP").IsPositive())
}

func TestMoneyIsNegative(t *testing.T) {
	assert.True(t, MustNewMoney(-100, "GBP").IsNegative())
	assert.False(t, MustNewMoney(100, "GBP").IsNegative())
	assert.False(t, Zero("GBP").IsNegative())
}

func TestMoneyMinorUnits(t *testing.T) {
	gbp := MustNewMoney(1050, "GBP")
	assert.Equal(t, int64(1050), gbp.MinorUnits())

	jpy := MustNewMoney(1000, "JPY")
	assert.Equal(t, int64(1000), jpy.MinorUnits())
}

func TestMoneyMajorUnits(t *testing.T) {
	gbp := MustNewMoney(1050, "GBP")
	assert.InDelta(t, 10.50, gbp.MajorUnits(), 0.001)

	jpy := MustNewMoney(1000, "JPY")
	assert.InDelta(t, 1000.0, jpy.MajorUnits(), 0.001)
}

func TestMoneyMarshalJSON(t *testing.T) {
	m := MustNewMoney(1050, "GBP")
	json, err := m.MarshalJSON()
	require.NoError(t, err)
	assert.Contains(t, string(json), `"amount":1050`)
	assert.Contains(t, string(json), `"currency":"GBP"`)
}

func TestMoneyValidate(t *testing.T) {
	m := MustNewMoney(100, "GBP")
	assert.NoError(t, m.Validate())

	invalid := Money{Amount: 100, Currency: "XYZ"}
	assert.Error(t, invalid.Validate())
}
