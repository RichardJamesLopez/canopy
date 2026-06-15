package engine

import (
	m "github.com/canopy-network/go-plugin/internal/dcb/dcbmath"
	t "github.com/canopy-network/go-plugin/internal/dcb/dcbtypes"
)

// SeasonDemand is the deterministic total market-demand curve in CU for a given
// block height: a smooth ramp over the years plus a yearly triangular wave.
// Pure function of height (and rules), so replay is exact.
func SeasonDemand(height uint64) int64 {
	h := int64(height)
	years := m.Div(m.FromInt(h), m.FromInt(BlocksPerYear)) // FP years elapsed
	growth := m.ONE + m.Mul(m.FromInt(DemandGrowthPerYear), years)
	if cap := m.FromInt(DemandGrowthCap); growth > cap {
		growth = cap
	}
	base := m.ToInt(m.Mul(m.FromInt(BaseDemand), growth))

	// yearly demand wave (period = 1 year of blocks)
	period := int64(BlocksPerYear)
	half := period / 2
	phase := h % period
	var tri m.FP // 0..ONE up then back down
	if phase < half {
		tri = m.Div(m.FromInt(phase), m.FromInt(half))
	} else {
		tri = m.ONE - m.Div(m.FromInt(phase-half), m.FromInt(half))
	}
	tri = tri*2 - m.ONE // map to [-ONE, ONE]
	amp := base * int64(WaveAmpPct) / 100
	wave := m.ToInt(m.Mul(m.FromInt(amp), tri))

	return maxI64(base+wave, 1)
}

func maxI64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

// YearBasePrice is the deterministic per-type base sale price ($/CU) for a given
// height: each type starts at AccelBase and drifts at its own annual rate. Pure
// function of (type, year) — no RNG, so replay is exact.
func YearBasePrice(k int, height uint64) m.FP {
	year := int64(height) / BlocksPerYear
	b := AccelBase[k]
	drift := m.Mul(b, m.Mul(AccelDriftPerYear[k], m.FromInt(year)))
	return m.ClampFP(b+drift, TypePriceFloor, TypePriceCeil)
}

// triWaveQ is a triangle wave in [-ONE, ONE] over a period of `period` quarters.
func triWaveQ(q, period int64) m.FP {
	if period < 2 {
		period = 2
	}
	half := period / 2
	phase := ((q % period) + period) % period
	var tri m.FP
	if phase < half {
		tri = m.Div(m.FromInt(phase), m.FromInt(half))
	} else {
		tri = m.ONE - m.Div(m.FromInt(phase-half), m.FromInt(half))
	}
	return tri*2 - m.ONE
}

// demandMix computes the per-type share of total demand for a given quarter,
// rotating deterministically and nudged by active news events (rm.mixShift).
// Always sums to ~ONE (renormalized).
func demandMix(height uint64, rm resolvedMods) [t.NACCEL]m.FP {
	q := int64(height) / QuarterBlocks
	var raw [t.NACCEL]m.FP
	var sum m.FP
	for k := 0; k < t.NACCEL; k++ {
		v := MixBase[k] + m.Mul(MixAmp[k], triWaveQ(q+MixPhaseQ[k], MixPeriodQ))
		v += rm.mixShift[k]
		if v < 0 {
			v = 0
		}
		raw[k] = v
		sum += v
	}
	var mix [t.NACCEL]m.FP
	if sum <= 0 {
		// Degenerate guard: fall back to an even split.
		for k := 0; k < t.NACCEL; k++ {
			mix[k] = m.ONE / t.NACCEL
		}
		return mix
	}
	for k := 0; k < t.NACCEL; k++ {
		mix[k] = m.Div(raw[k], sum)
	}
	return mix
}

// recomputeMarket refreshes the per-type demand mix and price for the current
// quarter. Called on quarter boundaries from Step. Deterministic in (height,
// fleet state, active events) — no RNG.
func recomputeMarket(s *t.State, rm resolvedMods) {
	mix := demandMix(s.Height, rm)
	s.DemandMix = mix
	for k := 0; k < t.NACCEL; k++ {
		demandK := m.ToInt(m.Mul(m.FromInt(s.MarketDemandCU), mix[k]))
		supplyK := playerTypeCU(s, k) + competitorTypeCU(s, k)
		if supplyK < 1 {
			supplyK = 1
		}
		ratio := m.ClampFP(m.Div(m.FromInt(demandK), m.FromInt(supplyK)), SDRatioMin, SDRatioMax)
		s.TypePrice[k] = m.ClampFP(m.Mul(YearBasePrice(k, s.Height), ratio), TypePriceFloor, TypePriceCeil)
	}
}

// playerTypeCU is the player's installed compute (CU) of one accelerator type,
// summed across regions.
func playerTypeCU(s *t.State, k int) int64 {
	var cu int64
	for r := 0; r < t.NREGION; r++ {
		cu += s.Servers[k][r] * Accel[k].CUPerUnit
	}
	return cu
}

