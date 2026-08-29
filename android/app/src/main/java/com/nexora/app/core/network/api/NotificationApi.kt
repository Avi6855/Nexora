package com.nexora.app.core.network.api

import com.nexora.app.core.model.ApiResponse
import com.nexora.app.core.model.Notification
import retrofit2.http.GET
import retrofit2.http.PUT
import retrofit2.http.Path

interface NotificationApi {
    @GET("v1/notifications")
    suspend fun getNotifications(): ApiResponse<List<Notification>>

    @PUT("v1/notifications/{notificationId}/read")
    suspend fun markAsRead(@Path("notificationId") notificationId: String): ApiResponse<Notification>

    @PUT("v1/notifications/read-all")
    suspend fun markAllAsRead(): ApiResponse<Unit>
}
