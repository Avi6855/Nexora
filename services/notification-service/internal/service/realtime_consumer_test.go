package service

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/nexora/nexora/services/notification-service/internal/domain"
)

// fakeNotifRepo captures the notifications the consumer produces.
type fakeNotifRepo struct {
	mu   sync.Mutex
	sent []*domain.Notification
}

func (r *fakeNotifRepo) Create(ctx context.Context, n *domain.Notification) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sent = append(r.sent, n)
	return nil
}
func (r *fakeNotifRepo) GetByID(ctx context.Context, id uuid.UUID) (*domain.Notification, error) {
	return nil, nil
}
func (r *fakeNotifRepo) GetByUserID(ctx context.Context, userID uuid.UUID, limit int) ([]*domain.Notification, error) {
	return nil, nil
}
func (r *fakeNotifRepo) MarkAsSent(ctx context.Context, n *domain.Notification) error   { return nil }
func (r *fakeNotifRepo) MarkAsRead(ctx context.Context, n *domain.Notification) error   { return nil }
func (r *fakeNotifRepo) MarkAsFailed(ctx context.Context, n *domain.Notification) error { return nil }

func (r *fakeNotifRepo) last(t *testing.T) *domain.Notification {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.sent) == 0 {
		t.Fatal("no notification was produced")
	}
	return r.sent[len(r.sent)-1]
}

func newCardEventHarness(t *testing.T) (*NotificationService, *fakeNotifRepo) {
	t.Helper()
	repo := &fakeNotifRepo{}
	// A nil hub keeps the assertion on the durable notification; the SSE fan-out
	// is the same code path with a hub attached.
	return NewNotificationService(repo, nil, nil, zerolog.Nop()), repo
}

func mustJSON(t *testing.T, v interface{}) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshaling event: %v", err)
	}
	return data
}

func TestStepUpEventBecomesAConfirmationNotificationWithTheCode(t *testing.T) {
	svc, repo := newCardEventHarness(t)
	userID := uuid.New()

	event := CardAuthorizationEvent{
		AuthorizationID: uuid.New().String(),
		CardID:          uuid.New().String(),
		UserID:          userID.String(),
		AccountID:       uuid.New().String(),
		Amount:          120_00,
		Currency:        "GBP",
		Merchant:        "Zara Online",
		MerchantCity:    "London",
		Status:          "CHALLENGED",
		Decision:        "CHALLENGE",
		ChallengeID:     uuid.New().String(),
		ChallengeOTP:    "424242",
	}

	if err := svc.HandleRealtimeEvent(context.Background(), "nexora.card.authorization.challenged", "card.authorization.challenged", mustJSON(t, event)); err != nil {
		t.Fatalf("HandleRealtimeEvent: %v", err)
	}

	notif := repo.last(t)
	if notif.UserID != userID {
		t.Fatalf("notification routed to %s, want %s", notif.UserID, userID)
	}
	if notif.NotificationType != domain.NotificationTypeSecurity {
		t.Fatalf("type = %s, want SECURITY for a step-up", notif.NotificationType)
	}
	if notif.Title != "Confirm your payment" {
		t.Fatalf("title = %q", notif.Title)
	}
	if !strings.Contains(notif.Body, "£120.00") || !strings.Contains(notif.Body, "424242") {
		t.Fatalf("body = %q, want the amount and the one-time code", notif.Body)
	}
	if !strings.Contains(string(notif.Metadata), "challenge_id") {
		t.Fatalf("metadata = %s, want the challenge id so the app can answer it", notif.Metadata)
	}
}

func TestRefundEventsDistinguishPartialFromFull(t *testing.T) {
	cases := []struct {
		name         string
		amount       int64
		refunded     int64
		wantTitle    string
		wantInBody   string
		wantMetadata string
	}{
		{
			name:         "partial refund",
			amount:       50_00,
			refunded:     20_00,
			wantTitle:    "Partial refund received",
			wantInBody:   "£20.00 refunded by Zara Online",
			wantMetadata: "refund_id",
		},
		{
			name:       "full refund",
			amount:     20_00,
			refunded:   20_00,
			wantTitle:  "Refund received",
			wantInBody: "£20.00 refunded by Zara Online",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, repo := newCardEventHarness(t)
			balance := int64(185_00)
			event := CardAuthorizationEvent{
				AuthorizationID: uuid.New().String(),
				UserID:          uuid.New().String(),
				AccountID:       uuid.New().String(),
				Amount:          tc.amount,
				Currency:        "GBP",
				Merchant:        "Zara Online",
				Status:          "PARTIALLY_REFUNDED",
				Decision:        "APPROVE",
				RefundedAmount:  tc.refunded,
				RefundID:        uuid.New().String(),
				BalanceAfter:    &balance,
			}

			if err := svc.HandleRealtimeEvent(context.Background(), "nexora.card.authorization.refunded", "card.authorization.refunded", mustJSON(t, event)); err != nil {
				t.Fatalf("HandleRealtimeEvent: %v", err)
			}

			notif := repo.last(t)
			if notif.Title != tc.wantTitle {
				t.Fatalf("title = %q, want %q", notif.Title, tc.wantTitle)
			}
			if !strings.Contains(notif.Body, tc.wantInBody) {
				t.Fatalf("body = %q, want it to contain %q", notif.Body, tc.wantInBody)
			}
			if !strings.Contains(notif.Body, "£185.00") {
				t.Fatalf("body = %q, want the balance after the refund", notif.Body)
			}
			if tc.wantMetadata != "" && !strings.Contains(string(notif.Metadata), tc.wantMetadata) {
				t.Fatalf("metadata = %s, want %q", notif.Metadata, tc.wantMetadata)
			}
		})
	}
}

func TestFailedStepUpDeclineIsExplainedToTheCustomer(t *testing.T) {
	svc, repo := newCardEventHarness(t)

	event := CardAuthorizationEvent{
		AuthorizationID: uuid.New().String(),
		UserID:          uuid.New().String(),
		AccountID:       uuid.New().String(),
		Amount:          120_00,
		Currency:        "GBP",
		Merchant:        "Zara Online",
		Status:          "DECLINED",
		Decision:        "DECLINE",
		DeclineReason:   "sca_failed",
	}

	if err := svc.HandleRealtimeEvent(context.Background(), "nexora.card.authorization.declined", "card.authorization.declined", mustJSON(t, event)); err != nil {
		t.Fatalf("HandleRealtimeEvent: %v", err)
	}

	notif := repo.last(t)
	if notif.NotificationType != domain.NotificationTypeSecurity || notif.Title != "Payment not confirmed" {
		t.Fatalf("notification = %s/%q, want SECURITY/Payment not confirmed", notif.NotificationType, notif.Title)
	}
	if !strings.Contains(notif.Body, "not confirmed") {
		t.Fatalf("body = %q", notif.Body)
	}
}

func TestUnrelatedEventsAreIgnored(t *testing.T) {
	svc, repo := newCardEventHarness(t)

	if err := svc.HandleRealtimeEvent(context.Background(), "nexora.audit.log", "audit.recorded", []byte(`{"anything":true}`)); err != nil {
		t.Fatalf("HandleRealtimeEvent: %v", err)
	}
	if len(repo.sent) != 0 {
		t.Fatalf("an unrelated event produced %d notifications", len(repo.sent))
	}
}
