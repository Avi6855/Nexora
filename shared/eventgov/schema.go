package eventgov

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

// ── 12. Schema compatibility ────────────────────────────────────────────────

// CompatibilityMode selects the direction a consumer compatibility check
// runs in.
type CompatibilityMode string

const (
	// ModeBackward: new schema can read data written by the old schema.
	// Removing fields is fine; adding a REQUIRED field or changing a
	// field type breaks old data.
	ModeBackward CompatibilityMode = "BACKWARD"
	// ModeForward: old schema can read data written by the new schema.
	// Adding fields is fine; removing a REQUIRED field or changing a
	// field type breaks new data for old readers.
	ModeForward CompatibilityMode = "FORWARD"
	// ModeFull requires both directions at once.
	ModeFull CompatibilityMode = "FULL"
)

// Field is one named, typed column of a producer schema.
type Field struct {
	Name       string `json:"name"`
	Type       string `json:"type"`
	Required   bool   `json:"required"`
	Deprecated bool   `json:"deprecated,omitempty"`
}

// Schema is a versioned producer contract for one topic.
type Schema struct {
	Topic   string  `json:"topic"`
	Version int     `json:"version"`
	Fields  []Field `json:"fields"`
}

// CompatibilityResult is the verdict of one check.
type CompatibilityResult struct {
	Compatible bool              `json:"compatible"`
	Reason     string            `json:"reason"`
	Mode       CompatibilityMode `json:"mode"`
}

// MigrationPlan suggests how to move from one version to another.
// Additive-only changes (optional fields added, nothing removed or
// retyped) are SAFE; anything else is UNSAFE and needs review.
type MigrationPlan struct {
	Topic       string   `json:"topic"`
	FromVersion int      `json:"from_version"`
	ToVersion   int      `json:"to_version"`
	Added       []Field  `json:"added"`
	Removed     []Field  `json:"removed"`
	TypeChanged []string `json:"type_changed"`
	Safe        bool     `json:"safe"`
	Verdict     string   `json:"verdict"`
}

// Registry stores versioned producer schemas per topic.
type Registry struct {
	mu      sync.Mutex
	schemas map[string]map[int]*Schema
}

// NewRegistry builds an empty schema registry.
func NewRegistry() *Registry {
	return &Registry{schemas: map[string]map[int]*Schema{}}
}

