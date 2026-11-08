# Egress Security — Design and Current Status

> **This build does not enforce egress.** There is no Docker network, nftables
> rule, namespace or eBPF control that prevents an agent from calling a
> provider directly. The broker is only a policy gateway, not a network
> boundary. Do not describe DockWeave as egress-isolated until the tasks in
> `todo/credential-vault.json` (`egress-*`) are done.

Language: English · [Deutsch](egress-security.de.md)

## Why a proxy alone is insufficient

If an agent container has bridge networking and a route to the internet, any
process (curl, Python, Node, Go, a custom TCP client, QUIC) can connect
directly to the provider and skip the broker. Proxy environment variables are
advisory: an agent can unset them or use a raw socket. Therefore credential
brokerage is only credible together with enforced egress.

## Required properties (target design)

1. Dedicated Docker network per agent class, no default route to the
   internet.
2. Host-side firewall (nftables) rules outside the agent's control:
   - allow DNS to a controlled resolver (or no DNS when using fixed IPs),
   - allow only broker destination (host gateway / broker container),
   - drop IPv4 and IPv6 elsewhere, including link-local and IMDS
     (`169.254.169.254`, `fd00:ec2::254`),
   - drop host-private ranges and the Docker socket.
3. Fail closed: if rules are missing or the broker is down, the container has
   no network, not unlimited network.
4. Reconcile on daemon restart and container recreation; never leave stale
   permissive rules.
5. No access to `/var/run/docker.sock`, no `CAP_NET_ADMIN` inside the agent.
6. DNS rebinding protection: validate the resolved IP at connect time, not
   only the hostname.
7. IPv6 parity and explicit handling of redirects/alternative protocols.

## Network profiles (contract; not yet enforced)

| Profile | Intended behaviour |
|---|---|
| `offline` | No network except loopback |
| `broker-only` | Only the broker endpoint; no direct internet |
| `restricted-development` | Broker + explicit allowlist (package mirrors, git) |
| `unrestricted-legacy` | Current bridge behaviour; explicitly unsafe |

`broker-only` must be the default for credential-backed agents once
enforcement exists.

## Why this is not implemented now

Implementing it correctly requires privileged host firewall changes and
isolated integration tests (root, nftables, Docker networks). The repository
rules require such changes to be tested in the privileged test container and
to be reversible; a half-implemented rule set could silently grant more
access than before. Shipping metadata that *looks* enforced would violate the
project rule "no pretending".

`dockweave egress status` therefore reports the truth and exits non-zero:
`egress isolation is NOT enforced by this build`.

## Implementation plan (tracked as `egress-001`… in the TODO)

1. Add a `network_profile` field to the agent model and persistence.
2. Daemon creates a dedicated Docker network per profile with internal=true
   where possible.
3. Daemon installs nftables rules in a dedicated table with an explicit
   comment/label; reconcile and remove only own rules.
4. Broker gets a fixed address on that network.
5. Fail closed on any setup error; refuse to start the agent.
6. Privileged tests: direct curl/Python/Node to the provider must fail;
   broker path must succeed; IPv6 and DNS rebinding attempts must fail;
   daemon restart must converge.
7. Document host firewall interactions and WSL2 limitations.
