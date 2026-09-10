// Package cardnet is Nexora's Card Network Message Gateway.
//
// Network messages (ISO 8583-flavoured) arrive in multiple format versions.
// This package parses them into ONE canonical internal representation that
// every downstream service consumes, so a new network message version is a
// parser concern, not a platform-wide migration.
//
// It also hosts:
//   - the Replay/Simulation Lab (deterministic message replay at scale),
//   - the Correlation Engine (app→payment→network→processor→settlement graph),
//   - the Advice Processor (out-of-order follow-up message sequencing).
package cardnet

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ── Canonical message model ─────────────────────────────────────────────────

// MessageType enumerates the network message lifecycle.
type MessageType string

const (
	MsgAuth         MessageType = "AUTH"     // 0100 authorization request
	MsgAuthResponse MessageType = "AUTH_RSP" // 0110 issuer response
	MsgReversal     MessageType = "REVERSAL" // 0400 (full or partial)
	MsgCapture      MessageType = "CAPTURE"  // 0200 presentment
	MsgRefund       MessageType = "REFUND"   // 0200 with credit indicator
	MsgAdvice       MessageType = "ADVICE"   // 0620/0630 asynchronous advice
	MsgDecline      MessageType = "DECLINE"  // negative response
	MsgChargeback   MessageType = "CHARGEBACK"
)

// CanonicalMessage is the single internal representation.
type CanonicalMessage struct {
	// Identity & lifecycle
	ID            string      `json:"id"`
	Type          MessageType `json:"type"`
	MTI           string      `json:"mti"` // raw ISO 8583 message type indicator
	FormatVersion string      `json:"format_version"`

	// Financial core
	AmountMinor  int64  `json:"amount_minor"`
	Currency     string `json:"currency"`
	STAN         string `json:"stan"` // systems trace audit number
	RRN          string `json:"rrn"`  // retrieval reference number
	AuthCode     string `json:"auth_code,omitempty"`
	ResponseCode string `json:"response_code"` // "00" approved, "51" insufficient funds...

	// Parties & context
	AcquirerID string `json:"acquirer_id"`
	IssuerID   string `json:"issuer_id,omitempty"`
	TerminalID string `json:"terminal_id"`
	MerchantID string `json:"merchant_id"`
	MCC        string `json:"mcc"` // merchant category code

	// Timing
	TransmissionTime time.Time `json:"transmission_time"`
	// OfflinePresentment marks transactions captured while the terminal was
	// offline (see OfflineHandler).
	OfflinePresentment bool `json:"offline_presentment,omitempty"`
	// PartialAmount for partial reversals/advice.
	PartialAmount int64 `json:"partial_amount,omitempty"`

	// Raw retains the original message for audit/replay.
	Raw map[string]string `json:"raw,omitempty"`
}

// Approved reports the issuer/network decision.
func (m *CanonicalMessage) Approved() bool { return m.ResponseCode == "00" }

// ── Parsing ─────────────────────────────────────────────────────────────────

var (
	ErrMalformedMessage = errors.New("malformed network message")
	ErrUnknownVersion   = errors.New("unsupported network message format version")
	ErrUnknownMTI       = errors.New("unrecognised MTI")
)

// Parser turns a raw field-map into CanonicalMessage. Version-aware.
type Parser interface {
	Version() string
	Parse(fields map[string]string) (*CanonicalMessage, error)
}

// Gateway normalises any registered format version.
type Gateway struct {
	mu      sync.RWMutex
	parsers map[string]Parser
}

func NewGateway() *Gateway {
	g := &Gateway{parsers: map[string]Parser{}}
	g.Register(&V1Parser{})
	g.Register(&V2Parser{})
	g.Register(&V3Parser{})
	return g
}

// Register adds a format parser (extensible without touching the gateway).
func (g *Gateway) Register(p Parser) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.parsers[p.Version()] = p
}

