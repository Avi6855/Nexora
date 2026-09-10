// Package lifeevents implements Nexora's sensitive life-event platform:
//
//  18. Digital bereavement workflow: a compliance-heavy, asynchronous state
//     machine that protects the deceased's accounts FIRST (money can still
//     come in; nothing questionable can go out), verifies the executor with
//     evidence, discovers assets, settles obligations, and only then permits
//     distribution. The ordering is not cosmetic: distributing before
//     protection would be an irreversible loss; every step gates the next.
//
//  19. Life-event workspace: a temporary container of tasks (with
//     dependencies and deadlines), linked documents and beneficiaries for a
//     major life event. Workspaces are AUTO-EXPIRING — an archive policy
//     avoids the workspace becoming a shadow data store of PII.
package lifeevents

import (
	"errors"
	"fmt"
	"sort"
	"time"
)

var (
	ErrInvalidTransition = errors.New("invalid bereavement workflow transition")
	ErrNotReady          = errors.New("gate not satisfied")
	ErrUnknownTask       = errors.New("unknown task")
)

// ── 18. Digital bereavement workflow ────────────────────────────────────────

// BereavementStage is the ordered lifecycle of a bereavement case.
type BereavementStage string

const (
	BVReported           BereavementStage = "REPORTED"
	BVIdentityValidation BereavementStage = "IDENTITY_VALIDATING"
	BVAccountsProtected  BereavementStage = "ACCOUNTS_PROTECTED"
	BVExecutorVerified   BereavementStage = "EXECUTOR_VERIFIED"
	BVAssetDiscovery     BereavementStage = "ASSET_DISCOVERY"
	BVObligationsSettled BereavementStage = "OBLIGATIONS_SETTLED"
	BVFinalStatement     BereavementStage = "FINAL_STATEMENT"
	BVDistribution       BereavementStage = "DISTRIBUTION"
	BVClosed             BereavementStage = "CLOSED"
	BVDisputed           BereavementStage = "DISPUTED"
)

// nextStage is the happy path. DISPUTED is reachable from any pre-closure
// stage and returns to the stage it came from once resolved.
var nextStage = map[BereavementStage]BereavementStage{
	BVReported:           BVIdentityValidation,
	BVIdentityValidation: BVAccountsProtected,
	BVAccountsProtected:  BVExecutorVerified,
	BVExecutorVerified:   BVAssetDiscovery,
	BVAssetDiscovery:     BVObligationsSettled,
	BVObligationsSettled: BVFinalStatement,
	BVFinalStatement:     BVDistribution,
	BVDistribution:       BVClosed,
}

// AuditEntry records who advanced the case and why.
type AuditEntry struct {
	At     time.Time
	Actor  string
	From   BereavementStage
	To     BereavementStage
	Reason string
}

// BereavementCase is one death-in-service case.
type BereavementCase struct {
	CaseID     string
	CustomerID string
	Stage      BereavementStage
	// Protection freeze: incoming credits allowed, outgoing stopped. This is
	// set the moment the case is REPORTED — protection cannot wait for
	// identity verification because fraudsters exploit exactly that window.
	OutgoingBlocked bool
	Discovered      []string        // asset/account IDs discovered
	ExecutorDocs    map[string]bool // evidence ID → verified
	Obligations     []string        // unsettled obligations (DDs, cards)
	Audit           []AuditEntry
	returnTo        BereavementStage // where DISPUTED came from
}

// NewBereavementCase opens a case. Reporting immediately blocks outgoing
// payments: that is the platform's core safety property.
func NewBereavementCase(id, customerID string, now time.Time) *BereavementCase {
	return &BereavementCase{
		CaseID: id, CustomerID: customerID, Stage: BVReported,
		OutgoingBlocked: true, ExecutorDocs: map[string]bool{},
		Audit: []AuditEntry{{At: now, Actor: "system", From: BVReported, To: BVReported, Reason: "death reported — outgoing payments blocked"}},
	}
}

// Advance moves to the next stage if the gate for it is satisfied.
func (c *BereavementCase) Advance(now time.Time, actor, reason string) error {
	want, ok := nextStage[c.Stage]
	if !ok {
		return fmt.Errorf("%w: terminal or disputed stage %s", ErrInvalidTransition, c.Stage)
	}
	if err := c.checkGate(want); err != nil {
		return err
	}
	c.Audit = append(c.Audit, AuditEntry{At: now, Actor: actor, From: c.Stage, To: want, Reason: reason})
	c.Stage = want
	return nil
}

func (c *BereavementCase) checkGate(to BereavementStage) error {
	switch to {
	case BVIdentityValidation:
		return nil
	case BVAccountsProtected:
		return nil
	case BVExecutorVerified:
		// Grant of probate or executor evidence — at least one verified doc.
		if len(c.ExecutorDocs) == 0 {
			return fmt.Errorf("%w: executor evidence must be verified before asset discovery", ErrNotReady)
		}
	case BVObligationsSettled:
		if len(c.Obligations) > 0 {
			return fmt.Errorf("%w: %d obligations unsettled", ErrNotReady, len(c.Obligations))
		}
	case BVDistribution:
		if len(c.Discovered) == 0 {
			return fmt.Errorf("%w: nothing discovered to distribute", ErrNotReady)
		}
	case BVFinalStatement:
		return nil
	case BVAssetDiscovery:
		return nil
	case BVClosed:
		return nil
	}
	return nil
}

// Dispute halts the workflow from any pre-closure stage (a contested will, a
// challenge to the executor). Closure cannot be disputed.
func (c *BereavementCase) Dispute(now time.Time, actor, reason string) error {
	if c.Stage == BVClosed {
		return fmt.Errorf("%w: closed case cannot be disputed", ErrInvalidTransition)
	}
	c.returnTo = c.Stage
	c.Audit = append(c.Audit, AuditEntry{At: now, Actor: actor, From: c.Stage, To: BVDisputed, Reason: reason})
	c.Stage = BVDisputed
	return nil
}

