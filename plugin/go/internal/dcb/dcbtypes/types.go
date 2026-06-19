// Package dcbtypes holds the data structures shared across every host of the
// DCB engine — the local prototype, the TUI, the leaderboard indexer, and
// (later) the Canopy FSM. It deliberately contains no logic and imports only
// dcbmath, so it can be a leaf dependency with no risk of pulling in I/O.
package dcbtypes

import "github.com/canopy-network/go-plugin/internal/dcb/dcbmath"

// FP is re-exported for convenience.
type FP = dcbmath.FP

// LenPrefix encodes segments in Canopy core's length-prefixed key convention,
// byte-identical to lib.EncodeLengthPrefixed / lib.JoinLenPrefix (canopy lib/util.go):
// each segment is preceded by a single length byte. EVERY key written through the
// plugin StateWrite MUST use this form, or core's makeVersionedKey →
// DecodeLengthPrefixed (store/versioned_store.go) panics "corrupt or incomplete key"
// on commit when it reads a raw byte as a bogus segment length.
//
// CONSTRAINT: each segment must be <= 255 bytes (the length is a single byte). This
// is for KEYS ONLY — values are stored opaquely by core and must NOT be length-prefixed
// (engine.EncodeState payloads exceed 255 bytes and would overflow the length byte).
func LenPrefix(segments ...[]byte) []byte {
	var out []byte
	for _, s := range segments {
		out = append(out, byte(len(s)))
		out = append(out, s...)
	}
	return out
}

// Single-byte DCB store-key prefixes. Chosen OUTSIDE core's reserved range 1-15
// (canopy fsm/state.go reserves single-byte prefixes 1-15 for accounts, pools,
// validators, etc. and panics if a plugin writes under them).
var (
	PlayerPrefix      = []byte{0x10} // per-player record:   LenPrefix(PlayerPrefix, id)
	LeaderboardPrefix = []byte{0x11} // leaderboard index:   LenPrefix(LeaderboardPrefix, id)
	SeedPrefix        = []byte{0x12} // per-block seed:       LenPrefix(SeedPrefix, height)
	SeasonSeedPrefix  = []byte{0x13} // genesis season seed:  LenPrefix(SeasonSeedPrefix)
)

// NREGION is the number of regions in the Land splinter.
const NREGION = 6

// Region indices. Order is normative (it determines weighted-pick tie-breaks
// and serialization layout).
const (
	RegVirginia = iota
	RegNordics
	RegTexas
	RegSingapore
	RegIreland
	RegEmerging
)

// RegionName is the display name for each region index.
var RegionName = [NREGION]string{
	"Virginia (US)",
	"Nordics",
	"Texas (US)",
	"Singapore",
	"Ireland",
	"Emerging frontier",
}

// NACCEL is the number of accelerator (server) types the operator can build.
// Compute is no longer one fungible resource: each type has its own per-unit
// power draw, cooling burden, compute output, buy price, and market.
const NACCEL = 5

// Accelerator type indices. Order is normative (serialization + market layout).
const (
	AccGPU      = iota // NVIDIA / AMD / Intel general accelerator
	AccTPU             // Google
	AccTrainium        // AWS Trainium / Inferentia
	AccMaia            // Microsoft
	AccMTIA            // Meta
)

// AccelName is the display name for each accelerator type.
var AccelName = [NACCEL]string{"GPU", "TPU", "Trainium", "Maia", "MTIA"}

// Infra kinds for the shared-pool buy actions (power/cooling/land/network).
const (
	InfraPower = iota
	InfraCooling
	InfraLand
	InfraNetwork
	NINFRA
)

// InfraName is the display name for each shared-infra kind.
var InfraName = [NINFRA]string{"Power", "Cooling", "Land", "Network"}

// Category is a random-event family.
type Category uint8

const (
	CatNews Category = iota
	CatClimate
	CatSupply
	CatDemand
	CatCompetitor
	CatGeopolitics
	NCATEGORY
)

// CategoryName is the display name for each category.
var CategoryName = [NCATEGORY]string{"News", "Climate", "Supply", "Demand", "Competitor", "Geopolitics"}

// Modifier is the bundle of multiplicative/additive effects an event applies
// while active. Multipliers default to dcbmath.ONE (×1.0); additive fields
// default to 0. A zero-value Modifier is NOT identity — always build with
// IdentityMod() (defined in the engine) or set fields explicitly.
type Modifier struct {
	DemandMult        FP         // global demand multiplier
	PriceMult         FP         // global price multiplier
	CostCUMult        FP         // server purchase price (all types)
	CostPUMult        FP         // power purchase price
	CostKUMult        FP         // cooling purchase price
	CostSlotMult      FP         // land (acre) purchase price
	PowerCostMult     FP         // electricity opex (region-targeted if Region >= 0)
	CoolingBurdenMult FP         // KU-per-CU burden (region-targeted)
	LandCostMult      FP         // land cost (region-targeted)
	LatencyExtra      FP         // extra latency penalty on region price (region-targeted)
	StaffCoverageMult FP         // SU_COVERAGE multiplier (talent effects)
	IncidentDrag      FP         // additive utilization loss [0,ONE]
	CapacityStrand    FP         // additive fraction of region capacity stranded [0,ONE]
	RateMult          FP         // funding interest-rate multiplier (macro rate events)
	MixShift          [NACCEL]FP // additive nudge to each type's demand-mix weight (signed)
	FreezeGrowth      bool       // if true, region cannot add capacity
}

