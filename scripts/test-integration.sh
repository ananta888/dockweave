#!/usr/bin/env bash
# Mount tests run only in this disposable namespace. Gate containers stay unprivileged.
set -euo pipefail
repo=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)
scratch=$(mktemp -d /tmp/dockweave-integration.XXXXXXXX)
runner="dockweave-integration-${scratch##*.}"
cleanup() {
  docker rm -f "$runner" >/dev/null 2>&1 || true
  rm -rf -- "$scratch"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
docker build -f "$repo/deployments/Dockerfile.integration" -t dockweave-integration:go1.27.3 "$repo"
docker image inspect node:22-bookworm >/dev/null 2>&1 || docker pull node:22-bookworm
# --network host: the broker-isolation test listens on the gateway IP of a
# Docker bridge network, which exists only in the host network namespace.
# The mount namespace stays private to this disposable container.
docker run --rm --name "$runner" --privileged --network host \
  -v "$repo:/src:ro" \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -v "$scratch:$scratch" \
  -e "TMPDIR=$scratch" -e DOCKWEAVE_INTEGRATION_REQUIRED=1 \
  dockweave-integration:go1.27.3 \
  go test -count=1 -tags integration -v ./tests/integration/... "$@"
