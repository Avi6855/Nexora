package txpolicy

// LowRiskThreshold bounds the shadow false-positive estimate: a block on a
// context with DestinationRisk below this is counted as an estimated false
// positive (no ground-truth labels exist in replay).
const LowRiskThreshold = 30

// SimulationResult compares a candidate policy against the current one over
// a batch of historical contexts.
type SimulationResult struct {
	CurrentBlocks      int            `json:"current_blocks"`
	NewBlocks          int            `json:"new_blocks"`
	Delta              int            `json:"delta"` // NewBlocks - CurrentBlocks
	FalsePositiveDelta int            `json:"false_positive_delta"`
	PerRuleDiff        map[string]int `json:"per_rule_diff"`
}

// Simulate runs current and candidate over contexts without touching either
// engine's audit log. Delta is the block-count change; FalsePositiveDelta is
// the change in low-risk blocks (the false-positive estimate); PerRuleDiff
// maps every rule id seen on either side to
// (candidate match count − current match count), so added rules show
// positive counts, removed rules negative, and tightened rules the shift.
func Simulate(current, candidate *Engine, contexts []Context) SimulationResult {
	res := SimulationResult{PerRuleDiff: map[string]int{}}
	curMatches := map[string]int{}
	candMatches := map[string]int{}
	curFP := 0
	candFP := 0

	for _, ctx := range contexts {
		if r := matchPure(current, ctx); r != nil {
			curMatches[r.ID]++
			if r.Effect == EffectBlock {
				res.CurrentBlocks++
				if ctx.DestinationRisk < LowRiskThreshold {
					curFP++
				}
			}
		}
		if r := matchPure(candidate, ctx); r != nil {
			candMatches[r.ID]++
			if r.Effect == EffectBlock {
				res.NewBlocks++
				if ctx.DestinationRisk < LowRiskThreshold {
					candFP++
				}
			}
		}
	}

	res.Delta = res.NewBlocks - res.CurrentBlocks
	res.FalsePositiveDelta = candFP - curFP
	for id, n := range curMatches {
		res.PerRuleDiff[id] -= n
	}
	for id, n := range candMatches {
		res.PerRuleDiff[id] += n
	}
	return res
}
