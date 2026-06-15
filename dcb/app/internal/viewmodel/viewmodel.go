// Package viewmodel builds the JSON view-model the front-end renders, from a
// pure engine State + the last BlockReport + season meta. It is host-agnostic
// (no js/wasm, no driver) so the same builder serves the local WASM bridge and
// any chain client that decodes State from the chain.
package viewmodel

import (
	"encoding/json"

	m "dcbapp/internal/dcbmath"
	t "dcbapp/internal/dcbtypes"
	"dcbapp/internal/engine"
)

// Policy is the JSON shape of the standing policy (region weights + leverage).
type Policy struct {
	RegionWeights []int64 `json:"regionWeights"`
	Leverage      int     `json:"leverage"`
}

// ToPolicy converts the JSON policy to an engine Policy (clamped).
func (vp Policy) ToPolicy() t.Policy {
	p := t.Policy{LeverageX: uint8(vp.Leverage)}
	for r := 0; r < t.NREGION && r < len(vp.RegionWeights); r++ {
		p.RegionWeights[r] = m.ClampInt(vp.RegionWeights[r], 0, 100)
	}
	return p
}

// FromPolicy converts an engine Policy to the JSON shape.
func FromPolicy(p t.Policy) Policy {
	vp := Policy{Leverage: int(p.LeverageX), RegionWeights: make([]int64, t.NREGION)}
	for r := 0; r < t.NREGION; r++ {
		vp.RegionWeights[r] = p.RegionWeights[r]
	}
	return vp
}

// Accel is one accelerator type's row for the Cost/Revenue tabs.
type Accel struct {
	Name        string  `json:"name"`
	Units       int64   `json:"units"`
	Price       float64 `json:"price"`
	CostUnit    int64   `json:"costUnit"`
	DemandShare float64 `json:"demandShare"`
	FleetShare  float64 `json:"fleetShare"`
	CUPerUnit   int64   `json:"cuPerUnit"`
	Delivered   int64   `json:"delivered"`
	Revenue     int64   `json:"revenue"`
	DemandCU    int64   `json:"demandCU"`
}

type Region struct {
	Name     string  `json:"name"`
	Weight   int64   `json:"weight"`
	Power    float64 `json:"power"`
	Cool     float64 `json:"cool"`
	Price    float64 `json:"price"`
	Land     float64 `json:"land"`
	Risk     string  `json:"risk"`
	Capacity int64   `json:"capacity"`
	Servers  int64   `json:"servers"`
}

type Event struct {
	Cat       string `json:"cat"`
	Name      string `json:"name"`
	Where     string `json:"where"`
	Remaining int64  `json:"remaining"`
}

type LB struct {
	Name  string `json:"name"`
	Score int64  `json:"score"`
	You   bool   `json:"you"`
}

