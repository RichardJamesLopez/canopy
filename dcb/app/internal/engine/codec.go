package engine

import (
	"crypto/sha256"
	"encoding/binary"

	t "dcbapp/internal/dcbtypes"
)

// Canonical state encoding. This is the determinism contract: a fixed-width,
// big-endian, fixed-field-order serialization with NO map iteration, so the
// bytes (and therefore the StateHash) are identical on every OS/arch and on
// every host (local prototype, WASM, Canopy FSM). Changing this layout is a
// rules change and must bump CodecVersion.
//
// Version 4: direct-build / typed-compute / monthly-block redesign.
const CodecVersion uint8 = 4

type enc struct{ b []byte }

func (e *enc) u8(x uint8) { e.b = append(e.b, x) }
func (e *enc) boolean(x bool) {
	if x {
		e.u8(1)
	} else {
		e.u8(0)
	}
}
func (e *enc) u64(x uint64) {
	var t [8]byte
	binary.BigEndian.PutUint64(t[:], x)
	e.b = append(e.b, t[:]...)
}
func (e *enc) i64(x int64) { e.u64(uint64(x)) }
func (e *enc) str(s string) {
	e.u64(uint64(len(s)))
	e.b = append(e.b, s...)
}

func (e *enc) mod(m t.Modifier) {
	e.i64(m.DemandMult)
	e.i64(m.PriceMult)
	e.i64(m.CostCUMult)
	e.i64(m.CostPUMult)
	e.i64(m.CostKUMult)
	e.i64(m.CostSlotMult)
	e.i64(m.PowerCostMult)
	e.i64(m.CoolingBurdenMult)
	e.i64(m.LandCostMult)
	e.i64(m.LatencyExtra)
	e.i64(m.StaffCoverageMult)
	e.i64(m.IncidentDrag)
	e.i64(m.CapacityStrand)
	e.i64(m.RateMult)
	for k := 0; k < t.NACCEL; k++ {
		e.i64(m.MixShift[k])
	}
	e.boolean(m.FreezeGrowth)
}

// EncodeState serializes a State canonically.
func EncodeState(s *t.State) []byte {
	e := &enc{b: make([]byte, 0, 768)}
	e.u8(CodecVersion)
	e.u64(s.Height)
	e.u64(s.Seed)
	e.i64(s.Capital)

	for k := 0; k < t.NACCEL; k++ {
		for r := 0; r < t.NREGION; r++ {
			e.i64(s.Servers[k][r])
		}
	}
	for r := 0; r < t.NREGION; r++ {
		e.i64(s.PowerPU[r])
	}
	for r := 0; r < t.NREGION; r++ {
		e.i64(s.CoolingKU[r])
	}
	for r := 0; r < t.NREGION; r++ {
		e.i64(s.LandAcres[r])
	}
	e.i64(s.StaffSU)
	e.i64(s.NetworkGbps)

	for k := 0; k < t.NACCEL; k++ {
		e.i64(s.TypePrice[k])
	}
	for k := 0; k < t.NACCEL; k++ {
		e.i64(s.DemandMix[k])
	}
	e.i64(s.MarketDemandCU)
	for r := 0; r < t.NREGION; r++ {
		e.i64(s.OperSmooth[r])
	}

	e.u64(uint64(len(s.Events)))
	for i := range s.Events {
		ev := s.Events[i]
		e.u8(uint8(ev.Cat))
		e.str(ev.Name)
		e.u8(uint8(ev.Region))
		e.i64(ev.Remaining)
		e.mod(ev.Mod)
	}

	for i := range s.Competitors {
		c := s.Competitors[i]
		e.str(c.Name)
		for k := 0; k < t.NACCEL; k++ {
			e.i64(c.Fleet[k])
		}
		e.i64(c.Capital)
		e.u8(uint8(c.RegionFocus))
		e.u8(uint8(c.TypeFocus))
		e.i64(c.SpendRate)
		e.i64(c.Score)
	}

	e.i64(s.SeasonScore)
	e.i64(s.PeakCapacity)
	e.i64(s.LifetimeUCD)
	e.i64(s.LifetimeOpEx)
	e.i64(s.LifetimeGross)
	e.boolean(s.RegionsUnlocked)
	e.boolean(s.NetworkUnlocked)
	e.boolean(s.LeverageUnlocked)
	e.i64(s.Debt)
	e.i64(s.DebtRate)
	e.i64(s.FundingReserve)
	e.i64(s.LastFundingBlock)
	e.i64(s.RedWeeks)
	e.boolean(s.GameOver)
	e.i64(s.StartCash)
	e.i64(s.PrestigeLevel)
	return e.b
}

// StateHash is the canonical fingerprint of a State, identical across hosts and
// architectures. This is what golden-trajectory tests pin and what on-chain
// consensus would compare.
func StateHash(s *t.State) [32]byte {
	return sha256.Sum256(EncodeState(s))
}

// ---- decoding (round-trip for on-chain storage) ----

type dec struct {
	b   []byte
	i   int
	err error
}

