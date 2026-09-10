// Package cheques implements Nexora's cheque processing intelligence and the
// clearing exception workflow.
//
// Cheque intake is a document-quality problem followed by a payments-lifecycle
// problem:
//
//  1. INTELLIGENCE: an image becomes data (amount, payee, MICR) through OCR,
//     but OCR confidence varies. Downstream decisions must be confidence-aware:
//     a 0.55 amount confidence is a retake, not a clearing instruction. Fraud
//     checks run on the extracted facts (amount anomalies, duplicate presentment,
//     account-name mismatch) and combine into a single score with named reasons.
//
//  2. EXCEPTIONS: normal clearing is a straight line
//     RECEIVED→VALIDATING→SUBMITTED→CLEARING→SETTLED, but the interesting work
//     is the branches — RETURNED, MISMATCH, DUPLICATE, DAMAGED — each with its
//     own automated workflow (retake, hold, refund, kill) instead of a human
//     reading a pile of queue items.
package cheques

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// ---------- Intelligence ----------

// ImageQuality is the capture-quality verdict for a cheque photo.
type ImageQuality struct {
	BlurScore      float64 `json:"blur_score"`      // 0 (sharp) .. 1 (unusable)
	GlareCoverage  float64 `json:"glare_coverage"`  // fraction of the image washed out
	CornersMissing bool    `json:"corners_missing"` // all four corners must be visible
	ResolutionDPI  int     `json:"resolution_dpi"`
}

// Problems lists why the image is not processable, customer-facing.
func (q ImageQuality) Problems() []string {
	var p []string
	if q.BlurScore > 0.4 {
		p = append(p, "the image is blurry")
	}
	if q.GlareCoverage > 0.15 {
		p = append(p, "there is glare over the writing")
	}
	if q.CornersMissing {
		p = append(p, "all four corners of the cheque are not visible")
	}
	if q.ResolutionDPI < 200 {
		p = append(p, "the resolution is too low")
	}
	return p
}

// Acceptable decides whether OCR may run on this image at all.
func (q ImageQuality) Acceptable() bool { return len(q.Problems()) == 0 }

// OCRResult is the raw extraction with per-field confidence.
type OCRResult struct {
	AmountMinor       int64   `json:"amount_minor"`
	AmountConfidence  float64 `json:"amount_confidence"`
	Payee             string  `json:"payee"`
	PayeeConfidence   float64 `json:"payee_confidence"`
	MICRSortCode      string  `json:"micr_sort_code"`
	MICRAccountNumber string  `json:"micr_account_number"`
	MICRValid         bool    `json:"micr_valid"`
}

// Cheque is the domain object under evaluation.
type Cheque struct {
	ID                 string       `json:"id"`
	AccountID          string       `json:"account_id"`
	PayerName          string       `json:"payer_name"`
	DepositAmountMinor int64        `json:"deposit_amount_minor"` // what the app UI was told
	Image              ImageQuality `json:"image"`
	OCR                *OCRResult   `json:"ocr,omitempty"`
	PreviouslySeen     bool         `json:"previously_seen"` // duplicate presentment signal
	AccountAgeDays     int          `json:"account_age_days"`
	At                 time.Time    `json:"at"`
}

// Verdict is the intelligence outcome.
type Verdict struct {
	Action       Action   `json:"action"`
	CustomerMsg  string   `json:"customer_msg"`
	FraudScore   int      `json:"fraud_score"`
	FraudReasons []string `json:"fraud_reasons"`
}

// Action is what the pipeline should do next.
type Action string

const (
	ActionClear      Action = "CLEAR"  // submit for clearing
	ActionRetake     Action = "RETAKE" // image quality too low
	ActionManual     Action = "MANUAL_REVIEW"
	ActionHoldRefund Action = "HOLD_REFUND" // high fraud risk, hold and prepare refund
)

// Confidence thresholds. Amount confidence is deliberately the strictest:
// misreading a number is the costliest OCR failure and the customer checks
// the amount last.
const (
	amountConfidenceGate   = 0.85
	payeeConfidenceGate    = 0.60
	fraudManualReviewScore = 25
	fraudRefundScore       = 50
)

