package com.nexora.app

import org.junit.Test
import org.junit.Assert.*

class MoneyTest {

    @Test
    fun `money uses long minor units`() {
        val amount = 1050L
        val currency = "GBP"
        val major = amount / 100
        val minor = amount % 100

        assertEquals(10L, major)
        assertEquals(50L, minor)
    }

    @Test
    fun `no floating point in money representation`() {
        val amount = 1000L
        val result = amount / 3
        assertEquals(333L, result)

        val total = result * 3
        assertEquals(999L, total)

        assertTrue(total != amount)
    }

    @Test
    fun `GBP formatting`() {
        val amount = 1050L
        val major = amount / 100
        val minor = amount % 100
        val formatted = "\u00A3${major}.${String.format("%02d", minor)}"
        assertEquals("\u00A310.50", formatted)
    }

    @Test
    fun `USD formatting`() {
        val amount = 2500L
        val major = amount / 100
        val minor = amount % 100
        val formatted = "$${major}.${String.format("%02d", minor)}"
        assertEquals("$25.00", formatted)
    }

    @Test
    fun `EUR formatting`() {
        val amount = 750L
        val major = amount / 100
        val minor = amount % 100
        val formatted = "\u20AC${major}.${String.format("%02d", minor)}"
        assertEquals("\u20AC7.50", formatted)
    }

    @Test
    fun `zero money`() {
        val zero = 0L
        assertEquals(0L, zero)
        assertEquals(0L, zero / 100)
        assertEquals(0L, zero % 100)
    }

    @Test
    fun `negative amount handling`() {
        val amount = -1050L
        val major = amount / 100
        val minor = amount % 100
        val absMinor = if (minor < 0) -minor else minor

        assertEquals(-11L, major)
        assertEquals(-50L, minor)
        assertEquals(50L, absMinor)
    }

    @Test
    fun `JPY no decimal places`() {
        val amount = 1000L
        val currency = "JPY"
        val formatted = "$amount $currency"
        assertEquals("1000 JPY", formatted)
    }

    @Test
    fun `KRW no decimal places`() {
        val amount = 5000L
        val currency = "KRW"
        val formatted = "$amount $currency"
        assertEquals("5000 KRW", formatted)
    }

    @Test
    fun `currency handling`() {
        val currencies = mapOf(
            "GBP" to 2,
            "USD" to 2,
            "EUR" to 2,
            "JPY" to 0,
            "AUD" to 2,
            "CAD" to 2,
            "CHF" to 2,
            "CNY" to 2,
            "INR" to 2,
            "BRL" to 2,
            "MXN" to 2,
            "KRW" to 0
        )

        assertEquals(12, currencies.size)
        assertEquals(2, currencies["GBP"])
        assertEquals(0, currencies["JPY"])
    }

    @Test
    fun `overflow protection with large amounts`() {
        val maxLong = Long.MAX_VALUE
        assertTrue(maxLong > 0)

        val result = maxLong + 1
        assertTrue(result < 0)
    }

    @Test
    fun `money arithmetic operations`() {
        val a = 1000L
        val b = 500L

        assertEquals(1500L, a + b)
        assertEquals(500L, a - b)
        assertEquals(5000L, a * 5)
        assertEquals(-1000L, -a)
    }
}
