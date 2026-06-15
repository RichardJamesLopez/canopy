package engine

import (
	m "dcbapp/internal/dcbmath"
	t "dcbapp/internal/dcbtypes"
)

var infraLabel = [4]string{"Power", "Cooling", "Land", "Staff"}

// Step is the normative per-block transition. ONE BLOCK = ONE MONTH. It is a
// PURE function of (state, policy, ctx): no clock, no ambient randomness, no
// I/O. Every validator computing this on the same inputs must reach a
// bit-identical result.
//
// Purchases are NOT made here — they are separate transactions (see actions.go)
// applied between Steps. Step only advances the world: events, demand, the
// quarterly market, production (with the reciprocity feedback), revenue, opex,
// competitors, score, and survival.
//
// Normative RNG draw order within a block: events stream (advanceEvents) first,
// then the competitors stream (one draw per competitor, in index order). There
// is no market RNG stream — per-type prices are deterministic in (height, fleet
// state, active events).
func Step(s t.State, p t.Policy, ctx t.StepContext) (t.State, t.BlockReport) {
	if s.GameOver {
		return s, t.BlockReport{Height: ctx.Height}
	}
	// 1. Seed the independent subsystem streams from the block seed.
	evRng := m.NewRNG(ctx.Seed, m.DomEvents)
	cpRng := m.NewRNG(ctx.Seed, m.DomCompetitors)

	// 2. Advance/spawn events, then fold active modifiers.
	var spawned []t.ActiveEvent
	s.Events, spawned = advanceEvents(s.Events, evRng, ctx.Height)
	rm := resolve(s.Events)

	// 3. Demand for this block (deterministic curve × event multiplier).
	demand := m.ToInt(m.Mul(m.FromInt(SeasonDemand(ctx.Height)), rm.demandMult))
	if demand < 1 {
		demand = 1
	}
	s.MarketDemandCU = demand

	// 4. Quarterly market recompute (demand mix + per-type prices). Keyed off
	// pure height%QuarterBlocks so eager and lazy catch-up agree exactly.
	if ctx.Height%QuarterBlocks == 0 {
		recomputeMarket(&s, rm)
	}

	// 5. Resolve the per-type production with the reciprocity feedback.
	perRegionType, prodType, rawTotal, bottleneck := production(&s, rm)

	// 6. Per-type demand cap (the match-the-mix reward): you can only sell up to
	// the share of demand wanting that type.
	var deliveredType [t.NACCEL]int64
	var ucdTotal int64
	for k := 0; k < t.NACCEL; k++ {
		demandK := m.ToInt(m.Mul(m.FromInt(demand), s.DemandMix[k]))
		d := prodType[k]
		if demandK < d {
			d = demandK
		}
		deliveredType[k] = d
		ucdTotal += d
	}
	if ucdTotal < rawTotal {
		bottleneck = "Demand (mix)"
	}

	// 7. Revenue. Split each type's delivered CU back across regions by that
	// type's production share, priced at the type price × region realization.
	var gross m.FP
	var revByType [t.NACCEL]m.FP
	for k := 0; k < t.NACCEL; k++ {
		if deliveredType[k] <= 0 || prodType[k] <= 0 {
			continue
		}
		for r := 0; r < t.NREGION; r++ {
			if perRegionType[k][r] <= 0 {
				continue
			}
			deliveredKR := m.MulDiv(deliveredType[k], perRegionType[k][r], prodType[k])
			if deliveredKR <= 0 {
				continue
			}
			priceR := m.Mul(s.TypePrice[k], m.Mul(effPriceMult(r, s.NetworkGbps, rm.latencyExtra[r]), rm.priceMult))
			revByType[k] += deliveredKR * priceR
		}
		gross += revByType[k]
	}

	// 8. OpEx and net.
	var opexPower m.FP
	var totalCU int64
	for r := 0; r < t.NREGION; r++ {
		var regPU int64
		for k := 0; k < t.NACCEL; k++ {
			totalCU += s.Servers[k][r] * Accel[k].CUPerUnit
		}
		regPU = s.PowerPU[r]
		regPowerCost := m.Mul(PowerCostAt(ctx.Height), m.Mul(Regions[r].PowerCostMult, rm.powerCost[r]))
		opexPower += regPU * regPowerCost
	}
	opexStaff := m.Mul(m.FromInt(s.StaffSU), StaffWageAt(ctx.Height))
	opexMaint := m.Mul(m.FromInt(totalCU), MaintRateAt(ctx.Height))
	opex := opexPower + opexStaff + opexMaint
	if s.Debt > 0 {
		opex += m.Mul(s.Debt, s.DebtRate) // interest at the rate locked when drawn
	}
	net := gross - opex
	s.Capital += net

	// 9. Advance competitors (one draw each, fixed order). They sell into the
	// same finite demand pool the player does.
	compCap := totalCompetitorCapacity(&s)
	marketFill := m.ONE
	if totalSupply := rawTotal + compCap; totalSupply > 0 {
		marketFill = m.ClampFP(m.Div(m.FromInt(demand), m.FromInt(totalSupply)), 0, m.ONE)
	}
	for i := range s.Competitors {
		stepCompetitor(&s.Competitors[i], s.TypePrice, marketFill, cpRng)
	}

	// 10. Score, sub-metrics, progression unlocks.
	s.SeasonScore += ucdTotal
	s.LifetimeUCD += ucdTotal
	s.LifetimeGross += gross
	s.LifetimeOpEx += opex
	if rawTotal > s.PeakCapacity {
		s.PeakCapacity = rawTotal
	}
	if !s.RegionsUnlocked && rawTotal >= UnlockRegionsCap {
		s.RegionsUnlocked = true
	}
	if !s.NetworkUnlocked && NetWorth(&s) >= UnlockNetworkNetWorth {
		s.NetworkUnlocked = true
	}
	if !s.LeverageUnlocked && s.PeakCapacity >= UnlockLeveragePeak {
		s.LeverageUnlocked = true
	}

	// 11. Survival: the run ends the moment cash goes negative. Purchases are
	// affordability-gated, so cash only drops below zero when a week's opex
	// exceeds the balance — i.e. the operator has run out of cash.
	if s.Capital < 0 {
		s.GameOver = true
	}

	report := t.BlockReport{
		Height: ctx.Height, UCD: ucdTotal, RawCapacity: rawTotal,
		Bottleneck: bottleneck, Utilization: regionAvgOper(&s), GrossRevenue: gross,
		OpEx: opex, NetRevenue: net, Demand: demand, NewEvents: spawned,
		DeliveredByType: deliveredType, RevenueByType: revByType,
		OpexPower: opexPower, OpexStaff: opexStaff, OpexMaint: opexMaint,
	}

	// 12. Advance height.
	s.Height = ctx.Height + 1
	return s, report
}

