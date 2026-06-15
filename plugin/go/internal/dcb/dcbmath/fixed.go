// Package dcbmath is the determinism backbone for DCB.
//
// It provides fixed-point arithmetic (int64 scaled by ONE = 1e6) and a seeded
// PRNG. The economic engine uses ONLY these primitives — never float64, never
// math/rand — so that every state transition is bit-identical across machines,
// architectures, and the local-vs-on-chain hosts. That bit-identity is what
// makes on-chain consensus possible.
package dcbmath

import "math/bits"

// FP is a fixed-point number with an implied scale of ONE (1e6).
// A value of 1.5 is stored as 1_500_000.
type FP = int64

// ONE is the fixed-point scale: 1.0 == 1_000_000.
const ONE FP = 1_000_000

// FromInt converts a whole number to fixed-point.
func FromInt(n int64) FP { return n * ONE }

// ToInt truncates a fixed-point value toward zero to a whole number.
func ToInt(x FP) int64 { return x / ONE }

// Pct builds a fixed-point fraction from a percentage (50 -> 0.5).
func Pct(p int64) FP { return p * ONE / 100 }

// Mul multiplies two fixed-point values: (a*b)/ONE, using a 128-bit
// intermediate so there is no overflow for any in-range operands. The result
// is truncated toward zero. Panics if the true result overflows int64 — we
// want a loud, deterministic failure rather than silent wraparound that would
// fork consensus.
func Mul(a, b FP) FP {
	neg := false
	ua, ub := a, b
	if ua < 0 {
		ua, neg = -ua, !neg
	}
	if ub < 0 {
		ub, neg = -ub, !neg
	}
	hi, lo := bits.Mul64(uint64(ua), uint64(ub))
	// Divide the 128-bit product by ONE.
	q, _ := bits.Div64(hi, lo, uint64(ONE))
	if q > uint64(1<<63-1) {
		panic("dcbmath: Mul overflow")
	}
	r := int64(q)
	if neg {
		return -r
	}
	return r
}

// Div divides two fixed-point values: (a*ONE)/b, truncated toward zero.
// Panics on divide-by-zero or overflow (loud and deterministic).
func Div(a, b FP) FP {
	if b == 0 {
		panic("dcbmath: Div by zero")
	}
	neg := false
	ua, ub := a, b
	if ua < 0 {
		ua, neg = -ua, !neg
	}
	if ub < 0 {
		ub, neg = -ub, !neg
	}
	hi, lo := bits.Mul64(uint64(ua), uint64(ONE))
	if hi >= uint64(ub) {
		panic("dcbmath: Div overflow")
	}
	q, _ := bits.Div64(hi, lo, uint64(ub))
	if q > uint64(1<<63-1) {
		panic("dcbmath: Div overflow")
	}
	r := int64(q)
	if neg {
		return -r
	}
	return r
}

// MulDiv computes a*b/c on plain integers with a 128-bit intermediate.
// Used for proportional splits (e.g. budget * weight / sumWeights) where we
// want exact integer floor division without an intermediate fixed-point scale.
func MulDiv(a, b, c int64) int64 {
	if c == 0 {
		panic("dcbmath: MulDiv by zero")
	}
	neg := false
	ua, ub, uc := a, b, c
	if ua < 0 {
		ua, neg = -ua, !neg
	}
	if ub < 0 {
		ub, neg = -ub, !neg
	}
	if uc < 0 {
		uc, neg = -uc, !neg
	}
	hi, lo := bits.Mul64(uint64(ua), uint64(ub))
	if hi >= uint64(uc) {
		panic("dcbmath: MulDiv overflow")
	}
	q, _ := bits.Div64(hi, lo, uint64(uc))
	if q > uint64(1<<63-1) {
		panic("dcbmath: MulDiv overflow")
	}
	r := int64(q)
	if neg {
		return -r
	}
	return r
}

// ClampFP constrains x to [lo, hi].
func ClampFP(x, lo, hi FP) FP {
	if x < lo {
		return lo
	}
	if x > hi {
		return hi
	}
	return x
}

// ClampInt constrains x to [lo, hi].
func ClampInt(x, lo, hi int64) int64 {
	if x < lo {
		return lo
	}
	if x > hi {
		return hi
	}
	return x
}

// MinInt returns the smaller of the arguments; the multi-arg form is the
// Leontief bottleneck used by the production function.
func MinInt(xs ...int64) int64 {
	m := xs[0]
	for _, x := range xs[1:] {
		if x < m {
			m = x
		}
	}
	return m
}

// MaxInt returns the larger of a and b.
func MaxInt(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
