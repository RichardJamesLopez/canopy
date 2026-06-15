package engine

import (
	"encoding/hex"
	"reflect"
	"testing"

	m "github.com/canopy-network/go-plugin/internal/dcb/dcbmath"
	t "github.com/canopy-network/go-plugin/internal/dcb/dcbtypes"
)

func seasonSeed(b byte) [32]byte {
	var s [32]byte
	for i := range s {
		s[i] = b
	}
	return s
}

// ceilFP rounds a non-negative fixed-point value up to a whole number.
func ceilFP(x m.FP) int64 {
	if x <= 0 {
		return 0
	}
	return (x + m.ONE - 1) / m.ONE
}

func roundUp10(n int64) int64 {
	if n <= 0 {
		return 0
	}
	return ((n + 9) / 10) * 10
}

// ensureSupport buys enough shared infra + staff to comfortably operate the
// current fleet (with headroom for region cooling burden). Deterministic.
func ensureSupport(s *t.State, p t.Policy) {
	var powerNeed, coolNeed, acreNeed, staffNeed m.FP
	for k := 0; k < t.NACCEL; k++ {
		var units int64
		for r := 0; r < t.NREGION; r++ {
			units += s.Servers[k][r]
		}
		powerNeed += units * Accel[k].PowerPerUnit
		coolNeed += units * Accel[k].CoolPerUnit
		acreNeed += units * Accel[k].AcrePerUnit
		staffNeed += units * Accel[k].StaffPerUnit
	}
	powerWant := ceilFP(m.Mul(powerNeed, m.Pct(120)))
	coolWant := ceilFP(m.Mul(coolNeed, m.Pct(160))) // headroom for region burden
	acreWant := ceilFP(acreNeed) + 1
	staffWant := ceilFP(staffNeed)

	var powerHave, coolHave, acreHave int64
	for r := 0; r < t.NREGION; r++ {
		powerHave += s.PowerPU[r]
		coolHave += s.CoolingKU[r]
		acreHave += s.LandAcres[r]
	}
	if d := powerWant - powerHave; d > 0 {
		_ = BuyPower(s, p, d)
	}
	if d := coolWant - coolHave; d > 0 {
		_ = BuyCooling(s, p, d)
	}
	if d := acreWant - acreHave; d > 0 {
		_ = BuyLand(s, p, d)
	}
	if d := roundUp10(staffWant - s.StaffSU); d > 0 {
		_ = Hire(s, d)
	}
}

// growBalanced reinvests spare cash into a balanced fleet across all accelerator
// types, keeping shared infra ahead of the fleet. Deterministic in state.
func growBalanced(s *t.State, p t.Policy) {
	ensureSupport(s, p)
	buffer := m.FromInt(100_000)
	headroom := m.FromInt(300_000) // enough for a bundle + its infra
	for s.Capital-buffer > headroom {
		progressed := false
		for k := 0; k < t.NACCEL; k++ {
			if Buy(s, p, k, 10) == nil {
				progressed = true
			}
		}
		ensureSupport(s, p)
		if !progressed {
			break
		}
	}
}

// runSeason drives the engine for `blocks` blocks under a fixed policy, running
// a deterministic balanced-build schedule at each year boundary so production,
// revenue, and the reciprocity feedback are all exercised. Returns the final
// state and the cumulative reports.
func runSeason(season [32]byte, p t.Policy, blocks uint64, prestige int64) (t.State, []t.BlockReport) {
	s := NewSeason(season, prestige)
	reports := make([]t.BlockReport, 0, blocks)
	for h := uint64(0); h < blocks; h++ {
		if !s.GameOver && h%uint64(BlocksPerYear) == 0 {
			growBalanced(&s, p)
		}
		ctx := t.StepContext{Height: h, Seed: m.BlockSeed(season, h, 1), RulesVersion: RulesVersion}
		var rep t.BlockReport
		s, rep = Step(s, p, ctx)
		reports = append(reports, rep)
	}
	return s, reports
}

func stateHash(s t.State) [32]byte { return StateHash(&s) }

