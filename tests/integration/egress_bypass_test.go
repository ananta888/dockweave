//go:build integration
// +build integration

package integration

import (
	"context"
	"fmt"
	"net"
	"os/exec"
	"testing"
	"time"

	"github.com/ananta888/dockweave/internal/dockerclient"
	"github.com/ananta888/dockweave/internal/egress"
)

// TestEgressBypassMatrix is the privileged bypass suite for egress-003: with
// the nftables table applied, a broker-only container must NOT reach a fake
// provider on the gateway (non-broker port) but MUST reach the broker port.
// It exercises the exact path an agent would use to bypass the credential
// broker. Run via `make test-integration`.
func TestEgressBypassMatrix(t *testing.T) {
	requireRoot(t)
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not available")
	}
	if _, err := exec.LookPath("nft"); err != nil {
		t.Skip("nft not available")
	}
	if err := exec.Command("docker", "image", "inspect", "node:22-bookworm").Run(); err != nil {
		t.Skip("node:22-bookworm image not available")
	}
	c, err := dockerclient.New()
	if err != nil {
		t.Skipf("docker client: %v", err)
	}
	ctx := context.Background()

	// Dedicated broker-only network.
	netName := fmt.Sprintf("dw-egress-bypass-%d", time.Now().UnixNano())
	gw, err := c.EnsureNetwork(ctx, netName, true)
	if err != nil {
		t.Fatalf("EnsureNetwork: %v", err)
	}
	defer func() { _ = c.RemoveNetwork(ctx, netName) }()
	iface, err := c.NetworkInterface(ctx, netName)
	if err != nil {
		t.Fatal(err)
	}

	// Apply the DockWeave egress table for this network.
	em := egress.New(egress.Config{Table: "dockweave-egress-bypass", BrokerPort: 8787})
	t.Cleanup(func() { _ = em.Remove(context.Background()) })
	if err := em.Ensure(ctx, iface, []string{gw}); err != nil {
		t.Fatalf("ensure egress table: %v", err)
	}

	// Fake provider on a non-broker port of the gateway (the bypass target).
	providerLn, err := net.Listen("tcp", gw+":18080")
	if err != nil {
		t.Fatalf("listen fake provider on gateway: %v", err)
	}
	defer providerLn.Close()
	go serveDrop(providerLn)

	// Fake broker on the allowed broker port.
	brokerLn, err := net.Listen("tcp", fmt.Sprintf("%s:%d", gw, 8787))
	if err != nil {
		t.Fatalf("listen fake broker on gateway: %v", err)
	}
	defer brokerLn.Close()
	go serveHTTP(brokerLn)

	// The provider must be unreachable from the container (dropped).
	providerScript := fmt.Sprintf(`
const http=require("http");
const t=setTimeout(()=>{console.log("PROVIDER_BLOCKED");process.exit(0)},4000);
http.get("http://%s:18080/",r=>{clearTimeout(t);console.log("PROVIDER_REACHABLE");process.exit(10)}).on("error",e=>{clearTimeout(t);console.log("PROVIDER_BLOCKED "+e.code);process.exit(0)});
`, gw)
	out, err := exec.Command("docker", "run", "--rm", "--network", netName,
		"node:22-bookworm", "node", "-e", providerScript).CombinedOutput()
	if err != nil {
		t.Fatalf("provider was reachable (egress bypass!): %v\n%s", err, out)
	}

	// The broker port must be reachable from the container.
	brokerScript := fmt.Sprintf(`
const http=require("http");
const t=setTimeout(()=>{console.log("BROKER_TIMEOUT");process.exit(2)},5000);
http.get("http://%s:%d/",r=>{clearTimeout(t);console.log("BROKER_OK");process.exit(0)}).on("error",e=>{clearTimeout(t);console.log("BROKER_FAIL "+e.code);process.exit(3)});
`, gw, 8787)
	out, err = exec.Command("docker", "run", "--rm", "--network", netName,
		"node:22-bookworm", "node", "-e", brokerScript).CombinedOutput()
	if err != nil {
		t.Fatalf("broker not reachable: %v\n%s", err, out)
	}
}

// serveDrop accepts and immediately closes connections (a reachable but
// non-functional endpoint — the bypass target the firewall must block).
func serveDrop(ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		_, _ = conn.Write([]byte("PROVIDER"))
		_ = conn.Close()
	}
}

// serveHTTP answers a minimal 200 so the node client considers it reachable.
func serveHTTP(ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		_, _ = conn.Write([]byte("HTTP/1.1 200 OK\r\nContent-Length: 2\r\nConnection: close\r\n\r\nok"))
		_ = conn.Close()
	}
}
