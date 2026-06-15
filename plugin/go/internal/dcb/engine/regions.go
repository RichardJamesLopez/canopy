package engine

import (
	m "github.com/canopy-network/go-plugin/internal/dcb/dcbmath"
	t "github.com/canopy-network/go-plugin/internal/dcb/dcbtypes"
)

// RegionProfile is the static modifier table for a region. All multipliers are
// fixed-point (ONE == 1.0). This table is the richest tuning surface — it is
// the entire risk/reward landscape of the Land splinter.
type RegionProfile struct {
	PowerCostMult m.FP  // electricity cost multiplier
	CoolingBurden m.FP  // KU required per CU (climate)
	PriceMult     m.FP  // price realized vs market (latency/distance to demand)
	LandCostMult  m.FP  // slot purchase cost multiplier
	GeoRiskWeight int64 // relative likelihood/severity of geopolitics targeting
	ClimateRiskWt int64 // relative likelihood of climate targeting
}

// Regions is indexed by the Reg* constants in dcbtypes. Order is normative.
var Regions = [t.NREGION]RegionProfile{
	t.RegVirginia: {
		PowerCostMult: m.ONE, CoolingBurden: m.ONE, PriceMult: m.ONE,
		LandCostMult: m.Pct(120), GeoRiskWeight: 5, ClimateRiskWt: 10,
	},
	t.RegNordics: {
		PowerCostMult: m.Pct(70), CoolingBurden: m.Pct(65), PriceMult: m.Pct(94),
		LandCostMult: m.Pct(90), GeoRiskWeight: 2, ClimateRiskWt: 4,
	},
	t.RegTexas: {
		PowerCostMult: m.Pct(80), CoolingBurden: m.Pct(130), PriceMult: m.Pct(98),
		LandCostMult: m.Pct(70), GeoRiskWeight: 4, ClimateRiskWt: 16,
	},
	t.RegSingapore: {
		PowerCostMult: m.Pct(130), CoolingBurden: m.Pct(145), PriceMult: m.Pct(105),
		LandCostMult: m.Pct(160), GeoRiskWeight: 9, ClimateRiskWt: 15,
	},
	t.RegIreland: {
		PowerCostMult: m.Pct(105), CoolingBurden: m.Pct(80), PriceMult: m.Pct(97),
		LandCostMult: m.Pct(110), GeoRiskWeight: 5, ClimateRiskWt: 5,
	},
	t.RegEmerging: {
		PowerCostMult: m.Pct(55), CoolingBurden: m.Pct(125), PriceMult: m.Pct(90),
		LandCostMult: m.Pct(45), GeoRiskWeight: 18, ClimateRiskWt: 13,
	},
}

// effPriceMult computes the realized-price multiplier for a region, applying
// the network latency-recovery bonus (distant regions recover toward 1.0x as
// network capacity grows) and any event latency penalty.
func effPriceMult(r int, networkGbps int64, latencyExtra m.FP) m.FP {
	base := Regions[r].PriceMult
	if base < m.ONE && networkGbps > 0 {
		recover := networkGbps * NetRecoverPerGbps
		gap := m.ONE - base
		if recover > gap {
			recover = gap
		}
		base += recover
	}
	base -= latencyExtra
	return m.ClampFP(base, m.Pct(50), m.Pct(150))
}