// competitorTypeCU sums the rival fleet's CU of one accelerator type.
func competitorTypeCU(s *t.State, k int) int64 {
	var cu int64
	for i := range s.Competitors {
		cu += s.Competitors[i].Fleet[k] * Accel[k].CUPerUnit
	}
	return cu
}

// totalCompetitorCapacity sums the rival fleet across all types (drives the
// market-wide demand fill that brakes overbuilding).
func totalCompetitorCapacity(s *t.State) int64 {
	var c int64
	for k := 0; k < t.NACCEL; k++ {
		c += competitorTypeCU(s, k)
	}
	return c
}

// compOpexPerCU is a rival's blended monthly operating cost per CU of fleet.
var compOpexPerCU = m.FP(6_000_000) // ~$6/CU/week

// stepCompetitor advances one rival's simplified per-type production +
// reinvestment. `fill` is the market-wide demand fill (demand / total supply):
// rivals can only sell their share of finite demand, so overbuilding
// self-limits. Always consumes exactly one competitor draw (stream alignment).
func stepCompetitor(c *t.Competitor, prices [t.NACCEL]m.FP, fill m.FP, rng *m.RNG) {
	regMul := Regions[c.RegionFocus].PriceMult
	var gross m.FP
	var fleetCU int64
	var sold int64
	for k := 0; k < t.NACCEL; k++ {
		cu := c.Fleet[k] * Accel[k].CUPerUnit
		fleetCU += cu
		soldK := m.ToInt(m.Mul(m.FromInt(cu), fill))
		sold += soldK
		gross += soldK * m.Mul(prices[k], regMul)
	}
	// One draw per competitor regardless of archetype (stream alignment).
	j := rng.RangeFP(m.Pct(90), m.Pct(110))
	gross = m.Mul(gross, j)

	opex := m.Mul(m.FromInt(fleetCU), compOpexPerCU)
	c.Capital += gross - opex
	if c.Capital < 0 {
		c.Capital = 0
	}
	c.Score += sold

	// Only expand when the market is absorbing supply (fill high). Reinvest into
	// the rival's focus type.
	budget := m.Mul(c.Capital, c.SpendRate)
	if fill >= m.Pct(60) && budget > 0 {
		focus := int(c.TypeFocus)
		if focus < 0 || focus >= t.NACCEL {
			focus = t.AccGPU
		}
		add := m.ToInt(m.Div(budget, CostServer[focus]))
		if add > 0 {
			c.Fleet[focus] += add
			c.Capital -= m.Mul(m.FromInt(add), CostServer[focus])
		}
	}
}

// NetWorth ($) is cash + undeployed funding reserve + 50% liquidation value of
// servers and land, minus debt. Borrowing is net-worth-neutral at draw, so you
// only climb by deploying capital profitably. (Score is cumulative CU, not net
// worth — this is a secondary metric and the Network unlock gate.)
func NetWorth(s *t.State) int64 {
	nw := s.Capital
	for k := 0; k < t.NACCEL; k++ {
		var units int64
		for r := 0; r < t.NREGION; r++ {
			units += s.Servers[k][r]
		}
		nw += m.Mul(m.FromInt(units), m.Mul(CostServer[k], m.Pct(50)))
	}
	for r := 0; r < t.NREGION; r++ {
		nw += m.Mul(m.FromInt(s.LandAcres[r]), m.Mul(CostAcre, m.Pct(50)))
	}
	nw -= s.Debt
	return m.ToInt(nw)
}

// CompetitorNetWorth approximates a rival's net worth in $ (cash + 50%
// liquidation of fleet). Rivals carry no debt.
func CompetitorNetWorth(c *t.Competitor) int64 {
	nw := c.Capital
	for k := 0; k < t.NACCEL; k++ {
		nw += m.Mul(m.FromInt(c.Fleet[k]), m.Mul(CostServer[k], m.Pct(50)))
	}
	return m.ToInt(nw)
}

// FundingRate returns the current per-block interest rate (a fraction): a slow
// funding-environment cycle over the years, modulated by active macro rate
// events. Deterministic in height. Locked (blended) into DebtRate when drawn.
func FundingRate(height uint64, events []t.ActiveEvent) m.FP {
	period := int64(FundRatePeriodDiv * BlocksPerYear)
	if period < 2 {
		period = 2
	}
	half := period / 2
	phase := int64(height) % period
	var tri m.FP
	if phase < half {
		tri = m.Div(m.FromInt(phase), m.FromInt(half))
	} else {
		tri = m.ONE - m.Div(m.FromInt(phase-half), m.FromInt(half))
	}
	tri = tri*2 - m.ONE
	rate := FundRateBase + m.Mul(m.Mul(FundRateBase, FundRateAmp), tri)
	rateMult := m.ONE
	for i := range events {
		rateMult = m.Mul(rateMult, events[i].Mod.RateMult)
	}
	rate = m.Mul(rate, rateMult)
	return m.ClampFP(rate, FundRateMin, FundRateMax)
}
