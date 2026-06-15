package fsm

import "dcbapp/internal/engine"

// SeedHost is a Host backed by a season seed with a settable height. It is the
// off-chain / local implementation of Host: the per-block entropy is the shared
// world seed. On Canopy this is replaced by an adapter that reports the chain
// height and reads the per-block seed recorded each BeginBlock (see CANOPY.md).
type SeedHost struct {
	H          uint64
	SeasonSeed [32]byte
}

func (h *SeedHost) Height() uint64              { return h.H }
func (h *SeedHost) Seed(height uint64) [32]byte { return engine.WorldSeed(h.SeasonSeed, height) }