// TestDeterminismDeepEqual: identical inputs => bit-identical state.
func TestDeterminismDeepEqual(t_ *testing.T) {
	seed := seasonSeed(0x11)
	p := DefaultPolicy()
	a, _ := runSeason(seed, p, 300, 0)
	b, _ := runSeason(seed, p, 300, 0)
	if !reflect.DeepEqual(a, b) {
		t_.Fatal("two runs with identical inputs diverged")
	}
}

// TestDeterminismHash: same inputs => same canonical hash; a different season
// seed => a different hash (the world actually changes).
func TestDeterminismHash(t_ *testing.T) {
	p := DefaultPolicy()
	a, _ := runSeason(seasonSeed(0x11), p, 200, 0)
	b, _ := runSeason(seasonSeed(0x11), p, 200, 0)
	if stateHash(a) != stateHash(b) {
		t_.Fatal("hash mismatch across identical runs")
	}
	c, _ := runSeason(seasonSeed(0x22), p, 200, 0)
	if stateHash(a) == stateHash(c) {
		t_.Fatal("different season seed produced identical state — world is not seed-dependent")
	}
}

// goldenHash pins the canonical StateHash of a fixed trajectory. If this
// changes, either the rules or the codec changed — bump the relevant version
// deliberately and update this value. It must be stable across OS/arch.
const goldenHash = "347b5c827901efb7f07139e0a993eef659067f41b966fd1607425a8861b1feea"

func TestGoldenTrajectory(t_ *testing.T) {
	final, _ := runSeason(seasonSeed(0xA5), DefaultPolicy(), 240, 3)
	got := hex.EncodeToString(mustSlice(StateHash(&final)))
	if goldenHash == "GOLDEN_PLACEHOLDER" {
		t_.Logf("golden hash (pin this): %s", got)
		return
	}
	if got != goldenHash {
		t_.Fatalf("trajectory hash changed:\n  got  %s\n  want %s\n(if intentional, bump RulesVersion/CodecVersion and update goldenHash)", got, goldenHash)
	}
}

func mustSlice(a [32]byte) []byte { return a[:] }

// TestSeasonPrestige: prestige is awarded by finishing rank and is bounded.
func TestSeasonPrestige(t_ *testing.T) {
	if g := PrestigeGain(1); g != 2 {
		t_.Fatalf("rank 1 should grant +2, got %d", g)
	}
	if g := PrestigeGain(3); g != 0 {
		t_.Fatalf("rank 3 should grant 0, got %d", g)
	}
	if lvl := ApplyPrestige(9, 2); lvl != PrestigeMaxLevel {
		t_.Fatalf("prestige should cap at %d, got %d", PrestigeMaxLevel, lvl)
	}
	base := seasonSeed(0xBB)
	if SeasonSeed(base, 1) == SeasonSeed(base, 2) {
		t_.Fatal("season 1 and 2 share a seed — worlds would be identical")
	}
	s0 := NewSeason(base, 0)
	s5 := NewSeason(base, 5)
	if s5.Capital <= s0.Capital {
		t_.Fatalf("prestige should raise start capital: %d vs %d", s5.Capital, s0.Capital)
	}
}

// TestFundingMechanic: borrowing adds directly to cash, books debt, locks a
// rate, and interest charges reduce cash each subsequent block.
func TestFundingMechanic(t_ *testing.T) {
	seed := seasonSeed(0xF1)
	s := NewSeason(seed, 0)
	cash0 := s.Capital

	TakeFunding(&s, m.FromInt(500_000), 100)
	// Borrowed money goes straight into cash.
	if s.Capital != cash0+m.FromInt(500_000) {
		t_.Fatalf("draw should raise cash by $500k: got %d (was %d)", s.Capital, cash0)
	}
	if s.Debt != m.FromInt(500_000) {
		t_.Fatalf("debt not booked: %d", s.Debt)
	}
	if s.DebtRate <= 0 {
		t_.Fatal("debt rate not locked at draw")
	}

	// Interest (and wages from starter fleet) reduce cash; debt principal stays.
	before := s.Capital
	s, _ = Step(s, DefaultPolicy(), t.StepContext{Height: 100, Seed: m.BlockSeed(seed, 100, 1), RulesVersion: RulesVersion})
	if s.Capital >= before {
		t_.Fatalf("interest/wages did not reduce cash: %d -> %d", before, s.Capital)
	}
	if s.Debt != m.FromInt(500_000) {
		t_.Fatalf("debt principal should stay booked: %d", s.Debt)
	}
}

