package engine

import (
	"encoding/binary"

	m "dcbapp/internal/dcbmath"
	t "dcbapp/internal/dcbtypes"
)

// NewSeason builds the starting state for a player at the start of a season.
// prestigeLevel is the only thing that carries across seasons (a small start
// capital bonus); everything else resets identically for all players.
func NewSeason(seed [32]byte, prestigeLevel int64) t.State {
	lvl := m.ClampInt(prestigeLevel, 0, PrestigeMaxLevel)
	startCap := m.Mul(m.FromInt(1_000_000), m.ONE+m.Pct(PrestigePerLevelPct*lvl))

	s := t.State{
		Height:           0,
		Seed:             binary.BigEndian.Uint64(seed[0:8]),
		Capital:          startCap,
		StartCash:        startCap,
		StaffSU:          StarterStaff,
		MarketDemandCU:   SeasonDemand(0),
		PrestigeLevel:    lvl,
		LastFundingBlock: -BlocksPerYear, // allow a funding draw immediately
	}
	s.LandAcres[t.RegVirginia] = StarterAcre

	// Starter fleet: 5 of each accelerator type in the home region, with enough
	// shared infra to operate them. This gives the player positive cashflow from
	// month 1 so they immediately see the reward loop before adding more capacity.
	for k := 0; k < t.NACCEL; k++ {
		s.Servers[k][t.RegVirginia] = 5
	}
	s.PowerPU[t.RegVirginia]   = 35 // covers ~28 PU peak draw with headroom
	s.CoolingKU[t.RegVirginia] = 35 // covers ~28 KU peak burden with headroom

	// Seed per-type prices and demand mix so the dashboard is meaningful before
	// the first Step (Step recomputes them on the quarter boundary at height 0).
	for k := 0; k < t.NACCEL; k++ {
		s.TypePrice[k] = AccelBase[k]
		s.DemandMix[k] = MixBase[k]
	}

	// Start the rival fleet near baseline demand (~150 CU total) spread across
	// types, so every per-type market is a live signal from block 1.
	s.Competitors = [4]t.Competitor{
		{Name: "Hyperion Compute", Capital: m.FromInt(200_000), RegionFocus: t.RegVirginia, TypeFocus: t.AccGPU, SpendRate: m.Pct(80)},
		{Name: "Aurora Grid", Capital: m.FromInt(200_000), RegionFocus: t.RegNordics, TypeFocus: t.AccTPU, SpendRate: m.Pct(50)},
		{Name: "Meridian DC", Capital: m.FromInt(200_000), RegionFocus: t.RegIreland, TypeFocus: t.AccMTIA, SpendRate: m.Pct(60)},
		{Name: "Kowloon Cloud", Capital: m.FromInt(200_000), RegionFocus: t.RegEmerging, TypeFocus: t.AccTrainium, SpendRate: m.Pct(70)},
	}
	s.Competitors[0].Fleet[t.AccGPU] = 40
	s.Competitors[1].Fleet[t.AccTPU] = 35
	s.Competitors[2].Fleet[t.AccMTIA] = 40
	s.Competitors[3].Fleet[t.AccTrainium] = 35
	return s
}

// DefaultPolicy is the standing configuration: build in the home region until
// the player unlocks regions and chooses to split. Purchases are made directly
// via the Buy/Sell/Hire/Fire actions, not a budget.
func DefaultPolicy() t.Policy {
	return t.Policy{
		RegionWeights: [t.NREGION]int64{t.RegVirginia: 100},
	}
}
