// Package failover implements multi-region DR/failover on top of the safe
// handover fencing pattern (shared/handover).
//
// A PRIMARY region serves writes while STANDBY regions replicate. The
// controller decides routing (PRIMARY / READ_ONLY_DEGRADED / FAILOVER) from
// primary health plus replication lag versus the RPO, fences stale writers
// with a monotone epoch, dedupes executed operation IDs across regions, and
// produces a post-recovery reconciliation report of diverged ops.
package failover

import (
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

var (
	// ErrFenced is returned when a write presents a stale epoch.
	ErrFenced = errors.New("write rejected: stale epoch (fenced)")
	// ErrUnknownRegion is returned for unregistered regions.
	ErrUnknownRegion = errors.New("unknown region")
	// ErrRegionExists is returned on duplicate registration.
	ErrRegionExists = errors.New("region already registered")
	// ErrOpRequired is returned when an op ID is empty.
	ErrOpRequired = errors.New("op id is required")
)

// Role is a region's DR role.
type Role string

const (
	RolePrimary Role = "PRIMARY"
	RoleStandby Role = "STANDBY"
)

// Route is the controller's routing decision.
type Route string

const (
	// RoutePrimary serves reads and writes from the primary.
	RoutePrimary Route = "PRIMARY"
	// RouteReadOnlyDegraded serves reads only: the primary is alive but
	// replication lag exceeds the RPO, so writes are unsafe.
	RouteReadOnlyDegraded Route = "READ_ONLY_DEGRADED"
	// RouteFailover directs traffic to a standby: the primary is unhealthy.
	RouteFailover Route = "FAILOVER"
)

// Config holds the RPO/RTO policy.
type Config struct {
	// RPOLagMs is the maximum tolerable replication lag in milliseconds.
	// Lag above this forces READ_ONLY_DEGRADED instead of PRIMARY.
	RPOLagMs int64 `json:"rpo_lag_ms"`
	// RTOSeconds is the recovery-time objective (advisory, surfaced in
	// routing reasons and the reconciliation report).
	RTOSeconds int64 `json:"rto_seconds"`
}

// DefaultConfig returns sane banking defaults.
func DefaultConfig() Config {
	return Config{RPOLagMs: 5000, RTOSeconds: 300}
}

// Region is a registered region.
type Region struct {
	Name string `json:"name"`
	Role Role   `json:"role"`
}

// RegionHealth is the latest reported health for a region.
type RegionHealth struct {
	Region     Region    `json:"region"`
	Healthy    bool      `json:"healthy"`
	LagMs      int64     `json:"lag_ms"`
	LastReport time.Time `json:"last_report"`
	Reported   bool      `json:"reported"`
}

// RouteDecision is the controller's routing answer.
type RouteDecision struct {
	Route  Route  `json:"route"`
	Target string `json:"target"`
	Epoch  uint64 `json:"epoch"`
	Reason string `json:"reason"`
}

// OpRecord is one executed operation in the deduped op log.
type OpRecord struct {
	OpID   string    `json:"op_id"`
	Region string    `json:"region"`
	Epoch  uint64    `json:"epoch"`
	At     time.Time `json:"at"`
}

// ReconciliationReport is the post-recovery diff between two regions.
type ReconciliationReport struct {
	Primary     string    `json:"primary"`
	Standby     string    `json:"standby"`
	Epoch       uint64    `json:"epoch"`
	PrimaryOnly []string  `json:"primary_only"`
	StandbyOnly []string  `json:"standby_only"`
	DivergedOps []string  `json:"diverged_ops"`
	ExecutedOps int       `json:"executed_ops"`
	RTOSeconds  int64     `json:"rto_seconds"`
	GeneratedAt time.Time `json:"generated_at"`
}

type regionState struct {
	name         string
	role         Role
	healthy      bool
	lagMs        int64
	lastReport   time.Time
	everReported bool
}

// Controller is the failover state machine.
type Controller struct {
	mu        sync.Mutex
	cfg       Config
	regions   map[string]*regionState
	epoch     uint64
	executed  map[string]OpRecord
	regionOps map[string]map[string]bool
}

// NewController builds a controller at epoch 1.
func NewController(cfg Config) *Controller {
	if cfg.RPOLagMs <= 0 {
		cfg.RPOLagMs = DefaultConfig().RPOLagMs
	}
	if cfg.RTOSeconds <= 0 {
		cfg.RTOSeconds = DefaultConfig().RTOSeconds
	}
	return &Controller{
		cfg:       cfg,
		regions:   map[string]*regionState{},
		epoch:     1,
		executed:  map[string]OpRecord{},
		regionOps: map[string]map[string]bool{},
	}
}

// RegisterRegion adds a region. Duplicates conflict.
func (c *Controller) RegisterRegion(name string, role Role) error {
	if name == "" {
		return fmt.Errorf("region name is required")
	}
	if role != RolePrimary && role != RoleStandby {
		return fmt.Errorf("role must be PRIMARY or STANDBY")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.regions[name]; ok {
		return fmt.Errorf("%w: %s", ErrRegionExists, name)
	}
	c.regions[name] = &regionState{name: name, role: role, healthy: true}
	c.regionOps[name] = map[string]bool{}
	return nil
}

// ReportHealth records a region's health + replication lag.
func (c *Controller) ReportHealth(region string, ok bool, lagMs int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	st, found := c.regions[region]
	if !found {
		return fmt.Errorf("%w: %s", ErrUnknownRegion, region)
	}
	st.healthy = ok
	st.lagMs = lagMs
	st.lastReport = time.Now().UTC()
	st.everReported = true
	return nil
}

// Regions returns a snapshot of region health.
func (c *Controller) Regions() []RegionHealth {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]RegionHealth, 0, len(c.regions))
	for _, st := range c.regions {
		out = append(out, RegionHealth{
			Region:     Region{Name: st.name, Role: st.role},
			Healthy:    st.healthy,
			LagMs:      st.lagMs,
			LastReport: st.lastReport,
			Reported:   st.everReported,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Region.Name < out[j].Region.Name })
	return out
}

// Route computes PRIMARY / READ_ONLY_DEGRADED / FAILOVER from primary health
// and replication lag versus the RPO.
func (c *Controller) Route() RouteDecision {
	c.mu.Lock()
	defer c.mu.Unlock()
	primary := ""
	primaryHealthy := true
	primaryLag := int64(0)
	for _, st := range c.regions {
		if st.role == RolePrimary {
			primary = st.name
			primaryHealthy = st.healthy
			primaryLag = st.lagMs
			break
		}
	}
	if primary == "" {
		// No primary declared: fail over to any healthy standby.
		if tgt := c.healthyStandbyLocked(); tgt != "" {
			return RouteDecision{Route: RouteFailover, Target: tgt, Epoch: c.epoch, Reason: "no primary registered; routing to healthy standby"}
		}
		return RouteDecision{Route: RouteFailover, Target: "", Epoch: c.epoch, Reason: "no primary registered and no healthy standby"}
	}
	if !primaryHealthy {
		if tgt := c.healthyStandbyLocked(); tgt != "" {
			return RouteDecision{Route: RouteFailover, Target: tgt, Epoch: c.epoch,
				Reason: fmt.Sprintf("primary %s unhealthy; failing over to %s", primary, tgt)}
		}
		return RouteDecision{Route: RouteFailover, Target: primary, Epoch: c.epoch,
			Reason: fmt.Sprintf("primary %s unhealthy; no healthy standby available", primary)}
	}
	if primaryLag > c.cfg.RPOLagMs {
		return RouteDecision{Route: RouteReadOnlyDegraded, Target: primary, Epoch: c.epoch,
			Reason: fmt.Sprintf("primary %s lag %dms exceeds RPO %dms; read-only", primary, primaryLag, c.cfg.RPOLagMs)}
	}
	return RouteDecision{Route: RoutePrimary, Target: primary, Epoch: c.epoch,
		Reason: fmt.Sprintf("primary %s healthy with lag %dms within RPO %dms", primary, primaryLag, c.cfg.RPOLagMs)}
}

func (c *Controller) healthyStandbyLocked() string {
	names := []string{}
	for _, st := range c.regions {
		if st.role == RoleStandby && st.healthy {
			names = append(names, st.name)
		}
	}
	sort.Strings(names)
	if len(names) == 0 {
		return ""
	}
	return names[0]
}

// CurrentEpoch returns the fencing epoch.
func (c *Controller) CurrentEpoch() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.epoch
}

