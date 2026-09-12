package moneymove

import (
	"fmt"
	"sort"
)

// Channels competing for liquidity in the overdraft coordinator.
const (
	ChannelCard        = "card"
	ChannelATM         = "atm"
	ChannelTransfer    = "transfer"
	ChannelDirectDebit = "direct_debit"
	ChannelFee         = "fee"
)

// Verdicts per claim.
const (
	VerdictApprove = "APPROVE"
	VerdictDecline = "DECLINE"
	VerdictQueue   = "QUEUE"
)

// channelPriority orders competing claims: lower wins. Card and ATM need an
// immediate answer, transfers and direct debits may queue, fees go last.
var channelPriority = map[string]int{
	ChannelCard:        0,
	ChannelATM:         1,
	ChannelTransfer:    2,
	ChannelDirectDebit: 3,
	ChannelFee:         4,
}

// Claim is one demand on available liquidity.
type Claim struct {
	ID      string `json:"id"`
	Channel string `json:"channel"`
	Amount  int64  `json:"amount"`
}

// Decision is the per-claim verdict.
type Decision struct {
	ClaimID string `json:"claim_id"`
	Channel string `json:"channel"`
	Amount  int64  `json:"amount"`
	Verdict string `json:"verdict"`
}

// Decide allocates available liquidity across claims in priority order.
// Each APPROVE consumes liquidity; deferrable channels (transfer,
// direct_debit) QUEUE when funds are short, while immediate channels
// (card, atm, fee) DECLINE.
func (s *Store) Decide(available int64, claims []Claim) ([]Decision, error) {
	if available < 0 {
		return nil, fmt.Errorf("available must be non-negative")
	}
	for i, c := range claims {
		if c.ID == "" {
			return nil, fmt.Errorf("claim %d: id is required", i)
		}
		if _, ok := channelPriority[c.Channel]; !ok {
			return nil, fmt.Errorf("claim %s: unknown channel %q", c.ID, c.Channel)
		}
		if c.Amount <= 0 {
			return nil, fmt.Errorf("claim %s: amount must be positive", c.ID)
		}
	}
	ordered := append([]Claim(nil), claims...)
	sort.SliceStable(ordered, func(i, j int) bool {
		return channelPriority[ordered[i].Channel] < channelPriority[ordered[j].Channel]
	})
	remaining := available
	out := make([]Decision, 0, len(ordered))
	for _, c := range ordered {
		var verdict string
		if c.Amount <= remaining {
			verdict = VerdictApprove
			remaining -= c.Amount
		} else if c.Channel == ChannelTransfer || c.Channel == ChannelDirectDebit {
			verdict = VerdictQueue
		} else {
			verdict = VerdictDecline
		}
		out = append(out, Decision{ClaimID: c.ID, Channel: c.Channel, Amount: c.Amount, Verdict: verdict})
	}
	s.logger.Info().Int64("available", available).Int("claims", len(claims)).Msg("moneymove overdraft decided")
	return out, nil
}
