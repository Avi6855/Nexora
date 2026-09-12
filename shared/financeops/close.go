package financeops

// Checklist gates the period close.
type Checklist struct {
	TransactionsComplete bool `json:"transactions_complete"`
	SettlementsComplete  bool `json:"settlements_complete"`
	NoOpenExceptions     bool `json:"no_open_exceptions"`
	ReconClean           bool `json:"recon_clean"`
}

// CloseResult is CLOSE when every check holds, else BLOCK with reasons.
type CloseResult struct {
	Verdict string   `json:"verdict"` // CLOSE or BLOCK
	Blocked bool     `json:"blocked"`
	Reasons []string `json:"reasons,omitempty"`
}

// CheckClose evaluates the close checklist.
func (s *Store) CheckClose(c Checklist) CloseResult {
	var reasons []string
	if !c.TransactionsComplete {
		reasons = append(reasons, "transactions_complete=false: pending transactions remain")
	}
	if !c.SettlementsComplete {
		reasons = append(reasons, "settlements_complete=false: unsettled items remain")
	}
	if !c.NoOpenExceptions {
		reasons = append(reasons, "no_open_exceptions=false: open exceptions remain")
	}
	if !c.ReconClean {
		reasons = append(reasons, "recon_clean=false: reconciliation breaks remain")
	}
	if len(reasons) == 0 {
		s.logger.Info().Msg("financeops close checked: CLOSE")
		return CloseResult{Verdict: "CLOSE", Blocked: false}
	}
	s.logger.Info().Int("reasons", len(reasons)).Msg("financeops close checked: BLOCK")
	return CloseResult{Verdict: "BLOCK", Blocked: true, Reasons: reasons}
}
