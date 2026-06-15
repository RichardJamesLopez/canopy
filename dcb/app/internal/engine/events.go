package engine

import (
	m "dcbapp/internal/dcbmath"
	t "dcbapp/internal/dcbtypes"
)

// eventTemplate describes a spawnable event. mod carries only the fields it
// touches (built on top of IdentityMod). regional events pick a target region
// weighted by climate/geo risk.
type eventTemplate struct {
	name           string
	mod            t.Modifier
	durMin, durMax int64
	weight         int64
	regional       bool
}

// modWith builds a Modifier starting from identity and applying f.
func modWith(f func(*t.Modifier)) t.Modifier {
	mod := IdentityMod()
	f(&mod)
	return mod
}

// catTemplates holds the weighted template tables per category. Order within a
// category is normative (weighted-pick tie-breaks). Event durations are in
// MONTHS now (1 block = 1 month), so they are short relative to the old weekly
// tuning.
var catTemplates = [t.NCATEGORY][]eventTemplate{
	t.CatNews: {
		{"AI breakthrough headline", modWith(func(x *t.Modifier) { x.DemandMult = m.Pct(125) }), 6, 12, 10, false},
		{"Regulatory scrutiny on data centers", modWith(func(x *t.Modifier) {
			x.CostCUMult, x.CostPUMult, x.CostKUMult, x.CostSlotMult = m.Pct(115), m.Pct(115), m.Pct(115), m.Pct(115)
		}), 9, 15, 8, false},
		{"Efficiency standard announced", modWith(func(x *t.Modifier) { x.CoolingBurdenMult = m.Pct(110); x.PriceMult = m.Pct(105) }), 12, 18, 7, false},
		{"Market correction", modWith(func(x *t.Modifier) { x.PriceMult = m.Pct(85) }), 6, 9, 8, false},
		{"Rate cut — cheap credit", modWith(func(x *t.Modifier) { x.RateMult = m.Pct(50) }), 9, 18, 6, false},
		{"Rate hike", modWith(func(x *t.Modifier) { x.RateMult = m.Pct(170) }), 9, 18, 6, false},
		{"Credit crunch", modWith(func(x *t.Modifier) { x.RateMult = m.Pct(260) }), 6, 12, 4, false},
	},
	t.CatClimate: {
		{"Heatwave", modWith(func(x *t.Modifier) { x.CoolingBurdenMult = m.Pct(140) }), 6, 9, 12, true},
		{"Cold snap (free cooling)", modWith(func(x *t.Modifier) { x.CoolingBurdenMult = m.Pct(70) }), 5, 7, 8, true},
		{"Drought / water restriction", modWith(func(x *t.Modifier) { x.CoolingBurdenMult = m.Pct(125) }), 8, 11, 8, true},
		{"Grid strain", modWith(func(x *t.Modifier) { x.PowerCostMult = m.Pct(130) }), 7, 10, 10, true},
	},
	t.CatSupply: {
		{"GPU shortage", modWith(func(x *t.Modifier) { x.CostCUMult = m.Pct(160) }), 12, 18, 10, false},
		{"Chip glut", modWith(func(x *t.Modifier) { x.CostCUMult = m.Pct(70) }), 9, 12, 8, false},
		{"Cooling-gear backorder", modWith(func(x *t.Modifier) { x.CostPUMult = m.Pct(135); x.CostKUMult = m.Pct(135) }), 11, 14, 7, false},
		{"New fab online", modWith(func(x *t.Modifier) { x.CostCUMult = m.Pct(85) }), 18, 24, 6, false},
	},
	t.CatDemand: {
		{"AI boom", modWith(func(x *t.Modifier) { x.DemandMult = m.Pct(180) }), 10, 14, 9, false},
		{"Inference fad", modWith(func(x *t.Modifier) { x.DemandMult = m.Pct(140) }), 5, 7, 8, false},
		{"Enterprise migration wave", modWith(func(x *t.Modifier) { x.DemandMult = m.Pct(120) }), 14, 18, 9, false},
		{"Demand slump", modWith(func(x *t.Modifier) { x.DemandMult = m.Pct(75) }), 6, 9, 7, false},
		// Mix-shifting events: customers rotate which accelerators they want.
		{"TPU breakthrough", modWith(func(x *t.Modifier) {
			x.MixShift[t.AccTPU] = m.Pct(20)
			x.MixShift[t.AccGPU] = -m.Pct(10)
		}), 8, 12, 6, false},
		{"GPU supercycle", modWith(func(x *t.Modifier) {
			x.MixShift[t.AccGPU] = m.Pct(25)
			x.MixShift[t.AccMTIA] = -m.Pct(8)
		}), 8, 12, 6, false},
		{"Hyperscaler custom-silicon shift", modWith(func(x *t.Modifier) {
			x.MixShift[t.AccTrainium] = m.Pct(12)
			x.MixShift[t.AccMaia] = m.Pct(12)
			x.MixShift[t.AccMTIA] = m.Pct(12)
			x.MixShift[t.AccGPU] = -m.Pct(20)
		}), 10, 16, 5, false},
	},
	t.CatCompetitor: {
		{"Rival hyperscaler IPO", modWith(func(x *t.Modifier) { x.PriceMult = m.Pct(92) }), 12, 18, 9, false},
		{"Rival outage", modWith(func(x *t.Modifier) { x.PriceMult = m.Pct(115) }), 4, 6, 8, false},
		{"Price war", modWith(func(x *t.Modifier) { x.PriceMult = m.Pct(80); x.DemandMult = m.Pct(115) }), 5, 7, 8, false},
		{"Talent poaching", modWith(func(x *t.Modifier) { x.StaffCoverageMult = m.Pct(80) }), 6, 9, 7, false},
	},
	t.CatGeopolitics: {
		{"Export controls", modWith(func(x *t.Modifier) { x.FreezeGrowth = true }), 12, 18, 8, true},
		{"Tariffs / nationalization risk", modWith(func(x *t.Modifier) {
			x.LandCostMult = m.Pct(150)
			x.PowerCostMult = m.Pct(150)
			x.CapacityStrand = m.Pct(10)
		}), 16, 22, 7, true},
		{"Sanctions / connectivity cut", modWith(func(x *t.Modifier) { x.LatencyExtra = m.Pct(25) }), 18, 24, 7, true},
		{"Power-grid nationalization", modWith(func(x *t.Modifier) { x.PowerCostMult = m.Pct(200); x.CapacityStrand = m.Pct(10) }), 20, 26, 5, true},
		{"Stability dividend", modWith(func(x *t.Modifier) { x.LandCostMult = m.Pct(85); x.PowerCostMult = m.Pct(85) }), 12, 18, 4, true},
	},
}

