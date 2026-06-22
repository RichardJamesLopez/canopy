package chainclient

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"testing"
)

// TestMarshalCanopyJSON_HexBytes proves the tx JSON encodes signature bytes as
// hex (the form the node's stringToBytes() decoder accepts), the inner msg as
// protojson (base64 bytes), and the uint64 envelope fields as JSON numbers. It
// also confirms the built signature stays self-consistent (encoding is
// transport-only and does not change sign-bytes).
func TestMarshalCanopyJSON_HexBytes(t *testing.T) {
	k, err := NewKey()
	if err != nil {
		t.Fatal(err)
	}
	mt, msg := MsgBuy(k.AddressBytes(), 1, 10)
	x, err := k.Build(mt, msg, Params{Fee: 10000, Height: 100, NetworkID: 1, ChainID: 243, TimeMicros: 123})
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := VerifySelf(x); err != nil || !ok {
		t.Fatalf("signature not self-consistent: ok=%v err=%v", ok, err)
	}
	jb, err := MarshalCanopyJSON(x)
	if err != nil {
		t.Fatal(err)
	}

	var got struct {
		Type string `json:"type"`
		Msg  struct {
			Address string `json:"address"` // protojson -> base64
			Kind    int    `json:"kind"`
			Qty     string `json:"qty"`
		} `json:"msg"`
		Signature struct {
			PublicKey string `json:"publicKey"`
			Signature string `json:"signature"`
		} `json:"signature"`
		Fee     uint64 `json:"fee"`
		ChainID uint64 `json:"chainID"`
	}
	if err := json.Unmarshal(jb, &got); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, jb)
	}

	// Signature bytes must be HEX (the node hex-decodes them).
	if _, err := hex.DecodeString(got.Signature.PublicKey); err != nil {
		t.Errorf("publicKey not hex: %q", got.Signature.PublicKey)
	}
	if _, err := hex.DecodeString(got.Signature.Signature); err != nil {
		t.Errorf("signature not hex: %q", got.Signature.Signature)
	}
	// Inner msg address is protojson base64 — must decode (via base64) to the
	// original address bytes.
	b, err := base64.StdEncoding.DecodeString(got.Msg.Address)
	if err != nil || hex.EncodeToString(b) != k.AddressHex() {
		t.Errorf("inner address not base64 of addr: %q (err=%v)", got.Msg.Address, err)
	}
	// uint64 envelope fields are JSON numbers with capital-ID keys.
	if got.Fee != 10000 || got.ChainID != 243 {
		t.Errorf("envelope number mismatch: fee=%d chainID=%d", got.Fee, got.ChainID)
	}
	if got.Type != "dcb_buy" || got.Msg.Qty != "10" || got.Msg.Kind != 1 {
		t.Errorf("field mismatch: %+v", got)
	}
}
