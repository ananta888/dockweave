# Egress Security — Design and Current Status

> **Status 2026-11-08:** `offline` and `broker-only` are enforced with Docker
> networks. `broker-only` uses an internal Docker network (no external
> route) and is verified by a privileged integration test: external egress
> fails (`ENETUNREACH`) while the host gateway stays reachable for the
> broker. **Still open:** host-gateway port filtering (nftables), DNS/IMDS
> hardening beyond the internal network, and `restricted-development`.

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

## Required next steps (`egress-003`)

1. nftables table with an explicit DockWeave label; allow only the broker
   port on the gateway, drop host-private ranges and IMDS explicitly.
2. Fail closed if rules are missing; reconcile on daemon restart.
3. Privileged suite: direct curl/Python/Node to provider must fail; broker
   must succeed; IPv6/DNS rebinding/private IP/IMDS must fail.
4. Document host firewall interactions and WSL2 limits.

## Network profiles contract

`broker-only` is the preferred mode for credential-backed agents. `offline`
is for agents that need no network. `unrestricted-legacy` remains available
only for backwards compatibility and is reported as unsafe.

