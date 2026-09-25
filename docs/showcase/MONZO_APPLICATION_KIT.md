# Monzo Application Kit

How to get **this repository** in front of a Monzo engineer or recruiter so that
it actually gets read, and how to defend it in an interview.

Everything in the "paste-ready" sections is written to be sent as-is.

---

## 1. Why cold DMs are not working

Connection requests to 50 recruiters produce ~zero replies, and that is not
personal. A Monzo recruiter receives hundreds of "I'd love to work at Monzo, I'm
a big fan" messages a week; none of them contain a reason to open a repository.
Three things are missing:

1. **A record to point at.** Recruiters work from the ATS. Until you have an
   application for a specific requisition, there is nothing for them to look up
   — so their only possible answer to your DM is "please apply online".
2. **A specific, verifiable hook.** "I built a banking platform in Go" is
   unverifiable noise. "I implemented the transactional outbox and the LWT
   idempotency claim, and here is the test that proves a double-tap cannot
   create two payments" is a claim that takes 30 seconds to check.
3. **A reason to be remembered.** Being a familiar name (commenting on their
   public engineering content, contributing to their open source) is what turns
   a cold DM into a warm one.

**Order that works:** apply → then message referencing the requisition → then
create public artifacts → then contribute.

---

## 2. Profile setup (do this before you message anyone)

- **Headline:** `Backend engineer — Go, Cassandra, Kafka, Kubernetes | built a
  card-authorization and ledger platform with exactly-once money movement`
- **Featured:** repository link + the 3-minute demo video (see §6). A video is
  what actually gets watched.
- **About:** three sentences — what you build, the stack, one hard problem you
  solved (the unknown-state / fail-closed decision), and the repository link.
- **Pinned post:** §3 Post A below.

Do not describe it as "a Monzo clone". Describe it as the internal machinery a
real-time bank needs. Naming another company's product signals "fan"; naming the
problem signals "engineer".

---

## 3. Posts worth writing (each one is a real engineering story)

### Post A — the architecture story

> Banking is not hard because of the features. It is hard because of what
> happens when a call times out.
>
> I spent the last few weeks building the money layer of a real-time bank in Go:
> 20 services on Cassandra, Kafka, Envoy and Kubernetes, with a native Android
> app on top.
>
> The parts I am actually proud of are invisible:
>
> • A payment's state change and its Kafka event are committed in one Cassandra
>   logged batch, so a payment can never move without its event existing
>   (transactional outbox).
> • A tap-to-pay authorization reserves real funds through a balance-checked
>   hold, and the decision path *fails closed* — if the risk engine or the ledger
>   cannot answer, the customer's card is declined rather than approved blind.
> • If a provider times out, the payment lands in UNKNOWN, never FAILED. Telling
>   someone "the payment failed" when the money might have moved is worse than
>   saying "we are checking".
> • One user action creates exactly one payment even when two requests race with
>   the same idempotency key — the key is claimed with a compare-and-set, and the
>   loser receives the winner's payment instead of creating a second one.
>
> 856 Go tests, no Docker needed to run them, and a list of the platform's own
> honest limits in the docs.
>
> Repo + 3-minute walkthrough in the comments.

### Post B — the bug story (usually the best performing)

> I found a race condition in my own code today, and it is the kind of bug that
> loses real money.
>
> My payment API was idempotent like this: read the payment by idempotency key,
> and if nothing exists, create it. Clean, obvious, and wrong.
>
> Two requests arriving together — a user double-tapping "send" on a flaky
> connection — both read "no payment yet", and both insert. One tap, two
> payments, twice the money out.
>
> The fix is to stop treating a read as a lock: claim the key with
> `INSERT ... IF NOT EXISTS` (Cassandra LWT), so exactly one writer wins, and
> the loser loads the winner's payment and returns it. If the write then fails,
> the claim is released so the client's retry is not blocked by its own claim
> (compensating action).
>
> There is now a test that fires 16 concurrent requests with the same key and
> asserts exactly one payment exists.
>
> Reads are not locks. Ever.

