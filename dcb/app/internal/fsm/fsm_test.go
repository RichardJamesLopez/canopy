package fsm

import (
	"crypto/sha256"
	"testing"

	t "dcbapp/internal/dcbtypes"
	"dcbapp/internal/engine"
)

// mutHost is a test Host whose height we advance manually ("mining blocks") and
// whose per-block seed is the same shared-world seed the local driver uses — so
// the FSM and the local eager loop face an identical world.
type mutHost struct {
	h    uint64
	seed [32]byte
}

func (m *mutHost) Height() uint64              { return m.h }
func (m *mutHost) Seed(height uint64) [32]byte { return engine.WorldSeed(m.seed, height) }

func season(b byte) [32]byte {
	var s [32]byte
	for i := range s {
		s[i] = b
	}
	return s
}

// eagerReference advances a fresh season block-by-block under a policy schedule:
// policy p0 for heights [0,switchAt), then p1. `acts` applies a direct-build
// action at the top of the iteration where state.Height == the keyed height
// (i.e. after the prior block's Step and before this height's Step) — exactly
// where the FSM's catch-up-then-mutate handlers land it. This is the ground
// truth the lazy FSM must reproduce exactly.
func eagerReference(seed [32]byte, p0, p1 t.Policy, switchAt, n uint64, acts map[uint64]func(*t.State, t.Policy)) t.State {
	s := engine.NewSeason(seed, 0)
	for h := uint64(0); h < n; h++ {
		pol := p0
		if h >= switchAt {
			pol = p1
		}
		if act, ok := acts[h]; ok {
			act(&s, pol)
		}
		ctx := t.StepContext{Height: h, Seed: engine.WorldSeed(seed, h), RulesVersion: engine.RulesVersion}
		s, _ = engine.Step(s, pol, ctx)
	}
	return s
}

// TestLocalVsFSMParity is the Phase 3 acceptance gate: a player advanced lazily
// through FSM transactions (StartRun → SetPolicy → Checkpoint) reaches a
// bit-identical state to the eager block-by-block reference. This proves lazy
// catch-up == eager evaluation, which is what makes on-chain consensus and
// off-chain replay agree.
func TestLocalVsFSMParity(t_ *testing.T) {
	seed := season(0x5e)
	p0 := engine.DefaultPolicy()
	p1 := engine.DefaultPolicy()
	p1.RegionWeights = [t.NREGION]int64{t.RegNordics: 60, t.RegEmerging: 40}

	const buyAt, switchAt, n = 3, 60, 240

	// A direct-build action schedule applied at buyAt (under p0, the pre-switch
	// policy). The eager reference and the FSM must apply it identically.
	acts := map[uint64]func(*t.State, t.Policy){
		buyAt: func(s *t.State, p t.Policy) {
			_ = engine.Buy(s, p, t.AccGPU, 50)
			_ = engine.BuyPower(s, p, 200)
			_ = engine.BuyCooling(s, p, 200)
			_ = engine.Hire(s, 10)
		},
	}
	want := eagerReference(seed, p0, p1, switchAt, n, acts)

	host := &mutHost{h: 0, seed: seed}
	f := New(NewMemStore(), host, seed)

	// Block 0: player joins (default policy p0).
	if err := f.StartRun(1, "you", 0); err != nil {
		t_.Fatal(err)
	}
	// Mine to buyAt, then run the direct-build actions (catches up under p0).
	host.h = buyAt
	if err := f.Buy(1, t.AccGPU, 50); err != nil {
		t_.Fatal(err)
	}
	if err := f.BuyInfra(1, t.InfraPower, 200); err != nil {
		t_.Fatal(err)
	}
	if err := f.BuyInfra(1, t.InfraCooling, 200); err != nil {
		t_.Fatal(err)
	}
	if err := f.Hire(1, 10); err != nil {
		t_.Fatal(err)
	}
	// Mine to switchAt, then commit p1 (catches up under p0 first).
	host.h = switchAt
	if err := f.SetPolicy(1, p1); err != nil {
		t_.Fatal(err)
	}
	// Mine to n, then checkpoint (catches up under p1).
	host.h = n
	if err := f.Checkpoint(1); err != nil {
		t_.Fatal(err)
	}

	rec, ok := f.GetPlayer(1)
	if !ok {
		t_.Fatal("player vanished")
	}
	if engine.StateHash(&rec.State) != engine.StateHash(&want) {
		t_.Fatalf("FSM lazy state diverged from eager reference\n  fsm  height=%d score=%d\n  want height=%d score=%d",
			rec.State.Height, rec.State.SeasonScore, want.Height, want.SeasonScore)
	}
	t_.Logf("parity OK: height=%d score=%d cap=%d", rec.State.Height, rec.State.SeasonScore, rec.State.PeakCapacity)
}

