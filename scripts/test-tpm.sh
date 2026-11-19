#!/usr/bin/env bash
# Test the TPM-backed SSH CA signer against a software TPM (swtpm).
# WSL2 does not expose the host TPM; swtpm provides a TPM 2.0 in userspace.
#
# Install swtpm either globally:
#   sudo apt install swtpm
# or into the userspace (no sudo):
#   mkdir -p /tmp/swtpm-pkgs && cd /tmp/swtpm-pkgs
#   apt download swtpm libtpms0
#   mkdir -p /tmp/swtpm-root && for f in *.deb; do dpkg-deb -x "$f" /tmp/swtpm-root; done
#
# Then run:
#   ./scripts/test-tpm.sh
set -euo pipefail

# Locate swtpm: PATH first, then the userspace extraction.
if command -v swtpm >/dev/null 2>&1; then
  SWTPM=$(command -v swtpm)
  export LD_LIBRARY_PATH="${LD_LIBRARY_PATH:-}"
elif [ -x /tmp/swtpm-root/usr/bin/swtpm ]; then
  SWTPM=/tmp/swtpm-root/usr/bin/swtpm
  export LD_LIBRARY_PATH="/tmp/swtpm-root/usr/lib/x86_64-linux-gnu:/tmp/swtpm-root/usr/lib/x86_64-linux-gnu/swtpm"
else
  echo "swtpm not found — install it:" >&2
  echo "  sudo apt install swtpm" >&2
  echo "  # or userspace: apt download swtpm libtpms0 && dpkg-deb -x ... /tmp/swtpm-root" >&2
  exit 1
fi

state=$(mktemp -d)
ctrl_port=$((2321 + RANDOM % 1000))
server_port=$((3321 + RANDOM % 1000))

echo "starting swtpm ($SWTPM) state=$state server=127.0.0.1:$server_port"
"$SWTPM" socket --tpm2 --tpmstate dir="$state" \
  --ctrl type=tcp,port="$ctrl_port" \
  --server type=tcp,port="$server_port" \
  --flags not-need-init &
SWTPM_PID=$!
trap 'kill "$SWTPM_PID" 2>/dev/null || true; rm -rf "$state"' EXIT

# Wait for the server socket to come up.
for _ in $(seq 1 50); do
  if (echo >/dev/tcp/127.0.0.1/"$server_port") 2>/dev/null; then break; fi
  sleep 0.1
done

cd "$(dirname "$0")/.."
DOCKWEAVE_TPM_SOCKET="127.0.0.1:$server_port" \
  go test ./internal/sshbroker -run TestTPMSignerEndToEnd -v
