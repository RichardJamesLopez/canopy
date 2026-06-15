// Package driver hosts the deterministic engine. A Driver feeds the engine its
// per-block inputs (height, seed) and holds the resulting state. It is the ONLY
// layer allowed to touch wall-clock, entropy sources, or the network — the
// engine stays pure. The local driver runs everything in-process for the
// prototype; a future chainread driver will satisfy the same interface against
// a live Canopy node, leaving the engine and TUI untouched.
package driver

import (
	t "dcbapp/internal/dcbtypes"
)

// Snapshot is a read-only view of the current game for the UI.
type Snapshot struct {
	State        t.State
	Policy       t.Policy
	Recent       []t.BlockReport // most recent blocks (oldest first)
	SeasonBlocks uint64
	PlayerName   string

	// Season meta.
	SeasonNum       int   // 1-based current season
	Prestige        int64 // current prestige level (carried across seasons)
	LastSeasonRank  int   // finishing rank of the previous season (0 if none)
	LastSeasonScore int64 // score of the previous season (0 if none)
}

// Driver advances and exposes a single player's game.
type Driver interface {
	// Tick advances exactly n blocks and returns the per-block reports.
	Tick(n int) []t.BlockReport
	// Submit commits a new standing policy (region weights, leverage).
	Submit(p t.Policy)
	// Buy purchases qty units of accelerator type `kind`.
	Buy(kind int, qty int64) error
	// Sell liquidates qty units of accelerator type `kind`.
	Sell(kind int, qty int64) error
	// Hire/Fire change staff in multiples of 10.
	Hire(n int64) error
	Fire(n int64) error
	// BuyInfra buys qty of a shared-infra kind (power/cooling/land/network).
	BuyInfra(infra int, qty int64) error
	// Fund draws `dollars` of credit at the current funding rate (debt + reserve).
	Fund(dollars int64)
	// Repay pays down `dollars` of debt principal.
	Repay(dollars int64)
	// EndGame ends the run (the front-end records the final score).
	EndGame()
	// Snapshot returns the current state for rendering.
	Snapshot() Snapshot
}