// catProb is the per-block (per-month) spawn probability for each category,
// fixed-point. Scaled up ~4× from the old weekly tuning so the monthly cadence
// produces a comparable rate of events.
var catProb = [t.NCATEGORY]m.FP{
	t.CatNews:        m.FP(17_000), // ~0.017/month
	t.CatClimate:     m.FP(26_000),
	t.CatSupply:      m.FP(13_000),
	t.CatDemand:      m.FP(17_000),
	t.CatCompetitor:  m.FP(13_000),
	t.CatGeopolitics: m.FP(11_000),
}

// advanceEvents decrements active-event lifetimes, drops the expired, then rolls
// new spawns for each category in order. Returns the surviving+new slice and the
// list of newly spawned events (for the report). All draws come from the events
// stream; the draw sequence is deterministic.
func advanceEvents(events []t.ActiveEvent, rng *m.RNG, height uint64) ([]t.ActiveEvent, []t.ActiveEvent) {
	kept := events[:0:0] // new backing array; never alias caller storage
	for _, e := range events {
		e.Remaining--
		if e.Remaining > 0 {
			kept = append(kept, e)
		}
	}

	// Events get more frequent as difficulty rises (markets grow more erratic).
	vol := volatilityMult(height)
	var spawned []t.ActiveEvent
	for cat := t.Category(0); cat < t.NCATEGORY; cat++ {
		if !rng.Chance(m.Mul(catProb[cat], vol)) {
			continue
		}
		tmpls := catTemplates[cat]
		weights := make([]int64, len(tmpls))
		for i := range tmpls {
			weights[i] = tmpls[i].weight
		}
		idx := rng.WeightedPick(weights)
		if idx < 0 {
			continue
		}
		tm := tmpls[idx]
		region := int8(-1)
		if tm.regional {
			region = int8(pickRegion(cat, rng))
		}
		dur := tm.durMin
		if tm.durMax > tm.durMin {
			dur += rng.Intn(tm.durMax - tm.durMin + 1)
		}
		ae := t.ActiveEvent{Cat: cat, Name: tm.name, Region: region, Remaining: dur, Mod: tm.mod}
		kept = append(kept, ae)
		spawned = append(spawned, ae)
	}
	return kept, spawned
}

