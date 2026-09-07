package com.nexora.app.core.model

import com.google.gson.annotations.SerializedName

/**
 * DependencyGraph mirrors control-plane-service domain.DependencyGraph: the
 * live service dependency health graph with blast radius and advisories.
 */
data class DependencyGraph(
    @SerializedName("nodes") val nodes: List<GraphNode> = emptyList(),
    @SerializedName("edges") val edges: List<GraphEdge> = emptyList(),
    @SerializedName("overall") val overall: String = "HEALTHY",
    @SerializedName("advisories") val advisories: List<String> = emptyList(),
    @SerializedName("checked_at") val checkedAt: String = ""
)

/** GraphNode is one service in the dependency graph view. */
data class GraphNode(
    @SerializedName("name") val name: String = "",
    @SerializedName("status") val status: String = "HEALTHY",
    @SerializedName("depends_on") val dependsOn: List<String> = emptyList(),
    @SerializedName("blast_radius") val blastRadius: Int = 0
)

/** GraphEdge declares that from calls to. */
data class GraphEdge(
    @SerializedName("from") val from: String = "",
    @SerializedName("to") val to: String = ""
)
