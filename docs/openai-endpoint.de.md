# OpenAI-kompatibler Endpoint (Modus 2)

DockWeave stellt seine **laufenden** Agenten als OpenAI-Chat-Modelle bereit.
Anders als ein One-Shot-Adapter wird eine Anfrage in die eigene TUI des Agenten
auf dem Haupt-TTY des Containers getippt (`/dev/console`). Der Turn ist damit
im DockWeave-Webterminal, in der TUI und in tmux sichtbar, und der Mensch kann
dieselbe Session danach weiterverwenden. Dieser Modus heißt **Modus 2**.

Beide Endpoints werden von `dockweaved` bedient und vom Web-Gateway unter
`/v1/` durchgereicht.

| Methode | Pfad | Zweck |
|---|---|---|
| `GET` | `/v1/models` | laufende Agenten als Modelle |
| `POST` | `/v1/chat/completions` | einen Agenten steuern (Streaming und nicht-Streaming) |

## Authentifizierung

Das Web-Gateway verlangt das übliche `dockweave_web`-Session-Cookie (oder
`?token=…`). Der Daemon-Socket nutzt die normale Socket-Policy
(`ADMIN_UIDS`).

## Beispiel

```bash
curl -s http://127.0.0.1:8790/v1/chat/completions \
  -H "Cookie: dockweave_web=$TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "demo-opencode",
    "messages": [{"role": "user", "content": "Nenne eine Farbe."}]
  }'
```

Das Modell ist der Agentenname (oder die ID). Es wird die **letzte**
`user`-Nachricht gesendet; frühere Nachrichten werden ignoriert, weil die
laufende TUI die Konversation bereits hält.

Streaming wird unterstützt (`"stream": true`). Da die Erfassung turn-basiert
ist, kommt der Inhalt als ein Delta, sobald der Turn fertig ist, gefolgt von
einem `stop`-Chunk und `data: [DONE]`. Während der Agent denkt, werden alle
15 s Keep-Alive-Kommentare gesendet.

## Ablauf eines Turns

1. Die Session attached am Haupt-TTY des Containers und hält einen
   Hintergrund-Reader offen. Das ist nötig: ein ungelesenes PTY blockiert die
   TUI irgendwann.
2. Ein virtuelles ANSI-Terminal rendert den Stream in ein Screen-Grid.
3. Der Prompt wird getippt, **Enter folgt als separater Write, nachdem der
   Harness reagiert hat**. Manche TUIs (codex) verwerfen ein Enter, das im
   selben Read wie der Text ankommt.
4. Mehrzeilige Prompts werden in Bracketed-Paste-Sequenzen verpackt, damit
   eingebettete Zeilenumbrüche nicht früh absenden.
5. Die Antwort wird nach dem letzten Echo des Prompts extrahiert und von
   TUI-Chrome befreit (Spinner, `+ Thought`, `Build · … tok/s`,
   codex-Statuszeilen).
6. Der Turn gilt als beendet, wenn der Harness seine Abschlusszeile druckt
   (`Worked for …` bei codex, `… tok/s` bei opencode) plus Ruhephase; ohne
   Abschlusszeile greift ein 30-s-Fallback.

## Betriebshinweise

- **Ein Turn pro Agent.** Gleichzeitige Anfragen an denselben Agenten werden
  durch einen Session-Lock serialisiert.
- **Das TTY wird vergrößert** (Default 160×50) beim ersten Attach, weil
  absolute Cursor-Adressierung passen muss. Andere Terminals am selben
  Container sehen diese Größe ebenfalls.
- **Sessions werden gecacht** pro Agent und verworfen, wenn der Container
  verschwindet oder der Agent über die API gestoppt/neu gestartet/entfernt
  wird.
- **Ein liegengebliebener Entwurf wird gelöscht** (Backspaces), aber nur nach
  einem nicht akzeptierten Submit — im Normalbetrieb wird ein Mensch, der
  gerade in der TUI tippt, nicht gestört.
- Fehler sind OpenAI-förmig: `agent_no_response` und `agent_session_closed`
  liefern `502`, Client-Timeout `504`.

## Grenzen

- `usage`-Tokenzahlen sind `0` — der Harness legt sie nicht offen.
- Der Endpoint kann ein Permission-Modal nicht von „denkt nach"
  unterscheiden; ein dauerhaft offenes Modal endet in `agent_no_response`.
- Die Antwort-Extraktion ist heuristisch pro Harness (opencode, codex). Neue
  Harnesses brauchen eigene Regeln für Abschluss und Chrome.
