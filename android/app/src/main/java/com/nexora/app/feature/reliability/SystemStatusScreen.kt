package com.nexora.app.feature.reliability

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import com.nexora.app.core.design.component.NexoraCard
import com.nexora.app.core.design.component.NexoraTopBar
import com.nexora.app.core.design.theme.NexoraPrimary
import com.nexora.app.core.design.theme.NexoraSuccess
import com.nexora.app.core.model.GraphNode

/**
 * SystemStatusScreen is the platform reliability view: every service in the
 * dependency graph with its live health, what it depends on, and its blast
 * radius — plus the auto-shedding advisories.
 */
@Composable
fun SystemStatusScreen(
    onNavigateBack: () -> Unit,
    viewModel: SystemStatusViewModel = hiltViewModel()
) {
    val uiState by viewModel.uiState.collectAsState()

    Column(
        modifier = Modifier
            .fillMaxSize()
            .background(MaterialTheme.colorScheme.background)
    ) {
        NexoraTopBar(title = "System status", onBackClick = onNavigateBack)

        when (val state = uiState) {
            is SystemStatusUiState.Loading -> Box(
                modifier = Modifier.fillMaxSize(),
                contentAlignment = Alignment.Center
            ) { Text("Checking the platform…", style = MaterialTheme.typography.bodyLarge) }

            is SystemStatusUiState.Error -> Column(
                modifier = Modifier
                    .fillMaxSize()
                    .padding(24.dp),
                horizontalAlignment = Alignment.CenterHorizontally,
                verticalArrangement = Arrangement.Center
            ) {
                Text(
                    state.message,
                    style = MaterialTheme.typography.bodyLarge,
                    color = MaterialTheme.colorScheme.error
                )
                TextButton(onClick = { viewModel.load() }) { Text("Retry") }
            }

            is SystemStatusUiState.Success -> LazyColumn(
                modifier = Modifier.fillMaxSize(),
                contentPadding = androidx.compose.foundation.layout.PaddingValues(16.dp),
                verticalArrangement = Arrangement.spacedBy(10.dp)
            ) {
                item {
                    NexoraCard {
                        Row(
                            modifier = Modifier
                                .fillMaxWidth()
                                .padding(14.dp),
                            verticalAlignment = Alignment.CenterVertically
                        ) {
                            StatusDot(state.graph.overall)
                            Spacer(modifier = Modifier.width(10.dp))
                            Column(modifier = Modifier.weight(1f)) {
                                Text(
                                    text = when (state.graph.overall) {
                                        "HEALTHY" -> "All systems operational"
                                        "DEGRADED" -> "Platform degraded — some features may be slow"
                                        else -> "Platform incident in progress"
                                    },
                                    style = MaterialTheme.typography.titleMedium,
                                    fontWeight = FontWeight.SemiBold
                                )
                                Text(
                                    text = "${state.graph.nodes.size} services monitored",
                                    style = MaterialTheme.typography.bodySmall,
                                    color = MaterialTheme.colorScheme.onSurfaceVariant
                                )
                            }
                        }
                    }
                }
                items(state.graph.advisories) { advisory ->
                    NexoraCard {
                        Row(
                            modifier = Modifier
                                .fillMaxWidth()
                                .padding(12.dp),
                            verticalAlignment = Alignment.CenterVertically
                        ) {
                            Text("⚠️", style = MaterialTheme.typography.titleMedium)
                            Spacer(modifier = Modifier.width(8.dp))
                            Text(
                                advisory,
                                style = MaterialTheme.typography.bodySmall,
                                color = MaterialTheme.colorScheme.onSurfaceVariant
                            )
                        }
                    }
                }
                item {
                    Text(
                        "Services",
                        style = MaterialTheme.typography.titleMedium,
                        fontWeight = FontWeight.SemiBold,
                        modifier = Modifier.padding(top = 6.dp)
                    )
                }
                items(state.graph.nodes.sortedBy { it.name }) { node ->
                    ServiceRow(node)
                }
            }
        }
    }
}

@Composable
private fun ServiceRow(node: GraphNode) {
    NexoraCard {
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .padding(12.dp),
            verticalAlignment = Alignment.CenterVertically
        ) {
            StatusDot(node.status)
            Spacer(modifier = Modifier.width(10.dp))
            Column(modifier = Modifier.weight(1f)) {
                Text(
                    text = node.name,
                    style = MaterialTheme.typography.bodyLarge,
                    fontWeight = FontWeight.Medium
                )
                if (node.dependsOn.isNotEmpty()) {
                    Text(
                        text = "depends on ${node.dependsOn.joinToString(", ")}",
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant
                    )
                }
            }
            if (node.blastRadius > 0) {
                Text(
                    text = "impact: ${node.blastRadius}",
                    style = MaterialTheme.typography.labelMedium,
                    color = if (node.status != "HEALTHY") MaterialTheme.colorScheme.error
                    else MaterialTheme.colorScheme.onSurfaceVariant
                )
            }
        }
    }
}

@Composable
private fun StatusDot(status: String) {
    val color = when (status) {
        "HEALTHY" -> NexoraSuccess
        "DEGRADED" -> MaterialTheme.colorScheme.tertiary
        else -> MaterialTheme.colorScheme.error
    }
    Box(
        modifier = Modifier
            .height(12.dp)
            .width(12.dp)
            .clip(CircleShape)
            .background(color)
    )
}
