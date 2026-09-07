package com.nexora.app.core.model

import com.google.gson.annotations.SerializedName

/**
 * DelegationGrant mirrors consent-service domain.DelegationGrant: a
 * time-bound, capability-scoped access grant ("give Avi view access for 7
 * days"). Money movement is never delegable.
 */
data class DelegationGrant(
    @SerializedName("grant_id") val grantId: String = "",
    @SerializedName("delegate_email") val delegateEmail: String = "",
    @SerializedName("label") val label: String = "",
    @SerializedName("scopes") val scopes: List<String> = emptyList(),
    @SerializedName("status") val status: String = "ACTIVE",
    @SerializedName("starts_at") val startsAt: String = "",
    @SerializedName("expires_at") val expiresAt: String = ""
) {
    val isActive: Boolean get() = status == "ACTIVE"
}

/** CreateGrantRequest is the "share access" payload. */
data class CreateGrantRequest(
    @SerializedName("delegate_email") val delegateEmail: String,
    @SerializedName("label") val label: String,
    @SerializedName("scopes") val scopes: List<String>,
    @SerializedName("duration_days") val durationDays: Int
)

/** GrantAuditRecord shows one exercised (or refused) scoped access. */
data class GrantAuditRecord(
    @SerializedName("grant_id") val grantId: String = "",
    @SerializedName("used_at") val usedAt: String = "",
    @SerializedName("action") val action: String = "",
    @SerializedName("resource") val resource: String = "",
    @SerializedName("allowed") val allowed: Boolean = false
)
