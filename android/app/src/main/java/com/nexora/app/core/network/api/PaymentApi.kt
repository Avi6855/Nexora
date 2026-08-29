package com.nexora.app.core.network.api

import com.nexora.app.core.model.ApiResponse
import com.nexora.app.core.model.Payment
import kotlinx.serialization.Serializable
import kotlinx.serialization.SerialName
import retrofit2.http.Body
import retrofit2.http.GET
import retrofit2.http.POST
import retrofit2.http.PUT
import retrofit2.http.Path

@Serializable
data class CreatePaymentRequest(
    @SerialName("account_id")
    val accountId: String,
    val amount: Long,
    val currency: String = "GBP",
    @SerialName("recipient_name")
    val recipientName: String,
    @SerialName("recipient_account_number")
    val recipientAccountNumber: String,
    @SerialName("recipient_sort_code")
    val recipientSortCode: String,
    val reference: String = "",
    val description: String = ""
)

interface PaymentApi {
    @GET("v1/payments")
    suspend fun getPayments(): ApiResponse<List<Payment>>

    @GET("v1/payments/{paymentId}")
    suspend fun getPayment(@Path("paymentId") paymentId: String): ApiResponse<Payment>

    @POST("v1/payments")
    suspend fun createPayment(@Body request: CreatePaymentRequest): ApiResponse<Payment>

    @PUT("v1/payments/{paymentId}/cancel")
    suspend fun cancelPayment(@Path("paymentId") paymentId: String): ApiResponse<Payment>
}
