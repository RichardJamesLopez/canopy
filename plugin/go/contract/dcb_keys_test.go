package contract

import (
	"bytes"
	"encoding/binary"
	"testing"

	t "github.com/canopy-network/go-plugin/internal/dcb/dcbtypes"
)

// decodeLengthPrefixed is a byte-for-byte replica of canopy core's
// lib.DecodeLengthPrefixed (canopy lib/util.go:804): it walks the key reading a
// single length byte per segment and panics "corrupt or incomplete key" when a
// length overruns the buffer. Replicated here (the plugin module cannot import
// canopy/lib) so this test fails exactly where core's makeVersionedKey →
// DecodeLengthPrefixed would panic on commit. If core's algorithm ever changes,
// update this to match.
func decodeLengthPrefixed(key []byte) (segments [][]byte) {
	var length int
	for i := 0; i < len(key); i += length {
		length = int(key[i])
		i++
		if i+length > len(key) {
			panic("corrupt or incomplete key")
		}
		segments = append(segments, key[i:i+length])
	}
	return
}

func beUint64(u uint64) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, u)
	return b
}

// TestPluginKeysAreLengthPrefixed proves every key the DCB plugin writes via
// StateWrite is decodable under Canopy core's length-prefixed convention, with a
// single-byte prefix segment outside the reserved core range 1-15. This is the
// regression guard for the "corrupt or incomplete key" panic (raw, un-encoded
// keys were sent straight to core's store and mis-decoded on commit).
func TestPluginKeysAreLengthPrefixed(t_ *testing.T) {
	type tc struct {
		name       string
		key        []byte
		wantPrefix byte
		wantSegs   int
		wantTail   []byte // expected segment[1] (nil if single-segment key)
	}

	ids := []uint64{0, 1, 42, 1 << 32, ^uint64(0)}
	heights := []uint64{0, 1, 7, 1_000_000, ^uint64(0)}

	var cases []tc
	for _, id := range ids {
		cases = append(cases,
			tc{"player", t.LenPrefix(t.PlayerPrefix, beUint64(id)), t.PlayerPrefix[0], 2, beUint64(id)},
			tc{"leaderboard", t.LenPrefix(t.LeaderboardPrefix, beUint64(id)), t.LeaderboardPrefix[0], 2, beUint64(id)},
		)
	}
	for _, h := range heights {
		cases = append(cases, tc{"seed", dcbSeedKey(h), t.SeedPrefix[0], 2, beUint64(h)})
	}
	cases = append(cases,
		tc{"season-seed", dcbSeasonSeedKey, t.SeasonSeedPrefix[0], 1, nil},
		// the leaderboard iterate prefix must itself be a valid length-prefixed key
		// and a byte-prefix of every leaderboard key.
		tc{"lb-iterate-prefix", t.LenPrefix(t.LeaderboardPrefix), t.LeaderboardPrefix[0], 1, nil},
	)

	for _, c := range cases {
		t_.Run(c.name, func(t_ *testing.T) {
			// (a) must not panic under core's decoder.
			defer func() {
				if r := recover(); r != nil {
					t_.Fatalf("%s key %x panicked in core decoder: %v", c.name, c.key, r)
				}
			}()
			segs := decodeLengthPrefixed(c.key)

			// (b) first segment is the expected single prefix byte, outside 1-15.
			if len(segs) != c.wantSegs {
				t_.Fatalf("%s key %x: got %d segments, want %d", c.name, c.key, len(segs), c.wantSegs)
			}
			if len(segs[0]) != 1 || segs[0][0] != c.wantPrefix {
				t_.Fatalf("%s key %x: prefix segment = %x, want single byte %#x", c.name, c.key, segs[0], c.wantPrefix)
			}
			if p := segs[0][0]; p >= 1 && p <= 15 {
				t_.Fatalf("%s prefix %#x collides with core-reserved range 1-15", c.name, p)
			}
			// (c) round-trips to the expected id/height tail.
			if c.wantTail != nil && !bytes.Equal(segs[1], c.wantTail) {
				t_.Fatalf("%s key %x: tail segment = %x, want %x", c.name, c.key, segs[1], c.wantTail)
			}
		})
	}
}

// TestLeaderboardIteratePrefixIsBytePrefix guards the iteration invariant: the
// prefix passed to store.Iterate must be a byte-prefix of every full key, or the
// leaderboard scan silently returns nothing.
func TestLeaderboardIteratePrefixIsBytePrefix(t_ *testing.T) {
	prefix := t.LenPrefix(t.LeaderboardPrefix)
	for _, id := range []uint64{0, 1, 99, ^uint64(0)} {
		full := t.LenPrefix(t.LeaderboardPrefix, beUint64(id))
		if !bytes.HasPrefix(full, prefix) {
			t_.Fatalf("iterate prefix %x is not a byte-prefix of key %x", prefix, full)
		}
	}
}
