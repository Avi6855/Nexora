package com.nexora.app.navigation

import androidx.compose.animation.AnimatedContentTransitionScope
import androidx.compose.animation.core.tween
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.animation.slideInHorizontally
import androidx.compose.animation.slideOutHorizontally
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.navigation.NavHostController
import androidx.navigation.NavType
import androidx.navigation.compose.NavHost
import androidx.navigation.compose.composable
import androidx.navigation.compose.currentBackStackEntryAsState
import androidx.navigation.compose.rememberNavController
import androidx.navigation.navArgument
import com.nexora.app.core.design.component.NexoraBottomBar
import com.nexora.app.feature.accounts.AccountDetailScreen
import com.nexora.app.feature.accounts.AccountsScreen
import com.nexora.app.feature.auth.LoginScreen
import com.nexora.app.feature.auth.OtpScreen
import com.nexora.app.feature.auth.RegisterScreen
import com.nexora.app.feature.cards.CardDetailScreen
import com.nexora.app.feature.cards.CardsScreen
import com.nexora.app.feature.disputes.DisputesScreen
import com.nexora.app.feature.disputes.ReportDisputeScreen
import com.nexora.app.feature.home.HomeScreen
import com.nexora.app.feature.insights.InsightsScreen
import com.nexora.app.feature.notifications.NotificationsScreen
import com.nexora.app.feature.payments.PaymentFailedScreen
import com.nexora.app.feature.payments.PaymentProcessingScreen
import com.nexora.app.feature.payments.PaymentSuccessScreen
import com.nexora.app.feature.payments.PaymentUnknownScreen
import com.nexora.app.feature.payments.SendMoneyScreen
import com.nexora.app.feature.pots.PotsScreen
import com.nexora.app.feature.profile.ProfileScreen
import com.nexora.app.feature.reliability.SystemStatusScreen
import com.nexora.app.feature.security.DelegatedAccessScreen
import com.nexora.app.feature.security.SecurityScreen
import com.nexora.app.feature.transactions.TransactionDetailScreen
import com.nexora.app.feature.transactions.TransactionsScreen
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.Scaffold
import androidx.compose.ui.Modifier

