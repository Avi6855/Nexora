package com.nexora.app.core.network.api

import com.nexora.app.core.model.AddEvidenceRequest
import com.nexora.app.core.model.CaseEvent
import com.nexora.app.core.model.CreateDisputeRequest
import com.nexora.app.core.model.DisputeCase
import retrofit2.http.Body
import retrofit2.http.GET
import retrofit2.http.POST
import retrofit2.http.Path
import retrofit2.http.Query

/**
 * DisputeApi talks to the Dispute Orchestration Platform (dispute-service,
 * routed through Envoy at /v1/disputes).
 */
interface DisputeApi {

    @POST("v1/disputes")
    suspend fun createDispute(@Body request: CreateDisputeRequest): DisputeCase

    @GET("v1/disputes")
    suspend fun listDisputes(@Query("limit") limit: Int = 50): List<DisputeCase>

    @GET("v1/disputes/{id}")
    suspend fun getDispute(@Path("id") caseId: String): DisputeCase

    @POST("v1/disputes/{id}/evidence")
    suspend fun addEvidence(
        @Path("id") caseId: String,
        @Body request: AddEvidenceRequest
    ): DisputeCase

    @GET("v1/disputes/{id}/events")
    suspend fun listEvents(@Path("id") caseId: String): List<CaseEvent>
}
