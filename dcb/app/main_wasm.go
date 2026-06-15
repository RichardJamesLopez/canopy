//go:build js && wasm

// Command dcbwasm is the browser build of DCB. It exposes the SAME engine and
// driver used by the terminal client to JavaScript via a tiny function surface,
// so there is zero game logic in JS and no risk of divergence from the on-chain
// rules. JS calls dcbNew/dcbTick/dcbBuy/... and renders the returned JSON
// view-model as a terminal-styled web UI. 1 block = 1 month.
package main

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"syscall/js"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"dcbapp/internal/chainclient"
	t "dcbapp/internal/dcbtypes"
	"dcbapp/internal/driver"
	"dcbapp/internal/engine"
	"dcbapp/internal/viewmodel"
)

// ONE is the fixed-point scale (mirror of dcbmath.ONE) for $ conversions.
const fpONE = 1_000_000

var drv driver.Driver

func main() {
	js.Global().Set("dcbNew", js.FuncOf(dcbNew))
	js.Global().Set("dcbTick", js.FuncOf(dcbTick))
	js.Global().Set("dcbSubmit", js.FuncOf(dcbSubmit))
	js.Global().Set("dcbSnapshot", js.FuncOf(dcbSnapshot))
	js.Global().Set("dcbBuy", js.FuncOf(dcbBuy))
	js.Global().Set("dcbSell", js.FuncOf(dcbSell))
	js.Global().Set("dcbHire", js.FuncOf(dcbHire))
	js.Global().Set("dcbFire", js.FuncOf(dcbFire))
	js.Global().Set("dcbBuyInfra", js.FuncOf(dcbBuyInfra))
	js.Global().Set("dcbFund", js.FuncOf(dcbFund))
	js.Global().Set("dcbRepay", js.FuncOf(dcbRepay))
	js.Global().Set("dcbEndGame", js.FuncOf(dcbEndGame))
	// Live (on-chain) mode: synchronous Go-only helpers; app.js does the HTTP.
	js.Global().Set("dcbChainKeyNew", js.FuncOf(dcbChainKeyNew))
	js.Global().Set("dcbChainAddress", js.FuncOf(dcbChainAddress))
	js.Global().Set("dcbChainBuildTx", js.FuncOf(dcbChainBuildTx))
	js.Global().Set("dcbChainViewModel", js.FuncOf(dcbChainViewModel))
	js.Global().Set("dcbEncodePolicy", js.FuncOf(dcbEncodePolicy))
	select {} // keep the Go runtime alive for callbacks
}

// dcbEncodePolicy(policyJSON) -> hex of EncodePolicy bytes (for dcb_set_policy).
func dcbEncodePolicy(_ js.Value, a []js.Value) any {
	var vp viewmodel.Policy
	if err := json.Unmarshal([]byte(a[0].String()), &vp); err != nil {
		return errJSON(err)
	}
	p := vp.ToPolicy()
	return hex.EncodeToString(engine.EncodePolicy(&p))
}

// ---- live (on-chain) helpers ----

func errJSON(err error) string {
	b, _ := json.Marshal(map[string]string{"error": err.Error()})
	return string(b)
}

// dcbChainKeyNew() -> {"hex","address"} : a fresh BLS keypair (app.js persists it).
func dcbChainKeyNew(_ js.Value, _ []js.Value) any {
	k, err := chainclient.NewKey()
	if err != nil {
		return errJSON(err)
	}
	b, _ := json.Marshal(map[string]string{"hex": k.Hex(), "address": k.AddressHex()})
	return string(b)
}

// dcbChainAddress(keyHex) -> address hex.
func dcbChainAddress(_ js.Value, a []js.Value) any {
	k, err := chainclient.KeyFromHex(a[0].String())
	if err != nil {
		return errJSON(err)
	}
	return k.AddressHex()
}