// Evaluate runs image quality → OCR confidence → fraud scoring in order.
// Quality gates fire before fraud: there is no point scoring fraud on an
// amount we may have misread.
func Evaluate(c Cheque) Verdict {
	if !c.Image.Acceptable() {
		return Verdict{
			Action:      ActionRetake,
			CustomerMsg: "Image quality too low — " + strings.Join(c.Image.Problems(), "; ") + ". Please retake.",
		}
	}
	o := c.OCR
	if o == nil {
		return Verdict{Action: ActionRetake, CustomerMsg: "Image quality too low — retake required."}
	}
	if o.AmountConfidence < amountConfidenceGate {
		return Verdict{
			Action:      ActionRetake,
			CustomerMsg: "We couldn't clearly read the amount — please retake the photo.",
		}
	}
	// MICR is the machine-printed line; if it disagrees with itself the
	// instrument is suspect regardless of the handwriting.
	if !o.MICRValid {
		return Verdict{Action: ActionManual, CustomerMsg: "Your cheque is being reviewed — we'll update you within 1 working day."}
	}
	// Amount in the image must agree with what the customer typed.
	if o.AmountMinor != c.DepositAmountMinor {
		return Verdict{
			Action:       ActionManual,
			CustomerMsg:  "The amount on the cheque doesn't match what you entered — we're reviewing it.",
			FraudScore:   25,
			FraudReasons: []string{"entered amount differs from image amount"},
		}
	}

	score := 0
	var reasons []string
	if c.PreviouslySeen {
		score += 60 // definite duplicate: hold outright
		reasons = append(reasons, "cheque previously presented (duplicate presentment)")
	}
	if o.PayeeConfidence < payeeConfidenceGate {
		score += 10
		reasons = append(reasons, "payee legibility below threshold")
	}
	if c.AccountAgeDays < 14 {
		score += 15
		reasons = append(reasons, "account under 14 days old")
	}
	if o.AmountMinor >= 500_00 {
		score += 10
		reasons = append(reasons, "high-value cheque")
	}

	v := Verdict{FraudScore: score, FraudReasons: reasons}
	switch {
	case score >= fraudRefundScore:
		v.Action = ActionHoldRefund
		v.CustomerMsg = "This deposit is on hold while we verify it."
	case score >= fraudManualReviewScore:
		v.Action = ActionManual
		v.CustomerMsg = "Your cheque is being reviewed — we'll update you within 1 working day."
	default:
		v.Action = ActionClear
		v.CustomerMsg = "Cheque accepted — funds will be visible after clearing."
	}
	return v
}

// ---------- Clearing lifecycle & exceptions ----------

// State is the clearing lifecycle state.
type State string

const (
	StateReceived   State = "RECEIVED"
	StateValidating State = "VALIDATING"
	StateSubmitted  State = "SUBMITTED"
	StateClearing   State = "CLEARING"
	StateSettled    State = "SETTLED"
	StateReturned   State = "RETURNED"
	StateMismatch   State = "MISMATCH"
	StateDuplicate  State = "DUPLICATE"
	StateDamaged    State = "DAMAGED"
	StateKilled     State = "KILLED"
)

// ExceptionKind enumerates the branch flows off the happy path.
type ExceptionKind string

const (
	ExceptReturned  ExceptionKind = "RETURNED"  // payer bank refused
	ExceptMismatch  ExceptionKind = "MISMATCH"  // amount/payee disagree
	ExceptDuplicate ExceptionKind = "DUPLICATE" // same cheque twice
	ExceptDamaged   ExceptionKind = "DAMAGED"   // physical damage detected at clearing
)

// TransitionError is returned for illegal lifecycle moves.
var ErrIllegalTransition = errors.New("illegal cheque state transition")

// workflow is the automated per-exception playbook.
type workflow struct {
	terminal  State
	notify    string
	automated []string // steps the platform performs, in order
}

var workflows = map[ExceptionKind]workflow{
	ExceptReturned: {
		terminal: StateReturned,
		notify:   "Cheque returned by the paying bank — funds will be withdrawn.",
		automated: []string{
			"reverse the provisional credit",
			"notify customer with return reason code",
			"offer dispute flow if customer contests",
		},
	},
	ExceptMismatch: {
		terminal: StateMismatch,
		notify:   "Details on the cheque need checking — we've paused clearing.",
		automated: []string{
			"freeze clearing",
			"queue for image-vs-entry comparison",
			"release to clearing on resolution",
		},
	},
	ExceptDuplicate: {
		terminal: StateDuplicate,
		notify:   "This cheque looks like one we've already paid in.",
		automated: []string{
			"block the second presentment",
			"link both records to one lifecycle",
			"notify customer",
		},
	},
	ExceptDamaged: {
		terminal: StateDamaged,
		notify:   "We couldn't process the cheque image — please deposit at a branch.",
		automated: []string{
			"mark physical item damaged",
			"request substitute image or branch deposit",
			"release hold on confirmation",
		},
	},
}