// AdvanceEpoch fences old writers by moving to the next epoch.
func (c *Controller) AdvanceEpoch() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.epoch++
	return c.epoch
}

// CheckWrite enforces write fencing: the token must match the current epoch.
func (c *Controller) CheckWrite(epoch uint64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if epoch != c.epoch {
		return fmt.Errorf("%w: writer epoch %d, current %d", ErrFenced, epoch, c.epoch)
	}
	return nil
}

// ExecuteOp records an operation exactly once across regions. The fencing
// epoch must match; duplicate op IDs are deduped (executed=false, nil error).
func (c *Controller) ExecuteOp(region, opID string, epoch uint64) (bool, error) {
	if opID == "" {
		return false, ErrOpRequired
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.regions[region]; !ok {
		return false, fmt.Errorf("%w: %s", ErrUnknownRegion, region)
	}
	if epoch != c.epoch {
		return false, fmt.Errorf("%w: writer epoch %d, current %d", ErrFenced, epoch, c.epoch)
	}
	if _, dup := c.executed[opID]; dup {
		return false, nil
	}
	c.executed[opID] = OpRecord{OpID: opID, Region: region, Epoch: epoch, At: time.Now().UTC()}
	if c.regionOps[region] == nil {
		c.regionOps[region] = map[string]bool{}
	}
	c.regionOps[region][opID] = true
	return true, nil
}

// RecoveryDiff builds the post-recovery reconciliation report: ops present in
// exactly one of the two regions are diverged.
func (c *Controller) RecoveryDiff(primary, standby string) (*ReconciliationReport, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.regions[primary]; !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnknownRegion, primary)
	}
	if _, ok := c.regions[standby]; !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnknownRegion, standby)
	}
	pOps := c.regionOps[primary]
	sOps := c.regionOps[standby]
	primaryOnly := []string{}
	standbyOnly := []string{}
	for op := range pOps {
		if !sOps[op] {
			primaryOnly = append(primaryOnly, op)
		}
	}
	for op := range sOps {
		if !pOps[op] {
			standbyOnly = append(standbyOnly, op)
		}
	}
	sort.Strings(primaryOnly)
	sort.Strings(standbyOnly)
	diverged := append(append([]string{}, primaryOnly...), standbyOnly...)
	sort.Strings(diverged)
	if primaryOnly == nil {
		primaryOnly = []string{}
	}
	if standbyOnly == nil {
		standbyOnly = []string{}
	}
	if diverged == nil {
		diverged = []string{}
	}
	return &ReconciliationReport{
		Primary:     primary,
		Standby:     standby,
		Epoch:       c.epoch,
		PrimaryOnly: primaryOnly,
		StandbyOnly: standbyOnly,
		DivergedOps: diverged,
		ExecutedOps: len(c.executed),
		RTOSeconds:  c.cfg.RTOSeconds,
		GeneratedAt: time.Now().UTC(),
	}, nil
}