// production resolves per-type operable compute under the reciprocity feedback.
// For each region it computes how much power/cooling/land the installed fleet
// needs vs the shared pools, plus a global staff-coverage ratio; the smallest
// ratio (Leontief) is the region's operable target. The target is SMOOTHED
// across blocks (RampUp/RampDown) so a starved input bites over ~1-2 blocks
// rather than snapping — and a balanced, proportional build sustains full
// operation. Returns per-type per-region operable CU, per-type totals, the
// grand total, and the dominant bottleneck label. Mutates s.OperSmooth.
func production(s *t.State, rm resolvedMods) (perRegionType [t.NACCEL][t.NREGION]int64, prodType [t.NACCEL]int64, total int64, bottleneck string) {
	// Global staff coverage ratio (people are not region-bound).
	var staffNeed m.FP
	for r := 0; r < t.NREGION; r++ {
		for k := 0; k < t.NACCEL; k++ {
			staffNeed += s.Servers[k][r] * Accel[k].StaffPerUnit
		}
	}
	if rm.staffCoverage > 0 {
		staffNeed = m.Div(staffNeed, rm.staffCoverage)
	}
	fStaff := m.ONE
	if staffNeed > 0 {
		fStaff = m.ClampFP(m.Div(m.FromInt(s.StaffSU), staffNeed), 0, m.ONE)
	}

	var bindWeight [4]int64 // Power, Cooling, Land, Staff
	anyConstraint := false

	for r := 0; r < t.NREGION; r++ {
		var regServers int64
		var powerNeed, coolBase, acreNeed m.FP
		for k := 0; k < t.NACCEL; k++ {
			n := s.Servers[k][r]
			regServers += n
			powerNeed += n * Accel[k].PowerPerUnit
			coolBase += n * Accel[k].CoolPerUnit
			acreNeed += n * Accel[k].AcrePerUnit
		}
		coolNeed := m.Mul(m.Mul(coolBase, Regions[r].CoolingBurden), rm.coolingBurden[r])

		fPower, fCool, fLand := m.ONE, m.ONE, m.ONE
		if powerNeed > 0 {
			fPower = m.ClampFP(m.Div(m.FromInt(s.PowerPU[r]), powerNeed), 0, m.ONE)
		}
		if coolNeed > 0 {
			fCool = m.ClampFP(m.Div(m.FromInt(s.CoolingKU[r]), coolNeed), 0, m.ONE)
		}
		if acreNeed > 0 {
			fLand = m.ClampFP(m.Div(m.FromInt(s.LandAcres[r]), acreNeed), 0, m.ONE)
		}

		fTarget := m.MinInt(fPower, fCool, fLand, fStaff)

		// Smooth toward target. Reset memory when the region is empty so a fresh
		// build doesn't inherit a stale high fraction.
		prev := s.OperSmooth[r]
		if regServers == 0 {
			prev = fTarget
		}
		rate := RampUp
		if fTarget < prev {
			rate = RampDown
		}
		oper := prev + m.Mul(fTarget-prev, rate)
		oper = m.ClampFP(oper, 0, m.ONE)
		s.OperSmooth[r] = oper

		// Apply incident drag (global) and region capacity strand.
		factor := m.Mul(oper, m.Mul(m.ONE-rm.incidentDrag, m.ONE-rm.strand[r]))

		for k := 0; k < t.NACCEL; k++ {
			cu := s.Servers[k][r] * Accel[k].CUPerUnit
			if cu <= 0 {
				continue
			}
			prod := m.ToInt(m.Mul(m.FromInt(cu), factor))
			if prod < 0 {
				prod = 0
			}
			perRegionType[k][r] = prod
			prodType[k] += prod
			total += prod
		}

		// Bottleneck binding for this region (weighted by its server count).
		if regServers > 0 && fTarget < m.ONE {
			anyConstraint = true
			ratios := [4]m.FP{fPower, fCool, fLand, fStaff}
			minIdx, minVal := 0, ratios[0]
			for i, v := range ratios {
				if v < minVal {
					minVal, minIdx = v, i
				}
			}
			w := regServers
			bindWeight[minIdx] += w
		}
	}

	bottleneck = "balanced"
	if anyConstraint {
		best := int64(0)
		for i := 0; i < 4; i++ {
			if bindWeight[i] > best {
				best, bottleneck = bindWeight[i], infraLabel[i]
			}
		}
	}

	// Global Network cap (only once unlocked); scales all regions proportionally.
	if s.NetworkUnlocked && s.NetworkGbps >= 0 {
		netCap := s.NetworkGbps * NetworkGbpsCU
		if netCap < total && total > 0 {
			for k := 0; k < t.NACCEL; k++ {
				var scaled int64
				for r := 0; r < t.NREGION; r++ {
					perRegionType[k][r] = m.MulDiv(perRegionType[k][r], netCap, total)
					scaled += perRegionType[k][r]
				}
				prodType[k] = scaled
			}
			total = netCap
			bottleneck = "Network"
		}
	}
	return perRegionType, prodType, total, bottleneck
}

// regionAvgOper is a representative operable fraction for the report's
// Utilization field: the server-weighted average of the per-region smoothed
// operable fraction.
func regionAvgOper(s *t.State) m.FP {
	var num m.FP
	var den int64
	for r := 0; r < t.NREGION; r++ {
		var regServers int64
		for k := 0; k < t.NACCEL; k++ {
			regServers += s.Servers[k][r]
		}
		if regServers <= 0 {
			continue
		}
		num += regServers * s.OperSmooth[r]
		den += regServers
	}
	if den <= 0 {
		return m.ONE
	}
	return m.Div(num, m.FromInt(den))
}
