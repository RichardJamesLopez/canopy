package engine

import (
	"dcbapp/internal/dcbmath"
	t "dcbapp/internal/dcbtypes"
)

// FP is the fixed-point type, re-exported for terse tuning tables.
type FP = dcbmath.FP

// RulesVersion is the current rules revision. Past versions stay compiled in so
// in-flight seasons replay under the rules they started with. Bumped to 2 for
// the direct-build / typed-compute / monthly-block redesign; to 4 for the
// difficulty progression, dynamic input prices, and asymmetric-chip retune.
const RulesVersion uint16 = 4

// Pacing. ONE BLOCK = ONE WEEK (5s real-time). A year is 52 blocks; a quarter is
// 13. The run is open-ended (survival), so there is no fixed season length —
// BlocksPerYear is the unit for annualizing schedules and pacing cycles.
const (
	BlocksPerYear = 52                 // weeks per year
	QuarterBlocks = 13                 // weeks per quarter (market recompute cadence)
	SeasonBlocks  = 50 * BlocksPerYear // legacy horizon used only for display fallbacks
	RedWeekLimit  = 4                  // consecutive weeks cash<0 before game over
)

// AccelProfile is the per-unit physical profile of one accelerator type. All
// per-unit infra draws are fixed-point so fractional draws are exact. One
// server unit produces CUPerUnit of compute at full operation.
type AccelProfile struct {
	CUPerUnit    int64 // useful compute one unit delivers at full operation
	PowerPerUnit FP    // PU drawn per unit
	CoolPerUnit  FP    // KU burden per unit (before region climate multiplier)
	StaffPerUnit FP    // people-fraction to operate one unit
	AcrePerUnit  FP    // acres footprint per unit
}

// Accel is the static per-type profile table, indexed by the Acc* constants.
// Order is normative. Each type carries its own power/cooling profile (the
// reciprocity surface) and trades off cost vs efficiency.
// Asymmetric profiles: the five types trade off output (CUPerUnit) against
// power/cooling/staff/land draw, so the best fleet mix changes with which types
// are in demand this quarter. GPU and MTIA are high-output flagships (2 CU/unit)
// that are power- and land-hungry; TPU/Maia are balanced; Trainium is the lean,
// modest-output efficiency play.
var Accel = [t.NACCEL]AccelProfile{
	t.AccGPU:      {CUPerUnit: 2, PowerPerUnit: dcbmath.FP(2_400_000), CoolPerUnit: dcbmath.FP(2_200_000), StaffPerUnit: dcbmath.FP(7_000), AcrePerUnit: dcbmath.FP(20_000)},
	t.AccTPU:      {CUPerUnit: 1, PowerPerUnit: dcbmath.FP(1_000_000), CoolPerUnit: dcbmath.FP(950_000), StaffPerUnit: dcbmath.FP(3_500), AcrePerUnit: dcbmath.FP(9_000)},
	t.AccTrainium: {CUPerUnit: 1, PowerPerUnit: dcbmath.FP(820_000), CoolPerUnit: dcbmath.FP(780_000), StaffPerUnit: dcbmath.FP(3_000), AcrePerUnit: dcbmath.FP(8_500)},
	t.AccMaia:     {CUPerUnit: 1, PowerPerUnit: dcbmath.FP(920_000), CoolPerUnit: dcbmath.FP(880_000), StaffPerUnit: dcbmath.FP(3_400), AcrePerUnit: dcbmath.FP(9_500)},
	t.AccMTIA:     {CUPerUnit: 2, PowerPerUnit: dcbmath.FP(2_000_000), CoolPerUnit: dcbmath.FP(1_900_000), StaffPerUnit: dcbmath.FP(6_500), AcrePerUnit: dcbmath.FP(19_000)},
}

// Reciprocity ramp rates: how fast the smoothed operable fraction moves toward
// its target each block. A starved input bites within ~1-2 blocks (months).
var (
	RampUp   = dcbmath.Pct(60)
	RampDown = dcbmath.Pct(70)
)

// Production / capacity constants.
const (
	UnlockSlotCU = 1  // CU per server unit, used only for unlock-threshold sizing
	StarterAcre  = 1  // free starter acres in the home region
	StarterStaff = 10 // free starting people
)

// Dollar costs and rates. Per-WEEK opex magnitudes (1 block = 1 week).
var (
	// $100k/yr skilled data-center-ops salary, applied prorated per week (≈$1,923/wk).
	// Rises 15%/yr (LaborInflPerYear) — labor is the fastest-climbing opex.
	StaffWage = dcbmath.FromInt(100_000) / BlocksPerYear // $/week per person
	MaintRate = dcbmath.FP(1_000_000) // $1/week per CU
	PowerCost = dcbmath.FromInt(12)   // $12/PU/week (home-region base) — electricity is
	// a major recurring cost (≈10x the old rate); grows toward the top cost at scale.
)

// Buy prices for shared infra (per unit / per acre / per person).
var (
	CostPU   = dcbmath.FromInt(1_000)  // $/PU
	CostKU   = dcbmath.FromInt(800)    // $/KU
	CostAcre = dcbmath.FromInt(50_000) // $/acre
	HireCost = dcbmath.FromInt(20_000) // $/person (hire/fire 10 at a time)

	// CostServer is the one-time buy price for one unit of each accelerator type.
	// Buy prices scale with output: the 2-CU flagships (GPU/MTIA) cost ~2× the
	// 1-CU types. This is the BASE price; the live price escalates with height
	// (see BuyPriceServer in difficulty.go).
	CostServer = [t.NACCEL]FP{
		t.AccGPU:      dcbmath.FromInt(6_400),
		t.AccTPU:      dcbmath.FromInt(3_000),
		t.AccTrainium: dcbmath.FromInt(2_600),
		t.AccMaia:     dcbmath.FromInt(2_900),
		t.AccMTIA:     dcbmath.FromInt(6_200),
	}
)

