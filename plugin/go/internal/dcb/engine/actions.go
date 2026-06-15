package engine

import (
	m "github.com/canopy-network/go-plugin/internal/dcb/dcbmath"
	t "github.com/canopy-network/go-plugin/internal/dcb/dcbtypes"
)

// Direct-build actions. These are PURE mutators of (state, args) — no RNG, no
// clock — applied as transactions BETWEEN Steps. The player buys/sells discrete
// units at deterministic base prices, paid from the funding reserve first then
// cash. Purchases are validated against the investable cap (cash + reserve) so
// buying can never push you into the red on its own; only opex does.
//
// Buy prices are the static CostServer/CostPU/CostKU/CostAcre/HireCost — event
// cost modifiers do NOT touch purchases (they are resolved only inside Step).

// actionError is a typed error for the action layer.
type actionError string

func (e actionError) Error() string { return string(e) }

const (
	ErrBadQty        = actionError("dcb: quantity must be positive")
	ErrBadStep       = actionError("dcb: staff must be hired/fired in multiples of 10")
	ErrInsufficient  = actionError("dcb: insufficient funds")
	ErrNotEnough     = actionError("dcb: not enough units to sell")
	ErrNetworkLocked = actionError("dcb: network not unlocked")
	ErrBadKind       = actionError("dcb: unknown kind")
)

// SellRefundPct is the fraction of a unit's buy price returned when sold back.
var SellRefundPct = m.Pct(50)

// CostGbps is the buy price for one Gbps of network capacity.
var CostGbps = m.FromInt(5_000)

// affordable reports whether cost fits within current cash.
func affordable(s *t.State, cost m.FP) bool {
	return cost <= s.Capital
}

// pay deducts cost from cash. Caller must have already checked affordability.
func pay(s *t.State, cost m.FP) {
	s.Capital -= cost
}

// regionWeights returns the effective placement weights: the policy's weights
// once regions are unlocked, otherwise the home region only.
func regionWeights(s *t.State, p t.Policy) [t.NREGION]int64 {
	if !s.RegionsUnlocked {
		return [t.NREGION]int64{t.RegVirginia: 1}
	}
	rw := p.RegionWeights
	var sum int64
	for _, x := range rw {
		if x > 0 {
			sum += x
		}
	}
	if sum == 0 {
		return [t.NREGION]int64{t.RegVirginia: 1}
	}
	return rw
}

// Buy purchases qty units of an accelerator type, distributing them across
// regions by the policy's region weights (home-only pre-unlock).
func Buy(s *t.State, p t.Policy, kind int, qty int64) error {
	if kind < 0 || kind >= t.NACCEL {
		return ErrBadKind
	}
	if qty <= 0 {
		return ErrBadQty
	}
	cost := m.Mul(m.FromInt(qty), BuyPriceServer(kind, s.Height))
	if !affordable(s, cost) {
		return ErrInsufficient
	}
	dist := distribute(qty, regionWeights(s, p))
	for r := 0; r < t.NREGION; r++ {
		s.Servers[kind][r] += dist[r]
	}
	pay(s, cost)
	return nil
}

// Sell liquidates qty units of an accelerator type across regions (proportional
// to current holdings), refunding SellRefundPct of the buy price.
func Sell(s *t.State, kind int, qty int64) error {
	if kind < 0 || kind >= t.NACCEL {
		return ErrBadKind
	}
	if qty <= 0 {
		return ErrBadQty
	}
	var owned int64
	var holdings [t.NREGION]int64
	for r := 0; r < t.NREGION; r++ {
		holdings[r] = s.Servers[kind][r]
		owned += holdings[r]
	}
	if qty > owned {
		return ErrNotEnough
	}
	dist := distribute(qty, holdings)
	for r := 0; r < t.NREGION; r++ {
		n := dist[r]
		if n > s.Servers[kind][r] {
			n = s.Servers[kind][r]
		}
		s.Servers[kind][r] -= n
	}
	// Refund at the BASE buy price (not the escalated price) so rising prices
	// can't be arbitraged by buying low and selling back high.
	refund := m.Mul(m.Mul(m.FromInt(qty), CostServer[kind]), SellRefundPct)
	s.Capital += refund
	return nil
}

