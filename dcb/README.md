# DCB — Data Center Builder (app + faucet)

The player-facing app and supporting service for the DCB nested chain. The
on-chain game logic is the Go plugin at [`../plugin/go`](../plugin/go); this
folder holds everything else needed to **deploy and play**.

Source of truth for the game engine is the `datacentergame` repo; the Go
packages here are vendored copies (so the fork is self-contained and
rebuildable). Re-vendor after engine changes and rebuild the wasm.

## Layout

- `web/` — the **deployable static frontend** (serve as-is on any static host:
  GitHub Pages, S3/Cloudflare, Vercel…). `dcb.wasm` is the compiled game/client.
  - Local play: open `index.html` (served over HTTP, e.g. `python3 -m http.server`).
  - Live (on-chain) play: `index.html?chain=live&node=<rpc>&chainId=<id>&faucet=<url>`.
- `app/` — Go module (`dcbapp`) that **builds `web/dcb.wasm`**. Contains the
  vendored engine + the in-browser chain client (`internal/chainclient`,
  `internal/canopytx`) + the wasm bridge. Rebuild: `cd app && ./build.sh`.
  Builds on Go ≥1.25 (imports `canopy/lib/crypto` for BLS signing).
- `faucet/` — Go module (`dcbfaucet`): funds new player wallets so the
  non-custodial browser client can play. Pinned to Go 1.25 (`toolchain` in
  go.mod) because it imports the full `canopy/lib`. The native token is used for
  tx fees only — the game economy is internal plugin state, not the chain token
  (see the "Token model" section in the parent's `CANOPY.md`).
  ```sh
  cd faucet && go run . -node http://<node>:50002 -key <house-bls-hex> \
    -chain <nested-chain-id> -grant 10000000 -listen :8088
  ```

## How it fits together (live mode)

1. Browser generates a BLS key (stored locally), calls the **faucet** to get
   funded.
2. Player actions become signed `dcb_*` transactions POSTed to the node `/v1/tx`
   (signing happens in-browser via the wasm; non-custodial).
3. State is read back from plugin-emitted `dcb/state` events
   (`/v1/query/events-by-address`) and rendered.

Pacing: 1 chain block (~20s) ≈ 1 game-month (`dcbStepsPerBlock=4` in the plugin).

See [`../../CANOPY.md`](../../CANOPY.md) on the parent (if present) for the full
integration model and the items still pending live-node validation.
