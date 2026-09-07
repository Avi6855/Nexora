package com.nexora.app.core.network.api

import com.nexora.app.core.model.DependencyGraph
import retrofit2.http.GET

/**
 * ReliabilityApi reads the Service Dependency Health Graph from the
 * control-plane (routed through Envoy at /v1/control).
 */
interface ReliabilityApi {

    @GET("v1/control/graph")
    suspend fun getDependencyGraph(): DependencyGraph
}
