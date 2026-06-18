package contract

// DCB (Data Center Builder) integration. This is the only customization a
// developer adds to a copy of the canopy go-plugin template: it wires the
// reusable, deterministic DCB game state machine (github.com/canopy-network/go-plugin/internal/dcb/fsm)
// onto the plugin's StateRead/StateWrite + block lifecycle. The game rules
// (engine) and the chain-shaped state machine (fsm) are imported unchanged —
// the same code the local prototype, the web build, and the parity test run.

import (
	"crypto/sha256"
	"encoding/binary"
	"math/rand"

	"github.com/canopy-network/go-plugin/internal/dcb/engine"
	dfsm "github.com/canopy-network/go-plugin/internal/dcb/fsm"
	t "github.com/canopy-network/go-plugin/internal/dcb/dcbtypes"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
)

// dcbSeasonSeedDefault is the fallback world seed when no genesis-derived seed is
// available (e.g. dev). The real seed is set in Genesis from the genesis JSON so
// every validator shares one world; see initSeason / seasonSeed. Per-block
// entropy does NOT come from this — it comes from recordDcbSeed (proposer ‖
// height) read back via dcbHost.
var dcbSeasonSeedDefault = sha256.Sum256([]byte("dcb/season/1"))

// dcbSeasonSeedKey persists the genesis-derived season seed in plugin state so it
// survives plugin restarts and is identical across validators.
var dcbSeasonSeedKey = []byte("dcb/season-seed")

var dcbSeedPrefix = []byte("dcb/seed")

// initSeason derives the season world seed from the genesis JSON (deterministic
// and identical for every validator) and persists it. Called from Genesis().
func (c *Contract) initSeason(genesisJSON []byte) {
	seed := dcbSeasonSeedDefault
	if len(genesisJSON) > 0 {
		seed = sha256.Sum256(genesisJSON)
	}
	(dcbStore{c}).Set(dcbSeasonSeedKey, seed[:])
	c.cachedSeasonSeed, c.seasonSeedSet = seed, true
}

// seasonSeed resolves the world seed: the cached genesis value, else the
// persisted state value, else the fixed default.
func (c *Contract) seasonSeed() [32]byte {
	if c.seasonSeedSet {
		return c.cachedSeasonSeed
	}
	if v, ok := (dcbStore{c}).Get(dcbSeasonSeedKey); ok && len(v) == 32 {
		copy(c.cachedSeasonSeed[:], v)
		c.seasonSeedSet = true
		return c.cachedSeasonSeed
	}
	return dcbSeasonSeedDefault
}

func dcbSeedKey(height uint64) []byte {
	k := make([]byte, len(dcbSeedPrefix)+8)
	copy(k, dcbSeedPrefix)
	binary.BigEndian.PutUint64(k[len(dcbSeedPrefix):], height)
	return k
}

// playerID derives a stable uint64 id from a 20-byte account address.
func playerID(addr []byte) uint64 {
	var b [8]byte
	copy(b[:], addr)
	return binary.BigEndian.Uint64(b[:])
}

// dcbStore adapts the plugin's batched StateRead/StateWrite to the synchronous
// fsm.Store the game logic expects.
type dcbStore struct{ c *Contract }

var _ dfsm.Store = dcbStore{}

func (s dcbStore) Get(key []byte) ([]byte, bool) {
	resp, perr := s.c.plugin.StateRead(s.c, &PluginStateReadRequest{
		Keys: []*PluginKeyRead{{QueryId: rand.Uint64(), Key: key}},
	})
	if perr != nil || resp == nil || resp.Error != nil ||
		len(resp.Results) == 0 || len(resp.Results[0].Entries) == 0 {
		return nil, false
	}
	v := resp.Results[0].Entries[0].Value
	return v, len(v) > 0
}

func (s dcbStore) Set(key, val []byte) {
	_, _ = s.c.plugin.StateWrite(s.c, &PluginStateWriteRequest{
		Sets: []*PluginSetOp{{Key: key, Value: val}},
	})
}

func (s dcbStore) Delete(key []byte) {
	_, _ = s.c.plugin.StateWrite(s.c, &PluginStateWriteRequest{
		Deletes: []*PluginDeleteOp{{Key: key}},
	})
}

func (s dcbStore) Iterate(prefix []byte, fn func(key, val []byte) bool) {
	resp, perr := s.c.plugin.StateRead(s.c, &PluginStateReadRequest{
		Ranges: []*PluginRangeRead{{QueryId: rand.Uint64(), Prefix: prefix}},
	})
	if perr != nil || resp == nil || resp.Error != nil || len(resp.Results) == 0 {
		return
	}
	for _, e := range resp.Results[0].Entries {
		if !fn(e.Key, e.Value) {
			return
		}
	}
}

// dcbHost provides the chain height (cached each BeginBlock) and the verifiable
// per-block seed (recorded each EndBlock), satisfying fsm.Host.
type dcbHost struct{ c *Contract }

var _ dfsm.Host = dcbHost{}

// dcbStepsPerBlock advances this many engine-weeks per chain block. One Canopy
// block is ~20s, so 4 ≈ one game-month per block (a game-year ≈ 13 blocks ≈
// 4.3 min). Game-time = chain blocks × dcbStepsPerBlock; this is the only lever
// since chain block time is fixed.
const dcbStepsPerBlock = 4

// Height is the target engine height (game-weeks) for catch-up: chain height
// scaled by the steps-per-block ratio.
func (h dcbHost) Height() uint64 { return h.c.dcbHeight * dcbStepsPerBlock }

