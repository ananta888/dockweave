package sshbroker

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"testing"

	"github.com/google/go-tpm/tpm2"
)

func eccPublic(curve tpm2.TPMECCCurve, x, y []byte) *tpm2.TPMTPublic {
	return &tpm2.TPMTPublic{
		Type:       tpm2.TPMAlgECC,
		Parameters: tpm2.NewTPMUPublicParms(tpm2.TPMAlgECC, &tpm2.TPMSECCParms{CurveID: curve}),
		Unique: tpm2.NewTPMUPublicID(tpm2.TPMAlgECC, &tpm2.TPMSECCPoint{
			X: tpm2.TPM2BECCParameter{Buffer: x}, Y: tpm2.TPM2BECCParameter{Buffer: y}}),
	}
}

// eccPublicKey converts the TPM point without hardware; it must round-trip a
// real P-256 key (also with stripped leading zero bytes) and reject points
// that are not on the curve.
func TestECCPublicKeyConversion(t *testing.T) {
	for i := 0; i < 64; i++ {
		k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := k.PublicKey.Bytes() // 0x04 || X(32) || Y(32)
		if err != nil {
			t.Fatal(err)
		}
		x, y := raw[1:33], raw[33:]
		// TPMs may return minimal-length big-endian buffers.
		for len(x) > 1 && x[0] == 0 {
			x = x[1:]
		}
		got, err := eccPublicKey(eccPublic(tpm2.TPMECCNistP256, x, y))
		if err != nil {
			t.Fatalf("convert: %v", err)
		}
		if !got.Equal(&k.PublicKey) {
			t.Fatal("converted key differs from the original")
		}
	}
	if _, err := eccPublicKey(eccPublic(tpm2.TPMECCNistP256, []byte{1}, []byte{2})); err == nil {
		t.Fatal("a point off the curve must be rejected")
	}
	if _, err := eccPublicKey(eccPublic(tpm2.TPMECCNistP384, []byte{1}, []byte{2})); err == nil {
		t.Fatal("a non-P-256 curve must be rejected")
	}
	if _, err := eccPublicKey(eccPublic(tpm2.TPMECCNistP256, make([]byte, 33), []byte{2})); err == nil {
		t.Fatal("an oversized coordinate must be rejected")
	}
}
