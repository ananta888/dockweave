#!/usr/bin/env bash
# Test the TPM-backed SSH CA signer against a software TPM (swtpm).
# WSL2 does not expose the host TPM; swtpm provides a TPM 2.0 in userspace.
#
#   sudo apt install swtpm
#   ./scripts/test-tpm.sh
set -euo pipefail

if ! command -v swtpm >/dev/null 2>&1; then
  echo "swtpm not found — install it first:" >&2
  echo "  sudo apt install swtpm" >&2
  exit 1
fi

state=$(mktemp -d)
ctrl_port=$((2321 + RANDOM % 1000))
server_port=$((3321 + RANDOM % 1000))

echo "starting swtpm (state=$state ctrl=127.0.0.1:$ctrl_port server=127.0.0.1:$server_port)"
swtpm socket --tpm2 --tpmstate dir="$state" \
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

DOCKWEAVE_TPM_SOCKET="127.0.0.1:$server_port" \
  go test ./internal/sshbroker -run TestTPMSignerEndToEnd -v
