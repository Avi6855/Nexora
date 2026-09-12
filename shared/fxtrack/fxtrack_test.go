package fxtrack

import (
	"testing"
	"time"
)

var fxStart = time.Date(2026, 5, 1, 9, 0, 0, 0, time.UTC)

func TestCorridorStageOrder(t *testing.T) {
	want := []Stage{StageCreated, StageCompliance, StageFXConversion, StageCorrespondent, StageSwiftProcessing, StageReceivingBank, StageCredited}
	if len(CorridorStages) != len(want) {
		t.Fatalf("stages = %v, want %v", CorridorStages, want)
	}
	for i := range want {
		if CorridorStages[i] != want[i] {
			t.Fatalf("stage %d = %s, want %s", i, CorridorStages[i], want[i])
		}
	}
}

func TestStartAdvanceTimeline(t *testing.T) {
	tr := NewTracker()
	if _, err := tr.StartTransfer("fx-1", "GBP-EUR", 10000, fxStart); err != nil {
		t.Fatalf("start failed: %v", err)
	}
	for _, want := range []Stage{StageCompliance, StageFXConversion, StageCorrespondent, StageSwiftProcessing, StageReceivingBank, StageCredited} {
		got, err := tr.AdvanceStage("fx-1", fxStart.Add(time.Hour))
		if err != nil {
			t.Fatalf("advance failed: %v", err)
		}
		if got.CurrentStage != want {
			t.Fatalf("stage = %s, want %s", got.CurrentStage, want)
		}
	}
	tl, err := tr.Timeline("fx-1")
	if err != nil {
		t.Fatalf("timeline failed: %v", err)
	}
	if len(tl) != len(CorridorStages) {
		t.Fatalf("timeline len = %d, want %d", len(tl), len(CorridorStages))
	}
	if tl[0].Stage != StageCreated || tl[len(tl)-1].Stage != StageCredited {
		t.Fatalf("timeline endpoints wrong: %+v", tl)
	}
	if _, err := tr.AdvanceStage("fx-1", fxStart.Add(2*time.Hour)); err == nil {
		t.Fatalf("advance past CREDITED must fail")
	}
}

func TestTimeoutEscalation(t *testing.T) {
	tr := NewTracker()
	if _, err := tr.StartTransfer("fx-slow", "GBP-USD", 5000, fxStart); err != nil {
		t.Fatalf("start failed: %v", err)
	}
	if _, err := tr.AdvanceStage("fx-slow", fxStart.Add(time.Minute)); err != nil {
		t.Fatalf("advance to COMPLIANCE failed: %v", err)
	}
	// COMPLIANCE SLA is 2h; 5h later the stage has overrun.
	info, err := tr.DelayedAt("fx-slow", fxStart.Add(5*time.Hour))
	if err != nil {
		t.Fatalf("delayedAt failed: %v", err)
	}
	if !info.Delayed {
		t.Fatalf("must be delayed after SLA overrun")
	}
	if info.Stage != StageCompliance {
		t.Fatalf("delay attributed to %s, want COMPLIANCE", info.Stage)
	}
	if info.Overrun != 2*time.Hour+59*time.Minute {
		t.Fatalf("overrun = %s, want 2h59m", info.Overrun)
	}
	if !info.Escalated {
		t.Fatalf("overrun must escalate")
	}
	got, err := tr.Get("fx-slow")
	if err != nil {
		t.Fatalf("get failed: %v", err)
	}
	if !got.Escalated || len(got.Escalations) == 0 {
		t.Fatalf("escalation must be sticky: %+v", got)
	}
	// Advancing out of the overrun stage keeps the escalation + flags the visit.
	adv, err := tr.AdvanceStage("fx-slow", fxStart.Add(5*time.Hour))
	if err != nil {
		t.Fatalf("advance after overrun failed: %v", err)
	}
	if !adv.Escalated {
		t.Fatalf("escalation must persist across advance")
	}
	tl, _ := tr.Timeline("fx-slow")
	foundOverrun := false
	for _, v := range tl {
		if v.Stage == StageCompliance && v.Overrun {
			foundOverrun = true
		}
	}
	if !foundOverrun {
		t.Fatalf("COMPLIANCE visit must be flagged overrun: %+v", tl)
	}
}

func TestETAWindowMath(t *testing.T) {
	tr := NewTracker()
	if _, err := tr.StartTransfer("fx-eta", "GBP-EUR", 1000, fxStart); err != nil {
		t.Fatalf("start failed: %v", err)
	}
	earliest, latest, err := tr.ETAWindow("fx-eta", fxStart)
	if err != nil {
		t.Fatalf("eta failed: %v", err)
	}
	// Remaining p50: 1m + 30m + 5m + 4h + 6h + 8h = 18h36m.
	wantP50 := time.Minute + 30*time.Minute + 5*time.Minute + 4*time.Hour + 6*time.Hour + 8*time.Hour
	// Remaining p95: 5m + 2h + 30m + 12h + 12h + 24h = 50h35m.
	wantP95 := 5*time.Minute + 2*time.Hour + 30*time.Minute + 12*time.Hour + 12*time.Hour + 24*time.Hour
	if got := earliest.Sub(fxStart); got != wantP50 {
		t.Fatalf("p50 window = %s, want %s", got, wantP50)
	}
	if got := latest.Sub(fxStart); got != wantP95 {
		t.Fatalf("p95 window = %s, want %s", got, wantP95)
	}
	if !earliest.Before(latest) {
		t.Fatalf("earliest %s must precede latest %s", earliest, latest)
	}
	// Elapsed time in the current stage is credited: 30s into CREATED leaves
	// 30s of p50 and 4m30s of p95 for that stage.
	e2, l2, err := tr.ETAWindow("fx-eta", fxStart.Add(30*time.Second))
	if err != nil {
		t.Fatalf("eta 2 failed: %v", err)
	}
	if got := e2.Sub(fxStart.Add(30 * time.Second)); got != wantP50-30*time.Second {
		t.Fatalf("credited p50 = %s, want %s", got, wantP50-30*time.Second)
	}
	if got := l2.Sub(fxStart.Add(30 * time.Second)); got != wantP95-30*time.Second {
		t.Fatalf("credited p95 = %s, want %s", got, wantP95-30*time.Second)
	}
}

func TestDelayAttributionCurrentStage(t *testing.T) {
	tr := NewTracker()
	if _, err := tr.StartTransfer("fx-attr", "GBP-JPY", 2000, fxStart); err != nil {
		t.Fatalf("start failed: %v", err)
	}
	// Fresh transfer is not delayed.
	info, err := tr.DelayedAt("fx-attr", fxStart.Add(time.Minute))
	if err != nil {
		t.Fatalf("delayedAt failed: %v", err)
	}
	if info.Delayed || info.Stage != StageCreated {
		t.Fatalf("fresh transfer must not be delayed: %+v", info)
	}
	// CREATED SLA is 5m; 10m later overrun is 5m on CREATED.
	info, err = tr.DelayedAt("fx-attr", fxStart.Add(10*time.Minute))
	if err != nil {
		t.Fatalf("delayedAt 2 failed: %v", err)
	}
	if !info.Delayed || info.Stage != StageCreated || info.Overrun != 5*time.Minute {
		t.Fatalf("attribution wrong: %+v", info)
	}
}