// RegisterSchema stores a new producer schema version. Re-registering the
// same topic+version is a conflict.
func (r *Registry) RegisterSchema(topic string, version int, fields []Field) (*Schema, error) {
	if strings.TrimSpace(topic) == "" {
		return nil, fmt.Errorf("%w: topic is required", ErrInvalid)
	}
	if version <= 0 {
		return nil, fmt.Errorf("%w: version must be positive", ErrInvalid)
	}
	if len(fields) == 0 {
		return nil, fmt.Errorf("%w: at least one field is required", ErrInvalid)
	}
	seen := map[string]bool{}
	for _, f := range fields {
		if strings.TrimSpace(f.Name) == "" {
			return nil, fmt.Errorf("%w: field name is required", ErrInvalid)
		}
		if strings.TrimSpace(f.Type) == "" {
			return nil, fmt.Errorf("%w: field %q type is required", ErrInvalid, f.Name)
		}
		if seen[f.Name] {
			return nil, fmt.Errorf("%w: duplicate field %q", ErrInvalid, f.Name)
		}
		seen[f.Name] = true
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	byVer, ok := r.schemas[topic]
	if !ok {
		byVer = map[int]*Schema{}
		r.schemas[topic] = byVer
	}
	if _, exists := byVer[version]; exists {
		return nil, fmt.Errorf("schema %s v%d: %w", topic, version, ErrConflict)
	}
	cp := make([]Field, len(fields))
	copy(cp, fields)
	s := &Schema{Topic: topic, Version: version, Fields: cp}
	byVer[version] = s
	out := *s
	out.Fields = append([]Field(nil), s.Fields...)
	return &out, nil
}

// GetSchema returns one stored version.
func (r *Registry) GetSchema(topic string, version int) (*Schema, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	byVer, ok := r.schemas[topic]
	if !ok {
		return nil, fmt.Errorf("topic %q: %w", topic, ErrNotFound)
	}
	s, ok := byVer[version]
	if !ok {
		return nil, fmt.Errorf("schema %s v%d: %w", topic, version, ErrNotFound)
	}
	out := *s
	out.Fields = append([]Field(nil), s.Fields...)
	return &out, nil
}

// CheckCompatibility verifies that version newVersion can be deployed
// against data written for oldVersion under the given mode.
func (r *Registry) CheckCompatibility(topic string, oldVersion, newVersion int, mode CompatibilityMode) (*CompatibilityResult, error) {
	switch mode {
	case ModeBackward, ModeForward, ModeFull:
	default:
		return nil, fmt.Errorf("%w: mode must be BACKWARD, FORWARD or FULL", ErrInvalid)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	byVer, ok := r.schemas[topic]
	if !ok {
		return nil, fmt.Errorf("topic %q: %w", topic, ErrNotFound)
	}
	oldS, ok := byVer[oldVersion]
	if !ok {
		return nil, fmt.Errorf("schema %s v%d: %w", topic, oldVersion, ErrNotFound)
	}
	newS, ok := byVer[newVersion]
	if !ok {
		return nil, fmt.Errorf("schema %s v%d: %w", topic, newVersion, ErrNotFound)
	}
	oldByName := fieldMap(oldS.Fields)
	newByName := fieldMap(newS.Fields)

	backwardOK, backwardReason := checkBackward(oldByName, newByName)
	forwardOK, forwardReason := checkForward(oldByName, newByName)

	switch mode {
	case ModeBackward:
		return &CompatibilityResult{Compatible: backwardOK, Reason: backwardReason, Mode: mode}, nil
	case ModeForward:
		return &CompatibilityResult{Compatible: forwardOK, Reason: forwardReason, Mode: mode}, nil
	default: // FULL
		if !backwardOK {
			return &CompatibilityResult{Compatible: false, Reason: "FULL blocked (backward): " + backwardReason, Mode: mode}, nil
		}
		if !forwardOK {
			return &CompatibilityResult{Compatible: false, Reason: "FULL blocked (forward): " + forwardReason, Mode: mode}, nil
		}
		return &CompatibilityResult{Compatible: true, Reason: "fully compatible: additive optional changes only", Mode: mode}, nil
	}
}

// checkBackward: new readers over old data. New REQUIRED fields with no
// old counterpart break; type changes break; removals are tolerated.
func checkBackward(oldByName, newByName map[string]Field) (bool, string) {
	for name, nf := range newByName {
		of, existed := oldByName[name]
		if !existed {
			if nf.Required {
				return false, fmt.Sprintf("BACKWARD incompatible: new required field %q has no value in old data", name)
			}
			continue
		}
		if of.Type != nf.Type {
			return false, fmt.Sprintf("BACKWARD incompatible: field %q changed type %s -> %s", name, of.Type, nf.Type)
		}
	}
	return true, "backward compatible: old data readable by new schema"
}

// checkForward: old readers over new data. Removed REQUIRED fields break;
// type changes break; additions are tolerated (old readers ignore them).
func checkForward(oldByName, newByName map[string]Field) (bool, string) {
	for name, of := range oldByName {
		nf, stillThere := newByName[name]
		if !stillThere {
			if of.Required {
				return false, fmt.Sprintf("FORWARD incompatible: required field %q removed in new schema", name)
			}
			continue
		}
		if of.Type != nf.Type {
			return false, fmt.Sprintf("FORWARD incompatible: field %q changed type %s -> %s", name, of.Type, nf.Type)
		}
	}
	return true, "forward compatible: new data readable by old schema"
}

// DeprecateField marks one field deprecated on one stored version.
func (r *Registry) DeprecateField(topic string, version int, field string) error {
	if strings.TrimSpace(field) == "" {
		return fmt.Errorf("%w: field is required", ErrInvalid)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	byVer, ok := r.schemas[topic]
	if !ok {
		return fmt.Errorf("topic %q: %w", topic, ErrNotFound)
	}
	s, ok := byVer[version]
	if !ok {
		return fmt.Errorf("schema %s v%d: %w", topic, version, ErrNotFound)
	}
	for i := range s.Fields {
		if s.Fields[i].Name == field {
			s.Fields[i].Deprecated = true
			return nil
		}
	}
	return fmt.Errorf("field %q in %s v%d: %w", field, topic, version, ErrNotFound)
}

// MigrationPlan diffs two stored versions and grades the move.
func (r *Registry) MigrationPlan(topic string, fromVersion, toVersion int) (*MigrationPlan, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	byVer, ok := r.schemas[topic]
	if !ok {
		return nil, fmt.Errorf("topic %q: %w", topic, ErrNotFound)
	}
	from, ok := byVer[fromVersion]
	if !ok {
		return nil, fmt.Errorf("schema %s v%d: %w", topic, fromVersion, ErrNotFound)
	}
	to, ok := byVer[toVersion]
	if !ok {
		return nil, fmt.Errorf("schema %s v%d: %w", topic, toVersion, ErrNotFound)
	}
	fromByName := fieldMap(from.Fields)
	toByName := fieldMap(to.Fields)
	plan := &MigrationPlan{Topic: topic, FromVersion: fromVersion, ToVersion: toVersion}
	for name, tf := range toByName {
		ff, existed := fromByName[name]
		if !existed {
			plan.Added = append(plan.Added, tf)
			continue
		}
		if ff.Type != tf.Type {
			plan.TypeChanged = append(plan.TypeChanged, name)
		}
	}
	for name, ff := range fromByName {
		if _, stillThere := toByName[name]; !stillThere {
			plan.Removed = append(plan.Removed, ff)
		}
	}
	sort.Slice(plan.Added, func(i, j int) bool { return plan.Added[i].Name < plan.Added[j].Name })
	sort.Slice(plan.Removed, func(i, j int) bool { return plan.Removed[i].Name < plan.Removed[j].Name })
	sort.Strings(plan.TypeChanged)
	safe := len(plan.Removed) == 0 && len(plan.TypeChanged) == 0
	for _, a := range plan.Added {
		if a.Required {
			safe = false
			break
		}
	}
	plan.Safe = safe
	if safe {
		plan.Verdict = "SAFE"
	} else {
		plan.Verdict = "UNSAFE"
	}
	return plan, nil
}

func fieldMap(fields []Field) map[string]Field {
	m := make(map[string]Field, len(fields))
	for _, f := range fields {
		m[f.Name] = f
	}
	return m
}
