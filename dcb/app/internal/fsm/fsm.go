package fsm

import (
	"encoding/binary"
	"errors"
	"sort"

	t "dcbapp/internal/dcbtypes"
	"dcbapp/internal/engine"
)

// Host supplies the block context the FSM cannot fetch itself: the current
// chain height and the verifiable per-block entropy at any height. On Canopy
// these come from the block header / proposer VRF; in tests from a deterministic
// world-seed function. Seed(height) MUST be reproducible for historical heights
// so lazy catch-up replays a player's trajectory exactly.
type Host interface {
	Height() uint64
	Seed(height uint64) [32]byte
}

// DefaultMaxCatchUp bounds how many blocks a single transaction will replay, so
// no one tx does unbounded work. A player who has been idle longer than this
// catches up across several interactions. Tune against the chain's per-tx
// compute budget (Phase 3 benchmark).
const DefaultMaxCatchUp uint64 = 50_000

// PlayerRecord is the on-chain per-player state: the deterministic engine State
// (whose Height is the last block it has been advanced to), the committed
// Policy, and a display name.
type PlayerRecord struct {
	Name   string
	State  t.State
	Policy t.Policy
}

// FSM is the mounted game state machine over a Store + Host.
type FSM struct {
	store      Store
	host       Host
	seasonSeed [32]byte
	maxCatchUp uint64
}

// New builds an FSM. seasonSeed seeds NewSeason for fresh players; per-block
// entropy comes from host.Seed.
func New(store Store, host Host, seasonSeed [32]byte) *FSM {
	return &FSM{store: store, host: host, seasonSeed: seasonSeed, maxCatchUp: DefaultMaxCatchUp}
}

// SetMaxCatchUp overrides the per-tx replay cap.
func (f *FSM) SetMaxCatchUp(n uint64) { f.maxCatchUp = n }

var (
	ErrExists   = errors.New("fsm: player already has a run this season")
	ErrNotFound = errors.New("fsm: no run for player")
)

// ---- transactions ----

// StartRun creates a fresh run for a player, joining at the current world
// height (a late joiner faces current market conditions with an empty site).
// prestige is the account's carried-over level (read from the account on-chain).
func (f *FSM) StartRun(id uint64, name string, prestige int64) error {
	if _, ok := f.store.Get(playerKey(id)); ok {
		return ErrExists
	}
	rec := PlayerRecord{
		Name:   name,
		State:  engine.NewSeason(f.seasonSeed, prestige),
		Policy: engine.DefaultPolicy(),
	}
	rec.State.Height = f.host.Height() // align the player's clock to the world
	f.save(id, &rec)
	f.indexLeaderboard(id, &rec)
	return nil
}

// SetPolicy catches the player up under their OLD policy to the current height
// (so the switch lands at the exact block), then commits the new policy.
func (f *FSM) SetPolicy(id uint64, p t.Policy) error {
	rec, ok := f.load(id)
	if !ok {
		return ErrNotFound
	}
	f.catchUp(rec)
	rec.Policy = p
	f.save(id, rec)
	f.indexLeaderboard(id, rec)
	return nil
}

// Checkpoint advances the player to the current height and refreshes their
// leaderboard entry. A player (or an indexer) sends this to realize idle gains.
func (f *FSM) Checkpoint(id uint64) error {
	_, err := f.AdvanceReport(id)
	return err
}

// AdvanceReport is Checkpoint that also returns the last block's report — used
// by the local FSM driver to feed the UI's "what happened" digest. (A real
// chain client reads state via RPC and peeks one block locally instead.)
func (f *FSM) AdvanceReport(id uint64) (t.BlockReport, error) {
	rec, ok := f.load(id)
	if !ok {
		return t.BlockReport{}, ErrNotFound
	}
	rep := f.catchUp(rec)
	f.save(id, rec)
	f.indexLeaderboard(id, rec)
	return rep, nil
}

// Fund catches the player up, then draws `dollars` of credit at the current rate.
func (f *FSM) Fund(id uint64, dollars int64) error {
	rec, ok := f.load(id)
	if !ok {
		return ErrNotFound
	}
	f.catchUp(rec)
	engine.FundDollars(&rec.State, dollars, f.host.Height())
	f.save(id, rec)
	f.indexLeaderboard(id, rec)
	return nil
}

