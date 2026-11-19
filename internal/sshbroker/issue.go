package sshbroker

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"time"

	"golang.org/x/crypto/ssh"
)

// IssuedCert is the result of a successful issuance: a short-lived user
// certificate plus the matching ephemeral private key. The private key is
// returned exactly once and must not be persisted beyond the session.
type IssuedCert struct {
	// Cert is the parsed certificate (used by tests and the ssh.Signer).
	Cert *ssh.Certificate
	// CertPEM is the authorized-key line for CertificateFile.
	CertPEM []byte
	// PrivateKeyPEM is the ephemeral private key (OpenSSH format) for ssh -i.
	PrivateKeyPEM []byte
	// Signer authenticates with Cert + PrivateKey (used by tests).
	Signer ssh.Signer
}

// IssueRequest is one issuance.
type IssueRequest struct {
	Agent string
	Host  string
	User  string
	// KeyID is recorded in the certificate for audit; defaults to "<agent>".
	KeyID string
	// TTL overrides the grant's TTL when set and smaller.
	TTL time.Duration
}

// Issue signs a short-lived user certificate for the requested destination.
// Policy is enforced by the caller (Broker.Issue); this only performs the
// crypto and marshalling.
func (c *CA) Issue(req IssueRequest) (*IssuedCert, error) {
	if req.Agent == "" || req.User == "" {
		return nil, fmt.Errorf("sshbroker: agent and user are required")
	}
	ttl := req.TTL
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	if ttl > MaxTTL {
		ttl = MaxTTL
	}
	keyID := req.KeyID
	if keyID == "" {
		keyID = req.Agent
	}

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("sshbroker: generate ephemeral key: %w", err)
	}
	pub, err := ssh.NewPublicKey(priv.Public())
	if err != nil {
		return nil, fmt.Errorf("sshbroker: marshal ephemeral key: %w", err)
	}

	now := time.Now()
	cert := &ssh.Certificate{
		Key:             pub,
		CertType:        ssh.UserCert,
		KeyId:           keyID,
		ValidPrincipals: []string{req.User},
		ValidAfter:      uint64(now.Add(-5 * time.Minute).Unix()),
		ValidBefore:     uint64(now.Add(ttl).Unix()),
		Permissions: ssh.Permissions{
			Extensions: map[string]string{
				"dockweave-agent": req.Agent,
			},
		},
	}
	if err := c.signCert(cert); err != nil {
		return nil, err
	}

	certSigner, err := ssh.NewCertSigner(cert, mustSigner(priv))
	if err != nil {
		return nil, fmt.Errorf("sshbroker: build cert signer: %w", err)
	}

	return &IssuedCert{
		Cert:          cert,
		CertPEM:       ssh.MarshalAuthorizedKey(cert),
		PrivateKeyPEM: marshalOpenSSHKey(priv),
		Signer:        certSigner,
	}, nil
}

func mustSigner(priv ed25519.PrivateKey) ssh.Signer {
	s, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		panic(fmt.Sprintf("sshbroker: wrap ephemeral key: %v", err))
	}
	return s
}

func marshalOpenSSHKey(priv ed25519.PrivateKey) []byte {
	block, err := ssh.MarshalPrivateKey(priv, "dockweave-ephemeral")
	if err != nil {
		// Fall back to PKCS8 if OpenSSH marshalling is unavailable.
		return marshalPKCS8(priv)
	}
	return pem.EncodeToMemory(block)
}

func marshalPKCS8(priv ed25519.PrivateKey) []byte {
	// Import cycle avoided: x509 marshalling lives in ca.go; inline via the
	// CA helper by re-encoding through a throwaway CA.
	c := &CA{priv: priv}
	b, _ := c.MarshalPrivateKey()
	return b
}
