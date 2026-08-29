package com.nexora.app.navigation

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
