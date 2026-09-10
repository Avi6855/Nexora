package idev

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/mux"

	"github.com/nexora/nexora/shared/identity"
)

func TestRecoveryWeakThenStrong(t *testing.T) {
	svc := NewService()
	id, _, err := svc.StartRecovery("acc1")
	if err != nil {
		t.Fatal(err)
	}
	for _, sig := range []identity.SignalKind{identity.SignalKnowledge, identity.SignalBehavioural} {
		if _, err := svc.PresentSignal(id, sig); err != nil {
			t.Fatal(err)
		}
	}
	if verified, _, err := svc.EvaluateRecovery(id); verified || err == nil {
		t.Fatal("weak signals alone must never verify")
	}
	// Duplicate signal is a conflict, not free score.
	if _, err := svc.PresentSignal(id, identity.SignalKnowledge); err == nil {
		t.Fatal("repeating a signal must fail")
	}
	// Strong evidence on a fresh session verifies.
	id2, _, err := svc.StartRecovery("acc1")
	if err != nil {
		t.Fatal(err)
	}
	for _, sig := range []identity.SignalKind{identity.SignalKnownDevice, identity.SignalDocMatch, identity.SignalBiometric} {
		if _, err := svc.PresentSignal(id2, sig); err != nil {
			t.Fatal(err)
		}
	}
	verified, _, err := svc.EvaluateRecovery(id2)
	if err != nil || !verified {
		t.Fatalf("strong evidence must verify: %v", err)
	}
	if _, _, err := svc.EvaluateRecovery("missing"); err == nil {
		t.Fatal("unknown session must fail")
	}
}