// Per-type market: yearly base sale price ($/CU) and its annual drift. Newer
// silicon trends cheaper; scarce types drift up. YearBasePrice folds these.
var (
	AccelBase = [t.NACCEL]FP{
		t.AccGPU:      dcbmath.FromInt(120),
		t.AccTPU:      dcbmath.FromInt(111),
		t.AccTrainium: dcbmath.FromInt(104),
		t.AccMaia:     dcbmath.FromInt(106),
		t.AccMTIA:     dcbmath.FromInt(115),
	}
	// Signed FP fraction of base applied per elapsed year.
	AccelDriftPerYear = [t.NACCEL]FP{
		t.AccGPU:      -dcbmath.Pct(4),
		t.AccTPU:      -dcbmath.Pct(3),
		t.AccTrainium: -dcbmath.Pct(2),
		t.AccMaia:     -dcbmath.Pct(2),
		t.AccMTIA:     -dcbmath.Pct(5),
	}
	TypePriceFloor = dcbmath.FromInt(25)
	TypePriceCeil  = dcbmath.FromInt(700)
	// S/D ratio band applied to the base price each quarter.
	SDRatioMin = dcbmath.Pct(25)
	SDRatioMax = dcbmath.FromInt(4)
)

// Demand-mix rotation: which accelerator types customers want, shifting quarter
// to quarter. MixBase sums to ONE; MixAmp is the quarterly swing amplitude;
// MixPhase offsets each type's cycle; MixPeriodQ is the rotation period in
// quarters.
var (
	MixBase = [t.NACCEL]FP{
		t.AccGPU:      dcbmath.Pct(30),
		t.AccTPU:      dcbmath.Pct(22),
		t.AccTrainium: dcbmath.Pct(16),
		t.AccMaia:     dcbmath.Pct(14),
		t.AccMTIA:     dcbmath.Pct(18),
	}
	// Large amplitudes + a short period make the FAVORED type rotate quarter to
	// quarter: each type's demand share swings hard around its base, and the
	// staggered phases hand leadership from one chip to the next every quarter.
	MixAmp = [t.NACCEL]FP{
		t.AccGPU:      dcbmath.Pct(22),
		t.AccTPU:      dcbmath.Pct(20),
		t.AccTrainium: dcbmath.Pct(16),
		t.AccMaia:     dcbmath.Pct(16),
		t.AccMTIA:     dcbmath.Pct(20),
	}
	MixPhaseQ  = [t.NACCEL]int64{0, 1, 2, 3, 4}
	MixPeriodQ = int64(4) // 4 quarters = 1-year mix rotation (favored chip rotates each quarter)
)

// Funding (debt) interest. The rate is a *fraction* per block (month), unscaled
// by the ×100 dollar rescale. Dynamic: a slow funding-environment cycle plus
// macro rate events; locked (blended) when the player draws.
var (
	FundRateBase = dcbmath.FP(1_000)  // ~0.10%/week center of the cycle
	FundRateAmp  = dcbmath.Pct(60)    // ±60% of base across the cycle
	FundRateMin  = dcbmath.FP(300)    // ~0.03%/week floor
	FundRateMax  = dcbmath.FP(12_000) // ~1.2%/week ceiling (after event spikes)
)

const FundRatePeriodDiv = 3 // funding-cycle period = FundRatePeriodDiv × BlocksPerYear

// Market demand curve.
var (
	BaseDemand          int64 = 300
	DemandGrowthPerYear int64 = 2  // structural demand grows ~+2× of base each year
	DemandGrowthCap     int64 = 60 // capped at 60× base (open-ended runs)
	WaveAmpPct                = 15 // yearly demand wave amplitude, % of base
)

// Network: each Gbps recovers a little region latency penalty toward 1.0x.
var NetRecoverPerGbps = dcbmath.FP(2_000) // 0.002 price-mult recovered / Gbps

// Progression thresholds.
const (
	UnlockRegionsCap      int64 = 500        // RawCapacity (CU) to unlock the region splinter
	UnlockNetworkNetWorth int64 = 25_000_000 // net worth ($) to unlock Network bucket
	UnlockLeveragePeak    int64 = 5_000      // peak capacity (CU) to unlock leverage
	NetworkGbpsCU         int64 = 200        // CU served per Gbps (Network min() term)
)

// Prestige: +2% start capital per level, capped at +20% (level 10).
const (
	PrestigePerLevelPct = 2
	PrestigeMaxLevel    = 10
)

// IdentityMod returns a Modifier that changes nothing (all mults = ONE).
func IdentityMod() t.Modifier {
	return t.Modifier{
		DemandMult:        dcbmath.ONE,
		PriceMult:         dcbmath.ONE,
		CostCUMult:        dcbmath.ONE,
		CostPUMult:        dcbmath.ONE,
		CostKUMult:        dcbmath.ONE,
		CostSlotMult:      dcbmath.ONE,
		PowerCostMult:     dcbmath.ONE,
		CoolingBurdenMult: dcbmath.ONE,
		LandCostMult:      dcbmath.ONE,
		LatencyExtra:      0,
		StaffCoverageMult: dcbmath.ONE,
		IncidentDrag:      0,
		CapacityStrand:    0,
		RateMult:          dcbmath.ONE,
		// MixShift defaults to all-zero (no nudge).
		FreezeGrowth: false,
	}
}
