package engine

import (
	m "dcbapp/internal/dcbmath"
)

// Difficulty / dynamic-pricing model.
//
// Input prices and operating costs escalate over time and step up every
// DifficultyTierYears game-years, so a static "set it and forget it" fleet
// eventually goes bankrupt and the operator must keep re-architecting. Every
// curve here is a PURE function of height (game-week): no RNG stream, no clock,
// no I/O. That keeps the engine, the view-model (which derives current prices
// from State.Height) and on-chain lazy catch-up bit-identical, and replay exact.
//
// "Moderate" ramp: a balanced fleet that is actively rebalanced survives, but
// set-and-forget goes red within ~2-3 five-year tiers.
const DifficultyTierYears = 5

var (
	// Per-year steady trajectories: fraction of base added per elapsed year.
	LandInflPerYear   = m.Pct(8) // land climbs the fastest
	LaborInflPerYear  = m.Pct(15) // wages + hiring climb fastest (skilled labor scarcity)
	UtilInflPerYear   = m.Pct(4) // power + maintenance climb mildly
	ServerInflPerYear = m.Pct(2) // chips drift up slowly

	// Per-tier step, added once per difficulty tier (every DifficultyTierYears).
	CostStepPerTier = m.Pct(12) // input prices jump each tier
	VolStepPerTier  = m.Pct(20) // market/event volatility rises each tier

	// Cooling price is erratic: a deterministic quarter-keyed noise band that
	// widens with volatility. CoolNoiseBase is the ±amplitude at tier 0.
	CoolNoiseBase = m.Pct(18)
)

// coolNoiseDomain separates the cooling-price noise from the run seed: the
// erratic cooling curve is identical for every player and every replay (a pure
// function of the quarter index), it is not part of the per-block RNG streams.
const coolNoiseDomain uint64 = 0xC001_C001_C001_C001

// difficultyTier is 0,1,2,... stepping up every DifficultyTierYears game-years.
func difficultyTier(h uint64) int64 {
	return (int64(h) / BlocksPerYear) / DifficultyTierYears
}

// years returns elapsed whole game-years at height h.
func years(h uint64) int64 { return int64(h) / BlocksPerYear }

// inflate returns base * (1 + perYear*elapsedYears + step*tier).
func inflate(base, perYear, step m.FP, h uint64) m.FP {
	factor := m.ONE + m.Mul(perYear, m.FromInt(years(h))) + m.Mul(step, m.FromInt(difficultyTier(h)))
	return m.Mul(base, factor)
}

// volatilityMult scales market-swing amplitudes and event frequency: 1.0 at
// tier 0, rising VolStepPerTier per tier.
func volatilityMult(h uint64) m.FP {
	return m.ONE + m.Mul(VolStepPerTier, m.FromInt(difficultyTier(h)))
}

// Per-input current prices (pure functions of height). These are the single
// source of truth consumed by actions.go (buy), step.go (opex) and the
// view-model (display).
func BuyPriceServer(k int, h uint64) m.FP { return inflate(CostServer[k], ServerInflPerYear, CostStepPerTier, h) }
func PriceAcre(h uint64) m.FP             { return inflate(CostAcre, LandInflPerYear, CostStepPerTier, h) }
func PricePU(h uint64) m.FP               { return inflate(CostPU, UtilInflPerYear, CostStepPerTier, h) }
func HireCostAt(h uint64) m.FP            { return inflate(HireCost, LaborInflPerYear, CostStepPerTier, h) }
func StaffWageAt(h uint64) m.FP           { return inflate(StaffWage, LaborInflPerYear, CostStepPerTier, h) }
func PowerCostAt(h uint64) m.FP           { return inflate(PowerCost, UtilInflPerYear, CostStepPerTier, h) }
func MaintRateAt(h uint64) m.FP           { return inflate(MaintRate, UtilInflPerYear, CostStepPerTier, h) }

// PriceKU is the cooling buy price: a mild steady climb plus a deterministic,
// quarter-keyed erratic band that widens with difficulty volatility.
func PriceKU(h uint64) m.FP {
	base := inflate(CostKU, UtilInflPerYear, CostStepPerTier, h)
	amp := m.Mul(CoolNoiseBase, volatilityMult(h))
	return m.Mul(base, m.ONE+m.Mul(amp, coolNoise(h)))
}

// resaleFrac is the fraction of a chip's base buy price recovered on sale at
// height h: starts at ResaleStartPct and depreciates ResaleDropPerYear each year
// down to ResaleFloorPct. Pure function of height.
func resaleFrac(h uint64) m.FP {
	return m.ClampFP(ResaleStartPct-m.Mul(ResaleDropPerYear, m.FromInt(years(h))), ResaleFloorPct, ResaleStartPct)
}

// ResaleValue is the current per-unit sell-back price for an accelerator type:
// resaleFrac of the BASE buy price (CostServer), so it only ever falls — no
// buy-low/sell-high arbitrage even as buy prices inflate.
func ResaleValue(kind int, h uint64) m.FP {
	return m.Mul(CostServer[kind], resaleFrac(h))
}

// coolNoise is deterministic pseudo-noise in [-ONE, ONE], keyed on the quarter
// index only (pure; identical every run and on replay).
func coolNoise(h uint64) m.FP {
	q := h / uint64(QuarterBlocks)
	n := splitmix64(coolNoiseDomain ^ q)
	span := uint64(2*m.ONE) + 1
	return int64(n%span) - m.ONE
}

// splitmix64 is the SplitMix64 finalizer, used here only for the pure cooling
// noise curve (not a stateful RNG stream).
func splitmix64(x uint64) uint64 {
	x += 0x9E3779B97F4A7C15
	x = (x ^ (x >> 30)) * 0xBF58476D1CE4E5B9
	x = (x ^ (x >> 27)) * 0x94D049BB133111EB
	return x ^ (x >> 31)
}
