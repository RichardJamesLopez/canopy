//go:build !(js && wasm)

// This stub exists so `go build ./...` and `go vet ./...` succeed on normal
// platforms. The real entrypoint is main_wasm.go, built only for GOOS=js.
package main

func main() {}
