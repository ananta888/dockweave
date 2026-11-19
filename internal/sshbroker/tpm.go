package sshbroker

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"encoding/asn1"
	"fmt"
	"math/big"
	"os"

	"github.com/google/go-tpm/tpm2"
	"github.com/google/go-tpm/tpm2/transport"
	"golang.org/x/crypto/ssh"
)

// TPMSigner signs SSH certificates with a non-exportable ECDSA P-256 key held
// in a TPM 2.0. The private key never leaves the TPM.
type TPMSigner struct {
	tpm    transport.TPMCloser
	handle tpm2.TPMHandle
	pub    ssh.PublicKey
}

// TPMAvailable reports whether a TPM 2.0 device is present.
func TPMAvailable() bool {
	for _, p := range []string{"/dev/tpmrm0", "/dev/tpm0"} {
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	return false
}

// NewTPMSigner creates a non-exportable ECDSA P-256 signing key in the TPM's
// owner hierarchy and returns a Signer backed by it.
func NewTPMSigner() (*TPMSigner, error) {
	t, err := transport.OpenTPM()
	if err != nil {
		return nil, fmt.Errorf("sshbroker: open TPM: %w", err)
	}
	tmpl := tpm2.TPMTPublic{
		Type:    tpm2.TPMAlgECC,
		NameAlg: tpm2.TPMAlgSHA256,
		ObjectAttributes: tpm2.TPMAObject{
			FixedTPM:            true,
			FixedParent:         true,
			SensitiveDataOrigin: true,
			UserWithAuth:        true,
			NoDA:                true,
			SignEncrypt:         true,
		},
		Parameters: tpm2.NewTPMUPublicParms(tpm2.TPMAlgECC, &tpm2.TPMSECCParms{
			Symmetric: tpm2.TPMTSymDefObject{Algorithm: tpm2.TPMAlgNull},
			Scheme: tpm2.TPMTECCScheme{
				Scheme:  tpm2.TPMAlgECDSA,
				Details: tpm2.NewTPMUAsymScheme(tpm2.TPMAlgECDSA, &tpm2.TPMSSigSchemeECDSA{HashAlg: tpm2.TPMAlgSHA256}),
			},
			CurveID: tpm2.TPMECCNistP256,
			KDF:     tpm2.TPMTKDFScheme{Scheme: tpm2.TPMAlgNull},
		}),
		Unique: tpm2.NewTPMUPublicID(tpm2.TPMAlgECC, &tpm2.TPMSECCPoint{
			X: tpm2.TPM2BECCParameter{Buffer: make([]byte, 32)},
			Y: tpm2.TPM2BECCParameter{Buffer: make([]byte, 32)},
		}),
	}
	create := tpm2.CreatePrimary{
		PrimaryHandle: tpm2.TPMRHOwner,
		InPublic:      tpm2.New2B(tmpl),
	}
	rsp, err := create.Execute(t)
	if err != nil {
		_ = t.Close()
		return nil, fmt.Errorf("sshbroker: create TPM signing key: %w", err)
	}
	pubTPMT, err := rsp.OutPublic.Contents()
	if err != nil {
		_ = t.Close()
		return nil, fmt.Errorf("sshbroker: read TPM public key: %w", err)
	}
	pub, err := eccPublicKey(pubTPMT)
	if err != nil {
		_ = t.Close()
		return nil, err
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		_ = t.Close()
		return nil, fmt.Errorf("sshbroker: marshal TPM public key: %w", err)
	}
	return &TPMSigner{tpm: t, handle: rsp.ObjectHandle, pub: sshPub}, nil
}

// eccPublicKey extracts an *ecdsa.PublicKey from a TPM ECC public area.
func eccPublicKey(p *tpm2.TPMTPublic) (*ecdsa.PublicKey, error) {
	parms, err := p.Parameters.ECCDetail()
	if err != nil {
		return nil, fmt.Errorf("sshbroker: not an ECC key: %w", err)
	}
	if parms.CurveID != tpm2.TPMECCNistP256 {
		return nil, fmt.Errorf("sshbroker: unsupported TPM curve %v (want NIST P-256)", parms.CurveID)
	}
	point, err := p.Unique.ECC()
	if err != nil {
		return nil, fmt.Errorf("sshbroker: read ECC point: %w", err)
	}
	return &ecdsa.PublicKey{
		Curve: elliptic.P256(),
		X:     new(big.Int).SetBytes(point.X.Buffer),
		Y:     new(big.Int).SetBytes(point.Y.Buffer),
	}, nil
}

// PublicKey returns the CA public key (Signer interface).
func (s *TPMSigner) PublicKey() ssh.PublicKey { return s.pub }

// SignCert signs a certificate with the TPM-held ECDSA key.
func (s *TPMSigner) SignCert(cert *ssh.Certificate) error {
	cert.Nonce = randomNonce()
	cert.SignatureKey = s.pub
	digest := sha256.Sum256(certBytesForSigning(cert))

	sign := tpm2.Sign{
		KeyHandle: s.handle,
		Digest:    tpm2.TPM2BDigest{Buffer: digest[:]},
		InScheme: tpm2.TPMTSigScheme{
			Scheme:  tpm2.TPMAlgECDSA,
			Details: tpm2.NewTPMUSigScheme(tpm2.TPMAlgECDSA, &tpm2.TPMSSchemeHash{HashAlg: tpm2.TPMAlgSHA256}),
		},
		Validation: tpm2.TPMTTKHashCheck{},
	}
	rsp, err := sign.Execute(s.tpm)
	if err != nil {
		return fmt.Errorf("sshbroker: TPM sign: %w", err)
	}
	ecc, err := rsp.Signature.Signature.ECDSA()
	if err != nil {
		return fmt.Errorf("sshbroker: TPM signature not ECDSA: %w", err)
	}
	// TPM returns raw r||s; SSH wants ASN.1 DER.
	der, err := asn1.Marshal(struct {
		R, S *big.Int
	}{
		new(big.Int).SetBytes(ecc.SignatureR.Buffer),
		new(big.Int).SetBytes(ecc.SignatureS.Buffer),
	})
	if err != nil {
		return fmt.Errorf("sshbroker: encode TPM signature: %w", err)
	}
	cert.Signature = &ssh.Signature{Format: "ecdsa-sha2-nistp256", Blob: der}
	return nil
}

// Verify reports whether a certificate was signed by this CA.
func (s *TPMSigner) Verify(cert *ssh.Certificate) bool {
	if cert == nil || cert.Signature == nil {
		return false
	}
	return s.pub.Verify(certBytesForSigning(cert), cert.Signature) == nil
}

// Close closes the TPM device.
func (s *TPMSigner) Close() error {
	if s.tpm == nil {
		return nil
	}
	return s.tpm.Close()
}

var _ Signer = (*TPMSigner)(nil)