// Seed derives a per-engine-step seed from the owning chain block's recorded
// seed, so the dcbStepsPerBlock sub-steps within a block are distinct yet
// deterministic and verifiable.
func (h dcbHost) Seed(engineHeight uint64) [32]byte {
	chainBlock := engineHeight / dcbStepsPerBlock
	sub := engineHeight % dcbStepsPerBlock
	var block [32]byte
	if v, ok := (dcbStore{h.c}).Get(dcbSeedKey(chainBlock)); ok && len(v) == 32 {
		copy(block[:], v)
	}
	hh := sha256.New()
	hh.Write(block[:])
	var sb [8]byte
	binary.BigEndian.PutUint64(sb[:], sub)
	hh.Write(sb[:])
	var out [32]byte
	copy(out[:], hh.Sum(nil))
	return out
}

// dcbFSM builds the game state machine bound to this block's chain context.
func (c *Contract) dcbFSM() *dfsm.FSM {
	return dfsm.New(dcbStore{c}, dcbHost{c}, c.seasonSeed())
}

// recordDcbSeed stores seed(height) = sha256(proposer ‖ height). Canopy does not
// expose its consensus VRF to handlers; the proposer address (from EndBlock) is
// the verifiable, consensus-determined entropy available here.
func (c *Contract) recordDcbSeed(height uint64, proposer []byte) {
	h := sha256.New()
	h.Write(proposer)
	var hb [8]byte
	binary.BigEndian.PutUint64(hb[:], height)
	h.Write(hb[:])
	var seed [32]byte
	copy(seed[:], h.Sum(nil))
	(dcbStore{c}).Set(dcbSeedKey(height), seed[:])
}

func dcbErr(err error) *PluginError { return NewError(100, "dcb", err.Error()) }

// dcbStateTypeURL labels the player-state event payload (declared in
// ContractConfig.EventTypeUrls). It must be a real proto type URL so the node's
// plugin-schema registry can resolve it; the Any.Value is a marshaled
// DcbStateEvent whose State field carries the engine.EncodeState bytes.
const dcbStateTypeURL = "type.googleapis.com/types.DcbStateEvent"

// stateEvent builds a player-state event: the encoded State wrapped in a
// DcbStateEvent under dcbStateTypeURL, tagged with the player address so clients
// read it via events-by-address.
func stateEvent(addr []byte, st *t.State) *Event {
	payload, _ := proto.Marshal(&DcbStateEvent{
		State:   engine.EncodeState(st),
		Height:  st.Height,
		Address: addr,
	})
	return &Event{
		EventType: "dcb_state",
		Address:   addr,
		Msg: &Event_Custom{Custom: &EventCustom{Msg: &anypb.Any{
			TypeUrl: dcbStateTypeURL,
			Value:   payload,
		}}},
	}
}

// deliver runs an FSM op for the player then emits the resulting state as an
// event, so external clients can read player state without a per-key RPC.
func (c *Contract) deliver(addr []byte, do func(f *dfsm.FSM, id uint64) error) *PluginDeliverResponse {
	id := playerID(addr)
	f := c.dcbFSM()
	if err := do(f, id); err != nil {
		return &PluginDeliverResponse{Error: dcbErr(err)}
	}
	resp := &PluginDeliverResponse{}
	if rec, ok := f.GetPlayer(id); ok {
		resp.Events = []*Event{stateEvent(addr, &rec.State)}
	}
	return resp
}

// ---- DeliverTx handlers ----

func (c *Contract) DeliverDcbStartRun(m *MessageDcbStartRun) *PluginDeliverResponse {
	return c.deliver(m.Address, func(f *dfsm.FSM, id uint64) error { return f.StartRun(id, m.Name, 0) })
}

func (c *Contract) DeliverDcbSetPolicy(m *MessageDcbSetPolicy) *PluginDeliverResponse {
	p, err := engine.DecodePolicy(m.Policy)
	if err != nil {
		return &PluginDeliverResponse{Error: dcbErr(err)}
	}
	return c.deliver(m.Address, func(f *dfsm.FSM, id uint64) error { return f.SetPolicy(id, p) })
}

func (c *Contract) DeliverDcbCheckpoint(m *MessageDcbCheckpoint) *PluginDeliverResponse {
	return c.deliver(m.Address, func(f *dfsm.FSM, id uint64) error { return f.Checkpoint(id) })
}

func (c *Contract) DeliverDcbBuy(m *MessageDcbBuy) *PluginDeliverResponse {
	return c.deliver(m.Address, func(f *dfsm.FSM, id uint64) error { return f.Buy(id, int(m.Kind), m.Qty) })
}

func (c *Contract) DeliverDcbSell(m *MessageDcbSell) *PluginDeliverResponse {
	return c.deliver(m.Address, func(f *dfsm.FSM, id uint64) error { return f.Sell(id, int(m.Kind), m.Qty) })
}

func (c *Contract) DeliverDcbHire(m *MessageDcbHire) *PluginDeliverResponse {
	return c.deliver(m.Address, func(f *dfsm.FSM, id uint64) error { return f.Hire(id, m.N) })
}

func (c *Contract) DeliverDcbFire(m *MessageDcbFire) *PluginDeliverResponse {
	return c.deliver(m.Address, func(f *dfsm.FSM, id uint64) error { return f.Fire(id, m.N) })
}

func (c *Contract) DeliverDcbBuyInfra(m *MessageDcbBuyInfra) *PluginDeliverResponse {
	return c.deliver(m.Address, func(f *dfsm.FSM, id uint64) error { return f.BuyInfra(id, int(m.Infra), m.Qty) })
}

// ---- CheckTx (stateless) ----

func (c *Contract) checkDcb(addr []byte) *PluginCheckResponse {
	if len(addr) != 20 {
		return &PluginCheckResponse{Error: ErrInvalidAddress()}
	}
	return &PluginCheckResponse{Recipient: addr, AuthorizedSigners: [][]byte{addr}}
}
