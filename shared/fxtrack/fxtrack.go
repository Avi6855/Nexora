// Package fxtrack implements international payment corridor tracking as a
// tested shared library.
//
// Corridor stages (in order):
// CREATED → COMPLIANCE → FX_CONVERSION → CORRESPONDENT → SWIFT_PROCESSING →
// RECEIVING_BANK → CREDITED
//
// Each stage carries an SLA (timeout escalation when the stage overruns),
// plus p50/p95 durations used for ETA window computation. Delay attribution
// reports the current stage plus its overrun.
package fxtrack

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// Stage is one corridor step.
type Stage string

const (
	StageCreated         Stage = "CREATED"
	StageCompliance      Stage = "COMPLIANCE"
	StageFXConversion    Stage = "FX_CONVERSION"
	StageCorrespondent   Stage = "CORRESPONDENT"
	StageSwiftProcessing Stage = "SWIFT_PROCESSING"
	StageReceivingBank   Stage = "RECEIVING_BANK"
	StageCredited        Stage = "CREDITED"
)

// CorridorStages is the ordered stage list.
var CorridorStages = []Stage{
	StageCreated,
	StageCompliance,
	StageFXConversion,
	StageCorrespondent,
	StageSwiftProcessing,
	StageReceivingBank,
	StageCredited,
}

type stageProfile struct {
	SLA time.Duration
	P50 time.Duration
	P95 time.Duration
}

// stageProfiles encodes per-stage SLA + ETA percentiles.
var stageProfiles = map[Stage]stageProfile{
	StageCreated:         {SLA: 5 * time.Minute, P50: time.Minute, P95: 5 * time.Minute},
	StageCompliance:      {SLA: 2 * time.Hour, P50: 30 * time.Minute, P95: 2 * time.Hour},
	StageFXConversion:    {SLA: 30 * time.Minute, P50: 5 * time.Minute, P95: 30 * time.Minute},
	StageCorrespondent:   {SLA: 12 * time.Hour, P50: 4 * time.Hour, P95: 12 * time.Hour},
	StageSwiftProcessing: {SLA: 12 * time.Hour, P50: 6 * time.Hour, P95: 12 * time.Hour},
	StageReceivingBank:   {SLA: 24 * time.Hour, P50: 8 * time.Hour, P95: 24 * time.Hour},
	StageCredited:        {SLA: 0, P50: 0, P95: 0},
}

var (
	// ErrTransferNotFound marks unknown transfer IDs.
	ErrTransferNotFound = errors.New("fx transfer not found")
	// ErrTransferExists marks duplicate transfer IDs.
	ErrTransferExists = errors.New("fx transfer already exists")
	// ErrTransferComplete marks advances past CREDITED.
	ErrTransferComplete = errors.New("fx transfer already credited")
)

// StageVisit is one timeline entry.
type StageVisit struct {
	Stage     Stage     `json:"stage"`
	EnteredAt time.Time `json:"entered_at"`
	ExitedAt  time.Time `json:"exited_at,omitempty"`
	Overrun   bool      `json:"overrun"`
}

// Transfer is the tracked cross-border payment.
type Transfer struct {
	ID           string       `json:"id"`
	Corridor     string       `json:"corridor"`
	AmountMinor  int64        `json:"amount_minor"`
	CreatedAt    time.Time    `json:"created_at"`
	CurrentStage Stage        `json:"current_stage"`
	StageEntered time.Time    `json:"stage_entered_at"`
	History      []StageVisit `json:"history"`
	Escalated    bool         `json:"escalated"`
	Escalations  []string     `json:"escalations,omitempty"`
}

// Tracker stores tracked transfers.
type Tracker struct {
	mu        sync.Mutex
	transfers map[string]*Transfer
}

// NewTracker returns an empty tracker.
func NewTracker() *Tracker {
	return &Tracker{transfers: make(map[string]*Transfer)}
}

func stageIndex(s Stage) int {
	for i, st := range CorridorStages {
		if st == s {
			return i
		}
	}
	return -1
}

