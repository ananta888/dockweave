package sshbroker

import (
	"crypto/rand"

	"golang.org/x/crypto/ssh"
)

// Signer signs SSH user certificates and exposes the CA public key. The CA
// private key may live in process memory (SoftwareSigner/CA), a TPM 2.0
// (TPMSigner), a PIV token (PIVSigner), or a cloud HSM/KMS; the rest of the
// broker only depends on this interface.
//
// Implementations must be non-exportable where hardware-backed: the private
// key never leaves the signer. See docs/ssh-broker-design.md.
type Signer interface {
	// PublicKey returns the CA public key (the TrustedUserCAKeys anchor).
	PublicKey() ssh.PublicKey
	// SignCert signs a certificate (setting Nonce, SignatureKey, Signature).
	SignCert(cert *ssh.Certificate) error
	// Verify reports whether a certificate was signed by this CA.
	Verify(cert *ssh.Certificate) bool
}

// certBytesForSigning returns the bytes a certificate signature covers,
// matching x/crypto's Certificate.bytesForSigning (serialize with the
// signature dropped, minus the trailing 4-byte signature length).
func certBytesForSigning(cert *ssh.Certificate) []byte {
	c2 := *cert
	c2.Signature = nil
	out := c2.Marshal()
	return out[:len(out)-4]
}

// randomNonce returns a fresh 32-byte certificate nonce.
func randomNonce() []byte {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("sshbroker: rand.Read: " + err.Error())
	}
	return b
}
