package dcbmath

import (
	"crypto/sha256"
	"encoding/binary"
)

// BlockSeed derives the deterministic per-block, per-player seed from the
// season seed, the block height, and the player id. On-chain this is the place
// to fold in the block header hash / proposer VRF; the shape (a 32-byte digest)
// stays identical so the engine is unaffected by where the entropy comes from.
//
// sha256 is deterministic and I/O-free, so this is safe to call from the pure
// engine and reproduces exactly across hosts.
func BlockSeed(season [32]byte, height uint64, player uint64) [32]byte {
	var buf [48]byte
	copy(buf[0:32], season[:])
	binary.BigEndian.PutUint64(buf[32:40], height)
	binary.BigEndian.PutUint64(buf[40:48], player)
	return sha256.Sum256(buf[:])
}