// VM is the full view-model the UI renders.
type VM struct {
	Season   int   `json:"season"`
	Prestige int64 `json:"prestige"`
	Score    int64 `json:"score"`
	Rank     int   `json:"rank"`
	Capital  int64 `json:"capital"`
	NetWorth int64 `json:"netWorth"`

	StartCash      int64   `json:"startCash"`
	Debt           int64   `json:"debt"`
	DebtRatePct    float64 `json:"debtRatePct"`
	FundingReserve int64   `json:"fundingReserve"`
	FundingRatePct float64 `json:"fundingRatePct"`
	Revenue        int64   `json:"revenue"`
	Costs          int64   `json:"costs"`
	Interest       int64   `json:"interest"`
	NetFlow        int64   `json:"netFlow"`
	OpexPower      int64   `json:"opexPower"`
	OpexStaff      int64   `json:"opexStaff"`
	OpexMaint      int64   `json:"opexMaint"`

	Capacity   int64   `json:"capacity"`
	Peak       int64   `json:"peak"`
	Bottleneck string  `json:"bottleneck"`
	Util       float64 `json:"util"`
	AvgPrice   float64 `json:"avgPrice"`
	Demand     int64   `json:"demand"`
	Supply     int64   `json:"supply"`
	UCD        int64   `json:"ucd"`

	Accelerators []Accel `json:"accelerators"`
	PowerPU      int64   `json:"powerPU"`
	CoolingKU    int64   `json:"coolingKU"`
	LandAcres    int64   `json:"landAcres"`
	StaffSU      int64   `json:"staffSU"`
	NetworkGbps  int64   `json:"networkGbps"`

	CostPU   int64 `json:"costPU"`
	CostKU   int64 `json:"costKU"`
	CostAcre int64 `json:"costAcre"`
	CostHire int64 `json:"costHire"`
	CostGbps int64 `json:"costGbps"`

	RegionsUnlocked  bool `json:"regionsUnlocked"`
	NetworkUnlocked  bool `json:"networkUnlocked"`
	LeverageUnlocked bool `json:"leverageUnlocked"`

	Regions     []Region `json:"regions"`
	Events      []Event  `json:"events"`
	Leaderboard []LB     `json:"leaderboard"`
	Policy      Policy   `json:"policy"`

	Week                 int64   `json:"week"`
	Year                 int64   `json:"year"`
	WeekOfYear           int64   `json:"weekOfYear"`
	RunwayWeeks          int64   `json:"runwayWeeks"`
	GameOver             bool    `json:"gameOver"`
	InvestableCap        int64   `json:"investableCap"`
	FundingOfferPct      float64 `json:"fundingOfferPct"`
	FundingCooldownWeeks int64   `json:"fundingCooldownWeeks"`

	LastSeasonRank  int   `json:"lastSeasonRank"`
	LastSeasonScore int64 `json:"lastSeasonScore"`
}

// Meta is the season/player context that isn't in State.
type Meta struct {
	Season          int
	Prestige        int64
	PlayerName      string
	LastSeasonRank  int
	LastSeasonScore int64
}

