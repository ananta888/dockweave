# DockWeave Credential-Broker — Codex (ChatGPT OAuth) einrichten

Richtet den Broker als unprivilegierten **systemd-User-Service** ein und
erneuert stündlich ein kurzlebiges Broker-Session-Token für Codex. Der Agent
hält nur ein `dws_…`-Token; das echte ChatGPT-OAuth-Bundle bleibt im
verschlüsselten Vault.

## 1. Vault-Passphrase

Der Broker muss unbeaufsichtigt entsperren. Eine 0600-Env-Datei anlegen (nie
eine weltlesbare Unit):

```bash
mkdir -p ~/.config/dockweave
umask 077
printf 'DOCKWEAVE_VAULT_PASSPHRASE=%s\n' "$(openssl rand -hex 32)" \
  > ~/.config/dockweave/broker.env
```

Diese Datei separat sichern. Verlust macht den Vault unbrauchbar; ein Leak
gibt alle gespeicherten Credentials preis.

## 2. Vault initialisieren

```bash
set -a; . ~/.config/dockweave/broker.env; set +a
timeout 2 ~/dockweave/bin/dockweave-broker --init \
  --data-dir ~/.local/share/dockweave/broker --listen 127.0.0.1:8787 || true
```

## 3. Units installieren

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

`loginctl enable-linger "$USER"` lässt die Units Logout/Reboot überleben.

## 4. Agent-Login importieren und Budget vergeben

Den Login des **Agentencontainers** importieren (den Host-Login getrennt
halten, damit sich die rotierenden Refresh-Tokens nicht gegenseitig
invalidieren):

```bash
export DOCKWEAVE_BROKER_ADMIN_SOCKET=~/.local/share/dockweave/broker/admin.sock
cid=$(docker ps -q --filter label=dockweave.agent.id=ag_…)
tmp=$(mktemp); chmod 600 "$tmp"
docker exec "$cid" cat /memory/.codex/auth.json > "$tmp"
dockweave secret import-codex codex-main --auth-file "$tmp" \
  --grant --agent developer --models '*' --rpm 60 --concurrent 4 --tokens-day 5000000
rm -f "$tmp"
```

## 5. Codex auf den Broker zeigen lassen

In `~/.codex/config.toml` des Agenten (`/memory/.codex/config.toml`):

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

Erstes Token schreiben (danach erneuert der Timer stündlich):

```bash
~/.local/bin/dockweave-codex-refresh ag_… developer codex-main 8h
```

Agent neu starten. Nach erfolgreicher Prüfung `auth.json` im Container
entfernen/umbenennen — der Agent braucht sie nicht mehr.

## Hinweise

- Der Broker lauscht am Docker-Bridge-Gateway (`172.17.0.1:8787`). Jeder
  Bridge-Container erreicht ihn, aber Requests brauchen ein `dws_…`-Token und
  einen passenden Grant (DENY per Default).
- Session-TTL 8 h; der Timer erneuert stündlich, es gibt also immer Überlappung.
- Budgets gelten pro Grant (`--rpm`, `--concurrent`, `--tokens-day`,
  `--usd-day`, …). Mehrere Agenten können ein Vault-Credential mit getrennten
  Grants und getrennten Budgets nutzen.
- Refresh-Tokens rotieren. Denselben Login nie gleichzeitig im Broker und in
  einem unabhängigen Client verwenden.
