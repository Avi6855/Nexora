package com.nexora.app

import org.junit.Test
import org.junit.Assert.*

class NavigationTest {

    sealed class Screen(val route: String) {
        data object Login : Screen("login")
        data object Register : Screen("register")
        data object Otp : Screen("otp/{email}") {
            fun createRoute(email: String) = "otp/$email"
        }
        data object Home : Screen("home")
        data object Accounts : Screen("accounts")
        data object AccountDetail : Screen("account/{accountId}") {
            fun createRoute(accountId: String) = "account/$accountId"
        }
        data object Transactions : Screen("transactions/{accountId}") {
            fun createRoute(accountId: String) = "transactions/$accountId"
        }
        data object TransactionDetail : Screen("transaction/{transactionId}") {
            fun createRoute(transactionId: String) = "transaction/$transactionId"
        }
        data object Cards : Screen("cards")
        data object Pots : Screen("pots")
        data object Profile : Screen("profile")
        data object Security : Screen("security")
        data object Notifications : Screen("notifications")
        data object SendMoney : Screen("send-money/{accountId}") {
            fun createRoute(accountId: String) = "send-money/$accountId"
        }
        data object ReviewPayment : Screen("review-payment")
        data object PaymentProcessing : Screen("payment-processing/{paymentId}") {
            fun createRoute(paymentId: String) = "payment-processing/$paymentId"
        }
        data object PaymentSuccess : Screen("payment-success/{paymentId}") {
            fun createRoute(paymentId: String) = "payment-success/$paymentId"
        }
        data object PaymentFailed : Screen("payment-failed/{paymentId}") {
            fun createRoute(paymentId: String) = "payment-failed/$paymentId"
        }
        data object PaymentUnknown : Screen("payment-unknown/{paymentId}") {
            fun createRoute(paymentId: String) = "payment-unknown/$paymentId"
        }
    }

    @Test
    fun `route definitions are correct`() {
        assertEquals("login", Screen.Login.route)
        assertEquals("register", Screen.Register.route)
        assertEquals("otp/{email}", Screen.Otp.route)
        assertEquals("home", Screen.Home.route)
        assertEquals("accounts", Screen.Accounts.route)
        assertEquals("account/{accountId}", Screen.AccountDetail.route)
        assertEquals("transactions/{accountId}", Screen.Transactions.route)
        assertEquals("transaction/{transactionId}", Screen.TransactionDetail.route)
        assertEquals("cards", Screen.Cards.route)
        assertEquals("pots", Screen.Pots.route)
        assertEquals("profile", Screen.Profile.route)
        assertEquals("security", Screen.Security.route)
        assertEquals("notifications", Screen.Notifications.route)
        assertEquals("send-money/{accountId}", Screen.SendMoney.route)
        assertEquals("review-payment", Screen.ReviewPayment.route)
        assertEquals("payment-processing/{paymentId}", Screen.PaymentProcessing.route)
        assertEquals("payment-success/{paymentId}", Screen.PaymentSuccess.route)
        assertEquals("payment-failed/{paymentId}", Screen.PaymentFailed.route)
        assertEquals("payment-unknown/{paymentId}", Screen.PaymentUnknown.route)
    }

    @Test
    fun `parameter extraction for Otp`() {
        val email = "test@example.com"
        val route = Screen.Otp.createRoute(email)
        assertEquals("otp/test@example.com", route)

        val extractedEmail = route.removePrefix("otp/")
        assertEquals(email, extractedEmail)
    }

    @Test
    fun `parameter extraction for AccountDetail`() {
        val accountId = "acc-123-abc"
        val route = Screen.AccountDetail.createRoute(accountId)
        assertEquals("account/acc-123-abc", route)

        val extractedId = route.removePrefix("account/")
        assertEquals(accountId, extractedId)
    }

    @Test
    fun `parameter extraction for Transactions`() {
        val accountId = "acc-456-def"
        val route = Screen.Transactions.createRoute(accountId)
        assertEquals("transactions/acc-456-def", route)
    }

    @Test
    fun `parameter extraction for TransactionDetail`() {
        val transactionId = "txn-789-ghi"
        val route = Screen.TransactionDetail.createRoute(transactionId)
        assertEquals("transaction/txn-789-ghi", route)
    }

    @Test
    fun `parameter extraction for SendMoney`() {
        val accountId = "acc-101-jkl"
        val route = Screen.SendMoney.createRoute(accountId)
        assertEquals("send-money/acc-101-jkl", route)
    }

    @Test
    fun `parameter extraction for PaymentProcessing`() {
        val paymentId = "pay-202-mno"
        val route = Screen.PaymentProcessing.createRoute(paymentId)
        assertEquals("payment-processing/pay-202-mno", route)
    }

    @Test
    fun `parameter extraction for PaymentSuccess`() {
        val paymentId = "pay-303-pqr"
        val route = Screen.PaymentSuccess.createRoute(paymentId)
        assertEquals("payment-success/pay-303-pqr", route)
    }

    @Test
    fun `parameter extraction for PaymentFailed`() {
        val paymentId = "pay-404-stu"
        val route = Screen.PaymentFailed.createRoute(paymentId)
        assertEquals("payment-failed/pay-404-stu", route)
    }

    @Test
    fun `parameter extraction for PaymentUnknown`() {
        val paymentId = "pay-505-vwx"
        val route = Screen.PaymentUnknown.createRoute(paymentId)
        assertEquals("payment-unknown/pay-505-vwx", route)
    }

    @Test
    fun `bottom bar routes`() {
        val bottomBarRoutes = listOf(
            Screen.Home.route,
            Screen.Accounts.route,
            Screen.Cards.route,
            Screen.Pots.route,
            Screen.Profile.route
        )

        assertEquals(5, bottomBarRoutes.size)
        assertTrue(bottomBarRoutes.contains("home"))
        assertTrue(bottomBarRoutes.contains("accounts"))
        assertTrue(bottomBarRoutes.contains("cards"))
        assertTrue(bottomBarRoutes.contains("pots"))
        assertTrue(bottomBarRoutes.contains("profile"))
    }

    @Test
    fun `total screen count`() {
        val screens = listOf(
            Screen.Login,
            Screen.Register,
            Screen.Otp,
            Screen.Home,
            Screen.Accounts,
            Screen.AccountDetail,
            Screen.Transactions,
            Screen.TransactionDetail,
            Screen.Cards,
            Screen.Pots,
            Screen.Profile,
            Screen.Security,
            Screen.Notifications,
            Screen.SendMoney,
            Screen.ReviewPayment,
            Screen.PaymentProcessing,
            Screen.PaymentSuccess,
            Screen.PaymentFailed,
            Screen.PaymentUnknown
        )

        assertEquals(19, screens.size)
    }
}
