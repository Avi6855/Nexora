package com.nexora.app

import org.junit.Test
import org.junit.Assert.*

class PaymentStateTest {

    private enum class PaymentState {
        CREATED,
        AUTHORIZED,
        PROCESSING,
        UNKNOWN,
        CONFIRMED,
        SETTLED,
        FAILED,
        REVERSED,
        CANCELLED
    }

    private val validTransitions = mapOf(
        PaymentState.CREATED to setOf(PaymentState.AUTHORIZED, PaymentState.FAILED, PaymentState.REVERSED, PaymentState.CANCELLED),
        PaymentState.AUTHORIZED to setOf(PaymentState.PROCESSING, PaymentState.FAILED, PaymentState.REVERSED, PaymentState.CANCELLED),
        PaymentState.PROCESSING to setOf(PaymentState.CONFIRMED, PaymentState.FAILED, PaymentState.UNKNOWN),
        PaymentState.UNKNOWN to setOf(PaymentState.CONFIRMED, PaymentState.FAILED),
        PaymentState.CONFIRMED to setOf(PaymentState.SETTLED, PaymentState.REVERSED),
        PaymentState.SETTLED to emptySet(),
        PaymentState.FAILED to emptySet(),
        PaymentState.REVERSED to emptySet(),
        PaymentState.CANCELLED to emptySet()
    )

    private fun canTransitionTo(from: PaymentState, to: PaymentState): Boolean {
        return validTransitions[from]?.contains(to) ?: false
    }

    @Test
    fun `valid transitions from CREATED`() {
        assertTrue(canTransitionTo(PaymentState.CREATED, PaymentState.AUTHORIZED))
        assertTrue(canTransitionTo(PaymentState.CREATED, PaymentState.FAILED))
        assertTrue(canTransitionTo(PaymentState.CREATED, PaymentState.REVERSED))
        assertTrue(canTransitionTo(PaymentState.CREATED, PaymentState.CANCELLED))
    }

    @Test
    fun `invalid transition from CREATED to PROCESSING`() {
        assertFalse(canTransitionTo(PaymentState.CREATED, PaymentState.PROCESSING))
    }

    @Test
    fun `valid transitions from AUTHORIZED`() {
        assertTrue(canTransitionTo(PaymentState.AUTHORIZED, PaymentState.PROCESSING))
        assertTrue(canTransitionTo(PaymentState.AUTHORIZED, PaymentState.FAILED))
        assertTrue(canTransitionTo(PaymentState.AUTHORIZED, PaymentState.REVERSED))
        assertTrue(canTransitionTo(PaymentState.AUTHORIZED, PaymentState.CANCELLED))
    }

    @Test
    fun `valid transitions from PROCESSING`() {
        assertTrue(canTransitionTo(PaymentState.PROCESSING, PaymentState.CONFIRMED))
        assertTrue(canTransitionTo(PaymentState.PROCESSING, PaymentState.FAILED))
        assertTrue(canTransitionTo(PaymentState.PROCESSING, PaymentState.UNKNOWN))
    }

    @Test
    fun `valid transitions from UNKNOWN`() {
        assertTrue(canTransitionTo(PaymentState.UNKNOWN, PaymentState.CONFIRMED))
        assertTrue(canTransitionTo(PaymentState.UNKNOWN, PaymentState.FAILED))
    }

    @Test
    fun `valid transitions from CONFIRMED`() {
        assertTrue(canTransitionTo(PaymentState.CONFIRMED, PaymentState.SETTLED))
        assertTrue(canTransitionTo(PaymentState.CONFIRMED, PaymentState.REVERSED))
    }

    @Test
    fun `no transitions from SETTLED`() {
        for (state in PaymentState.entries) {
            assertFalse(canTransitionTo(PaymentState.SETTLED, state))
        }
    }

    @Test
    fun `no transitions from FAILED`() {
        for (state in PaymentState.entries) {
            assertFalse(canTransitionTo(PaymentState.FAILED, state))
        }
    }

    @Test
    fun `no transitions from REVERSED`() {
        for (state in PaymentState.entries) {
            assertFalse(canTransitionTo(PaymentState.REVERSED, state))
        }
    }

    @Test
    fun `no transitions from CANCELLED`() {
        for (state in PaymentState.entries) {
            assertFalse(canTransitionTo(PaymentState.CANCELLED, state))
        }
    }

    @Test
    fun `state cannot transition to itself`() {
        for (state in PaymentState.entries) {
            assertFalse("State $state should not transition to itself", canTransitionTo(state, state))
        }
    }

    @Test
    fun `all states are defined`() {
        assertEquals(9, PaymentState.entries.size)
    }

    @Test
    fun `payment state enum values match expected`() {
        assertEquals("CREATED", PaymentState.CREATED.name)
        assertEquals("AUTHORIZED", PaymentState.AUTHORIZED.name)
        assertEquals("PROCESSING", PaymentState.PROCESSING.name)
        assertEquals("UNKNOWN", PaymentState.UNKNOWN.name)
        assertEquals("CONFIRMED", PaymentState.CONFIRMED.name)
        assertEquals("SETTLED", PaymentState.SETTLED.name)
        assertEquals("FAILED", PaymentState.FAILED.name)
        assertEquals("REVERSED", PaymentState.REVERSED.name)
        assertEquals("CANCELLED", PaymentState.CANCELLED.name)
    }
}
