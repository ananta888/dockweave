// Package sshbroker issues short-lived, destination-bound SSH user
// certificates so agents can reach Git/SSH hosts without ever holding a
// long-lived private key. The CA private key never leaves the broker; policy
// is DENY-by-default per agent → host/user.
//
// This is the Phase-2 core of docs/ssh-broker-design.md (CA storage, policy,
// certificate issuance and tests). FIDO2 and privileged egress tests are later
// phases.
package sshbroker

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"fmt"

	"golang.org/x/crypto/ssh"
)

// CA is an SSH certificate authority. Its private key never leaves the broker.
type CA struct {
	priv ed25519.PrivateKey
}

// GenerateCA creates a new ed25519 CA keypair.
func GenerateCA() (*CA, error) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate ca key: %w", err)
	}
	return &CA{priv: priv}, nil
}

// LoadCA parses a PEM-encoded ed25519 private key (PKCS8 or OpenSSH format).
func LoadCA(pemBytes []byte) (*CA, error) {
	key, err := ssh.ParseRawPrivateKey(pemBytes)
	if err != nil {
		return nil, fmt.Errorf("parse ca key: %w", err)
	}
	priv, err := toEd25519(key)
	if err != nil {
		return nil, err
	}
	return &CA{priv: priv}, nil
}

func toEd25519(key any) (ed25519.PrivateKey, error) {
	switch k := key.(type) {
	case ed25519.PrivateKey:
		return k, nil
	case *ed25519.PrivateKey:
		return *k, nil
	default:
		return nil, fmt.Errorf("ca key must be ed25519, got %T", key)
	}
}

// MarshalPrivateKey returns the CA private key as PKCS8 PEM (for storage in the
// encrypted vault; never returned to agents).
func (c *CA) MarshalPrivateKey() ([]byte, error) {
	der, err := x509.MarshalPKCS8PrivateKey(c.priv)
	if err != nil {
		return nil, fmt.Errorf("marshal ca key: %w", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), nil
}

// PublicKey returns the CA's public key (the trust anchor configured on SSH
// servers as TrustedUserCAKeys).
func (c *CA) PublicKey() ssh.PublicKey {
	pub, err := ssh.NewPublicKey(c.priv.Public())
	if err != nil {
		// ed25519 public keys always marshal; this cannot fail in practice.
		panic(fmt.Sprintf("sshbroker: marshal ca public key: %v", err))
	}
	return pub
}

func (c *CA) signer() (ssh.Signer, error) {
	s, err := ssh.NewSignerFromKey(c.priv)
	if err != nil {
		return nil, fmt.Errorf("wrap ca key: %w", err)
	}
	return s, nil
}

// signCert signs a certificate with the CA key.
func (c *CA) signCert(cert *ssh.Certificate) error {
	signer, err := c.signer()
	if err != nil {
		return err
	}
	if err := cert.SignCert(rand.Reader, signer); err != nil {
		return fmt.Errorf("sign certificate: %w", err)
	}
	return nil
}

// Verify reports whether the certificate's signature is valid for this CA.
func (c *CA) Verify(cert *ssh.Certificate) bool {
	if cert == nil || cert.Signature == nil {
		return false
	}
	c2 := *cert
	c2.Signature = nil
	out := c2.Marshal()
	// Match x/crypto's Certificate.bytesForSigning: the serialized
	// signature field is dropped (4-byte trailing length).
	out = out[:len(out)-4]
	return c.PublicKey().Verify(out, cert.Signature) == nil
}

// keysEqual compares two public keys by their wire encoding.
func keysEqual(a, b ssh.PublicKey) bool {
	return bytes.Equal(a.Marshal(), b.Marshal())
}