@Composable
fun NexoraNavGraph() {
    val navController = rememberNavController()
    val navBackStackEntry by navController.currentBackStackEntryAsState()
    val currentRoute = navBackStackEntry?.destination?.route

    val bottomBarRoutes = listOf(
        Screen.Home.route,
        Screen.Accounts.route,
        Screen.Cards.route,
        Screen.Pots.route,
        Screen.Profile.route
    )

    val showBottomBar = currentRoute in bottomBarRoutes

    Scaffold(
        bottomBar = {
            if (showBottomBar) {
                NexoraBottomBar(
                    currentRoute = currentRoute,
                    onNavigate = { route ->
                        navController.navigate(route) {
                            popUpTo(Screen.Home.route) { saveState = true }
                            launchSingleTop = true
                            restoreState = true
                        }
                    }
                )
            }
        }
    ) { paddingValues ->
        NavHost(
            navController = navController,
            startDestination = Screen.Login.route,
            modifier = Modifier.padding(paddingValues),
            enterTransition = {
                fadeIn(animationSpec = tween(300)) + slideIntoContainer(
                    AnimatedContentTransitionScope.SlideDirection.Left,
                    tween(300)
                )
            },
            exitTransition = {
                fadeOut(animationSpec = tween(300)) + slideOutOfContainer(
                    AnimatedContentTransitionScope.SlideDirection.Left,
                    tween(300)
                )
            },
            popEnterTransition = {
                fadeIn(animationSpec = tween(300)) + slideIntoContainer(
                    AnimatedContentTransitionScope.SlideDirection.Right,
                    tween(300)
                )
            },
            popExitTransition = {
                fadeOut(animationSpec = tween(300)) + slideOutOfContainer(
                    AnimatedContentTransitionScope.SlideDirection.Right,
                    tween(300)
                )
            }
        ) {
            composable(Screen.Login.route) {
                LoginScreen(
                    onNavigateToRegister = { navController.navigate(Screen.Register.route) },
                    onNavigateToOtp = { email -> navController.navigate(Screen.Otp.createRoute(email)) },
                    onNavigateToHome = {
                        navController.navigate(Screen.Home.route) {
                            popUpTo(Screen.Login.route) { inclusive = true }
                        }
                    }
                )
            }

            composable(Screen.Register.route) {
                RegisterScreen(
                    onNavigateToLogin = { navController.popBackStack() },
                    onNavigateToOtp = { email -> navController.navigate(Screen.Otp.createRoute(email)) }
                )
            }

            composable(
                route = Screen.Otp.route,
                arguments = listOf(navArgument("email") { type = NavType.StringType })
            ) { backStackEntry ->
                val email = backStackEntry.arguments?.getString("email") ?: ""
                OtpScreen(
                    email = email,
                    onNavigateToHome = {
                        navController.navigate(Screen.Home.route) {
                            popUpTo(Screen.Login.route) { inclusive = true }
                        }
                    }
                )
            }

            composable(Screen.Home.route) {
                HomeScreen(
                    onNavigateToAccount = { accountId ->
                        navController.navigate(Screen.AccountDetail.createRoute(accountId))
                    },
                    onNavigateToTransaction = { transactionId ->
                        navController.navigate(Screen.TransactionDetail.createRoute(transactionId))
                    },
                    onNavigateToNotifications = { navController.navigate(Screen.Notifications.route) },
                    onNavigateToInsights = { navController.navigate(Screen.Insights.route) }
                )
            }

            composable(Screen.Insights.route) {
                InsightsScreen(
                    onNavigateBack = { navController.popBackStack() }
                )
            }

            composable(Screen.Accounts.route) {
                AccountsScreen(
                    onNavigateToAccount = { accountId ->
                        navController.navigate(Screen.AccountDetail.createRoute(accountId))
                    }
                )
            }

            composable(
                route = Screen.AccountDetail.route,
                arguments = listOf(navArgument("accountId") { type = NavType.StringType })
            ) { backStackEntry ->
                val accountId = backStackEntry.arguments?.getString("accountId") ?: ""
                AccountDetailScreen(
                    accountId = accountId,
                    onNavigateBack = { navController.popBackStack() },
                    onNavigateToTransactions = { id ->
                        navController.navigate(Screen.Transactions.createRoute(id))
                    },
                    onNavigateToSendMoney = { id ->
                        navController.navigate(Screen.SendMoney.createRoute(id))
                    }
                )
            }

            composable(
                route = Screen.Transactions.route,
                arguments = listOf(navArgument("accountId") { type = NavType.StringType })
            ) { backStackEntry ->
                val accountId = backStackEntry.arguments?.getString("accountId") ?: ""
                TransactionsScreen(
                    accountId = accountId,
                    onNavigateBack = { navController.popBackStack() },
                    onNavigateToTransaction = { transactionId ->
                        navController.navigate(Screen.TransactionDetail.createRoute(transactionId))
                    }
                )
            }

            composable(
                route = Screen.TransactionDetail.route,
                arguments = listOf(navArgument("transactionId") { type = NavType.StringType })
            ) { backStackEntry ->
                val transactionId = backStackEntry.arguments?.getString("transactionId") ?: ""
                TransactionDetailScreen(
                    transactionId = transactionId,
                    onNavigateBack = { navController.popBackStack() },
                    onReportProblem = { entryId ->
                        navController.navigate(Screen.ReportDispute.createRoute(entryId))
                    }
                )
            }

            composable(Screen.Cards.route) {
                CardsScreen(
                    onNavigateToCardDetail = { cardId ->
                        navController.navigate(Screen.CardDetail.createRoute(cardId))
                    }
                )
            }

            composable(
                route = Screen.CardDetail.route,
                arguments = listOf(navArgument("cardId") { type = NavType.StringType })
            ) { backStackEntry ->
                val cardId = backStackEntry.arguments?.getString("cardId") ?: ""
                CardDetailScreen(
                    cardId = cardId,
                    onNavigateBack = { navController.popBackStack() }
                )
            }

            composable(Screen.Pots.route) {
                PotsScreen()
            }

            composable(Screen.Profile.route) {
                ProfileScreen(
                    onNavigateToSecurity = { navController.navigate(Screen.Security.route) },
                    onNavigateToDelegatedAccess = { navController.navigate(Screen.DelegatedAccess.route) },
                    onNavigateToDisputes = { navController.navigate(Screen.Disputes.route) },
                    onNavigateToSystemStatus = { navController.navigate(Screen.SystemStatus.route) },
                    onNavigateToLogin = {
                        navController.navigate(Screen.Login.route) {
                            popUpTo(0) { inclusive = true }
                        }
                    }
                )
            }

            composable(Screen.Security.route) {
                SecurityScreen(
                    onNavigateBack = { navController.popBackStack() },
                    onNavigateToDelegatedAccess = { navController.navigate(Screen.DelegatedAccess.route) }
                )
            }

            composable(Screen.DelegatedAccess.route) {
                DelegatedAccessScreen(
                    onNavigateBack = { navController.popBackStack() }
                )
            }

            composable(Screen.Disputes.route) {
                DisputesScreen(
                    onNavigateBack = { navController.popBackStack() },
                    onNavigateToTransactions = {
                        navController.navigate(Screen.Accounts.route)
                    }
                )
            }

            composable(
                route = Screen.ReportDispute.route,
                arguments = listOf(navArgument("entryId") { type = NavType.StringType })
            ) { backStackEntry ->
                val entryId = backStackEntry.arguments?.getString("entryId") ?: ""
                ReportDisputeScreen(
                    entryId = entryId,
                    onNavigateBack = { navController.popBackStack() }
                )
            }

            composable(Screen.SystemStatus.route) {
                SystemStatusScreen(
                    onNavigateBack = { navController.popBackStack() }
                )
            }

            composable(Screen.Notifications.route) {
                NotificationsScreen(
                    onNavigateBack = { navController.popBackStack() }
                )
            }

            composable(
                route = Screen.SendMoney.route,
                arguments = listOf(navArgument("accountId") { type = NavType.StringType })
            ) { backStackEntry ->
                val accountId = backStackEntry.arguments?.getString("accountId") ?: ""
                SendMoneyScreen(
                    accountId = accountId,
                    onNavigateBack = { navController.popBackStack() },
                    onNavigateToProcessing = { paymentId ->
                        navController.navigate(Screen.PaymentProcessing.createRoute(paymentId))
                    }
                )
            }

            composable(
                route = Screen.PaymentProcessing.route,
                arguments = listOf(navArgument("paymentId") { type = NavType.StringType })
            ) { backStackEntry ->
                val paymentId = backStackEntry.arguments?.getString("paymentId") ?: ""
                PaymentProcessingScreen(
                    paymentId = paymentId,
                    onNavigateToSuccess = { id ->
                        navController.navigate(Screen.PaymentSuccess.createRoute(id))
                    },
                    onNavigateToFailed = { id ->
                        navController.navigate(Screen.PaymentFailed.createRoute(id))
                    },
                    onNavigateToUnknown = { id ->
                        navController.navigate(Screen.PaymentUnknown.createRoute(id))
                    }
                )
            }

            composable(
                route = Screen.PaymentSuccess.route,
                arguments = listOf(navArgument("paymentId") { type = NavType.StringType })
            ) { backStackEntry ->
                val paymentId = backStackEntry.arguments?.getString("paymentId") ?: ""
                PaymentSuccessScreen(
                    paymentId = paymentId,
                    onNavigateToHome = {
                        navController.navigate(Screen.Home.route) {
                            popUpTo(Screen.Home.route) { inclusive = true }
                        }
                    }
                )
            }

            composable(
                route = Screen.PaymentFailed.route,
                arguments = listOf(navArgument("paymentId") { type = NavType.StringType })
            ) { backStackEntry ->
                val paymentId = backStackEntry.arguments?.getString("paymentId") ?: ""
                PaymentFailedScreen(
                    paymentId = paymentId,
                    onNavigateBack = { navController.popBackStack() },
                    onNavigateToHome = {
                        navController.navigate(Screen.Home.route) {
                            popUpTo(Screen.Home.route) { inclusive = true }
                        }
                    }
                )
            }

            composable(
                route = Screen.PaymentUnknown.route,
                arguments = listOf(navArgument("paymentId") { type = NavType.StringType })
            ) { backStackEntry ->
                val paymentId = backStackEntry.arguments?.getString("paymentId") ?: ""
                PaymentUnknownScreen(
                    paymentId = paymentId,
                    onNavigateBack = { navController.popBackStack() },
                    onNavigateToHome = {
                        navController.navigate(Screen.Home.route) {
                            popUpTo(Screen.Home.route) { inclusive = true }
                        }
                    }
                )
            }
        }
    }
}
