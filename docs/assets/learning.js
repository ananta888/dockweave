(() => {
  'use strict';

  const STORAGE = 'dockweave-learning-v1';
  const state = load();

  function load() {
    try {
      const s = JSON.parse(localStorage.getItem(STORAGE) || '{}');
      return { done: Array.isArray(s.done) ? s.done : [], checks: Array.isArray(s.checks) ? s.checks : [] };
    } catch { return { done: [], checks: [] }; }
  }
  function save() { try { localStorage.setItem(STORAGE, JSON.stringify(state)); } catch {} }
  const isDone = id => state.done.includes(id);

  const phases = [
    {
      id: 'm0', code: 'M0', title: 'Denken: Agent ≠ Modell',
      sub: 'Das mentale Modell, bevor du baust',
      body: [
        'Ein Modell erzeugt Text. Ein Harness macht daraus Aktionen.',
        'Der Agent ist Identität + Konfiguration + Harness-Laufzeit.',
        'Der Container ist die Sandbox — nicht die Identität.',
        'Trenne Modell, Harness, Sandbox, Policy und Credentials.'
      ],
      links: [['Konzeptleitfaden', 'https://github.com/ananta888/dockweave/blob/main/docs/learning/agent-harness.de.md']],
      quiz: {
        q: 'Was ist ein Harness?',
        options: ['Das LLM selbst', 'Die Schleife und Werkzeuge um das Modell', 'Ein Docker-Container', 'Ein API-Key'],
        answer: 1,
        ok: 'Richtig. Der Harness steuert das Modell und führt Tools aus.',
        bad: 'Nicht ganz: Das Modell erzeugt Text; der Harness macht daraus Aktionen.'
      }
    },
    {
      id: 'm1', code: 'M1', title: 'Der Agent-Loop',
      sub: 'planen → handeln → beobachten → wiederholen',
      body: [
        'Eine Schleife ruft das Modell auf, führt Tool-Calls aus und gibt Ergebnisse zurück.',
        'Setze maximale Schritte, Timeouts und Abbruchmöglichkeiten.',
        'Validiere Tool-Aufrufe gegen ein JSON-Schema.',
        'Begrenze Tool-Ausgaben, damit der Kontext nicht überläuft.'
      ],
      snippet: 'for step := 0; step < maxSteps; step++ {\n  resp := model.Chat(ctx, tools, history)\n  if len(resp.ToolCalls) == 0 { return resp.Text }\n  for _, call := range resp.ToolCalls {\n    history = append(history, executeTool(ctx, call))\n  }\n}',
      quiz: {
        q: 'Warum braucht der Loop ein Max-Steps-Limit?',
        options: ['Damit das Modell schneller antwortet', 'Damit ein Agent nicht endlos Tools aufruft', 'Damit Docker schneller startet', 'Damit der API-Key gültig bleibt'],
        answer: 1,
        ok: 'Genau. Ohne Limit kann eine Schleife dauerhaft weiterlaufen.',
        bad: 'Es geht um Begrenzung: Endlosschleifen und unkontrollierte Tool-Aufrufe verhindern.'
      }
    },
    {
      id: 'm2', code: 'M2', title: 'Sandbox',
      sub: 'Container, Rechte, Dateien, Ressourcen',
      body: [
        'Agent non-root starten, alle Capabilities droppen.',
        'Kein Docker-Socket und kein --privileged im Agenten.',
        'Nur explizit erlaubte Host-Verzeichnisse mounten.',
        'CPU-, Memory- und Netzwerklimits setzen.'
      ],
      snippet: 'docker run --rm -it \\\n  --user 1000:1000 --cap-drop ALL \\\n  --security-opt no-new-privileges \\\n  --network none --memory 2g --cpus 1 \\\n  -v /srv/project:/workspace/project:ro \\\n  codex-image',
      quiz: {
        q: 'Was gehört nicht in einen Agentencontainer?',
        options: ['Ein Projektverzeichnis', 'Der Docker-Socket', 'Ein Memory-Verzeichnis', 'Ein non-root User'],
        answer: 1,
        ok: 'Richtig. Der Docker-Socket würde die Sandbox aushebeln.',
        bad: 'Der Docker-Socket ist tabu: Damit könnte der Agent den Host kontrollieren.'
      }
    },
    {
      id: 'm3', code: 'M3', title: 'Steuerungsebene',
      sub: 'Daemon, CLI, Unix-Socket, SO_PEERCRED',
      body: [
        'Privilegierte Operationen laufen nur im Daemon.',
        'CLI/TUI/Web sind dünne Clients über einen lokalen Socket.',
        'SO_PEERCRED identifiziert den Peer; UID-Rollen autorisieren.',
        'Unbekannte UIDs werden abgelehnt, Monitor ist read-only.'
      ],
      quiz: {
        q: 'Warum läuft Docker nicht direkt in der CLI?',
        options: ['Weil die CLI sonst langsamer wäre', 'Weil Privilegien zentral und prüfbar bleiben müssen', 'Weil Docker nur root mag', 'Weil die CLI kein JSON kann'],
        answer: 1,
        ok: 'Genau. Eine zentrale Steuerungsebene macht Rechte und Audit prüfbar.',
        bad: 'Es geht um Privilegientrennung: nur der Daemon darf privilegiert handeln.'
      }
    },
    {
      id: 'm4', code: 'M4', title: 'Identität & Persistenz',
      sub: 'stabile Agenten, SQLite, Migrationen',
      body: [
        'Agenten-IDs bleiben stabil, auch wenn Container neu erstellt werden.',
        'Persistiere Identität, Typ, Image, Command und State.',
        'Workspaces, Grants, MCP/Skills und Events in eigenen Tabellen.',
        'Migrationen versionieren; keine Secrets in Metadaten.'
      ],
      quiz: {
        q: 'Was sollte die Agentenidentität überleben?',
        options: ['Nur den Container-Namen', 'Container-Neuerstellung und Neustarts', 'Nur den aktuellen Prozess', 'Den API-Key'],
        answer: 1,
        ok: 'Richtig. Identität ist unabhängig von Container-IDs.',
        bad: 'Die Identität muss eine Container-Neuerstellung überleben.'
      }
    },
    {
      id: 'm5', code: 'M5', title: 'Workspaces & Mounts',
      sub: 'Grants, Allowlist, TOCTOU, RO-Staging',
      body: [
        'Ein Grant ist eine Erlaubnis; der Mount macht sie wirksam.',
        'Nur registrierte Host-Pfade sind mountbar (Allowlist).',
        'Symlinks auflösen und die Quelle per openat2 erneut prüfen.',
        'Read-only sauber stagen; kein umount -l als Default.'
      ],
      snippet: 'privat binden → remount,bind,ro → mount --move\n# nicht: mount -o ro direkt in den Shared-Baum',
      quiz: {
        q: 'Warum reicht ein direktes `mount -o ro` nicht?',
        options: ['Weil Docker das ignoriert', 'Weil Remount-Flags nicht zuverlässig in Slave-Namespaces propagieren', 'Weil es zu langsam ist', 'Weil es nur für Dateien geht'],
        answer: 1,
        ok: 'Genau. Deshalb wird privat gestaged und dann verschoben.',
        bad: 'Die RO-Flags propagieren nicht zuverlässig — deshalb das Staging.'
      }
    },
    {
      id: 'm6', code: 'M6', title: 'Secrets & Broker',
      sub: 'secretless agents, Vault, Sessions',
      body: [
        'Keine echten API-Keys in Env, Image, Logs oder /memory.',
        'Vault: Argon2id + AES-256-GCM, AAD pro Eintrag, atomare Writes.',
        'Broker injiziert den echten Key nur für feste Provider-Ziele.',
        'Agent bekommt nur ein kurzlebiges Session-Token; Deny by default.'
      ],
      quiz: {
        q: 'Was bekommt der Agent vom Broker?',
        options: ['Den echten Provider-Key', 'Ein kurzlebiges Session-Token', 'Den Vault-Master-Key', 'Gar nichts'],
        answer: 1,
        ok: 'Richtig. Der echte Key bleibt im Broker.',
        bad: 'Der Agent bekommt nur ein eingeschränktes Session-Token, nie den echten Key.'
      }
    },
    {
      id: 'm7', code: 'M7', title: 'Egress',
      sub: 'Proxy ist keine Netzgrenze',
      body: [
        'Wenn der Agent direkt ins Internet darf, umgeht er den Broker.',
        '`offline`: kein Netz; `broker-only`: internes Docker-Netz.',
        'Netzwerkregeln liegen außerhalb des Agenten und fail closed.',
        'Host-Gateway-Portfilterung (nftables) ist ein eigener offener Schritt.'
      ],
      quiz: {
        q: 'Warum ist ein HTTP-Proxy allein keine Egress-Sicherheit?',
        options: ['Weil er zu langsam ist', 'Weil der Agent ihn umgehen kann, wenn er direkten Netzwerkzugriff hat', 'Weil er keine HTTPS kann', 'Weil er nur für Browser ist'],
        answer: 1,
        ok: 'Genau. Ohne erzwungene Netzgrenze ist der Proxy optional.',
        bad: 'Der Agent kann einen Proxy umgehen, wenn er direkt routen darf.'
      }
    },
    {
      id: 'm8', code: 'M8', title: 'Audit & Monitoring',
      sub: 'Hash-Kette, Events, Konflikte',
      body: [
        'Sicherheitsrelevante Aktionen als Events protokollieren.',
        'Hash-Kette macht nachträgliche Manipulation erkennbar.',
        'Docker- und Prozessdaten für Aktivität/Konflikte beobachten.',
        'Nie Secrets in Events oder Logs schreiben.'
      ],
      quiz: {
        q: 'Was macht eine Audit-Hash-Kette?',
        options: ['Sie verschlüsselt Events', 'Sie macht Manipulation an der Historie erkennbar', 'Sie löscht alte Events', 'Sie ersetzt Backups'],
        answer: 1,
        ok: 'Richtig. Jedes Event bindet an das vorherige.',
        bad: 'Die Kette dient der Manipulationserkennung, nicht der Verschlüsselung.'
      }
    },
    {
      id: 'm9', code: 'M9', title: 'UI: CLI, TUI, Web',
      sub: 'Bedienung ohne Sicherheitsverlust',
      body: [
        'CLI zuerst: testbar, skriptbar, ehrlich.',
        'TUI für interaktive Terminal-Bedienung.',
        'Web braucht ein Gateway: Browser kann keinen Unix-Socket.',
        'Token/Cookie, PTY über WebSocket, keine Secrets in der UI.'
      ],
      quiz: {
        q: 'Warum braucht eine Web-UI ein Gateway?',
        options: ['Weil Browser keinen Unix-Domain-Socket sprechen', 'Weil HTTP kein JSON kann', 'Weil Docker einen Browser braucht', 'Weil Tokens nur im Browser funktionieren'],
        answer: 0,
        ok: 'Genau. Das Gateway verbindet Browser HTTP/WS mit dem Unix-Socket.',
        bad: 'Browser können keine Unix-Sockets öffnen — daher das Gateway.'
      }
    },
    {
      id: 'm10', code: 'M10', title: 'MCP & Skills',
      sub: 'Werkzeuge pro Agent konfigurierbar',
      body: [
        'MCP ist JSON-RPC 2.0; Tools haben ein inputSchema (JSON Schema).',
        'Skills/MCP pro Agententyp als Vorlage speichern.',
        'Beim Agentenanlegen in eigene, editierbare Zeilen kopieren.',
        'Vor dem Start ins Memory-Home materialisieren.'
      ],
      quiz: {
        q: 'Beschreibt MCP einen ganzen Server als ein Standard-JSON?',
        options: ['Ja, genau ein Dokument', 'Nein: JSON-RPC-Methoden plus Schemas pro Tool', 'Nur für Claude', 'Nur für OpenAI'],
        answer: 1,
        ok: 'Richtig. Ein normalisierter Server-Contract ist eine Ergänzung, kein MCP-Standard.',
        bad: 'MCP definiert Methoden und Tool-Schemas, kein einzelnes Gesamt-JSON.'
      }
    }
  ];

  /* ---------- progress ---------- */
  const totalEl = document.getElementById('progress-total');
  const countEl = document.getElementById('progress-count');
  const barEl = document.getElementById('progress-bar');
  const badgesEl = document.getElementById('badges');
  totalEl.textContent = String(phases.length);

  function renderProgress() {
    const done = phases.filter(p => isDone(p.id)).length;
    countEl.textContent = String(done);
    barEl.style.width = `${(done / phases.length) * 100}%`;
    const badges = [
      ['🌱', 'Erste Schritte', done >= 1],
      ['🧱', 'Sandbox-Versteher', done >= 3],
      ['🔐', 'Secretless', done >= 6],
      ['🛡️', 'Grenzgänger', done >= 8],
      ['🏆', 'Systemarchitekt', done >= phases.length]
    ];
    badgesEl.innerHTML = badges.map(([i, label, on]) => `<span class="badge ${on ? 'on' : ''}">${i} ${label}</span>`).join('');
    if (done === phases.length && !state.celebrated) {
      state.celebrated = true; save(); confetti();
    }
  }

  function complete(id) {
    if (!isDone(id)) { state.done.push(id); save(); renderProgress(); }
    const el = document.querySelector(`[data-phase="${id}"]`);
    if (el) { el.classList.add('done'); const s = el.querySelector('.phase-status'); if (s) s.textContent = '✓ verstanden'; }
  }

  /* ---------- phases ---------- */
  const phasesEl = document.getElementById('phases');
  function renderPhases() {
    phasesEl.innerHTML = phases.map(p => `
      <details class="phase ${isDone(p.id) ? 'done' : ''}" data-phase="${p.id}">
        <summary>
          <span class="phase-badge">${p.code}</span>
          <span class="phase-title"><b>${p.title}</b><span>${p.sub}</span></span>
          <span class="phase-status">${isDone(p.id) ? '✓ verstanden' : 'offen'}</span>
        </summary>
        <div class="phase-body">
          <ul>${p.body.map(x => `<li>${x}</li>`).join('')}</ul>
          ${p.snippet ? `<pre><code>${escapeHtml(p.snippet)}</code></pre>` : ''}
          ${p.links ? `<div class="phase-links">${p.links.map(([l, u]) => `<a class="btn small ghost" href="${u}">${l}</a>`).join('')}</div>` : ''}
          <div class="quiz" data-quiz="${p.id}">
            <h4>Mini-Check</h4>
            <p>${p.quiz.q}</p>
            <div class="quiz-options">
              ${p.quiz.options.map((o, i) => `<button type="button" data-answer="${i}">${o}</button>`).join('')}
            </div>
            <div class="quiz-feedback"></div>
          </div>
          <div class="phase-actions">
            <button class="btn primary small" data-complete="${p.id}">Als verstanden markieren</button>
          </div>
        </div>
      </details>
    `).join('');

    phasesEl.querySelectorAll('.quiz').forEach(quiz => {
      const id = quiz.dataset.quiz;
      const p = phases.find(x => x.id === id);
      const fb = quiz.querySelector('.quiz-feedback');
      quiz.querySelectorAll('button[data-answer]').forEach(btn => {
        btn.addEventListener('click', () => {
          const ans = Number(btn.dataset.answer);
          quiz.querySelectorAll('button[data-answer]').forEach(b => b.classList.remove('correct', 'wrong'));
          if (ans === p.quiz.answer) {
            btn.classList.add('correct');
            fb.className = 'quiz-feedback ok';
            fb.textContent = '✓ ' + p.quiz.ok;
            complete(id);
          } else {
            btn.classList.add('wrong');
            fb.className = 'quiz-feedback bad';
            fb.textContent = '✗ ' + p.quiz.bad;
          }
        });
      });
    });
    phasesEl.querySelectorAll('[data-complete]').forEach(btn => {
      btn.addEventListener('click', () => complete(btn.dataset.complete));
    });
  }

  /* ---------- layers ---------- */
  const layers = [
    ['Operator / UI', 'Identität, Policy, Audit', 'Der Operator entscheidet. Die UI ist nur ein Client; Rechte werden zentral geprüft.'],
    ['Orchestrierung', 'Agenten, Ketten, Gates', 'Startet Agenten, verbindet Ketten und deterministische Gates — ohne selbst privilegiert zu sein.'],
    ['Sandbox', 'Container, Dateien, Netz', 'Begrenzt Prozesse, Dateien und Netzwerk. Ein Container allein ist keine vollständige Netzgrenze.'],
    ['Harness', 'Prompt, Tools, Loop', 'Das Programm um das Modell: Tool-Calls, Kontext, Schleife, Fehlerbehandlung.'],
    ['Modell', 'LLM-Provider', 'Erzeugt Text. Der Modell-Provider sieht nur die Anfrage — nicht deine Secrets-Datei.'],
    ['Memory & Skills', 'Memory, Knowledge, MCP', 'Persistentes Wissen und Werkzeuge. Pro Agent konfigurierbar, vor dem Start materialisiert.'],
    ['Credentials', 'Vault, Broker, Sessions', 'Hält echte Keys außerhalb des Agenten und gibt nur kurzlebige Session-Tokens heraus.']
  ];
  const layersEl = document.getElementById('layers');
  const layerExp = document.getElementById('layer-explanation');
  layersEl.innerHTML = layers.map(([name, sub], i) => `<button class="layer" data-layer="${i}"><b>${name}</b><span>${sub}</span></button>`).join('');
  layersEl.querySelectorAll('[data-layer]').forEach(btn => {
    btn.addEventListener('click', () => {
      layersEl.querySelectorAll('.layer').forEach(b => b.classList.remove('active'));
      btn.classList.add('active');
      const [name, , text] = layers[Number(btn.dataset.layer)];
      layerExp.innerHTML = `<h4>${name}</h4><p>${text}</p>`;
    });
  });

  /* ---------- simulator ---------- */
  const sim = [
    ['plan', 'Agent plant: „Ich muss die Datei lesen und dann patchen.“'],
    ['act', 'Tool-Call: read_file({ path: "src/main.go" })'],
    ['observe', 'Beobachtung: 312 Zeilen gelesen. Kontext aktualisiert.'],
    ['plan', 'Agent plant: „Ich ändere Zeile 42 und prüfe den Build.“'],
    ['act', 'Tool-Call: apply_patch(...)'],
    ['observe', 'Beobachtung: Patch angewendet. Build gestartet.'],
    ['act', 'Tool-Call: shell({ cmd: "go test ./..." })'],
    ['observe', 'Beobachtung: Tests grün.'],
    ['done', 'Fertig: Antwort an den Nutzer. (Im echten Harness: Max-Steps, Timeouts, Abbruch.)']
  ];
  let simIndex = 0;
  const logEl = document.getElementById('sim-log');
  document.getElementById('sim-next').addEventListener('click', () => {
    if (simIndex >= sim.length) return;
    const [kind, text] = sim[simIndex++];
    const line = document.createElement('div');
    line.className = 'sim-line ' + kind;
    line.textContent = text;
    logEl.appendChild(line);
    logEl.scrollTop = logEl.scrollHeight;
  });
  document.getElementById('sim-reset').addEventListener('click', () => {
    simIndex = 0;
    logEl.innerHTML = '<div class="sim-line plan">[bereit] Klicke „Nächster Schritt“.</div>';
  });

  /* ---------- checklist ---------- */
  const checks = [
    'Agent läuft non-root, ohne Caps, ohne Docker-Socket.',
    'Nur registrierte Host-Pfade sind mountbar.',
    'RO-Mounts werden korrekt gestaged.',
    'Keine Secrets in Env, Inspect, SQLite oder Logs.',
    'Broker: Deny by default, feste Ziele, keine Redirects.',
    'Session-Tokens gehasht, ablaufbar, widerrufbar.',
    'Egress: broker-only blockt externe Routen.',
    'Fehler führen zu Ablehnung, nicht zu mehr Rechten.',
    'Audit-Events enthalten keine Secrets.',
    'Offene Risiken sind dokumentiert.'
  ];
  const checkEl = document.getElementById('checklist');
  checkEl.innerHTML = checks.map((c, i) => `<label><input type="checkbox" data-check="${i}" ${state.checks.includes(i) ? 'checked' : ''}><span>${c}</span></label>`).join('');
  checkEl.querySelectorAll('input[data-check]').forEach(input => {
    input.addEventListener('change', () => {
      const i = Number(input.dataset.check);
      if (input.checked) { if (!state.checks.includes(i)) state.checks.push(i); }
      else state.checks = state.checks.filter(x => x !== i);
      save();
    });
  });

  /* ---------- confetti ---------- */
  function confetti() {
    const wrap = document.createElement('div');
    wrap.className = 'confetti';
    const colors = ['#58a6ff', '#bc8cff', '#f0c000', '#3fb950', '#f85149'];
    for (let i = 0; i < 70; i++) {
      const p = document.createElement('i');
      p.style.left = Math.random() * 100 + 'vw';
      p.style.background = colors[i % colors.length];
      p.style.animationDelay = (Math.random() * 0.8) + 's';
      p.style.animationDuration = (2.2 + Math.random() * 1.8) + 's';
      wrap.appendChild(p);
    }
    document.body.appendChild(wrap);
    setTimeout(() => wrap.remove(), 5000);
  }

  function escapeHtml(s) {
    return String(s).replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
  }

  renderPhases();
  renderProgress();
})();
