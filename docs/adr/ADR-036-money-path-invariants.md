# ADR-036: Money-Path Invariants Are Enforced by the Ledger, Not Assumed

## Status

Accepted

## Context

ADR-003 states that every financial transaction creates balanced debit and
credit entries, and ADR-021 states that a card capture settles the hold it was
authorised against. Auditing the money-out paths against those statements found
that the code did not actually enforce them:

1. **The payment booking had the legs the wrong way round.** The ledger's
   payment-event consumer credited the *paying* account, and `CREDIT` raises a
   balance — so settling an outbound payment paid the payer. Nothing else moved
   money on that path, so every settled payment did it.
2. **Reservation settlement wrote a single leg.** `SettleReservation` inserted
   only the customer debit. Total debits therefore exceeded total credits for
   the whole ledger — the invariant the financial checker asserts — and the
   counterparty side of every card capture was simply absent.
3. **The payment path had no availability check.** `CreateDoubleEntryTransaction`
   checks nothing (unlike `BookTransfer`), so a payment could drive an account
   negative. Combined with (4) this was reachable in normal operation.
4. **Nothing held the funds.** `Payment.ReservationID` was never set, so a
   payment in flight left the balance fully spendable: the same money could be
   committed to a card spend and to the payment, and only the settlement would
   reveal it.
5. **There was no counter-party account.** Two call sites hard-coded the same
   suspense UUID, which is why the missing counter-leg was easy not to notice.

## Decision

The ledger of record is the last line of defence for these invariants, and it
enforces them itself rather than trusting upstream services:

- **Every customer money movement is a balanced pair.** A settlement debits the
  customer and credits `domain.ClearingAccountID`; the payment booking does the
  same with the payer as the debit. Debits equal credits by construction, and the
  clearing account is a named constant instead of a copied literal.
- **Both legs are written in one logged batch** (`CreateEntryPair`). Writing them
  one at a time can leave a half-settlement behind on a crash, and a settlement
  is not retryable once the reservation is marked SETTLED.
- **The ledger refuses to overdraw.** A payment booking checks
  `GetAvailableBalance` and returns `ErrInsufficientFunds` rather than writing a
  negative balance. Upstream checks may race; this one cannot be raced away.
- **Claims are compensated.** The exactly-once booking mark is taken before the
  entries are written, so a failed booking releases it and a redelivery can
  retry. A claim with no booking would make the payment unbookable forever.
- **A refusal is recorded, not just logged.** A refused or unbookable payment
  files a `BOOKING_REFUSED` integrity event and a SEV1 incident against the
  account. Note deliberately: it does **not** freeze the account — a refusal is
  expected behaviour, not corruption.
- **Payment authorisation holds funds** (payment-service, when a ledger is
  configured). The hold is taken *before* the state change, an unreachable ledger
  fails closed, and the hold is released exactly once on FAILED, CANCELLED and
  SETTLED — but **not** on UNKNOWN, where the money may still have moved. Holds
  carry a TTL so an orphan cannot strand funds permanently.
- **Hold release is its own event.** `payment.reservation.released`, not
  `payment.reversed`: reversal means money returning to the customer, and a
  consumer acting on that would credit them a second time.

## Alternatives Considered

### Trust the upstream services

Leave the ledger a dumb writer and rely on payment/card services to be correct.
Rejected: it is exactly this assumption that let a sign error ship, and it puts
the availability guarantee behind an HTTP hop that can fail open or race.

### Fix the payment booking by reversing it in a second transaction

Post a compensating transaction instead of correcting the direction. Rejected:
the books would balance while the customer's history still showed the payment as
an incoming credit, and history is what disputes and the app read.

### Settle the hold and skip the event-driven booking for payments

Have `SettleReservation` produce the debit and mark the payment as booked, so the
`payment.settled` event never books it twice. This removes the residual window
described below, but it requires the ledger to identify payment-owned
reservations, so it is deliberately left as the next step rather than half-done.

### Freeze the account on a refused booking

Rejected: it would take a working account offline for an ordinary
insufficient-funds outcome, and it would make the freeze signal mean two
different things.

## Consequences

### Positive

- The books-balance invariant holds across the whole ledger, not just per account.
- Money cannot leave a customer account without a counter-leg, and cannot be
  committed twice while a payment is in flight.
- Refusals are auditable: `/v1/ledger/integrity/events` shows what was refused
  and why, instead of the error scrolling away in a consumer log.
- The regression tests are named for the invariants
  (`payment_booking_test.go`, `settlement_balance_test.go`, `hold_test.go`).

### Negative / Still Open

- On the success path the hold is released once the settlement event is durable,
  but the ledger books the real debit *from* that event. In that window a
  concurrent spend is refused loudly rather than silently overdrawn, yet the
  payment stays SETTLED with its debit unbooked. The alternative above closes it.
- A refused booking is visible but not retried; a DLQ with an explicit replay
  path is still needed (the claim compensation already makes replay safe).
- The clearing account is a single suspense account. Production splits it into
  scheme/acquirer/payable accounts with per-counterparty reconcilement.

## Implementation Notes

```go
// Every customer movement carries a counter-leg.
debit  := &domain.LedgerEntry{AccountID: accountID, EntryType: domain.EntryTypeDebit, ...}
credit := &domain.LedgerEntry{AccountID: domain.ClearingAccountID, EntryType: domain.EntryTypeCredit, ...}
if err := repo.CreateEntryPair(ctx, debit, credit); err != nil { ... } // one logged batch

// Availability is the ledger's own check, alongside BookTransfer's.
available, err := ledgerService.GetAvailableBalance(ctx, accountID)
if available < req.Amount { return domain.ErrInsufficientFunds }
```