// Parse routes by version and validates the canonical result.
func (g *Gateway) Parse(formatVersion string, fields map[string]string) (*CanonicalMessage, error) {
	g.mu.RLock()
	p, ok := g.parsers[formatVersion]
	g.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: %q (known: %v)", ErrUnknownVersion, formatVersion, g.Versions())
	}
	msg, err := p.Parse(fields)
	if err != nil {
		return nil, err
	}
	msg.FormatVersion = formatVersion
	if err := validate(msg); err != nil {
		return nil, err
	}
	return msg, nil
}

// Versions lists registered formats (deterministic).
func (g *Gateway) Versions() []string {
	g.mu.RLock()
	defer g.mu.RUnlock()
	out := make([]string, 0, len(g.parsers))
	for v := range g.parsers {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

func validate(m *CanonicalMessage) error {
	if m.AmountMinor < 0 {
		return fmt.Errorf("%w: negative amount %d", ErrMalformedMessage, m.AmountMinor)
	}
	if m.STAN == "" || m.RRN == "" {
		return fmt.Errorf("%w: STAN and RRN are mandatory correlation fields", ErrMalformedMessage)
	}
	if m.Currency == "" {
		return fmt.Errorf("%w: missing currency", ErrMalformedMessage)
	}
	if m.TerminalID == "" || m.MerchantID == "" {
		return fmt.Errorf("%w: terminal and merchant IDs are mandatory", ErrMalformedMessage)
	}
	return nil
}

// parseAmount handles "123.45" → 12345 minor units for 2-decimal currencies.
func parseAmount(raw string, minorUnits int) (int64, error) {
	if minorUnits < 0 || minorUnits > 4 {
		return 0, fmt.Errorf("unsupported minor units %d", minorUnits)
	}
	parts := strings.SplitN(raw, ".", 2)
	whole, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: amount %q", ErrMalformedMessage, raw)
	}
	frac := int64(0)
	if len(parts) == 2 {
		fs := parts[1]
		if len(fs) > minorUnits {
			fs = fs[:minorUnits] // truncate excess precision (networks send ≤ minor units)
		} else {
			for len(fs) < minorUnits {
				fs += "0"
			}
		}
		if fs != "" {
			frac, err = strconv.ParseInt(fs, 10, 64)
			if err != nil {
				return 0, fmt.Errorf("%w: amount fraction %q", ErrMalformedMessage, raw)
			}
		}
	}
	mult := int64(1)
	for i := 0; i < minorUnits; i++ {
		mult *= 10
	}
	return whole*mult + frac, nil
}

// ── Format v1: flat "DE12"-style keys ───────────────────────────────────────

type V1Parser struct{}

func (V1Parser) Version() string { return "v1" }

func (V1Parser) Parse(f map[string]string) (*CanonicalMessage, error) {
	mti := f["MTI"]
	m := &CanonicalMessage{MTI: mti, Raw: f}
	var err error
	if m.AmountMinor, err = parseAmount(f["DE4"], 2); err != nil {
		return nil, err
	}
	m.Currency = f["DE49"]
	m.STAN = f["DE11"]
	m.RRN = f["DE37"]
	m.AuthCode = f["DE38"]
	m.ResponseCode = f["DE39"]
	m.AcquirerID = f["DE32"]
	m.TerminalID = f["DE41"]
	m.MerchantID = f["DE42"]
	m.MCC = f["DE18"]
	m.Type, err = messageTypeFromMTI(mti)
	if err != nil {
		return nil, err
	}
	if t := f["DE7"]; t != "" {
		if m.TransmissionTime, err = parseNetTime(t); err != nil {
			return nil, err
		}
	} else {
		m.TransmissionTime = time.Now().UTC()
	}
	return m, nil
}

// ── Format v2: named keys, ISO amounts ──────────────────────────────────────

type V2Parser struct{}

func (V2Parser) Version() string { return "v2" }

func (V2Parser) Parse(f map[string]string) (*CanonicalMessage, error) {
	m := &CanonicalMessage{MTI: f["mti"], Raw: f}
	var err error
	if m.AmountMinor, err = parseAmount(f["amount"], 2); err != nil {
		return nil, err
	}
	m.Currency = f["currency"]
	m.STAN = f["stan"]
	m.RRN = f["rrn"]
	m.AuthCode = f["auth_code"]
	m.ResponseCode = f["response_code"]
	m.AcquirerID = f["acquirer"]
	m.TerminalID = f["terminal"]
	m.MerchantID = f["merchant"]
	m.MCC = f["mcc"]
	m.Type, err = messageTypeFromMTI(m.MTI)
	if err != nil {
		return nil, err
	}
	if t := f["transmission_time"]; t != "" {
		if m.TransmissionTime, err = time.Parse(time.RFC3339, t); err != nil {
			return nil, fmt.Errorf("%w: transmission_time %q", ErrMalformedMessage, t)
		}
	} else {
		m.TransmissionTime = time.Now().UTC()
	}
	return m, nil
}

// ── Format v3: nested-ish prefixes, zero-decimal amounts ────────────────────

type V3Parser struct{}

func (V3Parser) Version() string { return "v3" }

func (V3Parser) Parse(f map[string]string) (*CanonicalMessage, error) {
	m := &CanonicalMessage{MTI: f["hdr.mti"], Raw: f}
	// v3 sends amounts pre-scaled in minor units for GBP (2dp).
	amt, err := strconv.ParseInt(f["txn.amount_minor"], 10, 64)
	if err != nil {
		return nil, fmt.Errorf("%w: txn.amount_minor %q", ErrMalformedMessage, f["txn.amount_minor"])
	}
	m.AmountMinor = amt
	m.Currency = f["txn.currency"]
	m.STAN = f["txn.stan"]
	m.RRN = f["txn.rrn"]
	m.AuthCode = f["txn.auth_code"]
	m.ResponseCode = f["txn.response_code"]
	m.AcquirerID = f["party.acquirer"]
	m.TerminalID = f["party.terminal"]
	m.MerchantID = f["party.merchant"]
	m.MCC = f["party.mcc"]
	m.Type, err = messageTypeFromMTI(m.MTI)
	if err != nil {
		return nil, err
	}
	if t := f["hdr.time"]; t != "" {
		if m.TransmissionTime, err = time.Parse(time.RFC3339Nano, t); err != nil {
			return nil, fmt.Errorf("%w: hdr.time %q", ErrMalformedMessage, t)
		}
	} else {
		m.TransmissionTime = time.Now().UTC()
	}
	return m, nil
}

func messageTypeFromMTI(mti string) (MessageType, error) {
	switch mti {
	case "0100":
		return MsgAuth, nil
	case "0110":
		return MsgAuthResponse, nil
	case "0200":
		return MsgCapture, nil
	case "0400":
		return MsgReversal, nil
	case "0620", "0630":
		return MsgAdvice, nil
	case "":
		return "", fmt.Errorf("%w: empty MTI", ErrUnknownMTI)
	default:
		return "", fmt.Errorf("%w: %q", ErrUnknownMTI, mti)
	}
}

// parseNetTime parses DE7 (MMDDhhmmss) network time.
func parseNetTime(de7 string) (time.Time, error) {
	if len(de7) != 10 {
		return time.Time{}, fmt.Errorf("%w: DE7 %q", ErrMalformedMessage, de7)
	}
	now := time.Now().UTC()
	month, e1 := strconv.Atoi(de7[0:2])
	day, e2 := strconv.Atoi(de7[2:4])
	hour, e3 := strconv.Atoi(de7[4:6])
	min, e4 := strconv.Atoi(de7[6:8])
	sec, e5 := strconv.Atoi(de7[8:10])
	if e1 != nil || e2 != nil || e3 != nil || e4 != nil || e5 != nil {
		return time.Time{}, fmt.Errorf("%w: DE7 %q", ErrMalformedMessage, de7)
	}
	// DE7 carries no year: anchor to current year, roll back one year if that
	// lands in the future (year-end messages).
	t := time.Date(now.Year(), time.Month(month), day, hour, min, sec, 0, time.UTC)
	if t.After(now.Add(24 * time.Hour)) {
		t = t.AddDate(-1, 0, 0)
	}
	return t, nil
}