// ActiveEvent is a Modifier currently in force, with its remaining lifetime and
// optional region target (-1 = global). Name/Cat are kept for the report UI.
type ActiveEvent struct {
	Cat       Category
	Name      string
	Region    int8 // -1 = global
	Remaining int64
	Mod       Modifier
}

// Competitor is a named AI rival. It runs a reduced version of the production
// function; its aggregate fleet drives the per-type market price.
type Competitor struct {
	Name        string
	Fleet       [NACCEL]int64 // per-type effective unit count
	Capital     FP
	RegionFocus int8
	TypeFocus   int8 // accelerator type this rival over-indexes
	SpendRate   FP
	Score       int64 // cumulative CU sold (for the leaderboard)
}

// Policy is the standing configuration the player commits. Purchases are now
// imperative transactions (Buy/Sell/Hire/Fire), so the policy only carries the
// rarely-changed dials the engine reads every block.
type Policy struct {
	RegionWeights [NREGION]int64 // where new units are placed (relative shares)
	LeverageX     uint8          // 0 = none, 15 = 1.5x, 20 = 2.0x (tenths); gated by unlock
}

// State is the full per-player game state. It uses fixed-size arrays (no maps)
// so iteration order is deterministic and serialization is stable.
type State struct {
	Height uint64
	Seed   uint64 // season seed digest, for reference/UI

	Capital FP

	// Per-type, per-region installed accelerator counts (unit counts).
	// Serializes accel-major, region-minor.
	Servers [NACCEL][NREGION]int64

	// Shared physical pools all servers draw on (integer unit counts).
	PowerPU   [NREGION]int64
	CoolingKU [NREGION]int64
	LandAcres [NREGION]int64

	StaffSU     int64 // people; hire/fire in 10s
	NetworkGbps int64

	// Per-type market state: current realized $/CU and the share of total
	// demand wanting each type (Σ DemandMix ≈ ONE).
	TypePrice      [NACCEL]FP
	DemandMix      [NACCEL]FP
	MarketDemandCU int64

	// Smoothing memory for the reciprocity ramp: per-region operable fraction
	// carried across blocks so an input shortfall bites over ~1-2 blocks
	// instead of snapping.
	OperSmooth [NREGION]FP

	Events      []ActiveEvent
	Competitors [4]Competitor

	// Score & sub-metrics.
	SeasonScore   int64 // cumulative delivered CU (THE ranked metric)
	PeakCapacity  int64
	LifetimeUCD   int64
	LifetimeOpEx  FP
	LifetimeGross FP

	// Progression unlocks.
	RegionsUnlocked  bool
	NetworkUnlocked  bool
	LeverageUnlocked bool

	// Funding / debt (dollars, FP).
	Debt             FP    // outstanding principal (liability on the books)
	DebtRate         FP    // blended per-block interest rate locked at draw time
	FundingReserve   FP    // borrowed capex not yet deployed; spent before cash
	LastFundingBlock int64 // height of the last funding draw (cooldown); init -BlocksPerYear

	// Survival (open-ended run; cash may go negative). RedWeeks now counts
	// consecutive *months* in the red (1 block = 1 month).
	RedWeeks int64
	GameOver bool

	// Meta (set at season start, constant thereafter).
	StartCash     FP // starting cash this season (for the dashboard delta)
	PrestigeLevel int64
}

// StepContext carries the per-block inputs the engine needs but must not fetch
// itself. The host (driver/FSM) supplies these.
type StepContext struct {
	Height       uint64
	Seed         [32]byte
	RulesVersion uint16
}

// BlockReport summarizes what happened in a single Step, for the TUI/report.
// It is NOT part of the hashed State — extra fields here are free re: determinism.
type BlockReport struct {
	Height       uint64
	UCD          int64
	RawCapacity  int64
	Bottleneck   string // which input bound, or "Demand (mix)"/"Network"
	Utilization  FP
	GrossRevenue FP
	OpEx         FP
	NetRevenue   FP
	Demand       int64
	NewEvents    []ActiveEvent // events that spawned this block

	// Per-type and opex breakdown for the Revenue tab.
	DeliveredByType [NACCEL]int64 // CU sold this block, per accelerator type
	RevenueByType   [NACCEL]FP    // gross revenue this block, per type
	OpexPower       FP            // power opex this block
	OpexStaff       FP            // staff wages this block
	OpexMaint       FP            // maintenance opex this block
}
