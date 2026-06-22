// Package chainclient builds, signs, and decodes the on-chain DCB transactions
// for a non-custodial client (the browser wasm bundle). It reuses Canopy's BLS
// (lib/crypto — compiles to js/wasm) and the vendored tx proto (pkg/canopytx),
// so it never imports canopy/lib (which doesn't build for wasm). Pure logic:
// no HTTP, no js — the transport lives in the wasm bridge so this stays
// host-unit-testable.
package chainclient

import (
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/canopy-network/canopy/lib/crypto"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"

	tx "dcbapp/internal/canopytx"
	t "dcbapp/internal/dcbtypes"
	"dcbapp/internal/engine"
)

// det is the deterministic proto marshaller — must match canopy lib.Marshal so
// our sign-bytes equal the node's.
var det = proto.MarshalOptions{Deterministic: true}

// Key wraps a BLS private key.
type Key struct{ priv crypto.PrivateKeyI }

// NewKey generates a fresh BLS keypair.
func NewKey() (*Key, error) {
	p, err := crypto.NewBLS12381PrivateKey()
	if err != nil {
		return nil, err
	}
	return &Key{p}, nil
}

// KeyFromHex restores a key from its hex-encoded private bytes.
func KeyFromHex(h string) (*Key, error) {
	bz, err := hex.DecodeString(h)
	if err != nil {
		return nil, err
	}
	p, err := crypto.NewPrivateKeyFromBytes(bz)
	if err != nil {
		return nil, err
	}
	return &Key{p}, nil
}

func (k *Key) Hex() string         { return hex.EncodeToString(k.priv.Bytes()) }
func (k *Key) AddressBytes() []byte { return k.priv.PublicKey().Address().Bytes() }
func (k *Key) AddressHex() string  { return hex.EncodeToString(k.AddressBytes()) }

// Params carries the per-tx chain context the client must supply.
type Params struct {
	Fee        uint64
	Height     uint64 // current chain height (createdHeight)
	NetworkID  uint64
	ChainID    uint64 // the nested-chain id
	TimeMicros uint64 // unique temporal entropy (UnixMicro)
}

// Message constructors: return the (messageType, proto message) for each action.
// Address is the signer's 20-byte address.
func MsgStartRun(addr []byte, name string) (string, proto.Message) {
	return "dcb_start_run", &tx.MessageDcbStartRun{Address: addr, Name: name}
}
func MsgSetPolicy(addr, policy []byte) (string, proto.Message) {
	return "dcb_set_policy", &tx.MessageDcbSetPolicy{Address: addr, Policy: policy}
}
func MsgCheckpoint(addr []byte) (string, proto.Message) {
	return "dcb_checkpoint", &tx.MessageDcbCheckpoint{Address: addr}
}
func MsgBuy(addr []byte, kind int, qty int64) (string, proto.Message) {
	return "dcb_buy", &tx.MessageDcbBuy{Address: addr, Kind: uint32(kind), Qty: qty}
}
func MsgSell(addr []byte, kind int, qty int64) (string, proto.Message) {
	return "dcb_sell", &tx.MessageDcbSell{Address: addr, Kind: uint32(kind), Qty: qty}
}
func MsgHire(addr []byte, n int64) (string, proto.Message) {
	return "dcb_hire", &tx.MessageDcbHire{Address: addr, N: n}
}
func MsgFire(addr []byte, n int64) (string, proto.Message) {
	return "dcb_fire", &tx.MessageDcbFire{Address: addr, N: n}
}
func MsgBuyInfra(addr []byte, infra int, qty int64) (string, proto.Message) {
	return "dcb_buy_infra", &tx.MessageDcbBuyInfra{Address: addr, Infra: uint32(infra), Qty: qty}
}