// RepayDebt catches the player up, then repays `dollars` of debt principal.
func (f *FSM) RepayDebt(id uint64, dollars int64) error {
	rec, ok := f.load(id)
	if !ok {
		return ErrNotFound
	}
	f.catchUp(rec)
	engine.RepayDollars(&rec.State, dollars)
	f.save(id, rec)
	f.indexLeaderboard(id, rec)
	return nil
}

// Buy catches the player up, then purchases qty units of an accelerator type.
// The action lands at the current head height — identical to the eager
// reference — which is what preserves local-vs-FSM parity.
func (f *FSM) Buy(id uint64, kind int, qty int64) error {
	rec, ok := f.load(id)
	if !ok {
		return ErrNotFound
	}
	f.catchUp(rec)
	if err := engine.Buy(&rec.State, rec.Policy, kind, qty); err != nil {
		return err
	}
	f.save(id, rec)
	f.indexLeaderboard(id, rec)
	return nil
}

// Sell catches the player up, then liquidates qty units of a type.
func (f *FSM) Sell(id uint64, kind int, qty int64) error {
	rec, ok := f.load(id)
	if !ok {
		return ErrNotFound
	}
	f.catchUp(rec)
	if err := engine.Sell(&rec.State, kind, qty); err != nil {
		return err
	}
	f.save(id, rec)
	f.indexLeaderboard(id, rec)
	return nil
}

// Hire catches the player up, then hires n people (multiple of 10).
func (f *FSM) Hire(id uint64, n int64) error {
	rec, ok := f.load(id)
	if !ok {
		return ErrNotFound
	}
	f.catchUp(rec)
	if err := engine.Hire(&rec.State, n); err != nil {
		return err
	}
	f.save(id, rec)
	f.indexLeaderboard(id, rec)
	return nil
}

// Fire catches the player up, then fires n people (multiple of 10).
func (f *FSM) Fire(id uint64, n int64) error {
	rec, ok := f.load(id)
	if !ok {
		return ErrNotFound
	}
	f.catchUp(rec)
	if err := engine.Fire(&rec.State, n); err != nil {
		return err
	}
	f.save(id, rec)
	f.indexLeaderboard(id, rec)
	return nil
}

// BuyInfra catches the player up, then buys qty of a shared-infra kind
// (power/cooling/land/network).
func (f *FSM) BuyInfra(id uint64, infra int, qty int64) error {
	rec, ok := f.load(id)
	if !ok {
		return ErrNotFound
	}
	f.catchUp(rec)
	if err := engine.BuyInfra(&rec.State, rec.Policy, infra, qty); err != nil {
		return err
	}
	f.save(id, rec)
	f.indexLeaderboard(id, rec)
	return nil
}

// EndGame marks the player's run over (records via the front-end).
func (f *FSM) EndGame(id uint64) {
	rec, ok := f.load(id)
	if !ok {
		return
	}
	rec.State.GameOver = true
	f.save(id, rec)
	f.indexLeaderboard(id, rec)
}

// catchUp replays the deterministic trajectory from the player's last height to
// the current world height (bounded by maxCatchUp), using the verifiable
// per-block seed. Because seeds are reproducible, this equals an eager
// block-by-block advance — the property the parity test pins. Returns the last
// block's report (zero value if nothing was replayed).
func (f *FSM) catchUp(rec *PlayerRecord) t.BlockReport {
	target := f.host.Height()
	var advanced uint64
	var last t.BlockReport
	for rec.State.Height < target && advanced < f.maxCatchUp && !rec.State.GameOver {
		h := rec.State.Height
		ctx := t.StepContext{Height: h, Seed: f.host.Seed(h), RulesVersion: engine.RulesVersion}
		rec.State, last = engine.Step(rec.State, rec.Policy, ctx)
		advanced++
	}
	return last
}

// ---- reads ----

