package engine

import (
	m "github.com/canopy-network/go-plugin/internal/dcb/dcbmath"
)

// SeasonSeed derives the world seed for season n from a stable base seed.
// Every player on the same base sees the identical season-n world.
func SeasonSeed(base [32]byte, seasonNum int) [32]byte {
	return m.BlockSeed(base, ^uint64(0), uint64(seasonNum))
}

// WorldSeed is the per-block entropy for the shared world at a given height. It
// is player-independent: every player in a season experiences the SAME event
// timeline and market, which is what makes the leaderboard a fair comparison.
// On-chain this maps directly onto the block header hash / proposer VRF at that
// height (same for all players), so the FSM needs no per-player salt.
func WorldSeed(seasonSeed [32]byte, height uint64) [32]byte {
	return m.BlockSeed(seasonSeed, height, 0)
}

// PrestigeGain is the prestige awarded for finishing a season at a given
// leaderboard rank (1-based). Deliberately small so meta-progression is a nudge,
// not a moat — the cumulative cap (PrestigeMaxLevel) bounds the start-capital
// edge at +20%.
func PrestigeGain(rank int) int64 {
	switch rank {
	case 1:
		return 2
	case 2:
		return 1
	default:
		return 0
	}
}

// ApplyPrestige adds a gain to a level, clamped to the cap.
func ApplyPrestige(level, gain int64) int64 {
	return m.ClampInt(level+gain, 0, PrestigeMaxLevel)
}