// Hire adds n people (must be a positive multiple of 10).
func Hire(s *t.State, n int64) error {
	if n <= 0 {
		return ErrBadQty
	}
	if n%10 != 0 {
		return ErrBadStep
	}
	cost := m.Mul(m.FromInt(n), HireCostAt(s.Height))
	if !affordable(s, cost) {
		return ErrInsufficient
	}
	s.StaffSU += n
	pay(s, cost)
	return nil
}

// Fire removes n people (positive multiple of 10, no severance/refund).
func Fire(s *t.State, n int64) error {
	if n <= 0 {
		return ErrBadQty
	}
	if n%10 != 0 {
		return ErrBadStep
	}
	if n > s.StaffSU {
		n = s.StaffSU
	}
	s.StaffSU -= n
	return nil
}

// buyShared is the common path for the per-region shared infra pools.
func buyShared(s *t.State, p t.Policy, qty int64, unit m.FP, add func(r int, n int64)) error {
	if qty <= 0 {
		return ErrBadQty
	}
	cost := m.Mul(m.FromInt(qty), unit)
	if !affordable(s, cost) {
		return ErrInsufficient
	}
	dist := distribute(qty, regionWeights(s, p))
	for r := 0; r < t.NREGION; r++ {
		add(r, dist[r])
	}
	pay(s, cost)
	return nil
}

// BuyPower adds qty power units, distributed across regions by region weights.
func BuyPower(s *t.State, p t.Policy, qty int64) error {
	return buyShared(s, p, qty, PricePU(s.Height), func(r int, n int64) { s.PowerPU[r] += n })
}

// BuyCooling adds qty cooling units.
func BuyCooling(s *t.State, p t.Policy, qty int64) error {
	return buyShared(s, p, qty, PriceKU(s.Height), func(r int, n int64) { s.CoolingKU[r] += n })
}

// BuyLand adds acres of land.
func BuyLand(s *t.State, p t.Policy, acres int64) error {
	return buyShared(s, p, acres, PriceAcre(s.Height), func(r int, n int64) { s.LandAcres[r] += n })
}

// BuyNetwork adds gbps of network capacity (gated by the Network unlock).
func BuyNetwork(s *t.State, gbps int64) error {
	if !s.NetworkUnlocked {
		return ErrNetworkLocked
	}
	if gbps <= 0 {
		return ErrBadQty
	}
	cost := m.Mul(m.FromInt(gbps), CostGbps)
	if !affordable(s, cost) {
		return ErrInsufficient
	}
	s.NetworkGbps += gbps
	pay(s, cost)
	return nil
}

// BuyInfra dispatches a shared-infra purchase by InfraKind (used by the on-chain
// transaction handler and the WASM bridge).
func BuyInfra(s *t.State, p t.Policy, infra int, qty int64) error {
	switch infra {
	case t.InfraPower:
		return BuyPower(s, p, qty)
	case t.InfraCooling:
		return BuyCooling(s, p, qty)
	case t.InfraLand:
		return BuyLand(s, p, qty)
	case t.InfraNetwork:
		return BuyNetwork(s, qty)
	default:
		return ErrBadKind
	}
}

// distribute splits count across regions by integer weights using the
// largest-remainder method, deterministic with a fixed tie-break (lower index
// wins). Sum of result == count (when sum of weights > 0).
func distribute(count int64, weights [t.NREGION]int64) [t.NREGION]int64 {
	var out [t.NREGION]int64
	var sumW int64
	for _, w := range weights {
		if w > 0 {
			sumW += w
		}
	}
	if sumW <= 0 || count <= 0 {
		return out
	}
	var assigned int64
	type rem struct {
		idx int
		r   int64
	}
	var rems []rem
	for i := 0; i < t.NREGION; i++ {
		w := weights[i]
		if w <= 0 {
			continue
		}
		q := count * w / sumW
		out[i] = q
		assigned += q
		rems = append(rems, rem{i, (count * w) % sumW})
	}
	leftover := count - assigned
	for ; leftover > 0; leftover-- {
		best := -1
		var bestR int64 = -1
		for _, rr := range rems {
			if out[rr.idx] >= 0 && rr.r > bestR {
				bestR, best = rr.r, rr.idx
			}
		}
		if best < 0 {
			break
		}
		out[best]++
		for k := range rems {
			if rems[k].idx == best {
				rems[k].r = -1
			}
		}
	}
	return out
}