// TestCatchUpInChunks: a single tx never replays more than MaxCatchUp blocks,
// but repeated checkpoints still converge to the eager reference.
func TestCatchUpInChunks(t_ *testing.T) {
	seed := season(0x77)
	p := engine.DefaultPolicy()
	const n = 6000
	want := eagerReference(seed, p, p, 0, n, nil)

	host := &mutHost{h: n, seed: seed}
	f := New(NewMemStore(), host, seed)
	f.SetMaxCatchUp(1000) // force several chunks
	host.h = 0
	if err := f.StartRun(1, "you", 0); err != nil {
		t_.Fatal(err)
	}
	host.h = n
	for i := 0; i < 10; i++ { // 10 * 1000 >= 6000
		if err := f.Checkpoint(1); err != nil {
			t_.Fatal(err)
		}
	}
	rec, _ := f.GetPlayer(1)
	// Chunked lazy catch-up must equal the eager reference bit-for-bit (both
	// freeze identically if the run game-overs within the window).
	if engine.StateHash(&rec.State) != engine.StateHash(&want) {
		t_.Fatalf("chunked catch-up diverged from eager reference (height %d vs %d)", rec.State.Height, want.Height)
	}
}

// TestRecordCodecRoundTrip: encode/decode of a player record is lossless.
func TestRecordCodecRoundTrip(t_ *testing.T) {
	seed := season(0x33)
	host := &mutHost{h: 2000, seed: seed}
	f := New(NewMemStore(), host, seed)
	host.h = 0
	f.StartRun(7, "alice", 0)
	host.h = 2000
	f.Checkpoint(7)

	rec, _ := f.GetPlayer(7)
	b := encodeRecord(&rec)
	got, err := decodeRecord(b)
	if err != nil {
		t_.Fatal(err)
	}
	if got.Name != rec.Name {
		t_.Fatalf("name mismatch: %q vs %q", got.Name, rec.Name)
	}
	if engine.StateHash(&got.State) != engine.StateHash(&rec.State) {
		t_.Fatal("state round-trip mismatch")
	}
	if sha256.Sum256(engine.EncodePolicy(&got.Policy)) != sha256.Sum256(engine.EncodePolicy(&rec.Policy)) {
		t_.Fatal("policy round-trip mismatch")
	}
}

// TestLeaderboardIndex: the on-chain leaderboard ranks real players by score.
func TestLeaderboardIndex(t_ *testing.T) {
	seed := season(0x44)
	host := &mutHost{h: 0, seed: seed}
	f := New(NewMemStore(), host, seed)
	for id := uint64(1); id <= 3; id++ {
		f.StartRun(id, string(rune('A'+id-1)), 0)
	}
	host.h = 3000
	for id := uint64(1); id <= 3; id++ {
		f.Checkpoint(id)
	}
	lb := f.Leaderboard()
	if len(lb) != 3 {
		t_.Fatalf("expected 3 entries, got %d", len(lb))
	}
	for i := 1; i < len(lb); i++ {
		if lb[i-1].Score < lb[i].Score {
			t_.Fatal("leaderboard not sorted descending")
		}
	}
}

// BenchmarkCatchUp measures the cost of replaying blocks — informs MaxCatchUp
// against Canopy's per-tx compute budget.
func BenchmarkCatchUp(b *testing.B) {
	seed := season(0x9a)
	for i := 0; i < b.N; i++ {
		host := &mutHost{h: 0, seed: seed}
		f := New(NewMemStore(), host, seed)
		f.StartRun(1, "you", 0)
		host.h = 1000
		f.Checkpoint(1) // replay 1000 blocks
	}
}
