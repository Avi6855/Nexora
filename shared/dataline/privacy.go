package dataline

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"
)

// ── 14. Privacy analytics ───────────────────────────────────────────────────

// Tokenisable fields. Only these may enter the vault — anything else is
// rejected so raw identifiers cannot leak through a generic trapdoor.
const (
	FieldName    = "name"
	FieldAccount = "account"
	FieldAddress = "address"
)

// Audit actions recorded in the access log.
const (
	ActionTokenise    = "TOKENISE"
	ActionRotate      = "ROTATE"
	ActionCohortCount = "COHORT_COUNT"
)

func validPrivField(f string) bool {
	switch strings.ToLower(strings.TrimSpace(f)) {
	case FieldName, FieldAccount, FieldAddress:
		return true
	default:
		return false
	}
}

// TokenRecord is one vaulted token. The raw value is never stored.
type TokenRecord struct {
	Token     string    `json:"token"`
	Purpose   string    `json:"purpose"`
	Field     string    `json:"field"`
	CreatedAt time.Time `json:"created_at"`
}

// AuditEntry is one vault access record.
type AuditEntry struct {
	At      time.Time `json:"at"`
	Purpose string    `json:"purpose"`
	Action  string    `json:"action"`
	Field   string    `json:"field,omitempty"`
}

// Vault is a per-purpose HMAC token vault with aggregate-only cohort
// queries. Salts are purpose-bound: rotating a purpose issues a fresh
// salt and drops that purpose's tokens, so old tokens stop resolving.
type Vault struct {
	mu       sync.Mutex
	salts    map[string][]byte
	versions map[string]int
	tokens   map[string]TokenRecord
	audit    []AuditEntry
}

// NewVault builds an empty vault.
func NewVault() *Vault {
	return &Vault{
		salts:    map[string][]byte{},
		versions: map[string]int{},
		tokens:   map[string]TokenRecord{},
	}
}

func newSalt() ([]byte, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	return b, nil
}

// Tokenise HMAC-tokenises one raw value for a purpose. The mapping is
// deterministic per salt, so the same (purpose, field, raw) yields the
// same token until the purpose rotates.
func (v *Vault) Tokenise(purpose, field, raw string) (string, error) {
	if strings.TrimSpace(purpose) == "" {
		return "", fmt.Errorf("%w: purpose is required", ErrInvalid)
	}
	if !validPrivField(field) {
		return "", fmt.Errorf("%w: field must be one of name, account, address", ErrInvalid)
	}
	if strings.TrimSpace(raw) == "" {
		return "", fmt.Errorf("%w: value is required", ErrInvalid)
	}
	field = strings.ToLower(strings.TrimSpace(field))
	v.mu.Lock()
	defer v.mu.Unlock()
	salt, ok := v.salts[purpose]
	if !ok {
		var err error
		salt, err = newSalt()
		if err != nil {
			return "", err
		}
		v.salts[purpose] = salt
		v.versions[purpose] = 1
	}
	mac := hmac.New(sha256.New, append(salt, []byte(field)...))
	mac.Write([]byte(raw))
	token := hex.EncodeToString(mac.Sum(nil))
	if _, exists := v.tokens[token]; !exists {
		v.tokens[token] = TokenRecord{
			Token:     token,
			Purpose:   purpose,
			Field:     field,
			CreatedAt: time.Now().UTC(),
		}
	}
	v.audit = append(v.audit, AuditEntry{At: time.Now().UTC(), Purpose: purpose, Action: ActionTokenise, Field: field})
	return token, nil
}

// RotatePurpose issues a fresh salt for a purpose and invalidates every
// token minted under the old salt.
func (v *Vault) RotatePurpose(purpose string) error {
	if strings.TrimSpace(purpose) == "" {
		return fmt.Errorf("%w: purpose is required", ErrInvalid)
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	salt, err := newSalt()
	if err != nil {
		return err
	}
	v.salts[purpose] = salt
	v.versions[purpose]++
	for tok, rec := range v.tokens {
		if rec.Purpose == purpose {
			delete(v.tokens, tok)
		}
	}
	v.audit = append(v.audit, AuditEntry{At: time.Now().UTC(), Purpose: purpose, Action: ActionRotate})
	return nil
}

// Valid reports whether a token is live (false after its purpose rotates).
func (v *Vault) Valid(token string) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	_, ok := v.tokens[token]
	return ok
}

// CohortCount returns the aggregate count of live tokens for a purpose,
// optionally narrowed to one field. Raw values are never returned — the
// count is the only observable, which keeps queries aggregate-only.
func (v *Vault) CohortCount(purpose, field string) (int, error) {
	if strings.TrimSpace(purpose) == "" {
		return 0, fmt.Errorf("%w: purpose is required", ErrInvalid)
	}
	if field != "" && !validPrivField(field) {
		return 0, fmt.Errorf("%w: field must be one of name, account, address", ErrInvalid)
	}
	field = strings.ToLower(strings.TrimSpace(field))
	v.mu.Lock()
	defer v.mu.Unlock()
	n := 0
	for _, rec := range v.tokens {
		if rec.Purpose != purpose {
			continue
		}
		if field != "" && rec.Field != field {
			continue
		}
		n++
	}
	v.audit = append(v.audit, AuditEntry{At: time.Now().UTC(), Purpose: purpose, Action: ActionCohortCount, Field: field})
	return n, nil
}

// AuditLog returns a copy of the access audit entries in capture order.
func (v *Vault) AuditLog() []AuditEntry {
	v.mu.Lock()
	defer v.mu.Unlock()
	return append([]AuditEntry(nil), v.audit...)
}