// buildMsg maps (action, argsJSON) to the right plugin message. argsJSON carries
// the polymorphic args: {kind,qty} | {name} | {n} | {infra,qty} | {policy:hex}.
func buildMsg(action string, addr []byte, argsJSON string) (string, proto.Message, bool) {
	var a struct {
		Kind, Infra int
		Qty, N      int64
		Name        string
		Policy      string // hex of EncodePolicy bytes
	}
	_ = json.Unmarshal([]byte(argsJSON), &a)
	switch action {
	case "start_run":
		mt, m := chainclient.MsgStartRun(addr, a.Name)
		return mt, m, true
	case "set_policy":
		pol, _ := hex.DecodeString(a.Policy)
		mt, m := chainclient.MsgSetPolicy(addr, pol)
		return mt, m, true
	case "checkpoint":
		mt, m := chainclient.MsgCheckpoint(addr)
		return mt, m, true
	case "buy":
		mt, m := chainclient.MsgBuy(addr, a.Kind, a.Qty)
		return mt, m, true
	case "sell":
		mt, m := chainclient.MsgSell(addr, a.Kind, a.Qty)
		return mt, m, true
	case "hire":
		mt, m := chainclient.MsgHire(addr, a.N)
		return mt, m, true
	case "fire":
		mt, m := chainclient.MsgFire(addr, a.N)
		return mt, m, true
	case "infra":
		mt, m := chainclient.MsgBuyInfra(addr, a.Infra, a.Qty)
		return mt, m, true
	}
	return "", nil, false
}

// dcbChainBuildTx(keyHex, action, argsJSON, fee, height, networkId, chainId, timeMicros)
// -> protojson of the signed tx, ready for app.js to POST to /v1/tx.
func dcbChainBuildTx(_ js.Value, a []js.Value) any {
	k, err := chainclient.KeyFromHex(a[0].String())
	if err != nil {
		return errJSON(err)
	}
	mt, msg, ok := buildMsg(a[1].String(), k.AddressBytes(), a[2].String())
	if !ok {
		return errJSON(errBadAction)
	}
	p := chainclient.Params{
		Fee:        uint64(a[3].Int()),
		Height:     uint64(a[4].Int()),
		NetworkID:  uint64(a[5].Int()),
		ChainID:    uint64(a[6].Int()),
		TimeMicros: uint64(a[7].Int()),
	}
	x, err := k.Build(mt, msg, p)
	if err != nil {
		return errJSON(err)
	}
	jb, err := protojson.Marshal(x)
	if err != nil {
		return errJSON(err)
	}
	return string(jb)
}

type bad string

func (b bad) Error() string { return string(b) }

var errBadAction = bad("unknown action")

// dcbChainViewModel(curStateB64, prevStateB64|"", policyJSON, season, prestige, playerName)
// decodes the chain's player State (the dcb/state event payload) and builds the
// view-model. Flow fields aren't in State, so net/wk and ucd/wk are derived as
// deltas vs the previous snapshot.
func dcbChainViewModel(_ js.Value, a []js.Value) any {
	cur, err := decodeStateB64(a[0].String())
	if err != nil {
		return errJSON(err)
	}
	var policy viewmodel.Policy
	_ = json.Unmarshal([]byte(a[2].String()), &policy)
	meta := viewmodel.Meta{Season: a[3].Int(), Prestige: int64(a[4].Int()), PlayerName: a[5].String()}
	vm := viewmodel.Build(&cur, t.BlockReport{}, policy.ToPolicy(), meta)
	if prevB64 := a[1].String(); prevB64 != "" {
		if prev, e := decodeStateB64(prevB64); e == nil {
			vm.NetFlow = vm.Capital - int64(prev.Capital)/fpONE // Δcash since last snapshot
			vm.UCD = cur.SeasonScore - prev.SeasonScore          // CU delivered since last snapshot
		}
	}
	b, _ := json.Marshal(vm)
	return string(b)
}

