// Package fsm mounts the pure DCB engine into a blockchain state-machine shape:
// a key/value store of per-player records, transaction handlers, and lazy
// catch-up that advances a player's deterministic trajectory only when touched.
//
// It is deliberately decoupled from Canopy: it talks to a `Store` (KV) and a
// `Host` (block height + per-block verifiable seed) interface. The in-memory
// implementations here make it fully testable now; a thin Canopy adapter
// (documented in CANOPY.md) wires these to CanoTrie + the block header/VRF.
package fsm

import (
	"sort"
)

// Store is the minimal key/value persistence the FSM needs. On Canopy this is
// backed by CanoTrie; here by a map. Keys sort bytewise; Iterate visits a
// prefix in ascending key order.
type Store interface {
	Get(key []byte) ([]byte, bool)
	Set(key, val []byte)
	Delete(key []byte)
	Iterate(prefix []byte, fn func(key, val []byte) bool)
}

// MemStore is an in-memory Store for tests and the local FSM driver.
type MemStore struct {
	m map[string][]byte
}

// NewMemStore returns an empty in-memory store.
func NewMemStore() *MemStore { return &MemStore{m: map[string][]byte{}} }

func (s *MemStore) Get(key []byte) ([]byte, bool) {
	v, ok := s.m[string(key)]
	return v, ok
}

func (s *MemStore) Set(key, val []byte) {
	cp := make([]byte, len(val))
	copy(cp, val)
	s.m[string(key)] = cp
}

func (s *MemStore) Delete(key []byte) { delete(s.m, string(key)) }

// Iterate visits all entries whose key starts with prefix, in ascending key
// order (so iteration is deterministic — no map-range nondeterminism leaks out).
func (s *MemStore) Iterate(prefix []byte, fn func(key, val []byte) bool) {
	keys := make([]string, 0, len(s.m))
	p := string(prefix)
	for k := range s.m {
		if len(k) >= len(p) && k[:len(p)] == p {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		if !fn([]byte(k), s.m[k]) {
			return
		}
	}
}
