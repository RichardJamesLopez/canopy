// Command dcbfaucet funds new player wallets on a Canopy nested chain so the
// non-custodial, in-browser DCB client can play without the player holding the
// fee token. It is the only server in the live setup, and it custodies only the
// house key — never player keys. It reuses Canopy's lib (send tx build + JSON
// marshal) and posts to the node RPC directly (the canopy rpc.Client package
// can't be imported — it //go:embeds an explorer build absent from the module).
// Must build with Go 1.25 (see the toolchain directive in go.mod).
//
//	dcbfaucet -node http://localhost:50002 -key <house-bls-hex> -chain <id> -grant 10000000
package main

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/canopy-network/canopy/fsm"
	"github.com/canopy-network/canopy/lib"
	"github.com/canopy-network/canopy/lib/crypto"
)

func main() {
	node := flag.String("node", "http://localhost:50002", "Canopy RPC url")
	keyHex := flag.String("key", os.Getenv("DCB_FAUCET_KEY"), "house BLS private key (hex); or DCB_FAUCET_KEY")
	grant := flag.Uint64("grant", 10_000_000, "grant per wallet in micro-units (10 tokens)")
	fee := flag.Uint64("fee", 10_000, "tx fee in micro-units")
	netID := flag.Uint64("network", 1, "network id")
	chainID := flag.Uint64("chain", 1, "chain id (the nested-chain id)")
	listen := flag.String("listen", ":8088", "listen address")
	cooldown := flag.Duration("cooldown", time.Hour, "per-address funding cooldown")
	flag.Parse()

	if *keyHex == "" {
		log.Fatal("house key required: -key <hex> or DCB_FAUCET_KEY")
	}
	bz, err := hex.DecodeString(strings.TrimPrefix(*keyHex, "0x"))
	if err != nil {
		log.Fatalf("bad -key hex: %v", err)
	}
	house, err := crypto.NewPrivateKeyFromBytes(bz)
	if err != nil {
		log.Fatalf("bad house key: %v", err)
	}

	f := &faucet{
		node: strings.TrimRight(*node, "/"), house: house,
		grant: *grant, fee: *fee, netID: *netID, chainID: *chainID,
		cooldown: *cooldown, last: map[string]time.Time{},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/faucet", f.handle)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
	log.Printf("dcbfaucet: house=%s grant=%d chain=%d listen=%s node=%s",
		house.PublicKey().Address().String(), *grant, *chainID, *listen, f.node)
	log.Fatal(http.ListenAndServe(*listen, cors(mux)))
}

type faucet struct {
	node                        string
	house                       crypto.PrivateKeyI
	grant, fee, netID, chainID  uint64
	cooldown                    time.Duration
	mu                          sync.Mutex
	last                        map[string]time.Time
}

// height fetches the current chain height (POST /v1/query/height {"height":0}).
func (f *faucet) height() (uint64, error) {
	resp, err := http.Post(f.node+"/v1/query/height", "application/json", bytes.NewBufferString(`{"height":0}`))
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	var out struct {
		Height uint64 `json:"height"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return 0, err
	}
	return out.Height, nil
}

// submit posts a signed tx to /v1/tx (body = canopy lib.MarshalJSON(tx)) and
// returns the tx hash.
func (f *faucet) submit(tx lib.TransactionI) (string, error) {
	body, mErr := lib.MarshalJSON(tx)
	if mErr != nil {
		return "", mErr
	}
	resp, err := http.Post(f.node+"/v1/tx", "application/json", bytes.NewBuffer(body))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("node %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return strings.Trim(strings.TrimSpace(string(raw)), `"`), nil
}

func cors(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "content-type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		h.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("content-type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func (f *faucet) handle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "POST only"})
		return
	}
	var req struct {
		Address string `json:"address"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Address == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "address required"})
		return
	}
	addr, err := crypto.NewAddressFromString(strings.TrimPrefix(req.Address, "0x"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad address"})
		return
	}
	key := addr.String()

	// Per-address cooldown.
	f.mu.Lock()
	if last, ok := f.last[key]; ok && time.Since(last) < f.cooldown {
		f.mu.Unlock()
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "cooldown"})
		return
	}
	f.mu.Unlock()

	h, err := f.height()
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "height: " + err.Error()})
		return
	}
	tx, txErr := fsm.NewSendTransaction(f.house, addr, f.grant, f.netID, f.chainID, f.fee, h, "dcb-faucet")
	if txErr != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "build: " + txErr.Error()})
		return
	}
	hash, err := f.submit(tx)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "submit: " + err.Error()})
		return
	}
	f.mu.Lock()
	f.last[key] = time.Now()
	f.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"funded": true, "hash": hash, "grant": f.grant})
}
