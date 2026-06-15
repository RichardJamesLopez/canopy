package dcbmath

import "encoding/binary"

// RNG is a deterministic SplitMix64 stream. It is the ONLY source of
// randomness in the engine. It is seeded from a 32-byte block seed plus a
// domain tag, so different subsystems (events, market, competitors) draw from
// independent, reproducible streams — "domain separation". The draw order
// within a block is normative: changing it changes every outcome.
type RNG struct {
	state uint64
}

// Domain tags keep subsystem streams independent. They are part of the rules
// and must never change for a shipped season.
const (
	DomEvents      uint64 = 0x4556_454e_5453_0001 // "EVENTS"
	DomMarket      uint64 = 0x4d41_524b_4554_0002 // "MARKET"
	DomCompetitors uint64 = 0x434f_4d50_4554_0003 // "COMPET"
	DomEventTarget uint64 = 0x5441_5247_4554_0004 // "TARGET"
)

// NewRNG builds a stream from a 32-byte seed and a domain tag. The seed is
// folded into a single 64-bit word, then mixed with the domain, then run
// through one SplitMix64 step so nearby seeds diverge immediately.
func NewRNG(seed [32]byte, domain uint64) *RNG {
	s := binary.BigEndian.Uint64(seed[0:8])
	s ^= binary.BigEndian.Uint64(seed[8:16])
	s ^= binary.BigEndian.Uint64(seed[16:24])
	s ^= binary.BigEndian.Uint64(seed[24:32])
	s ^= domain
	r := &RNG{state: s}
	r.next() // discard one to decorrelate the folded seed
	return r
}

// next advances the SplitMix64 state and returns the next 64-bit value.
func (r *RNG) next() uint64 {
	r.state += 0x9e3779b97f4a7c15
	z := r.state
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
}

// Uint64 returns the next raw 64-bit draw.
func (r *RNG) Uint64() uint64 { return r.next() }

// Intn returns a value in [0, n) using Lemire's unbiased reduction.
func (r *RNG) Intn(n int64) int64 {
	if n <= 0 {
		panic("dcbmath: Intn non-positive")
	}
	// 64x64 -> high 64 bits gives a uniform map onto [0, n) for our purposes.
	hi, _ := mul64(r.next(), uint64(n))
	return int64(hi)
}

// RangeFP returns a fixed-point value uniformly in [lo, hi] (inclusive of lo,
// exclusive of hi at the resolution of ONE). Used for market jitter etc.
func (r *RNG) RangeFP(lo, hi FP) FP {
	if hi <= lo {
		return lo
	}
	span := hi - lo
	hiBits, _ := mul64(r.next(), uint64(span))
	return lo + int64(hiBits)
}

// Chance returns true with probability p (fixed-point in [0, ONE]).
func (r *RNG) Chance(p FP) bool {
	if p <= 0 {
		return false
	}
	if p >= ONE {
		return true
	}
	hi, _ := mul64(r.next(), uint64(ONE))
	return int64(hi) < p
}

// WeightedPick chooses an index in [0, len(weights)) proportional to weights.
// Returns -1 if all weights are zero. Deterministic given the stream.
func (r *RNG) WeightedPick(weights []int64) int {
	var total int64
	for _, w := range weights {
		if w > 0 {
			total += w
		}
	}
	if total <= 0 {
		return -1
	}
	roll := r.Intn(total)
	var acc int64
	for i, w := range weights {
		if w <= 0 {
			continue
		}
		acc += w
		if roll < acc {
			return i
		}
	}
	return len(weights) - 1
}

// mul64 is the 128-bit multiply used to map a uniform 64-bit draw onto a range
// without modulo bias.
func mul64(a, b uint64) (hi, lo uint64) {
	const mask = 0xffffffff
	a0, a1 := a&mask, a>>32
	b0, b1 := b&mask, b>>32
	t := a0 * b0
	w0 := t & mask
	k := t >> 32
	t = a1*b0 + k
	w1 := t & mask
	w2 := t >> 32
	t = a0*b1 + w1
	k = t >> 32
	hi = a1*b1 + w2 + k
	lo = (t << 32) | w0
	return hi, lo
}