// Build assembles the view-model from a pure State + the last block report +
// the standing policy + season meta.
func Build(s *t.State, last t.BlockReport, policy t.Policy, meta Meta) VM {
	caps := engine.RegionCapacities(s)
	servers := engine.RegionServers(s)
	regions := make([]Region, t.NREGION)
	for r := 0; r < t.NREGION; r++ {
		pr := engine.Regions[r]
		regions[r] = Region{
			Name: t.RegionName[r], Weight: policy.RegionWeights[r],
			Power: fpf(pr.PowerCostMult), Cool: fpf(pr.CoolingBurden),
			Price: fpf(pr.PriceMult), Land: fpf(pr.LandCostMult),
			Risk: riskWord(pr.GeoRiskWeight), Capacity: caps[r], Servers: servers[r],
		}
	}

	var totalUnits int64
	unitsByType := make([]int64, t.NACCEL)
	for k := 0; k < t.NACCEL; k++ {
		var u int64
		for r := 0; r < t.NREGION; r++ {
			u += s.Servers[k][r]
		}
		unitsByType[k] = u
		totalUnits += u
	}
	accels := make([]Accel, t.NACCEL)
	var avgPriceNum m.FP
	for k := 0; k < t.NACCEL; k++ {
		fleetShare := 0.0
		if totalUnits > 0 {
			fleetShare = float64(unitsByType[k]) / float64(totalUnits)
		}
		accels[k] = Accel{
			Name: t.AccelName[k], Units: unitsByType[k], Price: fpf(s.TypePrice[k]),
			CostUnit: m.ToInt(engine.CostServer[k]), DemandShare: fpf(s.DemandMix[k]),
			FleetShare: fleetShare, CUPerUnit: engine.Accel[k].CUPerUnit,
			Delivered: last.DeliveredByType[k], Revenue: m.ToInt(last.RevenueByType[k]),
			DemandCU: m.ToInt(m.Mul(m.FromInt(s.MarketDemandCU), s.DemandMix[k])),
		}
		avgPriceNum += m.Mul(s.TypePrice[k], s.DemandMix[k])
	}

	events := make([]Event, 0, len(s.Events))
	for _, e := range s.Events {
		where := "global"
		if e.Region >= 0 {
			where = t.RegionName[e.Region]
		}
		events = append(events, Event{Cat: t.CategoryName[e.Cat], Name: e.Name, Where: where, Remaining: e.Remaining})
	}

	board := engine.Leaderboard(s, meta.PlayerName)
	lb := make([]LB, len(board))
	for i, e := range board {
		lb[i] = LB{Name: e.Name, Score: e.Score, You: e.IsPlayer}
	}

	var powerPU, coolingKU, landAcres int64
	for r := 0; r < t.NREGION; r++ {
		powerPU += s.PowerPU[r]
		coolingKU += s.CoolingKU[r]
		landAcres += s.LandAcres[r]
	}

	demand := last.Demand
	if demand == 0 {
		demand = s.MarketDemandCU
	}
	cooldown := int64(engine.BlocksPerYear) - (int64(s.Height) - s.LastFundingBlock)
	if cooldown < 0 {
		cooldown = 0
	}

	return VM{
		Season: meta.Season, Prestige: meta.Prestige, Score: s.SeasonScore,
		Rank: engine.PlayerRank(s, meta.PlayerName), Capital: m.ToInt(s.Capital),
		NetWorth: engine.NetWorth(s), StartCash: m.ToInt(s.StartCash), Debt: m.ToInt(s.Debt),
		DebtRatePct:    float64(s.DebtRate) / float64(m.ONE) * 100,
		FundingReserve: 0,
		FundingRatePct: float64(engine.FundingRate(s.Height, s.Events)) / float64(m.ONE) * 100,
		Revenue:        m.ToInt(last.GrossRevenue),
		Interest:       m.ToInt(m.Mul(s.Debt, s.DebtRate)),
		Costs:          m.ToInt(last.OpEx - m.Mul(s.Debt, s.DebtRate)),
		NetFlow:        m.ToInt(last.NetRevenue),
		OpexPower:      m.ToInt(last.OpexPower), OpexStaff: m.ToInt(last.OpexStaff), OpexMaint: m.ToInt(last.OpexMaint),
		Capacity: last.RawCapacity, Peak: s.PeakCapacity, Bottleneck: last.Bottleneck,
		Util: float64(last.Utilization) / float64(m.ONE), AvgPrice: fpf(avgPriceNum),
		Demand: demand, Supply: last.RawCapacity + competitorCap(s), UCD: last.UCD,
		Accelerators: accels, PowerPU: powerPU, CoolingKU: coolingKU, LandAcres: landAcres,
		StaffSU: s.StaffSU, NetworkGbps: s.NetworkGbps,
		CostPU: m.ToInt(engine.CostPU), CostKU: m.ToInt(engine.CostKU), CostAcre: m.ToInt(engine.CostAcre),
		CostHire: m.ToInt(engine.HireCost), CostGbps: m.ToInt(engine.CostGbps),
		RegionsUnlocked: s.RegionsUnlocked, NetworkUnlocked: s.NetworkUnlocked, LeverageUnlocked: s.LeverageUnlocked,
		Regions: regions, Events: events, Leaderboard: lb, Policy: FromPolicy(policy),
		Week: int64(s.Height), Year: int64(s.Height) / int64(engine.BlocksPerYear),
		WeekOfYear:  int64(s.Height)%int64(engine.BlocksPerYear) + 1,
		RunwayWeeks: int64(engine.RedWeekLimit) - s.RedWeeks, GameOver: s.GameOver,
		InvestableCap:   m.ToInt(s.Capital),
		FundingOfferPct: float64(engine.FundingOffer(s, s.Height)) / float64(m.ONE) * 100,
		FundingCooldownWeeks: cooldown,
		LastSeasonRank:       meta.LastSeasonRank, LastSeasonScore: meta.LastSeasonScore,
	}
}

// BuildJSON is Build marshaled to a JSON string.
func BuildJSON(s *t.State, last t.BlockReport, policy t.Policy, meta Meta) string {
	b, _ := json.Marshal(Build(s, last, policy, meta))
	return string(b)
}

func competitorCap(s *t.State) int64 {
	var c int64
	for k := 0; k < t.NACCEL; k++ {
		for i := range s.Competitors {
			c += s.Competitors[i].Fleet[k] * engine.Accel[k].CUPerUnit
		}
	}
	return c
}

func fpf(x m.FP) float64 { return float64(x) / float64(m.ONE) }

func riskWord(geo int64) string {
	switch {
	case geo >= 12:
		return "HIGH"
	case geo >= 6:
		return "med"
	default:
		return "low"
	}
}
