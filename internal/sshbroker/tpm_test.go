package sshbroker

import (
	"net"
	"os"
	"testing"
	"time"
)

// TestTPMAvailable documents the TPM presence check.
func TestTPMAvailable(t *testing.T) {
	t.Logf("TPM device available: %v", TPMAvailable())
}

// TestNewTPMSignerWithoutTPM asserts the factory fails cleanly when no TPM is
// present (the fallback is the software CA).
func TestNewTPMSignerWithoutTPM(t *testing.T) {
	if TPMAvailable() || os.Getenv("DOCKWEAVE_TPM_SOCKET") != "" {
		t.Skip("TPM present; skipping the no-TPM assertion")
	}
	if _, err := NewTPMSigner(); err == nil {
		t.Fatal("NewTPMSigner must fail without a TPM device")
	}
}

// tpmSignerForTest returns a TPM signer, preferring a swtpm socket
// (DOCKWEAVE_TPM_SOCKET) then the device. It skips when neither is present.
func tpmSignerForTest(t *testing.T) *TPMSigner {
	t.Helper()
	if addr := os.Getenv("DOCKWEAVE_TPM_SOCKET"); addr != "" {
		conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
		if err != nil {
			t.Fatalf("connect to TPM socket %s: %v", addr, err)
		}
		s, err := NewTPMSignerFromReadWriter(conn)
		if err != nil {
			t.Fatalf("NewTPMSignerFromReadWriter: %v", err)
		}
		t.Cleanup(func() { _ = s.Close() })
		return s
	}
	if !TPMAvailable() {
		t.Skip("no TPM device (set DOCKWEAVE_TPM_DEVICE for swtpm CUSE, or DOCKWEAVE_TPM_SOCKET for a swtpm socket)")
	}
	s, err := NewTPMSigner()
	if err != nil {
		t.Fatalf("NewTPMSigner: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// TestTPMSignerEndToEnd issues a certificate with the TPM-held key and
// verifies the signature, exercising the full hardware path (only runs with a
// TPM or swtpm present).
func TestTPMSignerEndToEnd(t *testing.T) {
	s := tpmSignerForTest(t)

	issued, err := Issue(s, IssueRequest{Agent: "a", Host: "h", User: "u"})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if !s.Verify(issued.Cert) {
		t.Fatal("certificate not verified by the TPM public key")
	}
	if issued.Cert.CertType != userCertType {
		t.Fatalf("unexpected cert type %v", issued.Cert.CertType)
	}
	// A second issuance must also verify (fresh nonce each time).
	again, err := Issue(s, IssueRequest{Agent: "a", Host: "h", User: "u"})
	if err != nil {
		t.Fatalf("second Issue: %v", err)
	}
	if !s.Verify(again.Cert) {
		t.Fatal("second certificate not verified")
	}
}

const userCertType = 1
