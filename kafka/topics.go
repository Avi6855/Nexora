package kafka

const (
	TopicPrefix = "nexora"

	TopicUserRegistered       = "nexora.user.registered"
	TopicUserUpdated          = "nexora.user.updated"
	TopicUserDeactivated      = "nexora.user.deactivated"
	TopicAccountCreated       = "nexora.account.created"
	TopicAccountUpdated       = "nexora.account.updated"
	TopicAccountStatusChanged = "nexora.account.status_changed"
	TopicAccountBalanceChanged = "nexora.account.balance_changed"
	TopicPaymentCreated       = "nexora.payment.created"
	TopicPaymentAuthorized    = "nexora.payment.authorized"
	TopicPaymentConfirmed     = "nexora.payment.confirmed"
	TopicPaymentFailed        = "nexora.payment.failed"
	TopicPaymentReversed      = "nexora.payment.reversed"
	TopicTransferCreated      = "nexora.transfer.created"
	TopicTransferCompleted    = "nexora.transfer.completed"
	TopicTransferFailed       = "nexora.transfer.failed"
	TopicCardCreated          = "nexora.card.created"
	TopicCardFrozen           = "nexora.card.frozen"
	TopicCardUnfrozen         = "nexora.card.unfrozen"
	TopicPotCreated           = "nexora.pot.created"
	TopicPotDeposit           = "nexora.pot.deposit"
	TopicPotWithdrawal        = "nexora.pot.withdrawal"
	TopicLedgerTransactionCreated   = "nexora.ledger.transaction.created"
	TopicLedgerEntryCreated         = "nexora.ledger.entry.created"
	TopicLedgerReservationCreated   = "nexora.ledger.reservation.created"
	TopicLedgerReservationReleased  = "nexora.ledger.reservation.released"
	TopicLedgerReservationSettled   = "nexora.ledger.reservation.settled"
	TopicLedgerTransaction    = "nexora.ledger.transaction"
	TopicLedgerReservation    = "nexora.ledger.reservation"
	TopicNotificationCreated  = "nexora.notification.created"
	TopicNotificationSent     = "nexora.notification.sent"
	TopicNotificationRead     = "nexora.notification.read"
	TopicNotificationFailed   = "nexora.notification.failed"
	TopicFraudDetected        = "nexora.fraud.detected"
	TopicFraudReview          = "nexora.fraud.review"
	TopicReconciliationRequired      = "nexora.reconciliation.required"
	TopicReconciliationCompleted     = "nexora.reconciliation.completed"
	TopicReconciliationDiscrepancy   = "nexora.reconciliation.discrepancy_found"
	TopicReplayStarted        = "nexora.replay.started"
	TopicReplayCompleted      = "nexora.replay.completed"
	TopicReplayFailed         = "nexora.replay.failed"
	TopicSimulationCreated    = "nexora.simulation.created"
	TopicSimulationCompleted  = "nexora.simulation.completed"
	TopicPolicyCreated        = "nexora.policy.created"
	TopicPolicyActivated      = "nexora.policy.activated"
	TopicPolicyDeactivated    = "nexora.policy.deactivated"
	TopicPolicyDecision       = "nexora.policy.decision"
	TopicAuditEventRecorded   = "nexora.audit.event.recorded"
	TopicIncidentCreated      = "nexora.incident.created"
	TopicIncidentDetected     = "nexora.incident.detected"
	TopicIncidentUpdated      = "nexora.incident.updated"
	TopicIncidentResolved     = "nexora.incident.resolved"
	TopicAuditLog             = "nexora.audit.log"
)

var AllTopics = []string{
	TopicUserRegistered,
	TopicUserUpdated,
	TopicUserDeactivated,
	TopicAccountCreated,
	TopicAccountUpdated,
	TopicAccountStatusChanged,
	TopicAccountBalanceChanged,
	TopicPaymentCreated,
	TopicPaymentAuthorized,
	TopicPaymentConfirmed,
	TopicPaymentFailed,
	TopicPaymentReversed,
	TopicTransferCreated,
	TopicTransferCompleted,
	TopicTransferFailed,
	TopicCardCreated,
	TopicCardFrozen,
	TopicCardUnfrozen,
	TopicPotCreated,
	TopicPotDeposit,
	TopicPotWithdrawal,
	TopicLedgerTransactionCreated,
	TopicLedgerEntryCreated,
	TopicLedgerReservationCreated,
	TopicLedgerReservationReleased,
	TopicLedgerReservationSettled,
	TopicLedgerTransaction,
	TopicLedgerReservation,
	TopicNotificationCreated,
	TopicNotificationSent,
	TopicNotificationRead,
	TopicNotificationFailed,
	TopicFraudDetected,
	TopicFraudReview,
	TopicReconciliationRequired,
	TopicReconciliationCompleted,
	TopicReconciliationDiscrepancy,
	TopicReplayStarted,
	TopicReplayCompleted,
	TopicReplayFailed,
	TopicSimulationCreated,
	TopicSimulationCompleted,
	TopicPolicyCreated,
	TopicPolicyActivated,
	TopicPolicyDeactivated,
	TopicPolicyDecision,
	TopicAuditEventRecorded,
	TopicIncidentCreated,
	TopicIncidentDetected,
	TopicIncidentUpdated,
	TopicIncidentResolved,
	TopicAuditLog,
}