// GetPlayer returns the stored record (as last persisted; call Checkpoint to
// realize pending idle blocks first).
func (f *FSM) GetPlayer(id uint64) (PlayerRecord, bool) {
	rec, ok := f.load(id)
	if !ok {
		return PlayerRecord{}, false
	}
	return *rec, true
}

// LBEntry is one leaderboard row from the on-chain index.
type LBEntry struct {
	ID    uint64
	Name  string
	Score int64
}

// Leaderboard returns all indexed players ranked by score (desc, then id).
// Reflects the last Checkpoint/SetPolicy of each player (bounded staleness).
func (f *FSM) Leaderboard() []LBEntry {
	var out []LBEntry
	f.store.Iterate(lbPrefix, func(key, val []byte) bool {
		id := binary.BigEndian.Uint64(key[len(lbPrefix):])
		score := int64(binary.BigEndian.Uint64(val[:8]))
		name := string(val[8:])
		out = append(out, LBEntry{ID: id, Name: name, Score: score})
		return true
	})
	sort.SliceStable(out, func(a, b int) bool {
		if out[a].Score != out[b].Score {
			return out[a].Score > out[b].Score
		}
		return out[a].ID < out[b].ID
	})
	return out
}

// ---- storage ----

func (f *FSM) load(id uint64) (*PlayerRecord, bool) {
	b, ok := f.store.Get(playerKey(id))
	if !ok {
		return nil, false
	}
	rec, err := decodeRecord(b)
	if err != nil {
		return nil, false
	}
	return &rec, true
}

func (f *FSM) save(id uint64, rec *PlayerRecord) {
	f.store.Set(playerKey(id), encodeRecord(rec))
}

func (f *FSM) indexLeaderboard(id uint64, rec *PlayerRecord) {
	val := make([]byte, 8+len(rec.Name))
	binary.BigEndian.PutUint64(val[:8], uint64(rec.State.SeasonScore))
	copy(val[8:], rec.Name)
	f.store.Set(lbKey(id), val)
}

// ---- keys ----

var (
	playerPrefix = []byte("p/")
	lbPrefix     = []byte("l/")
)

func playerKey(id uint64) []byte { return idKey(playerPrefix, id) }
func lbKey(id uint64) []byte     { return idKey(lbPrefix, id) }

func idKey(prefix []byte, id uint64) []byte {
	k := make([]byte, len(prefix)+8)
	copy(k, prefix)
	binary.BigEndian.PutUint64(k[len(prefix):], id)
	return k
}

// ---- record codec (canonical, round-trippable) ----

func encodeRecord(rec *PlayerRecord) []byte {
	st := engine.EncodeState(&rec.State)
	pol := engine.EncodePolicy(&rec.Policy)
	out := make([]byte, 0, len(st)+len(pol)+len(rec.Name)+24)
	out = appendChunk(out, []byte(rec.Name))
	out = appendChunk(out, st)
	out = appendChunk(out, pol)
	return out
}

func decodeRecord(b []byte) (PlayerRecord, error) {
	name, b, err := readChunk(b)
	if err != nil {
		return PlayerRecord{}, err
	}
	stB, b, err := readChunk(b)
	if err != nil {
		return PlayerRecord{}, err
	}
	polB, _, err := readChunk(b)
	if err != nil {
		return PlayerRecord{}, err
	}
	st, err := engine.DecodeState(stB)
	if err != nil {
		return PlayerRecord{}, err
	}
	pol, err := engine.DecodePolicy(polB)
	if err != nil {
		return PlayerRecord{}, err
	}
	return PlayerRecord{Name: string(name), State: st, Policy: pol}, nil
}

func appendChunk(dst, chunk []byte) []byte {
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], uint64(len(chunk)))
	dst = append(dst, n[:]...)
	return append(dst, chunk...)
}

func readChunk(b []byte) (chunk, rest []byte, err error) {
	if len(b) < 8 {
		return nil, nil, errors.New("fsm: truncated record")
	}
	n := int(binary.BigEndian.Uint64(b[:8]))
	b = b[8:]
	if n < 0 || n > len(b) {
		return nil, nil, errors.New("fsm: bad chunk length")
	}
	return b[:n], b[n:], nil
}