// pickRegion selects a target region weighted by the category's risk field.
func pickRegion(cat t.Category, rng *m.RNG) int {
	weights := make([]int64, t.NREGION)
	for r := 0; r < t.NREGION; r++ {
		if cat == t.CatGeopolitics {
			weights[r] = Regions[r].GeoRiskWeight
		} else {
			weights[r] = Regions[r].ClimateRiskWt
		}
	}
	idx := rng.WeightedPick(weights)
	if idx < 0 {
		return t.RegVirginia
	}
	return idx
}

// resolvedMods is the per-block aggregate of all active event modifiers.
type resolvedMods struct {
	demandMult, priceMult              m.FP
	costCU, costPU, costKU, costSlot   m.FP
	staffCoverage                      m.FP
	incidentDrag                       m.FP
	mixShift                           [t.NACCEL]m.FP
	powerCost, coolingBurden, landCost [t.NREGION]m.FP
	latencyExtra                       [t.NREGION]m.FP
	strand                             [t.NREGION]m.FP
	freeze                             [t.NREGION]bool
}

// resolve folds all active events into a single multiplier set. Global scalar
// effects multiply regardless of target; region effects apply to the target
// region (or all regions when Region < 0).
func resolve(events []t.ActiveEvent) resolvedMods {
	rm := resolvedMods{
		demandMult: m.ONE, priceMult: m.ONE,
		costCU: m.ONE, costPU: m.ONE, costKU: m.ONE, costSlot: m.ONE,
		staffCoverage: m.ONE, incidentDrag: 0,
	}
	for r := 0; r < t.NREGION; r++ {
		rm.powerCost[r] = m.ONE
		rm.coolingBurden[r] = m.ONE
		rm.landCost[r] = m.ONE
		rm.latencyExtra[r] = 0
		rm.strand[r] = 0
	}
	for _, e := range events {
		md := e.Mod
		rm.demandMult = m.Mul(rm.demandMult, md.DemandMult)
		rm.priceMult = m.Mul(rm.priceMult, md.PriceMult)
		rm.costCU = m.Mul(rm.costCU, md.CostCUMult)
		rm.costPU = m.Mul(rm.costPU, md.CostPUMult)
		rm.costKU = m.Mul(rm.costKU, md.CostKUMult)
		rm.costSlot = m.Mul(rm.costSlot, md.CostSlotMult)
		rm.staffCoverage = m.Mul(rm.staffCoverage, md.StaffCoverageMult)
		rm.incidentDrag += md.IncidentDrag
		for k := 0; k < t.NACCEL; k++ {
			rm.mixShift[k] += md.MixShift[k]
		}

		apply := func(r int) {
			rm.powerCost[r] = m.Mul(rm.powerCost[r], md.PowerCostMult)
			rm.coolingBurden[r] = m.Mul(rm.coolingBurden[r], md.CoolingBurdenMult)
			rm.landCost[r] = m.Mul(rm.landCost[r], md.LandCostMult)
			rm.latencyExtra[r] += md.LatencyExtra
			rm.strand[r] += md.CapacityStrand
			if md.FreezeGrowth {
				rm.freeze[r] = true
			}
		}
		if e.Region < 0 {
			for r := 0; r < t.NREGION; r++ {
				apply(r)
			}
		} else {
			apply(int(e.Region))
		}
	}
	rm.incidentDrag = m.ClampFP(rm.incidentDrag, 0, m.ONE)
	for r := 0; r < t.NREGION; r++ {
		rm.strand[r] = m.ClampFP(rm.strand[r], 0, m.ONE)
	}
	return rm
}
