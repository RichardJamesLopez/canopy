package engine

import (
	m "dcbapp/internal/dcbmath"
	t "dcbapp/internal/dcbtypes"
)

// FundRateLeverageCeil caps the leverage-escalated offered rate.
var FundRateLeverageCeil = m.FP(12_000) // ~1.2%/week hard ceiling on the offered rate

// FundingOffer is the rate a NEW draw would be locked at right now: the market
// funding rate escalated by leverage (debt ÷ net worth), so stacking debt — or
// borrowing while broke — gets expensive. Display this to the player.
func FundingOffer(s *t.State, height uint64) m.FP {
	base := FundingRate(height, s.Events)
	nwFP := m.FromInt(NetWorth(s))
	if nwFP < m.ONE {
		nwFP = m.ONE
	}
	lev := m.ONE + m.Div(s.Debt, nwFP)
	return m.ClampFP(m.Mul(base, lev), FundRateMin, FundRateLeverageCeil)
}

// FundingAvailable reports whether the once-a-year cooldown has elapsed.
func FundingAvailable(s *t.State, height uint64) bool {
	return int64(height)-s.LastFundingBlock >= BlocksPerYear
}

// TakeFunding draws `amount` of credit at the leverage-escalated offered rate.
// The principal is added directly to Cash and booked as a Debt liability.
// Subject to a once-per-year cooldown; a draw during the cooldown is a no-op.
// The blended DebtRate is locked at draw.
func TakeFunding(s *t.State, amount m.FP, height uint64) {
	if amount <= 0 || !FundingAvailable(s, height) {
		return
	}
	rate := FundingOffer(s, height)
	newDebt := s.Debt + amount
	// principal-weighted blend of the locked rates
	s.DebtRate = m.Div(m.Mul(s.Debt, s.DebtRate)+m.Mul(amount, rate), newDebt)
	s.Debt = newDebt
	s.Capital += amount // borrowed money goes straight to cash
	s.LastFundingBlock = int64(height)
}

// FundDollars / RepayDollars are whole-dollar wrappers for the drivers (so they
// need not import the fixed-point package).
func FundDollars(s *t.State, dollars int64, height uint64) {
	TakeFunding(s, m.FromInt(dollars), height)
}
func RepayDollars(s *t.State, dollars int64) { Repay(s, m.FromInt(dollars)) }

// Repay pays down debt principal from cash. Clears the rate once fully repaid.
func Repay(s *t.State, amount m.FP) {
	if amount <= 0 || s.Debt <= 0 {
		return
	}
	if amount > s.Debt {
		amount = s.Debt
	}
	// Repay as much as cash allows; can't repay more than we have.
	if amount > s.Capital {
		amount = s.Capital
	}
	s.Capital -= amount
	s.Debt -= amount
	if s.Debt <= 0 {
		s.Debt = 0
		s.DebtRate = 0
	}
}
