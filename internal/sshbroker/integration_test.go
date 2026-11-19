package sshbroker

import (
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// startTestServer runs an in-process SSH server that trusts only user
// certificates signed by the given CA (mimicking a server configured with
// TrustedUserCAKeys). No privileges are required.
func startTestServer(t *testing.T, ca *CA) string {
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
					_ = ch.Reject(ssh.UnknownChannelType, "test server")
				}
			}(conn)
		}
	}()
	return ln.Addr().String()
}

func TestIssuedCertAuthenticates(t *testing.T) {
	ca, _ := GenerateCA()
	p := NewPolicy()
	_ = p.Add(Grant{Agent: "a", Host: "github.com", User: "git"})
	b := New(ca, p)
	addr := startTestServer(t, ca)

	issued, err := b.Issue("a", "github.com", "git", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &ssh.ClientConfig{
		User:            "git",
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(issued.Signer)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         5 * time.Second,
	}
	client, err := ssh.Dial("tcp", addr, cfg)
	if err != nil {
		t.Fatalf("dial with issued cert: %v", err)
	}
	_ = client.Close()
}

func TestNonIssuedKeyRejected(t *testing.T) {
	ca, _ := GenerateCA()
	addr := startTestServer(t, ca)

	// An unrelated key (not signed by the CA) must be rejected.
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	signer, _ := ssh.NewSignerFromKey(priv)
	cfg := &ssh.ClientConfig{
		User:            "git",
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         5 * time.Second,
	}
	if _, err := ssh.Dial("tcp", addr, cfg); err == nil {
		t.Fatal("connection with a non-CA-signed key must fail")
	}
}
