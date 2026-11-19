package sshbroker

import "testing"

// TestTPMAvailable documents the TPM presence check. It never fails; it only
// logs whether a TPM 2.0 device is available so the CI/hardware run can be
// distinguished from a software-only environment.
func TestTPMAvailable(t *testing.T) {
	t.Logf("TPM available: %v", TPMAvailable())
}

// TestNewTPMSignerWithoutTPM asserts the factory fails cleanly when no TPM is
// present (the fallback is the software CA).
func TestNewTPMSignerWithoutTPM(t *testing.T) {
	if TPMAvailable() {
		t.Skip("TPM present; skipping the no-TPM assertion")
	}
	if _, err := NewTPMSigner(); err == nil {
		t.Fatal("NewTPMSigner must fail without a TPM device")
	}
}