### Post C — the teaching story

> Cards decline in 200 milliseconds. Here is the order I ended up with, and why
> the cheap checks come first:
>
> 1. Is the card active? (local row, no network)
> 2. Is the currency right?
> 3. The customer's own controls — online payments off, ATM off, gambling block
> 4. Risk scoring over stored history
> 5. A balance-checked hold in the ledger
> 6. Persist the decision and emit the event
>
> The customer's own toggles are at step 3, *before* the risk engine, because
> they are the most authoritative and the cheapest signal we have. And every
> failure branch ends in the same place: decline.
>
> Fail closed, or do not ship it.

---

## 4. DM templates

### 4a. Recruiter, after you have submitted an application

> Hi {name} — I applied for {requisition title} ({req id}) today, so this is not
> an ask for a favour, just a pointer in case it helps triage.
>
> I built a banking platform on the stack in the posting (Go, Cassandra, Kafka,
> Envoy, Kubernetes): 20 services, real-time card authorization, transactional
> outbox, exactly-once payment booking and a fail-closed decision path, with the
> tests and the architecture decision records in the repo. {link}
>
> If it is useful I have a 3-minute walkthrough video — happy to send it on. If
> the profile is not a fit for this req, no problem at all, and thank you for
> reading.

Why it works: it names the requisition (so they can find it), gives one
verifiable artifact, and explicitly removes the obligation to reply.

### 4b. Engineer, asking a technical question (best cold-open)

> Hi {name} — you wrote/published {specific thing, e.g. the Stand-in post}.
>
> I have been building a stand-in handover myself (monotone epochs, fencing
> tokens, drain/freeze/capture), and I hit a decision I could not settle from
> the outside: when the primary comes back, do you reconcile by replaying the
> secondary's writes forward, or by diffing state and refusing to auto-merge on
> divergence?
>
> My current answer is diff-and-refuse ({link to the compatibility check}), but I
> suspect it is naive at real volume. Would you be willing to tell me where it
> breaks?

Why it works: it is a real question with your own attempt attached. Engineers
reply to that far more than to "can I have a referral".

### 4c. Referral ask (only after the two above, or if someone engages first)

> Thanks for the reply — that helped, and I changed the design because of it.
>
> One practical question: I have applied for {req}. If you have a referral
> channel you are comfortable using, would you be open to it? Either way, I
> appreciate the time you already spent.

Never send a referral ask as the first message.

---

## 5. Being findable (the part most people skip)

- **Comment substantively, not with praise.** On Monzo's engineering
  publications, X/LinkedIn posts and conference talks, add the "here is how I
  handled that" detail. Familiar names get replies.
- **Contribute to their open source.** Monzo's GitHub organisation
  (`github.com/monzo`) publishes 176 repositories of Go infrastructure used to
  build the platform — for example `typhon` (their RPC layer) and `phosphor`
  (distributed tracing). A merged PR there is the single highest-signal move
  available to you: your name appears in their notifications with working code.
  Look for stale issues, missing tests and doc gaps in the Go tools rather than
  attempting a large refactor.
- **Answer where they are.** Monzo's community forum and their public postmortem
  threads are full of specific questions; being the person who answers well is
  free credibility.

---

## 6. The 3-minute demo video (script)

Nobody will start 20 services on a laptop. Record it once, put it in the
Featured section, and link it in every message.

