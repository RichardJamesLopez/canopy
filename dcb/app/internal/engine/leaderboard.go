package engine

import (
	"sort"

	t "dcbapp/internal/dcbtypes"
)

// LBEntry is one row of the leaderboard.
type LBEntry struct {
	Name     string
	Score    int64
	IsPlayer bool
}

// Leaderboard ranks the player against the AI competitors by CUMULATIVE COMPUTE
// ORGANIZED (CU) — the season's ranked metric. Deterministic ordering: score
// desc, then name.
func Leaderboard(s *t.State, playerName string) []LBEntry {
	entries := make([]LBEntry, 0, len(s.Competitors)+1)
	entries = append(entries, LBEntry{Name: playerName, Score: s.SeasonScore, IsPlayer: true})
	for i := range s.Competitors {
		entries = append(entries, LBEntry{Name: s.Competitors[i].Name, Score: s.Competitors[i].Score})
	}
	sort.SliceStable(entries, func(a, b int) bool {
		if entries[a].Score != entries[b].Score {
			return entries[a].Score > entries[b].Score
		}
		return entries[a].Name < entries[b].Name
	})
	return entries
}

// PlayerRank returns the player's 1-based rank on the leaderboard.
func PlayerRank(s *t.State, playerName string) int {
	for i, e := range Leaderboard(s, playerName) {
		if e.IsPlayer {
			return i + 1
		}
	}
	return 0
}
