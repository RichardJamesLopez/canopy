package driver

import (
	t "dcbapp/internal/dcbtypes"
	"dcbapp/internal/engine"
	"dcbapp/internal/fsm"
)

// FSMDriver runs the game through the on-chain state machine (pkg/fsm) in
// process, instead of stepping the engine directly. It satisfies the same
// driver.Driver interface as Local, so the TUI and web UI render against the
// chain code path with no changes — proving the seam. Because lazy FSM
// catch-up is provably equal to the eager engine loop (TestLocalVsFSMParity),
// this produces identical results to Local; the value is architectural: this
// is the template the real chainread driver (RPC-backed) will follow.
type FSMDriver struct {
	base     [32]byte
	player   uint64
	name     string
	season   int
	prestige int64

	fsm  *fsm.FSM
	host *fsm.SeedHost

	policy t.Policy
	recent []t.BlockReport

	lastRank  int
	lastScore int64
}

// NewFSM starts season 1 for one player, running through an in-process FSM.
func NewFSM(base [32]byte, player uint64, name string, prestige int64) *FSMDriver {
	d := &FSMDriver{base: base, player: player, name: name, season: 1, prestige: prestige}
	d.startSeason()
	return d
}

func (d *FSMDriver) startSeason() {
	seed := engine.SeasonSeed(d.base, d.season)
	d.host = &fsm.SeedHost{H: 0, SeasonSeed: seed}
	d.fsm = fsm.New(fsm.NewMemStore(), d.host, seed)
	d.policy = engine.DefaultPolicy()
	_ = d.fsm.StartRun(d.player, d.name, d.prestige)
	d.recent = d.recent[:0]
}

// Tick advances n blocks via FSM transactions, rolling into the next season at
// the boundary, and returns the per-chunk reports.
func (d *FSMDriver) Tick(n int) []t.BlockReport {
	d.host.H += uint64(n)
	rep, _ := d.fsm.AdvanceReport(d.player)
	out := []t.BlockReport{rep}
	d.recent = append(d.recent, out...)
	if len(d.recent) > recentCap {
		d.recent = append([]t.BlockReport(nil), d.recent[len(d.recent)-recentCap:]...)
	}
	return out
}

// Submit commits a new policy via a SetPolicy transaction.
func (d *FSMDriver) Submit(p t.Policy) {
	d.policy = p
	_ = d.fsm.SetPolicy(d.player, p)
}

// Direct-build actions route through the FSM (catch-up-then-mutate txs).
func (d *FSMDriver) Buy(kind int, qty int64) error  { return d.fsm.Buy(d.player, kind, qty) }
func (d *FSMDriver) Sell(kind int, qty int64) error { return d.fsm.Sell(d.player, kind, qty) }
func (d *FSMDriver) Hire(n int64) error             { return d.fsm.Hire(d.player, n) }
func (d *FSMDriver) Fire(n int64) error             { return d.fsm.Fire(d.player, n) }
func (d *FSMDriver) BuyInfra(infra int, qty int64) error {
	return d.fsm.BuyInfra(d.player, infra, qty)
}

// Fund / Repay route through the FSM (funding tx).
func (d *FSMDriver) Fund(dollars int64)  { _ = d.fsm.Fund(d.player, dollars) }
func (d *FSMDriver) Repay(dollars int64) { _ = d.fsm.RepayDebt(d.player, dollars) }

// EndGame ends the run.
func (d *FSMDriver) EndGame() { d.fsm.EndGame(d.player) }

// Snapshot reads the player's current on-chain record.
func (d *FSMDriver) Snapshot() Snapshot {
	rec, _ := d.fsm.GetPlayer(d.player)
	return Snapshot{
		State:           rec.State,
		Policy:          d.policy,
		Recent:          d.recent,
		SeasonBlocks:    engine.SeasonBlocks,
		PlayerName:      d.name,
		SeasonNum:       d.season,
		Prestige:        d.prestige,
		LastSeasonRank:  d.lastRank,
		LastSeasonScore: d.lastScore,
	}
}