// StartTransfer begins tracking a transfer at CREATED.
func (t *Tracker) StartTransfer(id, corridor string, amountMinor int64, now time.Time) (*Transfer, error) {
	if id == "" {
		return nil, fmt.Errorf("transfer id is required")
	}
	if corridor == "" {
		return nil, fmt.Errorf("corridor is required")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.transfers[id]; ok {
		return nil, ErrTransferExists
	}
	tr := &Transfer{
		ID:           id,
		Corridor:     corridor,
		AmountMinor:  amountMinor,
		CreatedAt:    now,
		CurrentStage: StageCreated,
		StageEntered: now,
		History:      []StageVisit{{Stage: StageCreated, EnteredAt: now}},
	}
	t.transfers[id] = tr
	return copyTransfer(tr), nil
}

// Get returns one transfer copy.
func (t *Tracker) Get(id string) (*Transfer, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	tr, ok := t.transfers[id]
	if !ok {
		return nil, ErrTransferNotFound
	}
	return copyTransfer(tr), nil
}

// AdvanceStage moves the transfer one stage forward, flagging timeout
// escalation when the exited stage overran its SLA.
func (t *Tracker) AdvanceStage(id string, now time.Time) (*Transfer, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	tr, ok := t.transfers[id]
	if !ok {
		return nil, ErrTransferNotFound
	}
	idx := stageIndex(tr.CurrentStage)
	if idx < 0 {
		return nil, fmt.Errorf("unknown stage %q", tr.CurrentStage)
	}
	if tr.CurrentStage == StageCredited {
		return nil, ErrTransferComplete
	}
	// Timeout escalation: leaving a stage after its SLA marks the transfer
	// escalated and records the overrun on the timeline entry.
	prof := stageProfiles[tr.CurrentStage]
	if prof.SLA > 0 && now.Sub(tr.StageEntered) > prof.SLA {
		tr.Escalated = true
		msg := fmt.Sprintf("stage %s overran SLA %s", tr.CurrentStage, prof.SLA)
		tr.Escalations = append(tr.Escalations, msg)
		for i := range tr.History {
			if tr.History[i].Stage == tr.CurrentStage && tr.History[i].ExitedAt.IsZero() {
				tr.History[i].Overrun = true
			}
		}
	}
	next := CorridorStages[idx+1]
	// Close the current visit.
	for i := range tr.History {
		if tr.History[i].Stage == tr.CurrentStage && tr.History[i].ExitedAt.IsZero() {
			tr.History[i].ExitedAt = now
		}
	}
	tr.CurrentStage = next
	tr.StageEntered = now
	tr.History = append(tr.History, StageVisit{Stage: next, EnteredAt: now})
	return copyTransfer(tr), nil
}

// ETAWindow computes the ETA window from `now`: earliest = now + remaining
// p50, latest = now + remaining p95. Elapsed time in the current stage is
// credited against that stage's percentiles (clamped at zero).
func (t *Tracker) ETAWindow(id string, now time.Time) (earliest, latest time.Time, err error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	tr, ok := t.transfers[id]
	if !ok {
		return time.Time{}, time.Time{}, ErrTransferNotFound
	}
	if tr.CurrentStage == StageCredited {
		return now, now, nil
	}
	idx := stageIndex(tr.CurrentStage)
	elapsed := now.Sub(tr.StageEntered)
	if elapsed < 0 {
		elapsed = 0
	}
	var remP50, remP95 time.Duration
	for i := idx; i < len(CorridorStages); i++ {
		st := CorridorStages[i]
		prof := stageProfiles[st]
		if st == StageCredited {
			continue
		}
		if i == idx {
			if r := prof.P50 - elapsed; r > 0 {
				remP50 += r
			}
			if r := prof.P95 - elapsed; r > 0 {
				remP95 += r
			}
			continue
		}
		remP50 += prof.P50
		remP95 += prof.P95
	}
	return now.Add(remP50), now.Add(remP95), nil
}

// DelayInfo attributes delay to the current stage plus its overrun.
type DelayInfo struct {
	Stage     Stage         `json:"stage"`
	Delayed   bool          `json:"delayed"`
	Overrun   time.Duration `json:"overrun_ns"`
	SLA       time.Duration `json:"sla_ns"`
	Escalated bool          `json:"escalated"`
}

// DelayedAt attributes delay at `now`. When the current stage has overrun
// its SLA the transfer is marked escalated (timeout escalation is sticky).
func (t *Tracker) DelayedAt(id string, now time.Time) (DelayInfo, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	tr, ok := t.transfers[id]
	if !ok {
		return DelayInfo{}, ErrTransferNotFound
	}
	prof := stageProfiles[tr.CurrentStage]
	if prof.SLA == 0 {
		return DelayInfo{Stage: tr.CurrentStage, Escalated: tr.Escalated}, nil
	}
	elapsed := now.Sub(tr.StageEntered)
	if elapsed > prof.SLA {
		tr.Escalated = true
		msg := fmt.Sprintf("stage %s overran SLA %s", tr.CurrentStage, prof.SLA)
		already := false
		for _, e := range tr.Escalations {
			if e == msg {
				already = true
			}
		}
		if !already {
			tr.Escalations = append(tr.Escalations, msg)
		}
		return DelayInfo{Stage: tr.CurrentStage, Delayed: true, Overrun: elapsed - prof.SLA, SLA: prof.SLA, Escalated: true}, nil
	}
	return DelayInfo{Stage: tr.CurrentStage, Delayed: false, SLA: prof.SLA, Escalated: tr.Escalated}, nil
}

// Timeline returns the stage visit history with the current stage last.
func (t *Tracker) Timeline(id string) ([]StageVisit, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	tr, ok := t.transfers[id]
	if !ok {
		return nil, ErrTransferNotFound
	}
	out := append([]StageVisit(nil), tr.History...)
	return out, nil
}

func copyTransfer(tr *Transfer) *Transfer {
	cp := *tr
	cp.History = append([]StageVisit(nil), tr.History...)
	cp.Escalations = append([]string(nil), tr.Escalations...)
	return &cp
}