// ChequeLifecycle drives one cheque through clearing with exception branches.
type ChequeLifecycle struct {
	ID           string        `json:"id"`
	State        State         `json:"state"`
	History      []LifeEvent   `json:"history"`
	Exception    ExceptionKind `json:"exception,omitempty"`
	ReturnReason string        `json:"return_reason,omitempty"`
	SettledMinor int64         `json:"settled_minor,omitempty"`
}

// LifeEvent records one transition for audit.
type LifeEvent struct {
	At   time.Time `json:"at"`
	From State     `json:"from"`
	To   State     `json:"to"`
	Note string    `json:"note,omitempty"`
}

// NewLifecycle starts a cheque at RECEIVED.
func NewLifecycle(id string, now time.Time) *ChequeLifecycle {
	l := &ChequeLifecycle{ID: id, State: StateReceived}
	l.History = append(l.History, LifeEvent{At: now, To: StateReceived, Note: "ingested"})
	return l
}

var happyPath = map[State]State{
	StateReceived:   StateValidating,
	StateValidating: StateSubmitted,
	StateSubmitted:  StateClearing,
	StateClearing:   StateSettled,
}

// Advance moves the cheque one happy-path step. Exceptions and settled are
// absorbing; you cannot clear a cheque that has been returned.
func (l *ChequeLifecycle) Advance(now time.Time, note string) error {
	next, ok := happyPath[l.State]
	if !ok {
		return fmt.Errorf("%w: cannot advance from %s", ErrIllegalTransition, l.State)
	}
	l.transition(next, now, note)
	return nil
}

// RaiseException branches into an exception workflow. Duplicate additionally
// links the two presentments via Note for the correlation tooling.
func (l *ChequeLifecycle) RaiseException(kind ExceptionKind, now time.Time, detail string) ([]string, error) {
	wf, ok := workflows[kind]
	if !ok {
		return nil, fmt.Errorf("unknown exception kind %q", kind)
	}
	if l.State == StateSettled || l.State == StateKilled {
		return nil, fmt.Errorf("%w: cannot raise %s on %s", ErrIllegalTransition, kind, l.State)
	}
	l.Exception = kind
	l.transition(wf.terminal, now, detail)
	if kind == ExceptReturned {
		l.ReturnReason = detail
	}
	if kind == ExceptDuplicate {
		l.ReturnReason = detail
	}
	return wf.automated, nil
}

// ResolveAfterException completes an exception workflow:
//   - MISMATCH  → resume clearing (details confirmed)
//   - DUPLICATE → kill the duplicate record
//   - RETURNED  → settle the reversal (funds withdrawn)
//   - DAMAGED   → killed (customer re-deposits via branch)
func (l *ChequeLifecycle) ResolveAfterException(now time.Time, approve bool) error {
	switch l.Exception {
	case ExceptMismatch:
		if !approve {
			l.transition(StateKilled, now, "mismatch unresolved")
			return nil
		}
		l.Exception = ""
		l.transition(StateClearing, now, "mismatch resolved — resumed clearing")
		return nil
	case ExceptDuplicate:
		l.transition(StateKilled, now, "duplicate record closed")
		return nil
	case ExceptReturned:
		if approve {
			l.SettledMinor = 0
			l.transition(StateKilled, now, "reversal settled — funds withdrawn")
		} else {
			l.transition(StateClearing, now, "return contested — dispute opened")
		}
		return nil
	case ExceptDamaged:
		l.transition(StateKilled, now, "damaged item closed — branch deposit advised")
		return nil
	case "":
		return fmt.Errorf("%w: no exception active", ErrIllegalTransition)
	default:
		return fmt.Errorf("unknown exception %q", l.Exception)
	}
}

func (l *ChequeLifecycle) transition(to State, now time.Time, note string) {
	l.History = append(l.History, LifeEvent{At: now, From: l.State, To: to, Note: note})
	l.State = to
}

// Timeline renders the full audit trail, oldest first.
func (l *ChequeLifecycle) Timeline() []LifeEvent {
	out := make([]LifeEvent, len(l.History))
	copy(out, l.History)
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out
}