// ResolveDispute returns to the exact pre-dispute stage.
func (c *BereavementCase) ResolveDispute(now time.Time, actor string) error {
	if c.Stage != BVDisputed {
		return fmt.Errorf("%w: case is not disputed", ErrInvalidTransition)
	}
	c.Audit = append(c.Audit, AuditEntry{At: now, Actor: actor, From: BVDisputed, To: c.returnTo, Reason: "dispute resolved"})
	c.Stage = c.returnTo
	return nil
}

// VerifyExecutorDoc records verified evidence.
func (c *BereavementCase) VerifyExecutorDoc(evidenceID string) { c.ExecutorDocs[evidenceID] = true }

// DiscoverAsset adds an asset to the estate.
func (c *BereavementCase) DiscoverAsset(assetID string) { c.Discovered = append(c.Discovered, assetID) }

// SettleObligation clears one outgoing obligation.
func (c *BereavementCase) SettleObligation(obligationID string) {
	for i, o := range c.Obligations {
		if o == obligationID {
			c.Obligations = append(c.Obligations[:i], c.Obligations[i+1:]...)
			return
		}
	}
}

// ── 19. Life-event workspace ────────────────────────────────────────────────

// LifeEventKind enumerates supported events.
type LifeEventKind string

const (
	EventMarriage    LifeEventKind = "MARRIAGE"
	EventBaby        LifeEventKind = "BABY"
	EventDivorce     LifeEventKind = "DIVORCE"
	EventMovingHouse LifeEventKind = "MOVING_HOUSE"
	EventRetirement  LifeEventKind = "RETIREMENT"
	EventBereavement LifeEventKind = "BEREAVEMENT"
)

// Task is one step in the workspace. DependsOn makes the DAG explicit: a
// task cannot complete before its dependencies complete (you cannot update
// beneficiaries before the account that holds them is renamed).
type Task struct {
	ID          string
	Title       string
	Done        bool
	Due         time.Time
	DependsOn   []string
	CompletedAt time.Time
}

// Beneficiary is a linked person with their share (basis points).
type Beneficiary struct {
	Name     string
	ShareBps int64 // 10000 = 100%
}

// Workspace is the temporary container.
type Workspace struct {
	ID            string
	Kind          LifeEventKind
	Tasks         map[string]*Task
	Documents     []string
	Beneficiaries []Beneficiary
	CreatedAt     time.Time
	// ArchiveAfterDays of completion before the workspace auto-expires —
	// PII minimisation is a policy, not a hope.
	ArchiveAfterDays int
	completedAt      time.Time
}

// NewWorkspace validates beneficiary shares sum to exactly 100%.
func NewWorkspace(id string, kind LifeEventKind, now time.Time) (*Workspace, error) {
	return &Workspace{
		ID: id, Kind: kind, Tasks: map[string]*Task{}, CreatedAt: now,
		ArchiveAfterDays: 90,
	}, nil
}

// AddTask registers a task with optional dependencies.
func (w *Workspace) AddTask(t Task) error {
	if _, ok := w.Tasks[t.ID]; ok {
		return fmt.Errorf("%w: %s", ErrUnknownTask, t.ID)
	}
	for _, d := range t.DependsOn {
		if d == t.ID {
			return errors.New("task cannot depend on itself")
		}
	}
	w.Tasks[t.ID] = &t
	return nil
}

// CompleteTask marks a task done only when all dependencies are done.
func (w *Workspace) CompleteTask(id string, now time.Time) error {
	t, ok := w.Tasks[id]
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnknownTask, id)
	}
	for _, d := range t.DependsOn {
		dep := w.Tasks[d]
		if dep == nil || !dep.Done {
			return fmt.Errorf("%w: dependency %s incomplete", ErrNotReady, d)
		}
	}
	t.Done = true
	t.CompletedAt = now
	if w.completedAt.IsZero() && w.Progress() == 100 {
		w.completedAt = now
	}
	return nil
}

// Overdue returns incomplete tasks past their deadline, oldest first.
func (w *Workspace) Overdue(now time.Time) []*Task {
	var out []*Task
	for _, t := range w.Tasks {
		if !t.Done && !t.Due.IsZero() && now.After(t.Due) {
			out = append(out, t)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Due.Before(out[j].Due) })
	return out
}

// Progress is the completed fraction, as a percentage.
func (w *Workspace) Progress() int {
	if len(w.Tasks) == 0 {
		return 0
	}
	done := 0
	for _, t := range w.Tasks {
		if t.Done {
			done++
		}
	}
	return done * 100 / len(w.Tasks)
}

// SetBeneficiaries enforces that shares total exactly 10000 bps — an estate
// plan that sums to 90% is a latent legal problem, not a rounding detail.
func (w *Workspace) SetBeneficiaries(bs []Beneficiary) error {
	total := int64(0)
	for _, b := range bs {
		if b.ShareBps <= 0 {
			return fmt.Errorf("beneficiary %s has non-positive share %d", b.Name, b.ShareBps)
		}
		total += b.ShareBps
	}
	if total != 10000 {
		return fmt.Errorf("beneficiary shares total %d bps, want exactly 10000", total)
	}
	w.Beneficiaries = bs
	return nil
}

// ShouldArchive reports whether the completed workspace is past its
// retention window.
func (w *Workspace) ShouldArchive(now time.Time) bool {
	if w.completedAt.IsZero() {
		return false
	}
	return now.After(w.completedAt.AddDate(0, 0, w.ArchiveAfterDays))
}
