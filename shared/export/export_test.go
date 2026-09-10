package export

import (
	"strings"
	"testing"
	"time"
)

func mkEvents() []Event {
	day := func(d int) time.Time { return time.Date(2026, 9, d, 12, 0, 0, 0, time.UTC) }
	return []Event{
		{At: day(1), Kind: "TRANSACTION", Payload: map[string]string{"merchant": "Tesco", "amount": "-4820", "pan": "4929123456781234", "risk_score": "77"}},
		{At: day(2), Kind: "TRANSACTION", Payload: map[string]string{"merchant": "Cafe", "amount": "-350", "account_number": "12345678", "staff_notes": "customer called"}},
		{At: day(3), Kind: "CARD", Payload: map[string]string{"card_id": "c-9", "state": "ACTIVE"}},
	}
}

func TestFullPipeline(t *testing.T) {
	p := NewPlatform(NewRedactor(), &Encryptor{KeyID: "k1"})
	j, err := p.Request("cust-1", time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if j.State != StateQueued {
		t.Fatalf("new job must be QUEUED, got %s", j.State)
	}
	if err := p.Run(j.ID, mkEvents()); err != nil {
		t.Fatal(err)
	}
	if j.State != StateComplete || j.Progress() != 100 {
		t.Fatalf("job must complete: %+v", j)
	}
	// Watermark = max event time, not wall clock.
	want := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	if !j.NewWatermark.Equal(want) {
		t.Fatalf("watermark %v, want %v", j.NewWatermark, want)
	}
	url, err := p.Download(j.ID, time.Now())
	if err != nil || url == "" {
		t.Fatalf("download after completion: %v %q", err, url)
	}
}

func TestRedactionPolicy(t *testing.T) {
	r := NewRedactor()
	out := r.Redact(map[string]string{
		"pan": "4929123456781234", "account_number": "12345678",
		"auth_token": "secret", "merchant": "Tesco", "risk_score": "77",
	})
	if _, ok := out["auth_token"]; ok {
		t.Fatal("auth_token must be dropped")
	}
	if _, ok := out["risk_score"]; ok {
		t.Fatal("risk_score must be dropped")
	}
	if out["account_number"] != "****5678" {
		t.Fatalf("account masking wrong: %q", out["account_number"])
	}
	if out["pan"] != "492912******1234" {
		t.Fatalf("pan masking wrong: %q", out["pan"])
	}
	if out["merchant"] != "Tesco" {
		t.Fatal("customer-facing fields must survive")
	}
}

func TestIncrementalDelta(t *testing.T) {
	p := NewPlatform(NewRedactor(), &Encryptor{KeyID: "k1"})
	events := mkEvents()

	full, _ := p.Request("cust-1", time.Time{})
	if err := p.Run(full.ID, events); err != nil {
		t.Fatal(err)
	}
	// Next export starts from the checkpoint.
	since := p.LatestWatermark("cust-1")
	if since.IsZero() {
		t.Fatal("watermark must be recorded after full export")
	}
	delta, _ := p.Request("cust-1", since)
	newEvent := Event{At: time.Date(2026, 9, 5, 9, 0, 0, 0, time.UTC), Kind: "TRANSACTION",
		Payload: map[string]string{"merchant": "Bookshop", "amount": "-1299"}}
	if err := p.Run(delta.ID, append(events, newEvent)); err != nil {
		t.Fatal(err)
	}
	if delta.SinceWatermark != since {
		t.Fatalf("delta must start at prior watermark: %v", delta.SinceWatermark)
	}
	if !delta.NewWatermark.Equal(newEvent.At) {
		t.Fatalf("delta watermark %v, want %v", delta.NewWatermark, newEvent.At)
	}
	// A delta with nothing new still completes with an unchanged watermark.
	empty, _ := p.Request("cust-1", p.LatestWatermark("cust-1"))
	if err := p.Run(empty.ID, events); err != nil {
		t.Fatal(err)
	}
	if empty.NewWatermark.IsZero() || empty.NewWatermark.Before(delta.NewWatermark) {
		t.Fatalf("empty delta watermark must not regress: %v", empty.NewWatermark)
	}
}

func TestPackageTTLExpiry(t *testing.T) {
	p := NewPlatform(NewRedactor(), &Encryptor{KeyID: "k1"})
	j, _ := p.Request("cust-1", time.Time{})
	if err := p.Run(j.ID, mkEvents()); err != nil {
		t.Fatal(err)
	}
	after := j.ExpiresAt.Add(time.Minute)
	if _, err := p.Download(j.ID, after); err != ErrPackageStale {
		t.Fatalf("expired package must fail: %v", err)
	}
	if j.State != StateExpired {
		t.Fatalf("job must move to EXPIRED, got %s", j.State)
	}
}

func TestFailureAndInvalidTransitions(t *testing.T) {
	p := NewPlatform(NewRedactor(), &Encryptor{KeyID: "k1"})
	j, _ := p.Request("cust-1", time.Time{})
	if _, err := p.Download(j.ID, time.Now()); err != ErrNotComplete {
		t.Fatalf("download before completion must fail: %v", err)
	}
	if err := p.Fail(j.ID, "aggregation timeout"); err != nil {
		t.Fatal(err)
	}
	if j.State != StateFailed || j.FailReason == "" {
		t.Fatalf("job must be FAILED with reason: %+v", j)
	}
	if err := p.Run(j.ID, mkEvents()); err == nil {
		t.Fatal("cannot run a failed job")
	}
	if _, err := p.Get("nope"); err != ErrJobNotFound {
		t.Fatalf("unknown job: %v", err)
	}
}

func TestEventsAreSortedInArchive(t *testing.T) {
	p := NewPlatform(NewRedactor(), &Encryptor{KeyID: "k1"})
	j, _ := p.Request("cust-1", time.Time{})
	events := mkEvents()
	// Feed out of order; the archive must still be deterministic.
	events[0], events[2] = events[2], events[0]
	if err := p.Run(j.ID, events); err != nil {
		t.Fatal(err)
	}
	j2, _ := p.Request("cust-1", time.Time{})
	events2 := mkEvents()
	if err := p.Run(j2.ID, events2); err != nil {
		t.Fatal(err)
	}
	if j.Checksum != j2.Checksum {
		t.Fatal("same events in any order must produce identical archive checksum")
	}
	if !strings.HasPrefix(j.Checksum, j.DownloadURL[len(j.DownloadURL)-12:]) {
		t.Fatalf("download URL must carry checksum prefix: %s vs %s", j.DownloadURL, j.Checksum)
	}
}