func TestDeviceTrustLifecycle(t *testing.T) {
	svc := NewService()
	if _, err := svc.RegisterDevice("phone"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RegisterDevice("watch"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RegisterDevice("phone"); err == nil {
		t.Fatal("duplicate registration must conflict")
	}
	if err := svc.LinkDevices("phone", "watch", "MFA_PAIRED"); err != nil {
		t.Fatal(err)
	}
	if err := svc.TrustDevice("phone"); err != nil {
		t.Fatal(err)
	}
	if st, _ := svc.DeviceState("watch"); st != identity.DeviceTrusted {
		t.Fatalf("paired device should be trusted, got %s", st)
	}
	if err := svc.RevokeDevice("phone"); err != nil {
		t.Fatal(err)
	}
	if st, _ := svc.DeviceState("watch"); st != identity.DeviceSuspicious {
		t.Fatalf("distrust must cascade, got %s", st)
	}
	if err := svc.TrustDevice("phone"); err == nil {
		t.Fatal("revoked device cannot be trusted")
	}
	if err := svc.FlagDevice("missing"); err == nil {
		t.Fatal("unknown device must fail")
	}
	if _, err := svc.DeviceState("missing"); err == nil {
		t.Fatal("unknown device state must fail")
	}
}

func TestDeviceExpiry(t *testing.T) {
	svc := NewService()
	if _, err := svc.RegisterDevice("old"); err != nil {
		t.Fatal(err)
	}
	expired := svc.ExpireDevices(-time.Hour) // non-positive → 90d default
	if len(expired) != 0 {
		t.Fatalf("fresh device must not expire: %v", expired)
	}
	// Windows wall-clock granularity can return identical timestamps for
	// back-to-back Now() calls, so sleep past the expiry horizon.
	time.Sleep(5 * time.Millisecond)
	expired = svc.ExpireDevices(time.Millisecond)
	if len(expired) != 1 || expired[0] != "old" {
		t.Fatalf("stale device must expire: %v", expired)
	}
	if st, _ := svc.DeviceState("old"); st != identity.DeviceExpired {
		t.Fatalf("state %s", st)
	}
}

func TestPasskeysAndRecoveryCreds(t *testing.T) {
	svc := NewService()
	ch, err := svc.IssueChallenge("acc1")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.ConsumeChallenge(ch, "acc1"); err != nil {
		t.Fatal(err)
	}
	if err := svc.ConsumeChallenge(ch, "acc1"); err == nil {
		t.Fatal("challenge replay must be rejected")
	}
	ch2, _ := svc.IssueChallenge("acc1")
	if err := svc.ConsumeChallenge(ch2, "acc2"); err == nil {
		t.Fatal("challenge bound to account")
	}

	if err := svc.EnrolRecovery("acc1", "rc1"); err != nil {
		t.Fatal(err)
	}
	if err := svc.EnrolRecovery("acc1", "rc2"); err != nil {
		t.Fatal(err)
	}
	if err := svc.EnrolRecovery("acc1", "rc3"); err == nil {
		t.Fatal("credential cap must hold")
	}
	if n := svc.ValidRecoveryCount("acc1"); n != 2 {
		t.Fatalf("valid %d", n)
	}
	if err := svc.RotateRecovery("acc1", "rc1", "rc1b"); err != nil {
		t.Fatal(err)
	}
	if err := svc.UseRecovery("acc1", "rc1"); err == nil {
		t.Fatal("rotated-away credential must be unusable")
	}
	if err := svc.UseRecovery("acc1", "rc1b"); err != nil {
		t.Fatal(err)
	}
	if n := svc.ValidRecoveryCount("acc1"); n != 2 {
		t.Fatalf("rotation must preserve count: %d", n)
	}
}

func TestIdevHTTPRoutes(t *testing.T) {
	svc := NewService()
	h := NewHandlers(svc)
	router := mux.NewRouter()
	h.RegisterRoutes(router)

	// Recovery: open session → weak evaluate is 409 → strong verifies.
	body, _ := json.Marshal(map[string]string{"account_id": "acc-http"})
	req := httptest.NewRequest("POST", "/v1/recovery/sessions", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("start session %d: %s", rec.Code, rec.Body.String())
	}
	var sess map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &sess); err != nil {
		t.Fatal(err)
	}
	sessID, _ := sess["session_id"].(string)
	if sessID == "" {
		t.Fatalf("missing session_id: %v", sess)
	}

	sigBody, _ := json.Marshal(map[string]string{"kind": string(identity.SignalKnowledge)})
	req = httptest.NewRequest("POST", "/v1/recovery/sessions/"+sessID+"/signals", bytes.NewReader(sigBody))
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("present signal %d: %s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest("POST", "/v1/recovery/sessions/"+sessID+"/evaluate", nil)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("weak evaluate status %d", rec.Code)
	}

	// Unknown session → 404.
	req = httptest.NewRequest("POST", "/v1/recovery/sessions/missing/signals", bytes.NewReader(sigBody))
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing session status %d", rec.Code)
	}

	// Devices: register → link → trust → get; duplicate → 409.
	devBody, _ := json.Marshal(map[string]string{"device_id": "phone"})
	req = httptest.NewRequest("POST", "/v1/devices", bytes.NewReader(devBody))
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("register device %d: %s", rec.Code, rec.Body.String())
	}
	req = httptest.NewRequest("POST", "/v1/devices", bytes.NewReader(devBody))
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("duplicate device status %d", rec.Code)
	}
	req = httptest.NewRequest("GET", "/v1/devices/missing", nil)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing device status %d", rec.Code)
	}

	// Passkeys: challenge → consume; replay → 409.
	chBody, _ := json.Marshal(map[string]string{"account_id": "acc-http"})
	req = httptest.NewRequest("POST", "/v1/passkeys/challenges", bytes.NewReader(chBody))
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("issue challenge %d: %s", rec.Code, rec.Body.String())
	}
	var chResp map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &chResp); err != nil {
		t.Fatal(err)
	}
	consumeBody, _ := json.Marshal(map[string]string{"account_id": "acc-http"})
	req = httptest.NewRequest("POST", "/v1/passkeys/challenges/"+chResp["challenge_id"]+"/consume", bytes.NewReader(consumeBody))
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("consume %d: %s", rec.Code, rec.Body.String())
	}
	req = httptest.NewRequest("POST", "/v1/passkeys/challenges/"+chResp["challenge_id"]+"/consume", bytes.NewReader(consumeBody))
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("replay status %d", rec.Code)
	}

	// Recovery credentials: enrol → rotate → use.
	enrolBody, _ := json.Marshal(map[string]string{"account_id": "acc-http", "credential_id": "rc1"})
	req = httptest.NewRequest("POST", "/v1/passkeys/recovery-credentials", bytes.NewReader(enrolBody))
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("enrol %d: %s", rec.Code, rec.Body.String())
	}
	rotBody, _ := json.Marshal(map[string]string{"account_id": "acc-http", "old_credential_id": "rc1", "new_credential_id": "rc1b"})
	req = httptest.NewRequest("POST", "/v1/passkeys/recovery-credentials/rotate", bytes.NewReader(rotBody))
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("rotate %d: %s", rec.Code, rec.Body.String())
	}
	useBody, _ := json.Marshal(map[string]string{"account_id": "acc-http", "credential_id": "rc1b"})
	req = httptest.NewRequest("POST", "/v1/passkeys/recovery-credentials/use", bytes.NewReader(useBody))
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("use %d: %s", rec.Code, rec.Body.String())
	}
}
