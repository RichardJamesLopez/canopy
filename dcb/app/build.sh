#!/usr/bin/env bash
# Rebuild the DCB web client (wasm) into ../web. Run from dcb/app.
set -euo pipefail
GOOS=js GOARCH=wasm go build -o ../web/dcb.wasm .
cp "$(go env GOROOT)/lib/wasm/wasm_exec.js" ../web/wasm_exec.js
echo "built ../web/dcb.wasm"
