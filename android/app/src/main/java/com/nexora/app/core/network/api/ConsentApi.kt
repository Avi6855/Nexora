package com.nexora.app.core.network.api

import com.nexora.app.core.model.CreateGrantRequest
import com.nexora.app.core.model.DelegationGrant
import com.nexora.app.core.model.GrantAuditRecord
import retrofit2.http.Body
import retrofit2.http.GET
import retrofit2.http.POST
import retrofit2.http.Path
import retrofit2.http.Query

/**
 * ConsentApi talks to the Delegated Access + Consent Centre (consent-service,
 * routed through Envoy at /v1/consent).
 */
interface ConsentApi {

    @POST("v1/consent/grants")
    suspend fun createGrant(@Body request: CreateGrantRequest): DelegationGrant

    @GET("v1/consent/grants")
    suspend fun listGrants(@Query("limit") limit: Int = 50): List<DelegationGrant>

    @POST("v1/consent/grants/{id}/revoke")
    suspend fun revokeGrant(@Path("id") grantId: String)

    @GET("v1/consent/grants/{id}/audit")
    suspend fun listAudit(
        @Path("id") grantId: String,
        @Query("limit") limit: Int = 50
    ): List<GrantAuditRecord>
}
