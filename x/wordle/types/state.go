package types

const (
	// Consensus threshold (66%)
	ConsensusThreshold = 66
)

func (c ConsensusInfo) HasConsensus() bool {
	if c.TotalValidators == 0 {
		return false
	}
	percentage := (c.VotedYes * 100) / c.TotalValidators
	return percentage >= ConsensusThreshold
}