// signBytes is the canonical pre-image: the tx marshaled deterministically with
// the signature omitted (matches canopy Transaction.GetSignBytes).
func signBytes(x *tx.Transaction) ([]byte, error) {
	return det.Marshal(&tx.Transaction{
		MessageType:   x.MessageType,
		Msg:           x.Msg,
		CreatedHeight: x.CreatedHeight,
		Time:          x.Time,
		Fee:           x.Fee,
		Memo:          x.Memo,
		NetworkId:     x.NetworkId,
		ChainId:       x.ChainId,
	})
}

// Build assembles and BLS-signs a transaction for the given message.
func (k *Key) Build(msgType string, msg proto.Message, p Params) (*tx.Transaction, error) {
	any, err := anypb.New(msg) // TypeUrl = type.googleapis.com/types.MessageDcb*
	if err != nil {
		return nil, err
	}
	x := &tx.Transaction{
		MessageType:   msgType,
		Msg:           any,
		CreatedHeight: p.Height,
		Time:          p.TimeMicros,
		Fee:           p.Fee,
		NetworkId:     p.NetworkID,
		ChainId:       p.ChainID,
	}
	sb, err := signBytes(x)
	if err != nil {
		return nil, err
	}
	x.Signature = &tx.Signature{PublicKey: k.priv.PublicKey().Bytes(), Signature: k.priv.Sign(sb)}
	return x, nil
}

// VerifySelf checks a built tx's signature against its sign-bytes (self-consistency).
func VerifySelf(x *tx.Transaction) (bool, error) {
	if x.Signature == nil {
		return false, fmt.Errorf("unsigned")
	}
	pub, err := crypto.NewPublicKeyFromBytes(x.Signature.PublicKey)
	if err != nil {
		return false, err
	}
	sb, err := signBytes(x)
	if err != nil {
		return false, err
	}
	return pub.VerifyBytes(sb, x.Signature.Signature), nil
}

// MarshalCanopyJSON encodes a signed transaction in the exact JSON shape the
// node's /v1/tx decoder accepts (mirrors canopy lib.Transaction.MarshalJSON):
// top-level key "type", uint64 envelope fields as JSON numbers, "msg" as
// protojson (base64 inner bytes), and "signature" as HEX. protojson alone
// renders the signature bytes as base64, which the node hex-decodes and
// rejects ("stringToBytes() ... encoding/hex: invalid byte"). The signature is
// over the deterministic protobuf binary, so the transport encoding here does
// not change sign-bytes. canopy/lib can't be imported in wasm, so the format
// is replicated.
func MarshalCanopyJSON(x *tx.Transaction) ([]byte, error) {
	inner, err := x.Msg.UnmarshalNew() // concrete MessageDcb* (registered at compile time)
	if err != nil {
		return nil, err
	}
	msgJSON, err := protojson.Marshal(inner)
	if err != nil {
		return nil, err
	}
	out := map[string]any{
		"type": x.MessageType,
		"msg":  json.RawMessage(msgJSON),
	}
	if x.Signature != nil {
		out["signature"] = map[string]string{
			"publicKey": hex.EncodeToString(x.Signature.PublicKey),
			"signature": hex.EncodeToString(x.Signature.Signature),
		}
	}
	if x.Time != 0 {
		out["time"] = x.Time
	}
	if x.CreatedHeight != 0 {
		out["createdHeight"] = x.CreatedHeight
	}
	if x.Fee != 0 {
		out["fee"] = x.Fee
	}
	if x.Memo != "" {
		out["memo"] = x.Memo
	}
	if x.NetworkId != 0 {
		out["networkID"] = x.NetworkId
	}
	if x.ChainId != 0 {
		out["chainID"] = x.ChainId
	}
	return json.Marshal(out)
}

// DecodeStateEvent decodes a dcb/state event payload back into a State. The
// plugin emits the Any.Value as a marshaled DcbStateEvent envelope whose State
// field holds the raw engine.EncodeState bytes.
func DecodeStateEvent(value []byte) (t.State, error) {
	var ev tx.DcbStateEvent
	if err := proto.Unmarshal(value, &ev); err != nil {
		return t.State{}, err
	}
	return engine.DecodeState(ev.State)
}
