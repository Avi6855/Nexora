# ADR-037: Strong Customer Authentication Is a Step-Up State, Not a Flag

**Status:** Accepted
**Date:** 2026-09-25
**Context:** Card issuing, PSD2 / PSD2-SCA, EMV 3-D Secure, card refunds

## Context

Monzo's card platform declines or challenges a presentment before it takes the
customer's money. The regulation behind that behaviour is PSD2's strong customer
authentication (SCA): a card-not-present payment must be authenticated by the
cardholder unless a named exemption applies, and the authentication must be
bound to the amount and payee and be time-limited.

Two shapes of implementation were on the table:

1. **A per-card boolean** (`require_sca`) checked at authorization time, with the
   challenge handled by an external 3-D Secure server.
2. **A first-class authorization state** in which the presentment exists, the
   decision is "challenged", and no money is held until the cardholder answers.

Option 1 is cheaper but hides three properties that matter: whether an exemption
or an authentication approved the payment, whether a code was already spent, and
what happens to a hold when a cardholder never answers.

## Decision

The card platform models SCA as `AuthStatusChallenged`, with the PSD2 rules in
`shared/schemes/sca.go` and the persistence/state machine in card-service.

**1. The exemption policy is a pure function, owned by the scheme package.**
`schemes.SCAPolicy.Evaluate(SCAContext)` returns the exemption a presentment may
rely on — card-present CVM, low value, transaction-risk analysis (scored by the
real fraud engine, not a static amount check), merchant-initiated, trusted
beneficiary, cardholder whitelist — or "none" when the cardholder must
authenticate. It is data in, data out: exhaustively testable, identical on every
decision path, and reusable by any surface that has to make the same call
(open banking, payment initiation).

**2. No funds are held while the cardholder is authenticating.** The challenge is
raised between the risk decision and the ledger reservation, so an unanswered
step-up can never freeze a customer's balance. The hold is taken only once the
correct code is accepted.

**3. The challenge is consumed before the hold is taken.** The one-time code is
stored as a SHA-256 digest, compared in constant time, bounded to three attempts
and five minutes, and every state change is a Cassandra lightweight transaction
guarded on the attempt count it was computed from. A replayed correct answer
therefore cannot take a second hold, and two concurrent wrong guesses cannot both
write "one attempt used". Consuming first means a race or a ledger refusal
declines the payment rather than holding money the customer did not authorise:
fail closed, no orphan hold.

**4. The code is delivered out of band and never stored in the clear.** The
authorization response tells the merchant "challenged" (HTTP 202) and carries no
code. The code travels on the internal `card.authorization.challenged` event to
the notification pipeline, which turns it into a confirm-payment feed item and
SSE push. It is never logged, and `otp_hash` is never serialised.

**5. An unanswered challenge is closed at its deadline, by a sweep that is safe
to run anywhere.** A step-up nobody answers must still reach an end, or the
merchant waits on an authentication that can never arrive. Cassandra cannot
range-read a table by a non-key column, so each challenge is also written into
the hourly bucket its deadline falls in (`card_sca_challenges_by_expiry`, with
`expires_at` as a clustering column and a TTL on the row). The sweep reads the
buckets covering the challenge TTL with a bounded range query, expires the
challenge and declines the presentment. Concurrency needs no coordination: the
challenge transition is guarded by the attempt count, so a cardholder who answers
while the sweep is running wins and the sweep skips them.

**6. A step-up is bound to what the cardholder saw.** The challenge row carries
the card, account, amount and merchant, and answering it with a different
presentment's challenge (or another card's) is refused. The cardholder approves
exactly one amount at one merchant.

**7. The customer approves the money coming back too.** Refunds are the same
discipline in the opposite direction: only *captured* presentments are
refundable, partial refunds total no more than the captured amount under an
optimistic-concurrency claim on the refundable balance, and the ledger of record
books the balanced customer-credit/suspense-debit pair exactly once under the
refund id as its idempotency key (`BookRefund`, `CreateEntryPair`). A credit that
never landed has its claim written back so the customer is not left short by a
row that says they were paid.

## Consequences

```go
// The exemption the approval relied on is recorded on the row.
exemption := scaPolicy.Evaluate(schemes.SCAContext{
    AmountMinor: req.Amount, CardPresent: !req.IsEcommerce(), RiskScore: decision.RiskScore,
})
if exemption == schemes.SCAExemptionNone {
    return s.raiseChallenge(ctx, card, req, authID, now, started, decision) // CHALLENGED, no hold
}

// The step-up is answered once, then the hold is taken.
if err := s.persistChallenge(ctx, challenge, expectedAttempts); err != nil { return nil, err } // single use
reservation, err := s.ledger.Reserve(ctx, auth.AccountID, auth.Amount, auth.Currency, auth.AuthorizationID, "2m")
if err != nil { return s.declineChallenged(ctx, auth, reason) } // fail closed, no hold
```

- Every authorization row now says **how** it was approved: an SCA exemption
  (`sca_exemption`), or a completed challenge (`challenge_id`).
- The card-not-present path gained a state the merchant, the app and the
  notification pipeline all understand, instead of a boolean nobody could audit
  afterwards.
- Failing closed is preserved on both new failure surfaces: a missing code source
  declines with `sca_unavailable`; a step-up the ledger will not fund declines
  with `ledger_unavailable` / `insufficient_funds`.

## Open items (deliberately visible)

- **The low-value exemption omits PSD2's cumulative limits** (five low-value
  presentments or €100 since the last strong authentication). Those need a
  per-account counter row; the policy already accepts the counters as inputs, so
  wiring them is a data change rather than a code change.
- **The expiry sweep is per-instance and leaves the challenge row behind.**
  Every instance sweeps the same buckets each minute; the single-use transition
  makes that correct but wasteful. The challenge row itself is never deleted
  (only its expiry-index entry is), so a long-lived deployment accumulates settled
  rows until a retention job exists.
- **The trusted-beneficiary and cardholder-whitelist registers are not modelled.**
  The policy honours them; no register feeds them yet.
- **The merchant-initiated claim is verified against the merchant's own captured
  history**, which stops self-exemption but is not as strong as linking the
  follow-on to the initial authenticated transaction id.

## Alternatives considered

- **Per-card `require_sca` boolean.** Rejected: it cannot express exemptions, it
  loses the audit trail of how a payment was approved, and it puts the decision
  out of reach of the challenge lifecycle.
- **Challenge with the hold already taken.** Rejected: it freezes customer money
  for an authentication the customer may never complete, and it needs a release
  path for every timeout — a hold the customer never authorised.
- **Store the one-time code so the app can reveal it.** Rejected: a readable code
  is a second credential in the datastore. The code is delivered to the
  cardholder's channel (push/SMS) and only its digest is kept.
- **Refund through `BookTransfer` (clearing account as the source).** Rejected in
  favour of a dedicated `BookRefund` intent: the ledger of record owns both legs
  and the idempotency key, so the caller never has to know which internal account
  the money returns from.