// TestGameOver: a fleet bought with no power (production halts) plus ongoing
// wages drains cash; sustained red ends the run and freezes the world.
func TestGameOver(t_ *testing.T) {
	seed := seasonSeed(0xDE)
	s := NewSeason(seed, 0)
	p := DefaultPolicy()
	_ = Buy(&s, p, t.AccGPU, 155) // big capex (~$992k of $1M), but no power/cooling → zero production
	var endHeight uint64
	for h := uint64(0); h < 40; h++ {
		s, _ = Step(s, p, t.StepContext{Height: h, Seed: m.BlockSeed(seed, h, 1), RulesVersion: RulesVersion})
		if s.GameOver {
			endHeight = h
			break
		}
	}
	if !s.GameOver {
		t_.Fatalf("expected game over after sustained red; redWeeks=%d cash=%d", s.RedWeeks, s.Capital)
	}
	// World should freeze: stepping again is a no-op.
	h2 := endHeight + 1
	before := StateHash(&s)
	s, _ = Step(s, p, t.StepContext{Height: h2, Seed: m.BlockSeed(seed, h2, 1), RulesVersion: RulesVersion})
	if StateHash(&s) != before {
		t_.Fatal("world did not freeze at game over")
	}
}

// TestFundingCooldownAndRate: only one draw per year, and the offered rate rises
// with leverage.
func TestFundingCooldownAndRate(t_ *testing.T) {
	seed := seasonSeed(0xC0)
	s := NewSeason(seed, 0)

	offer0 := FundingOffer(&s, 100)
	TakeFunding(&s, m.FromInt(500_000), 100)
	if s.Debt != m.FromInt(500_000) {
		t_.Fatalf("first draw not booked: %d", s.Debt)
	}
	TakeFunding(&s, m.FromInt(500_000), 100+BlocksPerYear/2)
	if s.Debt != m.FromInt(500_000) {
		t_.Fatalf("cooldown not enforced: debt=%d", s.Debt)
	}
	if FundingOffer(&s, 100) <= offer0 {
		t_.Fatalf("offered rate did not rise with leverage: %d -> %d", offer0, FundingOffer(&s, 100))
	}
	TakeFunding(&s, m.FromInt(500_000), 100+BlocksPerYear)
	if s.Debt <= m.FromInt(500_000) {
		t_.Fatalf("draw after cooldown should add debt: %d", s.Debt)
	}
}

// TestCompounds: under a balanced build the business grows — score accrues,
// capacity climbs, the region splinter unlocks, and net revenue goes positive.
func TestCompounds(t_ *testing.T) {
	p := DefaultPolicy()
	final, reports := runSeason(seasonSeed(0x33), p, 360, 0)
	if final.SeasonScore <= 0 {
		t_.Fatalf("no score accrued: %d", final.SeasonScore)
	}
	if final.PeakCapacity < UnlockRegionsCap {
		t_.Fatalf("capacity never reached region-unlock threshold: peak=%d", final.PeakCapacity)
	}
	if !final.RegionsUnlocked {
		t_.Fatal("regions never unlocked despite sufficient capacity")
	}
	sawPositiveNet := false
	for _, r := range reports {
		if r.NetRevenue > 0 {
			sawPositiveNet = true
			break
		}
	}
	if !sawPositiveNet {
		t_.Fatal("net revenue never went positive")
	}
	t_.Logf("360-month run: score=%d peak=%d capital=%.0f netWorth=%d regions=%v network=%v",
		final.SeasonScore, final.PeakCapacity, float64(final.Capital)/float64(m.ONE), NetWorth(&final), final.RegionsUnlocked, final.NetworkUnlocked)
}

// TestPriceBounded: every per-type market price stays within its floor/ceil.
func TestPriceBounded(t_ *testing.T) {
	seed := seasonSeed(0x44)
	p := DefaultPolicy()
	s := NewSeason(seed, 0)
	for h := uint64(0); h < 600; h++ {
		if !s.GameOver && h%uint64(BlocksPerYear) == 0 {
			growBalanced(&s, p)
		}
		s, _ = Step(s, p, t.StepContext{Height: h, Seed: m.BlockSeed(seed, h, 1), RulesVersion: RulesVersion})
		for k := 0; k < t.NACCEL; k++ {
			if s.TypePrice[k] < TypePriceFloor || s.TypePrice[k] > TypePriceCeil {
				t_.Fatalf("type %d price out of bounds at h=%d: %d", k, h, s.TypePrice[k])
			}
		}
	}
}

