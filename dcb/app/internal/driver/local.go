package driver

import (
	t "dcbapp/internal/dcbtypes"
	"dcbapp/internal/engine"
)

// recentCap bounds the rolling window of reports kept for the UI.
const recentCap = 200

// Local is an in-process driver: it derives each block's seed from the current
// season seed and steps the engine directly. No clock, no network. It owns the
// cross-season meta (season number + prestige) and rolls over automatically at
// the season boundary. Deterministic given (base seed, player id, prestige,
// policy history + tick schedule).
type Local struct {
	base    [32]byte
	seed    [32]byte // current season's world seed
	player  uint64
	name    string
	state   t.State
	policy  t.Policy
	recent  []t.BlockReport
	season  int
	presLvl int64

	lastRank  int
	lastScore int64
}

// NewLocal starts season 1 for one player on a stable base ("world") seed.
func NewLocal(base [32]byte, player uint64, name string, prestige int64) *Local {
	d := &Local{
		base:    base,
		player:  player,
		name:    name,
		policy:  engine.DefaultPolicy(),
		season:  1,
		presLvl: prestige,
	}
	d.seed = engine.SeasonSeed(base, d.season)
	d.state = engine.NewSeason(d.seed, d.presLvl)
	return d
}

// Tick advances n blocks (open-ended survival; the engine freezes on game over).
func (d *Local) Tick(n int) []t.BlockReport {
	out := make([]t.BlockReport, 0, n)
	for i := 0; i < n; i++ {
		h := d.state.Height
		ctx := t.StepContext{
			Height:       h,
			Seed:         engine.WorldSeed(d.seed, h),
			RulesVersion: engine.RulesVersion,
		}
		ns, rep := engine.Step(d.state, d.policy, ctx)
		d.state = ns
		out = append(out, rep)
	}
	d.recent = append(d.recent, out...)
	if len(d.recent) > recentCap {
		d.recent = append([]t.BlockReport(nil), d.recent[len(d.recent)-recentCap:]...)
	}
	return out
}

// EndGame ends the run (the front-end records the final net worth).
func (d *Local) EndGame() { d.state.GameOver = true }

// Submit commits a new policy.
func (d *Local) Submit(p t.Policy) { d.policy = p }

// Direct-build actions mutate state in-process between ticks.
func (d *Local) Buy(kind int, qty int64) error  { return engine.Buy(&d.state, d.policy, kind, qty) }
func (d *Local) Sell(kind int, qty int64) error { return engine.Sell(&d.state, kind, qty) }
func (d *Local) Hire(n int64) error             { return engine.Hire(&d.state, n) }
func (d *Local) Fire(n int64) error             { return engine.Fire(&d.state, n) }
func (d *Local) BuyInfra(infra int, qty int64) error {
	return engine.BuyInfra(&d.state, d.policy, infra, qty)
}

// Fund draws credit at the current rate; Repay pays down principal.
func (d *Local) Fund(dollars int64)  { engine.FundDollars(&d.state, dollars, d.state.Height) }
func (d *Local) Repay(dollars int64) { engine.RepayDollars(&d.state, dollars) }

// Snapshot returns a view for rendering.
func (d *Local) Snapshot() Snapshot {
	return Snapshot{
		State:           d.state,
		Policy:          d.policy,
		Recent:          d.recent,
		SeasonBlocks:    engine.SeasonBlocks,
		PlayerName:      d.name,
		SeasonNum:       d.season,
		Prestige:        d.presLvl,
		LastSeasonRank:  d.lastRank,
		LastSeasonScore: d.lastScore,
	}
}