| Time | Show | Say |
|---|---|---|
| 0:00–0:20 | The Android app on screen, one card tap | "This is a card tap. Everything after this happens in the next 300 ms." |
| 0:20–0:45 | The architecture diagram (README) | "20 Go services, Cassandra as the system of record, Kafka for async, Envoy at the edge." |
| 0:45–1:20 | `docker-compose up -d`, then the authorize call from the runbook | "Merchant presentment in, decision out: risk scored against stored history, funds reserved in the ledger." |
| 1:20–1:50 | `cqlsh` reading `card_authorizations` and `ledger_entries` | "Every number is derived from double-entry rows that really exist. Nothing is mocked." |
| 1:50–2:20 | Stop the ledger container, present again | "Now the ledger is down. Fail closed: the card is declined, because approving without a hold is how banks lose money." |
| 2:20–2:45 | `go test ./...` in payment-service and card-service | "856 Go tests, and none of them need Docker — including the one that fires 16 concurrent requests with the same idempotency key." |
| 2:45–3:00 | The honest-limits section of the docs | "Here is what it does not do yet: no CDC-driven relay, in-process SSE hub, no load test committed. That is the next work." |

Two rules: show failures and limits on camera (it reads as senior), and never
claim production traffic.

---

## 7. Interview defence map

| Likely question | Answer with | The trap they are setting |
|---|---|---|
| How do you avoid a dual write between the DB and Kafka? | `shared/outbox`, `persistCreate`/`persistTransition`, ADR-005 | Claiming "exactly-once delivery". Be honest: at-least-once delivery + idempotent consumer + an LWT claim row. |
| How do you make money movement idempotent? | `ClaimIdempotencyKey`, `ledger_payment_marks`, `roundup_processed` | Saying "the read prevents duplicates". Explain the TOCTOU race and the compare-and-set. |
| What happens when a provider times out? | `markUnknown`, ADR-007 | Saying FAILED. Unknown is a first-class state; the money may have moved. |
| The risk engine is down at 3am. What does the customer see? | `decline("risk_service_unavailable")`, `TestRiskEngineUnavailableFailsClosed` | Saying "we retry". Availability is not worth unfunded approvals. |
| Why Cassandra, and what is wrong with your model? | `cassandra/init/schema.cql`, ADR-001, and the honest-limits section | Defending `ALLOW FILTERING`. Volunteer it as dev-scale and describe the lookup-table fix. |
| How would this scale to 5k TPS? | Latency budgets (`shared/latency`), adaptive concurrency (`shared/concurrency`), backpressure (`shared/backpressure`), per-account single-writer scoping | Pretending you have numbers. Say what is measured and what is not. |
| How do you stop a stolen card being used online, without declining your customer's real payments? | `shared/schemes/sca.go` (exemption policy), `raiseChallenge`/`CompleteSCAChallenge`, ADR-037 | Saying "we set a require-3DS flag". Explain that the exempted flows (card present, low value, low-risk MCC, merchant-initiated) are the reason a real card is usable, and that no money is held while the customer is authenticating. |
| A merchant refunds a customer. How do you guarantee they are paid exactly once, and never more than they paid? | `RefundAuthorization` (claim on the refundable balance), `BookRefund` + `CreateEntryPair` (balanced pair, idempotent on the refund id), the rollback of a claim whose credit never landed | Saying "the refund reverses the original entry". The ledger is append-only: a refund is a *new* balanced pair, and the ceiling is enforced by optimistic concurrency, not by hoping the retries are serial. |
| Tell me about a time you were wrong. | Post B story: the bug you found in your own code. | Perfection. The self-found race is the story. |
| Why Monzo? | The Stand-in activation post, real-time card authorisation, the open-source Go infrastructure | Generic admiration. Be specific about their engineering, not their brand. |

---

## 8. Anti-patterns

- Do not send the repository as a zip or a wall of screenshots.
- Do not say "production-grade" or "enterprise-ready" about a system with no
  traffic. Say what it proves and what it does not.
- Do not mass-message the same text to every employee: two well-chosen people
  beat fifty identical DMs.
- Do not apply to both requisitions with different CVs; apply for the L40 role
  and mention L50 interest in the first call.
- Do not hide the AI-assisted parts of the build. Being able to explain every
  design decision is what matters, and it is the only thing you will be tested
  on.