// TestFullSeasonNoPanic: a long season simulates without panic or overflow,
// ending either solvent with capacity or in a clean game-over.
func TestFullSeasonNoPanic(t_ *testing.T) {
	p := DefaultPolicy()
	final, _ := runSeason(seasonSeed(0x55), p, SeasonBlocks, 0)
	t_.Logf("full season: score=%d peak=%d capital=%.0f netWorth=%d gameOver=%v height=%d",
		final.SeasonScore, final.PeakCapacity, float64(final.Capital)/float64(m.ONE), NetWorth(&final), final.GameOver, final.Height)
	if final.PeakCapacity <= 0 {
		t_.Fatalf("no capacity ever built: peak=%d", final.PeakCapacity)
	}
	if final.GameOver {
		return
	}
	if final.Height != SeasonBlocks {
		t_.Fatalf("height mismatch: %d", final.Height)
	}
}

// TestReciprocity: a fleet starved of staff collapses to near-zero production
// within a couple of blocks, while the same fleet with balanced inputs keeps
// producing — the core feedback loop.
func TestReciprocity(t_ *testing.T) {
	seed := seasonSeed(0x99)
	p := DefaultPolicy()

	// Balanced: buy a fleet and full supporting infra.
	bal := NewSeason(seed, 0)
	_ = Buy(&bal, p, t.AccGPU, 50)
	ensureSupport(&bal, p)
	var balRep t.BlockReport
	for h := uint64(0); h < 4; h++ {
		bal, balRep = Step(bal, p, t.StepContext{Height: h, Seed: m.BlockSeed(seed, h, 1), RulesVersion: RulesVersion})
	}

	// Starved: same servers + infra but fire all staff.
	starved := NewSeason(seed, 0)
	_ = Buy(&starved, p, t.AccGPU, 50)
	ensureSupport(&starved, p)
	_ = Fire(&starved, starved.StaffSU) // not a multiple of 10? StarterStaff=10 and Hire adds 10s
	var stRep t.BlockReport
	for h := uint64(0); h < 4; h++ {
		starved, stRep = Step(starved, p, t.StepContext{Height: h, Seed: m.BlockSeed(seed, h, 1), RulesVersion: RulesVersion})
	}

	if balRep.RawCapacity <= 0 {
		t_.Fatalf("balanced build produced nothing: %d", balRep.RawCapacity)
	}
	if stRep.RawCapacity*4 >= balRep.RawCapacity {
		t_.Fatalf("starved build did not collapse: starved=%d balanced=%d", stRep.RawCapacity, balRep.RawCapacity)
	}
}

// TestRegionSplinterDiffers: concentrating land in the cheap-but-risky Emerging
// region produces a materially different outcome than the safe Nordics.
func TestRegionSplinterDiffers(t_ *testing.T) {
	seed := seasonSeed(0x66)

	emerging := DefaultPolicy()
	emerging.RegionWeights = [t.NREGION]int64{t.RegEmerging: 100}

	nordics := DefaultPolicy()
	nordics.RegionWeights = [t.NREGION]int64{t.RegNordics: 100}

	eFinal, _ := runSeason(seed, emerging, 360, 0)
	nFinal, _ := runSeason(seed, nordics, 360, 0)
	t_.Logf("emerging score=%d peak=%d cap=%.0f | nordics score=%d peak=%d cap=%.0f",
		eFinal.SeasonScore, eFinal.PeakCapacity, float64(eFinal.Capital)/float64(m.ONE),
		nFinal.SeasonScore, nFinal.PeakCapacity, float64(nFinal.Capital)/float64(m.ONE))
	// Both builds are demand-capped to the same delivered CU, so the region
	// choice shows up on the money axis (power/cooling cost + price realization),
	// not the CU score. A material capital difference proves it's a real decision.
	if eFinal.Capital == nFinal.Capital {
		t_.Fatal("region choice had zero effect on outcome — splinter is cosmetic")
	}
}
