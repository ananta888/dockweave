# Credential-Broker-Demo

Startet einen **Fake**-OpenAI-kompatiblen Provider und den DockWeave-Broker
lokal. Es werden ausschließlich Dummy-Credentials verwendet; die Demo zeigt:

- der Agent/Client sieht nur ein `dws_…`-Session-Token,
- der Broker injiziert den echten (Dummy-)Vault-Wert upstream,
- Streaming/Usage und das secret-freie Request-Log funktionieren,
- Budget-/Zähler werden aktualisiert.

```bash
./examples/credential-broker/demo.sh
```

Voraussetzungen: Go-Toolchain, `curl`, `awk`.

| Datei | Zweck |
|---|---|
| `fake-provider/main.go` | Lokaler `/chat/completions`-Upstream, der den erhaltenen Auth-Header ausgibt |
| `providers.demo.json` | Zeigt den `openrouter`-Adapter auf den Fake-Provider (http, nur lokal) |
| `demo.sh` | End-to-End-Demo mit temporärem Datenverzeichnis und Cleanup |

Die Demo nutzt `--allow-insecure-http`, weil der Fake-Upstream unverschlüsselt
auf Loopback läuft. Niemals mit einem echten Provider verwenden; der Broker
lehnt Nicht-HTTPS-Provider-URLs standardmäßig ab.

Für das echte OpenCode/OpenRouter-Setup siehe
[`docs/credential-broker.de.md`](../../docs/credential-broker.de.md).
