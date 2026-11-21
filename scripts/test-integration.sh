#!/usr/bin/env bash
# Mount tests run only in this disposable namespace. Gate containers stay unprivileged.
set -euo pipefail
repo=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)
scratch=$(mktemp -d /tmp/dockweave-integration.XXXXXXXX)
runner="dockweave-integration-${scratch##*.}"
cleanup() {
  docker rm -f "$runner" >/dev/null 2>&1 || true
  # Gate sandboxes are started on the host daemon and survive an aborted
  # runner. Remove only gate containers that mount this run's scratch tree.
  for id in $(docker ps -aq --filter label=dockweave.gate=true 2>/dev/null); do
    if docker inspect -f '{{range .Mounts}}{{.Source}} {{end}}' "$id" 2>/dev/null | grep -qF "$scratch/"; then
      docker rm -f "$id" >/dev/null 2>&1 || true
    fi
  done
  rm -rf -- "$scratch"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
docker build -f "$repo/deployments/Dockerfile.integration" -t dockweave-integration:go1.27.3 "$repo"
# Digest-pinned gate/probe image (multi-arch index of node:22-bookworm).
# Update deliberately: docker buildx imagetools inspect node:22-bookworm
gate_image="node:22-bookworm@sha256:e13726ca604184d7a91fcacab1fc09eefd642dc28e79ac34e85540dd93b78cfb"
docker image inspect "$gate_image" >/dev/null 2>&1 || docker pull "$gate_image"
# --network host: the broker-isolation test listens on the gateway IP of a
# Docker bridge network, which exists only in the host network namespace.
# The mount namespace stays private to this disposable container.
# Consequence: the image deliberately ships no nft binary. With the host
# network namespace, the nftables egress tests (TestEgressTable*,
# TestEgressBypassMatrix) would change the host firewall; they need their own
# network namespace or a disposable VM runner and skip here.
docker run --rm --name "$runner" --privileged --network host \
  -v "$repo:/src:ro" \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -v "$scratch:$scratch" \
  -e "TMPDIR=$scratch" -e DOCKWEAVE_INTEGRATION_REQUIRED=1 -e "DOCKWEAVE_GATE_TEST_IMAGE=$gate_image" \
  dockweave-integration:go1.27.3 \
  go test -count=1 -tags integration -v ./tests/integration/... "$@"