func decodeStateB64(b64 string) (t.State, error) {
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return t.State{}, err
	}
	return chainclient.DecodeStateEvent(raw)
}

func dcbNew(_ js.Value, args []js.Value) any {
	season, name, prestige, chain := "week-1", "you", int64(0), false
	if len(args) > 0 {
		season = args[0].String()
	}
	if len(args) > 1 {
		name = args[1].String()
	}
	if len(args) > 2 {
		prestige = int64(args[2].Int())
	}
	if len(args) > 3 {
		chain = args[3].Truthy()
	}
	base := sha256.Sum256([]byte(season))
	if chain {
		drv = driver.NewFSM(base, 1, name, prestige)
	} else {
		drv = driver.NewLocal(base, 1, name, prestige)
	}
	return snapshotJSON()
}

func dcbTick(_ js.Value, args []js.Value) any {
	if drv == nil {
		return "{}"
	}
	n := 1
	if len(args) > 0 {
		n = args[0].Int()
	}
	drv.Tick(n)
	return snapshotJSON()
}

func dcbSnapshot(_ js.Value, _ []js.Value) any {
	if drv == nil {
		return "{}"
	}
	return snapshotJSON()
}

func dcbSubmit(_ js.Value, args []js.Value) any {
	if drv == nil || len(args) == 0 {
		return "{}"
	}
	var vp viewmodel.Policy
	if err := json.Unmarshal([]byte(args[0].String()), &vp); err != nil {
		return snapshotJSON()
	}
	drv.Submit(vp.ToPolicy())
	return snapshotJSON()
}

func dcbBuy(_ js.Value, args []js.Value) any {
	if drv != nil && len(args) >= 2 {
		_ = drv.Buy(args[0].Int(), int64(args[1].Int()))
	}
	return snapshotJSON()
}

func dcbSell(_ js.Value, args []js.Value) any {
	if drv != nil && len(args) >= 2 {
		_ = drv.Sell(args[0].Int(), int64(args[1].Int()))
	}
	return snapshotJSON()
}

func dcbHire(_ js.Value, args []js.Value) any {
	if drv != nil && len(args) >= 1 {
		_ = drv.Hire(int64(args[0].Int()))
	}
	return snapshotJSON()
}

func dcbFire(_ js.Value, args []js.Value) any {
	if drv != nil && len(args) >= 1 {
		_ = drv.Fire(int64(args[0].Int()))
	}
	return snapshotJSON()
}

func dcbBuyInfra(_ js.Value, args []js.Value) any {
	if drv != nil && len(args) >= 2 {
		_ = drv.BuyInfra(args[0].Int(), int64(args[1].Int()))
	}
	return snapshotJSON()
}

func dcbEndGame(_ js.Value, _ []js.Value) any {
	if drv == nil {
		return "{}"
	}
	drv.EndGame()
	return snapshotJSON()
}

func dcbFund(_ js.Value, args []js.Value) any {
	if drv == nil || len(args) == 0 {
		return "{}"
	}
	drv.Fund(int64(args[0].Int()))
	return snapshotJSON()
}

func dcbRepay(_ js.Value, args []js.Value) any {
	if drv == nil || len(args) == 0 {
		return "{}"
	}
	drv.Repay(int64(args[0].Int()))
	return snapshotJSON()
}


// snapshotJSON builds the view-model JSON from the driver's current snapshot,
// reusing the shared pkg/viewmodel builder.
func snapshotJSON() string {
	snap := drv.Snapshot()
	var last t.BlockReport
	if n := len(snap.Recent); n > 0 {
		last = snap.Recent[n-1]
	}
	return viewmodel.BuildJSON(&snap.State, last, snap.Policy, viewmodel.Meta{
		Season:          snap.SeasonNum,
		Prestige:        snap.Prestige,
		PlayerName:      snap.PlayerName,
		LastSeasonRank:  snap.LastSeasonRank,
		LastSeasonScore: snap.LastSeasonScore,
	})
}
