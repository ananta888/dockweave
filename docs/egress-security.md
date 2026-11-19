# Egress Security — Design and Current Status

> **Status 2026-11-19:** `offline` and `broker-only` are enforced with Docker
> networks. `broker-only` uses an internal Docker network (no external
> route) and is verified by a privileged integration test: external egress
> fails (`ENETUNREACH`) while the host gateway stays reachable for the
> broker. **nftables enforcement (`egress-003`) is implemented** as an
> opt-in, fail-closed host firewall (`EGRESS_NFTABLES=true`); the full
> bypass suite still needs a privileged run. `restricted-development`
> remains unimplemented (fail closed).

Language: English · [Deutsch](egress-security.de.md)

## Why a proxy alone is insufficient

If an agent container has bridge networking and a route to the internet, any
process (curl, Python, Node, Go, a custom TCP client, QUIC) can connect
directly to the provider and skip the broker. Proxy environment variables are
advisory: an agent can unset them or use a raw socket. Credential brokerage is
therefore only credible together with enforced egress.

## Implemented: network profiles

| Profile | Enforced behaviour |
|---|---|
| `offline` | Docker `--network none`; no network at all |
| `broker-only` | Dedicated internal Docker network `dockweave-broker-only`; no external route; host gateway reachable for the broker |
| `restricted-development` | **Not implemented.** Agent start is refused (fail closed) |
| `unrestricted-legacy` | Current bridge behaviour; explicitly unsafe |

`dockweave agent create --profile broker-only ...` or
`dockweave egress profile AGENT broker-only` selects a profile (applied at the
next start/restart). `dockweave egress status AGENT` reports the profile,
enforcement flag, network, gateway and broker base URL. Unknown profiles are
rejected; a failure to create/verify the network refuses the start.

The daemon sets `DOCKWEAVE_BROKER_BASE_URL=http://<gateway>:8787` inside
broker-only agents. It is not a secret and contains no credential.

## Verification

`tests/integration/egress_network_test.go` creates an internal network and
proves:

1. the host gateway is reachable from the container (the broker path),
2. an external HTTPS connection fails with `ENETUNREACH`.

Run it with `go test -tags integration ./tests/integration -run Egress`.

## Residual risk (open)

- **Host gateway exposure:** the internal network still allows the agent to
  reach any host service bound to the gateway address, not only the broker.
  Filtering this requires host nftables rules (`egress-003`).
- **IPv6/DNS/IMDS:** Docker's internal network blocks external routes, but
  this has not yet been exhaustively tested for every host/Docker version;
  add explicit checks before claiming full isolation.
- **WSL2/Docker variations:** not verified.
- **Container recreation/daemon restart:** reconcile is provided by the
  per-start `EnsureNetwork`; stale-network cleanup is not automated.

## Host firewall (`egress-003`)

Implemented in `internal/egress` and wired into the daemon behind
`EGRESS_NFTABLES=true` (off by default). When enabled, starting a
`broker-only` agent applies a DockWeave-owned nftables table
(`inet dockweave-egress`) that:

1. filters only traffic arriving on the broker-only bridge interface,
2. allows the broker port on the gateway address,
3. drops private ranges, link-local/metadata (IMDS), IPv6 ULA/link-local/
   multicast and DNS,
4. drops everything else arriving from that bridge (default deny).

Application is fail-closed: missing `nft`, non-root, or a failed apply
refuses the agent start. The table is idempotent (`nft list table` check).

Residual: the full bypass suite (direct curl/Python/Node to a provider must
fail while the broker path succeeds; IPv6/DNS-rebinding/private-IP/IMDS
matrix) is a privileged test (`tests/integration/egress_nftables_test.go`
currently covers table round-trip only) and must run in the privileged CI
environment.

## Platform compatibility (WSL2 / Windows)

DockWeave is **Linux-only**. There is no native Windows support: the daemon
relies on `mount(2)`, `openat2`, `/proc`, `SO_PEERCRED` and a Linux Docker
Engine, none of which exist natively on Windows.

| Environment | Status |
|---|---|
| Linux (native, kernel ≥ 5.12) | Supported; reference platform. |
| Linux in a VM | Supported; behaves like native Linux. |
| WSL2 (Ubuntu etc.) | Works with Docker Desktop (WSL2 backend) for the agent runtime and network profiles, with caveats: |
|  | — host-level nftables rules (`egress-003`) apply inside the WSL2 VM, not the Windows host; enforcement is per-WSL2-distro. |
|  | — Docker Desktop's internal network topology can differ between versions; `broker-only` isolation must be re-verified per version (the privileged egress test does this). |
|  | — the broker must bind an address reachable from agent containers (`127.0.0.1` on the same distro, or the WSL2 VM gateway). |
| macOS / Windows (native) | Not supported. Docker Desktop runs a Linux VM; DockWeave would have to run inside that VM, never on the host OS. |

The privilege split (daemon + `mountd`) and the mount manager require a Linux
host with `CAP_SYS_ADMIN`. On WSL2 that means running inside the WSL2 VM.

## Network profiles contract

`broker-only` is the preferred mode for credential-backed agents. `offline`
is for agents that need no network. `unrestricted-legacy` remains available
only for backwards compatibility and is reported as unsafe.

