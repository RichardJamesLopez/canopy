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
)

// dcbSeasonSeed seeds NewSeason for fresh players. Fixed here; in production set
// it from genesis (PluginGenesisRequest). Per-block entropy does NOT come from
// this — it comes from recordDcbSeed (proposer ‖ height) read back via dcbHost.
var dcbSeasonSeed = sha256.Sum256([]byte("dcb/season/1"))

var dcbSeedPrefix = []byte("dcb/seed")

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

func (h dcbHost) Height() uint64 { return h.c.dcbHeight }

func (h dcbHost) Seed(height uint64) [32]byte {
	var out [32]byte
	if v, ok := (dcbStore{h.c}).Get(dcbSeedKey(height)); ok && len(v) == 32 {
		copy(out[:], v)
	}
	return out
}

// dcbFSM builds the game state machine bound to this block's chain context.
func (c *Contract) dcbFSM() *dfsm.FSM {
	return dfsm.New(dcbStore{c}, dcbHost{c}, dcbSeasonSeed)
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

// ---- DeliverTx handlers ----

func (c *Contract) DeliverDcbStartRun(m *MessageDcbStartRun) *PluginDeliverResponse {
	if err := c.dcbFSM().StartRun(playerID(m.Address), m.Name, 0); err != nil {
		return &PluginDeliverResponse{Error: dcbErr(err)}
	}
	return &PluginDeliverResponse{}
}

func (c *Contract) DeliverDcbSetPolicy(m *MessageDcbSetPolicy) *PluginDeliverResponse {
	p, err := engine.DecodePolicy(m.Policy)
	if err != nil {
		return &PluginDeliverResponse{Error: dcbErr(err)}
	}
	if err := c.dcbFSM().SetPolicy(playerID(m.Address), p); err != nil {
		return &PluginDeliverResponse{Error: dcbErr(err)}
	}
	return &PluginDeliverResponse{}
}

func (c *Contract) DeliverDcbCheckpoint(m *MessageDcbCheckpoint) *PluginDeliverResponse {
	if err := c.dcbFSM().Checkpoint(playerID(m.Address)); err != nil {
		return &PluginDeliverResponse{Error: dcbErr(err)}
	}
	return &PluginDeliverResponse{}
}

func (c *Contract) DeliverDcbBuy(m *MessageDcbBuy) *PluginDeliverResponse {
	if err := c.dcbFSM().Buy(playerID(m.Address), int(m.Kind), m.Qty); err != nil {
		return &PluginDeliverResponse{Error: dcbErr(err)}
	}
	return &PluginDeliverResponse{}
}

func (c *Contract) DeliverDcbSell(m *MessageDcbSell) *PluginDeliverResponse {
	if err := c.dcbFSM().Sell(playerID(m.Address), int(m.Kind), m.Qty); err != nil {
		return &PluginDeliverResponse{Error: dcbErr(err)}
	}
	return &PluginDeliverResponse{}
}

func (c *Contract) DeliverDcbHire(m *MessageDcbHire) *PluginDeliverResponse {
	if err := c.dcbFSM().Hire(playerID(m.Address), m.N); err != nil {
		return &PluginDeliverResponse{Error: dcbErr(err)}
	}
	return &PluginDeliverResponse{}
}

func (c *Contract) DeliverDcbFire(m *MessageDcbFire) *PluginDeliverResponse {
	if err := c.dcbFSM().Fire(playerID(m.Address), m.N); err != nil {
		return &PluginDeliverResponse{Error: dcbErr(err)}
	}
	return &PluginDeliverResponse{}
}

func (c *Contract) DeliverDcbBuyInfra(m *MessageDcbBuyInfra) *PluginDeliverResponse {
	if err := c.dcbFSM().BuyInfra(playerID(m.Address), int(m.Infra), m.Qty); err != nil {
		return &PluginDeliverResponse{Error: dcbErr(err)}
	}
	return &PluginDeliverResponse{}
}

// ---- CheckTx (stateless) ----

func (c *Contract) checkDcb(addr []byte) *PluginCheckResponse {
	if len(addr) != 20 {
		return &PluginCheckResponse{Error: ErrInvalidAddress()}
	}
	return &PluginCheckResponse{Recipient: addr, AuthorizedSigners: [][]byte{addr}}
}