func (d *dec) need(n int) bool {
	if d.err != nil {
		return false
	}
	if d.i+n > len(d.b) {
		d.err = errShort
		return false
	}
	return true
}
func (d *dec) u8() uint8 {
	if !d.need(1) {
		return 0
	}
	v := d.b[d.i]
	d.i++
	return v
}
func (d *dec) boolean() bool { return d.u8() == 1 }
func (d *dec) u64() uint64 {
	if !d.need(8) {
		return 0
	}
	v := binary.BigEndian.Uint64(d.b[d.i : d.i+8])
	d.i += 8
	return v
}
func (d *dec) i64() int64 { return int64(d.u64()) }
func (d *dec) str() string {
	n := int(d.u64())
	if n < 0 || !d.need(n) {
		return ""
	}
	s := string(d.b[d.i : d.i+n])
	d.i += n
	return s
}
func (d *dec) mod() t.Modifier {
	var md t.Modifier
	md.DemandMult = d.i64()
	md.PriceMult = d.i64()
	md.CostCUMult = d.i64()
	md.CostPUMult = d.i64()
	md.CostKUMult = d.i64()
	md.CostSlotMult = d.i64()
	md.PowerCostMult = d.i64()
	md.CoolingBurdenMult = d.i64()
	md.LandCostMult = d.i64()
	md.LatencyExtra = d.i64()
	md.StaffCoverageMult = d.i64()
	md.IncidentDrag = d.i64()
	md.CapacityStrand = d.i64()
	md.RateMult = d.i64()
	for k := 0; k < t.NACCEL; k++ {
		md.MixShift[k] = d.i64()
	}
	md.FreezeGrowth = d.boolean()
	return md
}

var errShort = errBadCodec("dcb/codec: input truncated")

type errBadCodec string

func (e errBadCodec) Error() string { return string(e) }

// DecodeState is the inverse of EncodeState. It is the on-chain read path:
// validators decode stored player state, advance it, and re-encode.
func DecodeState(b []byte) (t.State, error) {
	d := &dec{b: b}
	var s t.State
	if v := d.u8(); v != CodecVersion {
		return s, errBadCodec("dcb/codec: unsupported state codec version")
	}
	s.Height = d.u64()
	s.Seed = d.u64()
	s.Capital = d.i64()

	for k := 0; k < t.NACCEL; k++ {
		for r := 0; r < t.NREGION; r++ {
			s.Servers[k][r] = d.i64()
		}
	}
	for r := 0; r < t.NREGION; r++ {
		s.PowerPU[r] = d.i64()
	}
	for r := 0; r < t.NREGION; r++ {
		s.CoolingKU[r] = d.i64()
	}
	for r := 0; r < t.NREGION; r++ {
		s.LandAcres[r] = d.i64()
	}
	s.StaffSU = d.i64()
	s.NetworkGbps = d.i64()

	for k := 0; k < t.NACCEL; k++ {
		s.TypePrice[k] = d.i64()
	}
	for k := 0; k < t.NACCEL; k++ {
		s.DemandMix[k] = d.i64()
	}
	s.MarketDemandCU = d.i64()
	for r := 0; r < t.NREGION; r++ {
		s.OperSmooth[r] = d.i64()
	}

	nEvents := int(d.u64())
	if nEvents > 0 && nEvents <= len(b) {
		s.Events = make([]t.ActiveEvent, 0, nEvents)
		for k := 0; k < nEvents; k++ {
			var ev t.ActiveEvent
			ev.Cat = t.Category(d.u8())
			ev.Name = d.str()
			ev.Region = int8(d.u8())
			ev.Remaining = d.i64()
			ev.Mod = d.mod()
			s.Events = append(s.Events, ev)
		}
	}
	for i := 0; i < 4; i++ {
		var c t.Competitor
		c.Name = d.str()
		for k := 0; k < t.NACCEL; k++ {
			c.Fleet[k] = d.i64()
		}
		c.Capital = d.i64()
		c.RegionFocus = int8(d.u8())
		c.TypeFocus = int8(d.u8())
		c.SpendRate = d.i64()
		c.Score = d.i64()
		s.Competitors[i] = c
	}
	s.SeasonScore = d.i64()
	s.PeakCapacity = d.i64()
	s.LifetimeUCD = d.i64()
	s.LifetimeOpEx = d.i64()
	s.LifetimeGross = d.i64()
	s.RegionsUnlocked = d.boolean()
	s.NetworkUnlocked = d.boolean()
	s.LeverageUnlocked = d.boolean()
	s.Debt = d.i64()
	s.DebtRate = d.i64()
	s.FundingReserve = d.i64()
	s.LastFundingBlock = d.i64()
	s.RedWeeks = d.i64()
	s.GameOver = d.boolean()
	s.StartCash = d.i64()
	s.PrestigeLevel = d.i64()
	return s, d.err
}

// EncodePolicy canonically serializes a Policy.
func EncodePolicy(p *t.Policy) []byte {
	e := &enc{b: make([]byte, 0, 64)}
	for r := 0; r < t.NREGION; r++ {
		e.i64(p.RegionWeights[r])
	}
	e.u8(p.LeverageX)
	return e.b
}

// DecodePolicy is the inverse of EncodePolicy.
func DecodePolicy(b []byte) (t.Policy, error) {
	d := &dec{b: b}
	var p t.Policy
	for r := 0; r < t.NREGION; r++ {
		p.RegionWeights[r] = d.i64()
	}
	p.LeverageX = d.u8()
	return p, d.err
}
