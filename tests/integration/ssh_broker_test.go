//go:build integration
// +build integration

package integration

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/ananta888/dockweave/internal/sshbroker"
)

// startCAHost runs an in-process SSH server that trusts only user certificates
// signed by the CA (the equivalent of TrustedUserCAKeys). It returns the port.
func startCAHost(t *testing.T, ca *sshbroker.CA) int {
	t.Helper()
	_, hostPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	hostSigner, err := ssh.NewSignerFromKey(hostPriv)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &ssh.ServerConfig{
		PublicKeyCallback: (&ssh.CertChecker{
			IsUserAuthority: func(auth ssh.PublicKey) bool {
				return keysEqual(auth, ca.PublicKey())
			},
		}).Authenticate,
	}
	cfg.AddHostKey(hostSigner)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	port := ln.Addr().(*net.TCPAddr).Port
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				sconn, chans, reqs, err := ssh.NewServerConn(c, cfg)
				if err != nil {
					_ = c.Close()
					return
				}
				defer sconn.Close()
				go ssh.DiscardRequests(reqs)
				for ch := range chans {
					if ch.ChannelType() != "session" {
						_ = ch.Reject(ssh.UnknownChannelType, "unsupported")
						continue
					}
					sch, creqs, err := ch.Accept()
					if err != nil {
						continue
					}
					go func() {
						for r := range creqs {
							if r.Type == "exec" || r.Type == "shell" {
								_ = r.Reply(true, nil)
								_, _ = sch.Write([]byte("ok\n"))
								// Report exit-status 0 so the client returns 0.
								_, _ = sch.SendRequest("exit-status", false,
									ssh.Marshal(struct{ Status uint32 }{0}))
								_ = sch.Close()
							} else {
								_ = r.Reply(false, nil)
							}
						}
					}()
				}
			}(conn)
		}
	}()
	return port
}

func keysEqual(a, b ssh.PublicKey) bool {
	return strings.EqualFold(string(a.Marshal()), string(b.Marshal()))
}

// issue writes a cert+key for the given destination and returns their paths.
func issue(t *testing.T, agent, host, user string, ttl time.Duration) (keyPath, certPath string, ca *sshbroker.CA) {
	t.Helper()
	ca, _ = sshbroker.GenerateCA()
	p := sshbroker.NewPolicy()
	if err := p.Add(sshbroker.Grant{Agent: agent, Host: host, User: user}); err != nil {
		t.Fatal(err)
	}
	issued, err := sshbroker.New(ca, p).Issue(agent, host, user, ttl)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	keyPath = filepath.Join(dir, "key")
	certPath = filepath.Join(dir, "cert.pub")
	if err := os.WriteFile(keyPath, issued.PrivateKeyPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(certPath, issued.CertPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	return keyPath, certPath, ca
}

// TestSSHCertAuthenticatesWithRealClient proves a CA-issued cert authenticates
// against a CA-trusting server using the real OpenSSH client.
func TestSSHCertAuthenticatesWithRealClient(t *testing.T) {
	if _, err := exec.LookPath("ssh"); err != nil {
		t.Skip("ssh binary not available")
	}
	keyPath, certPath, ca := issue(t, "a", "127.0.0.1", "git", time.Minute)
	port := startCAHost(t, ca)

	out, err := exec.Command("ssh",
		"-i", keyPath,
		"-o", "CertificateFile="+certPath,
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "LogLevel=ERROR",
		"-p", strconv.Itoa(port),
		"-l", "git",
		"127.0.0.1",
		"true",
	).CombinedOutput()
	if err != nil {
		t.Fatalf("ssh with CA cert failed: %v\n%s", err, out)
	}
}

// TestSSHCertRejectsForeignKey proves a key not signed by the CA is rejected.
func TestSSHCertRejectsForeignKey(t *testing.T) {
	if _, err := exec.LookPath("ssh"); err != nil {
		t.Skip("ssh binary not available")
	}
	ca, _ := sshbroker.GenerateCA()
	port := startCAHost(t, ca)

	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	block, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(t.TempDir(), "key")
	_ = os.WriteFile(keyPath, pem.EncodeToMemory(block), 0o600)

	_, err = exec.Command("ssh",
		"-i", keyPath,
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "LogLevel=ERROR",
		"-o", "BatchMode=yes",
		"-p", strconv.Itoa(port),
		"-l", "git",
		"127.0.0.1",
		"true",
	).CombinedOutput()
	if err == nil {
		t.Fatal("ssh with a non-CA-signed key must fail")
	}
}

// TestSSHCertExpiredRejected proves an expired certificate is rejected.
func TestSSHCertExpiredRejected(t *testing.T) {
	if _, err := exec.LookPath("ssh"); err != nil {
		t.Skip("ssh binary not available")
	}
	keyPath, certPath, ca := issue(t, "a", "127.0.0.1", "git", time.Second)
	port := startCAHost(t, ca)

	time.Sleep(2 * time.Second)
	out, err := exec.Command("ssh",
		"-i", keyPath,
		"-o", "CertificateFile="+certPath,
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "LogLevel=ERROR",
		"-o", "BatchMode=yes",
		"-p", strconv.Itoa(port),
		"-l", "git",
		"127.0.0.1",
		"true",
	).CombinedOutput()
	if err == nil {
		t.Fatalf("expired certificate must be rejected\n%s", out)
	}
}

// TestSSHEgressBlockedDirectSSH is the Phase-5 privileged matrix: a broker-only
// agent must not reach port 22 (or any host service) directly and must go
// through the wrapper. It needs a running broker, an agent container on the
// broker-only network and nftables egress enforcement, so it runs only in the
// privileged CI environment.
func TestSSHEgressBlockedDirectSSH(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not available")
	}
	t.Skip("needs broker + broker-only agent container + nftables egress (privileged CI); see docs/ssh-broker-design.md Phase 5")
}
