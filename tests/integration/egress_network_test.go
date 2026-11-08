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
)

// TestBrokerOnlyNetworkIsolation verifies the broker-only building block:
// an internal Docker network blocks external egress but keeps the host
// gateway reachable for the broker. Run via `make test-integration`.
func TestBrokerOnlyNetworkIsolation(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not available")
	}
	if err := exec.Command("docker", "image", "inspect", "node:22-bookworm").Run(); err != nil {
		t.Skip("node:22-bookworm image not available")
	}
	c, err := dockerclient.New()
	if err != nil {
		t.Skipf("docker client: %v", err)
	}
	ctx := context.Background()
	name := fmt.Sprintf("dw-egress-test-%d", time.Now().UnixNano())
	gw, err := c.EnsureNetwork(ctx, name, true)
	if err != nil {
		t.Fatalf("EnsureNetwork: %v", err)
	}
	defer func() { _ = c.RemoveNetwork(ctx, name) }()

	ln, err := net.Listen("tcp", gw+":0")
	if err != nil {
		t.Fatalf("listen on gateway %s: %v", gw, err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			_, _ = conn.Write([]byte("HTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\nok"))
			_ = conn.Close()
		}
	}()
	port := ln.Addr().(*net.TCPAddr).Port

	script := fmt.Sprintf(`
const http=require("http");
const t=setTimeout(()=>{console.log("TIMEOUT");process.exit(2)},4000);
http.get("http://%s:%d/",r=>{clearTimeout(t);console.log("GATEWAY_OK");process.exit(0)}).on("error",e=>{clearTimeout(t);console.log("GATEWAY_FAIL "+e.code);process.exit(3)});
`, gw, port)
	out, err := exec.Command("docker", "run", "--rm", "--network", name,
		"node:22-bookworm", "node", "-e", script).CombinedOutput()
	if err != nil {
		t.Fatalf("host gateway not reachable: %v\n%s", err, out)
	}

	script = `
const https=require("https");
const t=setTimeout(()=>{console.log("TIMEOUT");process.exit(2)},5000);
https.get("https://1.1.1.1/",()=>{clearTimeout(t);console.log("EXTERNAL_OK");process.exit(10)}).on("error",e=>{clearTimeout(t);console.log("EXTERNAL_BLOCKED");process.exit(0)});
`
	out, err = exec.Command("docker", "run", "--rm", "--network", name,
		"node:22-bookworm", "node", "-e", script).CombinedOutput()
	if err != nil {
		t.Fatalf("external egress was not blocked: %v\n%s", err, out)
	}
}
