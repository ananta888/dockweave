# DockWeave credential broker — Codex (ChatGPT OAuth) setup

This installs the broker as an unprivileged **systemd user service** and refreshes
a short-lived broker session token for Codex every hour. The agent holds only a
`dws_…` token; the real ChatGPT OAuth bundle stays in the encrypted vault.

## 1. Vault passphrase

The broker must unlock unattended. Create a 0600 env file (never a
world-readable unit):

```bash
mkdir -p ~/.config/dockweave
umask 077
printf 'DOCKWEAVE_VAULT_PASSPHRASE=%s\n' "$(openssl rand -hex 32)" \
  > ~/.config/dockweave/broker.env
```

Back this file up separately. Losing it makes the vault unrecoverable; leaking
it exposes every stored credential.

## 2. Initialize the vault

```bash
set -a; . ~/.config/dockweave/broker.env; set +a
timeout 2 ~/dockweave/bin/dockweave-broker --init \
  --data-dir ~/.local/share/dockweave/broker --listen 127.0.0.1:8787 || true
```

## 3. Install the units

```bash
mkdir -p ~/.config/systemd/user
cp deployments/credential-broker/dockweave-broker.service ~/.config/systemd/user/
cp deployments/credential-broker/dockweave-session-refresh.service ~/.config/systemd/user/
cp deployments/credential-broker/dockweave-session-refresh.timer ~/.config/systemd/user/
install -Dm755 deployments/credential-broker/dockweave-codex-refresh.sh ~/.local/bin/dockweave-codex-refresh
systemctl --user daemon-reload
systemctl --user enable --now dockweave-broker.service
systemctl --user enable --now dockweave-session-refresh.timer
```

`loginctl enable-linger "$USER"` makes the units survive logout/reboot.

## 4. Import the agent login and grant a budget

Import the **agent container's** login (keep your host login separate so the
rotating refresh tokens do not invalidate each other):

```bash
export DOCKWEAVE_BROKER_ADMIN_SOCKET=~/.local/share/dockweave/broker/admin.sock
cid=$(docker ps -q --filter label=dockweave.agent.id=ag_…)
tmp=$(mktemp); chmod 600 "$tmp"
docker exec "$cid" cat /memory/.codex/auth.json > "$tmp"
dockweave secret import-codex codex-main --auth-file "$tmp" \
  --grant --agent developer --models '*' --rpm 60 --concurrent 4 --tokens-day 5000000
rm -f "$tmp"
```

## 5. Point Codex at the broker

In the agent's `~/.codex/config.toml` (`/memory/.codex/config.toml`):

```toml
model_provider = "dockweave"
model = "gpt-6.1-sol"

[model_providers.dockweave]
name = "DockWeave"
base_url = "http://172.17.0.1:8787/p/codex-chatgpt"
wire_api = "responses"

[model_providers.dockweave.auth]
command = "/bin/sh"
args = ["-c", "cat /memory/.config/dockweave/codex-token"]
refresh_interval_ms = 300000
```

Write the first token (the timer then refreshes hourly):

```bash
~/.local/bin/dockweave-codex-refresh ag_… developer codex-main 8h
```

Restart the agent. Remove/rename the container's `auth.json` once verified: the
agent no longer needs it.

## Notes

- The broker listens on the Docker bridge gateway (`172.17.0.1:8787`). Every
  bridge container can reach it, but requests require a `dws_…` token and a
  matching grant (DENY by default).
- Session TTL is 8 h; the timer renews hourly, so there is always overlap.
- Budgets are per grant (`--rpm`, `--concurrent`, `--tokens-day`,
  `--usd-day`, …). Several agents can share one vault credential with separate
  grants and separate budgets.
- The refresh token rotates. Never use the same login in the broker and in an
  independent client at the same time.
