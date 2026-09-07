package com.nexora.app.core.model

import com.google.gson.annotations.SerializedName

/**
 * DisputeCase mirrors dispute-service domain.DisputeCase: a long-running
 * chargeback case moving through the workflow state machine
 * (SUBMITTED → ELIGIBILITY → AWAITING_EVIDENCE → MERCHANT_RESPONSE →
 * UNDER_REVIEW → RESOLVED).
 */
data class DisputeCase(
    @SerializedName("case_id") val caseId: String = "",
    @SerializedName("account_id") val accountId: String = "",
    @SerializedName("entry_id") val entryId: String = "",
    @SerializedName("transaction_id") val transactionId: String = "",
    @SerializedName("amount") val amount: Long = 0,
    @SerializedName("currency") val currency: String = "GBP",
    @SerializedName("merchant") val merchant: String = "",
    @SerializedName("category") val category: String = "",
    @SerializedName("reason") val reason: String = "",
    @SerializedName("description") val description: String = "",
    @SerializedName("status") val status: String = "OPEN",
    @SerializedName("stage") val stage: String = "SUBMITTED",
    @SerializedName("eligibility") val eligibility: DisputeEligibility = DisputeEligibility(),
    @SerializedName("resolution") val resolution: String? = null,
    @SerializedName("provisional_credit") val provisionalCredit: Boolean = false,
    @SerializedName("deadline") val deadline: String? = null,
    @SerializedName("created_at") val createdAt: String = ""
) {
    val isOpen: Boolean get() = status == "OPEN"
}

/** DisputeEligibility is the scheme-rule verdict computed at case creation. */
data class DisputeEligibility(
    @SerializedName("eligible") val eligible: Boolean = false,
    @SerializedName("reason") val reason: String = "",
    @SerializedName("evidence_deadline") val evidenceDeadline: String = "",
    @SerializedName("merchant_deadline") val merchantDeadline: String = "",
    @SerializedName("provisional_credit") val provisionalCredit: Boolean = false
)

/** CaseEvent is one entry in the case's audit timeline. */
data class CaseEvent(
    @SerializedName("event_type") val eventType: String = "",
    @SerializedName("actor") val actor: String = "",
    @SerializedName("note") val note: String = "",
    @SerializedName("created_at") val createdAt: String = ""
)

/** Dispute create request payload. */
data class CreateDisputeRequest(
    @SerializedName("entry_id") val entryId: String,
    @SerializedName("reason") val reason: String,
    @SerializedName("description") val description: String
)

/** Evidence upload payload (small docs at demo scale). */
data class AddEvidenceRequest(
    @SerializedName("evidence_type") val evidenceType: String,
    @SerializedName("filename") val filename: String,
    @SerializedName("content") val content: String
)

/** Human-readable reason labels for the report-a-problem picker. */
val disputeReasonOptions: List<Pair<String, String>> = listOf(
    "FRAUD" to "I didn't authorise this payment",
    "NOT_RECEIVED" to "I never received what I paid for",
    "NOT_AS_DESCRIBED" to "It wasn't as described",
    "DUPLICATE" to "I was charged twice",
    "INCORRECT_AMOUNT" to "I was charged the wrong amount",
    "CANCELLED_RECURRING" to "I cancelled but was still charged"
)
