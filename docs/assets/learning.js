(() => {
  'use strict';

  const GH_DW = 'https://github.com/ananta888/dockweave/blob/main/';
  const GH_HR = 'https://github.com/herdrdev/herdr/blob/master/';
  const GH_PP = 'https://github.com/paperclipai/paperclip/blob/master/';
  const POS_KEY = 'dockweave-deck-pos';
  const CHK_KEY = 'dockweave-deck-checks';

  /* ================= helpers ================= */
  function escapeHtml(s) {
    return String(s).replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
  }
  function rv(i, extra) { return `class="rv ${extra || ''}" style="--i:${i}"`; }

  function renderMd(src) {
    const lines = String(src).split('\n');
    const out = [];
    let i = 0;
    const inline = s => escapeHtml(s)
      .replace(/`([^`]+)`/g, '<code>$1</code>')
      .replace(/\*\*([^*]+)\*\*/g, '<strong>$1</strong>')
      .replace(/\[([^\]]+)\]\(([^)]+)\)/g, '<a href="$2" target="_blank" rel="noopener">$1</a>');
    while (i < lines.length) {
      const line = lines[i];
      const fence = line.match(/^\s*(```|~~~)([A-Za-z0-9_-]*)\s*$/);
      if (fence) {
        const buf = []; i++;
        while (i < lines.length && !/^\s*(```|~~~)\s*$/.test(lines[i])) { buf.push(lines[i]); i++; }
        i++;
        out.push('<pre><code>' + escapeHtml(buf.join('\n')) + '</code></pre>');
        continue;
      }
      if (/^\s*$/.test(line)) { i++; continue; }
      if (/^\s*-{3,}\s*$/.test(line)) { out.push('<hr>'); i++; continue; }
      const h = line.match(/^(#{1,6})\s+(.*)$/);
      if (h) { const lvl = Math.min(2 + h[1].length, 6); out.push('<h' + lvl + '>' + inline(h[2]) + '</h' + lvl + '>'); i++; continue; }
      if (/^\s*[-*]\s+\[[ xX]\]\s+/.test(line)) {
        const items = [];
        while (i < lines.length && /^\s*[-*]\s+\[[ xX]\]\s+/.test(lines[i])) {
          const m = lines[i].match(/^\s*[-*]\s+\[([ xX])\]\s+(.*)$/);
          items.push('<li><input type="checkbox" disabled ' + (m[1].toLowerCase() === 'x' ? 'checked' : '') + '> ' + inline(m[2]) + '</li>');
          i++;
        }
        out.push('<ul>' + items.join('') + '</ul>'); continue;
      }
      if (/^\s*[-*+]\s+/.test(line)) {
        const items = [];
        while (i < lines.length && /^\s*[-*+]\s+/.test(lines[i])) { items.push('<li>' + inline(lines[i].replace(/^\s*[-*+]\s+/, '')) + '</li>'); i++; }
        out.push('<ul>' + items.join('') + '</ul>'); continue;
      }
      if (/^\s*\d+[.)]\s+/.test(line)) {
        const items = [];
        while (i < lines.length && /^\s*\d+[.)]\s+/.test(lines[i])) { items.push('<li>' + inline(lines[i].replace(/^\s*\d+[.)]\s+/, '')) + '</li>'); i++; }
        out.push('<ol>' + items.join('') + '</ol>'); continue;
      }
      if (/^\s*>/.test(line)) {
        const buf = [];
        while (i < lines.length && /^\s*>/.test(lines[i])) { buf.push(lines[i].replace(/^\s*>\s?/, '')); i++; }
        out.push('<blockquote>' + inline(buf.join(' ')) + '</blockquote>'); continue;
      }
      if (/^\s*\|.*\|\s*$/.test(line) && i + 1 < lines.length && /^\s*\|?[\s:|-]+\|?\s*$/.test(lines[i + 1])) {
        const header = line.split('|').slice(1, -1).map(c => c.trim()); i += 2;
        const rows = [];
        while (i < lines.length && /^\s*\|.*\|\s*$/.test(lines[i])) { rows.push(lines[i].split('|').slice(1, -1).map(c => c.trim())); i++; }
        out.push('<table><thead><tr>' + header.map(c => '<th>' + inline(c) + '</th>').join('') + '</tr></thead><tbody>' + rows.map(r => '<tr>' + r.map(c => '<td>' + inline(c) + '</td>').join('') + '</tr>').join('') + '</tbody></table>');
        continue;
      }
      out.push('<p>' + inline(line) + '</p>'); i++;
    }
    return out.join('\n');
  }

  /* ================= page renderers ================= */
  function codeBlock(file, lang, code, url) {
    return `<div class="codefile">
      <div class="codefile-head"><span class="fname">${file}</span>${url ? `<a href="${url}" target="_blank" rel="noopener">↗</a>` : ''}<span class="lang">${lang}</span></div>
      <pre><code>${escapeHtml(code)}</code></pre>
    </div>`;
  }

  function renderPage(ch, p, pi, total) {
    switch (p.t) {
      case 'intro':
        return `<div class="pg-intro">
          <span class="kick" ${rv(0)}>${ch.kicker}</span>
          <h1 ${rv(1)}>${ch.title}</h1>
          <p class="sub" ${rv(2)}>${ch.sub || ''}</p>
          <p class="hint" ${rv(3)}>Seite ${pi + 1} von ${total} · <span class="hint-keys">weiter mit <kbd>→</kbd> oder Klick</span></p>
        </div>`;

      case 'bullets':
        return `<div class="page-head"><h2 ${rv(0)}>${p.title}</h2>${p.sub ? `<p class="sub" ${rv(1)}>${p.sub}</p>` : ''}</div>
        <div class="bullets">${p.points.map((pt, i) => `<div class="bullet" ${rv(i + 1)}><span class="mark">▸</span><span class="t">${pt}</span></div>`).join('')}</div>`;

      case 'numbered':
        return `<div class="page-head"><h2 ${rv(0)}>${p.title}</h2>${p.sub ? `<p class="sub" ${rv(1)}>${p.sub}</p>` : ''}</div>
        <div class="olist">${p.items.map((it, i) => `<div class="item" ${rv(i + 1)}><span class="n">${i + 1}</span><span class="t">${it}</span></div>`).join('')}</div>`;

      case 'code':
        return `<div class="page-head"><h2 ${rv(0)}>${p.title}</h2></div>
          <div ${rv(1)}>${codeBlock(p.file, p.lang, p.code, p.url)}</div>`;

      case 'codePair':
        return `<div class="page-head"><h2 ${rv(0)}>${p.title}</h2></div>
        <div class="codepair">
          <div ${rv(1)}>${codeBlock('DockWeave · ' + p.dw.file, p.dw.lang, p.dw.code, p.dw.url)}</div>
          <div ${rv(2)}>${codeBlock('herdr · ' + p.hr.file, p.hr.lang, p.hr.code, p.hr.url)}</div>
        </div>`;

      case 'md':
        return `<div class="page-head"><h2 ${rv(0)}>${p.title}</h2></div>
          <div class="mdview" ${rv(1)}>${renderMd(p.md)}</div>`;

      case 'table':
        return `<div class="page-head"><h2 ${rv(0)}>${p.title}</h2></div>
        <div class="tbl-wrap" ${rv(1)}>
          <table class="tbl"><thead><tr>${p.head.map(h => `<th>${h}</th>`).join('')}</tr></thead>
          <tbody>${p.rows.map((r, i) => `<tr ${rv(i + 1)}>${r.map(c => `<td>${c}</td>`).join('')}</tr>`).join('')}</tbody></table>
        </div>`;

      case 'quiz':
        return `<div class="page-head"><h2 ${rv(0)}>Mini-Check</h2></div>
        <div class="quiz" data-quiz data-answer="${p.answer}" data-ok="${escapeHtml(p.ok)}" data-bad="${escapeHtml(p.bad)}" ${rv(1)}>
          <p class="q">${p.q}</p>
          <div class="quiz-options">${p.options.map((o, i) => `<button type="button" data-opt="${i}">${o}</button>`).join('')}</div>
          <div class="quiz-feedback"></div>
        </div>`;

      case 'layers':
        return `<div class="page-head"><h2 ${rv(0)}>Die Schichten</h2><p class="sub" ${rv(1)}>Jede Schicht ist eine Vertrauensgrenze. Klicke für die Erklärung.</p></div>
        <div class="layers" data-layers ${rv(1)}></div>
        <div class="layer-explanation" data-layer-exp ${rv(2)}><h4>Los geht's</h4><p>Wähle eine Schicht.</p></div>`;

      case 'checklist':
        return `<div class="page-head"><h2 ${rv(0)}>${p.title}</h2><p class="sub" ${rv(1)}>Hake ab, was dein eigener Aufbau erfüllt. Der Stand bleibt lokal gespeichert.</p></div>
        <div class="checklist" data-checklist ${rv(1)}>${p.items.map((c, i) => `<label ${rv(0)}><input type="checkbox" data-check="${i}"><span>${c}</span></label>`).join('')}</div>`;

      case 'sim':
        return `<div class="page-head"><h2 ${rv(0)}>Spielwiese: der Agent-Loop</h2><p class="sub" ${rv(1)}>planen → handeln → beobachten. Klicke dich durch einen simulierten Lauf.</p></div>
        <div class="simulator" ${rv(1)}>
          <div class="sim-log" data-sim-log><div class="sim-line plan">[bereit] Klicke „Nächster Schritt“.</div></div>
          <div class="sim-actions"><button class="btn primary" data-sim-next>Nächster Schritt</button><button class="btn ghost" data-sim-reset>Zurücksetzen</button></div>
        </div>`;

      case 'cards':
        return `<div class="page-head"><h2 ${rv(0)}>${p.title}</h2>${p.sub ? `<p class="sub" ${rv(1)}>${p.sub}</p>` : ''}</div>
        <div class="cards">${p.cards.map((c, i) => `<div class="card" ${rv(i + 1)}>${c.icon ? `<div class="icon">${c.icon}</div>` : ''}<h3>${c.title}</h3><p>${c.text}</p></div>`).join('')}</div>`;

      case 'vsCards':
        return `<div class="page-head"><h2 ${rv(0)}>${p.title}</h2></div>
        <div class="vs-cards">
          <div class="vs-card dw" ${rv(1)}><h3>DockWeave <span class="tag">Go · Sandbox + Control Plane</span></h3><div class="role">Container-Sandbox, Workspace-Grants, Credential-Broker, Egress, Audit — die <strong>Sicherheits- und Grenzebene</strong>.</div><p><strong>Repo:</strong> <a href="https://github.com/ananta888/dockweave" target="_blank" rel="noopener">ananta888/dockweave</a></p><p>Führt KI-Agenten in <strong>isolierten Docker-Containern</strong> aus. Liefert Identität, Umgebung und Grenzen — bringt aber <strong>keinen eigenen Harness</strong> mit (nutzt OpenCode, Codex, Claude Code).</p></div>
          <div class="vs-card hr" ${rv(2)}><h3>herdr <span class="tag">Rust · Terminal-Runtime</span></h3><div class="role">Terminal-Multiplexer &amp; Session-Manager — die <strong>Betriebs- und Bedienebene</strong>.</div><p><strong>Repo:</strong> <a href="https://github.com/herdrdev/herdr" target="_blank" rel="noopener">herdrdev/herdr</a> · <a href="https://herdr.dev" target="_blank" rel="noopener">herdr.dev</a></p><p>„the runtime your coding agents live on“ — hält Terminals in einem Hintergrund-Server am Leben (detach/reattach), bündelt lokale + SSH-Maschinen und lässt Agenten Panes steuern. <strong>Ownet die Terminals, nicht die Container.</strong></p></div>
        </div>`;

      case 'procontra':
        return `<div class="page-head"><h2 ${rv(0)}>${p.title}</h2></div>
        <div class="procontra">${p.cols.map((c, i) => `<div class="pc-col ${c.tone}" ${rv(i + 1)}><h4>${c.head}</h4><ul>${c.items.map(x => `<li>${x}</li>`).join('')}</ul></div>`).join('')}</div>`;

      case 'vsCards3':
        return `<div class="page-head"><h2 ${rv(0)}>${p.title}</h2></div>
        <div class="vs-cards">
          <div class="vs-card dw" ${rv(1)}><h3>DockWeave <span class="tag">Go · Sandbox</span></h3><div class="role">Container-Sandbox, Grants, Credential-Broker, Egress, Audit — die <strong>Sicherheits- und Grenzebene</strong>.</div><p><strong>Repo:</strong> <a href="https://github.com/ananta888/dockweave" target="_blank" rel="noopener">ananta888/dockweave</a></p><p>Führt Agenten in <strong>isolierten Docker-Containern</strong> aus. Liefert Identität, Umgebung und Grenzen.</p></div>
          <div class="vs-card hr" ${rv(2)}><h3>herdr <span class="tag">Rust · Terminal</span></h3><div class="role">Terminal-Multiplexer &amp; Session-Manager — die <strong>Betriebs- und Bedienebene</strong>.</div><p><strong>Repo:</strong> <a href="https://github.com/herdrdev/herdr" target="_blank" rel="noopener">herdrdev/herdr</a></p><p>Hält Terminals in einem Hintergrund-Server am Leben (detach/reattach), bündelt lokale + SSH-Maschinen. <strong>Ownet die Terminals.</strong></p></div>
          <div class="vs-card pp" ${rv(3)}><h3>paperclip <span class="tag">TS · Orchestration</span></h3><div class="role">Task-Manager, Org-Chart, Budgets, Governance, Heartbeats — die <strong>Management- und Orchestrierungsebene</strong>.</div><p><strong>Repo:</strong> <a href="https://github.com/paperclipai/paperclip" target="_blank" rel="noopener">paperclipai/paperclip</a> · <a href="https://paperclip.ing" target="_blank" rel="noopener">paperclip.ing</a></p><p>Verwaltet eine <strong>„Company“ aus KI-Agenten</strong>: Ziele, Rollen, Freigaben, Kosten. Agenten laufen in <strong>Heartbeats</strong>.</p></div>
        </div>`;

      case 'codeTriple':
        return `<div class="page-head"><h2 ${rv(0)}>${p.title}</h2></div>
        <div class="codetriple">
          <div ${rv(1)}>${codeBlock('DockWeave · ' + p.dw.file, p.dw.lang, p.dw.code, p.dw.url)}</div>
          <div ${rv(2)}>${codeBlock('herdr · ' + p.hr.file, p.hr.lang, p.hr.code, p.hr.url)}</div>
          <div ${rv(3)}>${codeBlock('paperclip · ' + p.pp.file, p.pp.lang, p.pp.code, p.pp.url)}</div>
        </div>`;

      case 'note':
        return `<div class="note ${p.tone === 'blue' ? 'blue' : ''}" ${rv(0)}>${p.text}</div>`;

      case 'threeCols':
        return `<div class="page-head"><h2 ${rv(0)}>${p.title}</h2>${p.sub ? `<p class="sub" ${rv(1)}>${p.sub}</p>` : ''}</div>
        <div class="threecols">${p.cols.map((c, i) => `<div class="tc-col ${c.tone}" ${rv(i + 1)}><h4>${c.head}</h4><ul>${c.items.map(x => `<li>${x}</li>`).join('')}</ul></div>`).join('')}</div>`;
    }
    return '';
  }

  /* ================= chapters ================= */
  const chapters = [
    {
      id: 'intro', kicker: 'Intro', title: 'Baue dein eigenes Agenten-System',
      sub: 'Kein fertiges Produkt, sondern ein Verstehensweg: vom Agent-Loop über die Sandbox bis zu secretless Credentials, Egress, Audit, UI und MCP — mit einem direkten Vergleich DockWeave · herdr · paperclip.',
      pages: [
        { t: 'intro' },
        { t: 'cards', title: 'Worum geht es?', sub: 'Ein Agent ist nicht ein Programm, sondern ein System aus Schichten.', cards: [
          { icon: '🧠', title: 'Harness', text: 'Macht aus Modell-Text echte Aktionen: Tools, Shell, Dateien, Schleife.' },
          { icon: '📦', title: 'Sandbox', text: 'Begrenzt, was der Agent erreichen kann — Dateien, Prozesse, Netzwerk.' },
          { icon: '🔐', title: 'Policy & Secrets', text: 'Entscheidet, wer was darf — und hält echte Keys aus dem Agenten heraus.' }
        ]},
        { t: 'layers' },
        { t: 'bullets', title: 'So liest du diese Präsentation', points: [
          'Jedes <b>Modul</b> (M0–M16) ist ein Kapitel mit mehreren durchklickbaren Vollbild-Seiten.',
          'Navigiere mit den <b>Pfeiltasten</b> <kbd>→</kbd> <kbd>←</kbd> oder den Buttons unten.',
          'Die <b>Übersicht</b> (☰) springt direkt zu jedem Kapitel.',
          'In Quiz-Seiten klickst du eine Antwort an; Checklisten speichern deinen Stand lokal.',
          'Überall ist <b>echter Code</b> verlinkt — DockWeave (Go), herdr (Rust) und paperclip (TypeScript).'
        ]}
      ]
    },
    {
      id: 'vs', kicker: 'Vergleich', title: 'DockWeave · herdr · paperclip',
      sub: 'Drei „Runtimes für Coding-Agents“, drei Ebenen — und wie sie sich kombinieren lassen.', pages: [
        { t: 'note', tone: 'blue', text: 'Ein Agenten-Stack hat <strong>drei Ebenen</strong>, und jedes dieser drei Tools besetzt genau eine: <strong>paperclip</strong> = Management (wer macht was, wann, mit welchem Budget), <strong>herdr</strong> = Laufzeit/Session (wo lebt das Terminal), <strong>DockWeave</strong> = Sicherheit (was darf der Agent). Sie sind kein Ersatz füreinander — sie stapeln sich.' },
        { t: 'vsCards3', title: 'Drei Runtimes, drei Ebenen' },
        { t: 'table', title: 'Dimensionen', head: ['Dimension', 'DockWeave', 'herdr', 'paperclip'], rows: [
          ['Ebene', 'Container- / Sicherheitsebene', 'Terminal- / Prozess-Ebene', 'Management- / Orchestrierungs-Ebene'],
          ['Kernfrage', 'Was darf der Agent?', 'Wo läuft der Agent?', 'Wer macht was, wann, mit welchem Budget?'],
          ['Sprache', 'Go (Daemon + CLI + TUI + Web)', 'Rust (ein Binary)', 'TypeScript (Node + React)'],
          ['Isolation', '<span class="ok">Docker, cap-drop ALL, non-root</span>', '<span class="no">keine — deine Rechte</span>', '<span class="warn">lokal unsandboxed, Sandbox via Provider</span>'],
          ['Harness', 'keinen eigenen (OpenCode/Codex/Claude)', 'keinen eigenen (führt deine Agenten aus)', 'keinen eigenen (Adapter: Claude, Codex, OpenClaw, HTTP)'],
          ['Identität', '<span class="ok">stabile ID, unabhängig vom Container</span>', '<span class="warn">terminal_id / pane_id</span>', '<span class="ok">Agent im Org-Chart (Rolle, Titel, Berichtslinie)</span>'],
          ['Secrets', '<span class="ok">Vault + Broker (secretless)</span>', '<span class="no">keine Verwaltung — deine Keys</span>', '<span class="warn">verschlüsselte Secrets, aber Injection statt secretless</span>'],
          ['Egress / Netz', '<span class="ok">offline / broker-only, fail closed</span>', '<span class="no">keine Netzgrenze</span>', '<span class="warn">Tool-Gateway (Allowed / Ask / Off)</span>'],
          ['Audit', '<span class="ok">Hash-Ketten-Events</span>', '<span class="no">kein Audit-Trail</span>', '<span class="ok">Activity, Runs, Kosten, Aktionen</span>'],
          ['Session / Detach', '<span class="warn">Container stop/start</span>', '<span class="ok">detach/reattach, Layout-Restore</span>', '<span class="ok">Heartbeat + Session-Resume</span>'],
          ['Mehrere Maschinen', '<span class="no">nein (single host + Docker)</span>', '<span class="ok">ja (lokale + SSH-Maschinen)</span>', '<span class="ok">Cloud/Sandbox-Provider, Multi-Org</span>'],
          ['Agent-nativ', 'über REST-API / CLI steuerbar', 'CLI + Socket-API: spawn, prompt, wait', 'Heartbeats, Assignments, Routines, Webhooks']
        ]},
        { t: 'codeTriple', title: 'Zustand eines Agenten — drei Antworten',
          dw: { file: 'internal/model/model.go', lang: 'go', url: GH_DW + 'internal/model/model.go', code:
`type Agent struct {
    ID   string \`json:"id"\`   // ag_…, stabil
    Type string \`json:"type"\` // codex | opencode | …
    UID  int    \`json:"uid"\`
    State      AgentState \`json:"state"\`
    ContainerID string    \`json:"container_id,omitempty"\`
}` },
          hr: { file: 'src/api/schema/agents.rs', lang: 'rust', url: GH_HR + 'src/api/schema/agents.rs', code:
`pub struct AgentInfo {
    pub terminal_id: String,
    pub pane_id: String,
    pub tab_id: String,
    pub workspace_id: String,
    pub agent_status: AgentStatus,
    // idle | blocked | working | done | unknown
}` },
          pp: { file: 'packages/adapter-utils/src/acpx-engine/session-codec.ts', lang: 'ts', url: GH_PP + 'packages/adapter-utils/src/acpx-engine/session-codec.ts', code:
`// Session-Resume über Heartbeats hinweg
export const sessionCodec = {
  deserialize(record) {
    const runtimeSessionName = readString(record.runtimeSessionName);
    const agentSessionId = readString(record.agentSessionId);
    return {
      ...(runtimeSessionName ? { runtimeSessionName } : {}),
      ...(agentSessionId ? { agentSessionId } : {}),
      ...(readString(record.cwd) ? { cwd: readString(record.cwd) } : {}),
    };
  },
};` } },
        { t: 'codePair', title: 'Start & Isolation — wie wird ein Agent gestartet?',
          dw: { file: 'internal/dockerclient/dockerclient.go', lang: 'go', url: GH_DW + 'internal/dockerclient/dockerclient.go', code:
`host := &container.HostConfig{
    CapDrop:     []string{"ALL"},
    Privileged:  false,
    SecurityOpt: []string{"no-new-privileges"},
    NetworkMode: container.NetworkMode(a.NetworkMode),
    User:        fmt.Sprintf("%d:%d", a.UID, a.GID),
}` },
          hr: { file: 'src/api/schema/agents.rs', lang: 'rust', url: GH_HR + 'src/api/schema/agents.rs', code:
`pub struct AgentStartParams {
    pub name: String,      // Anzeigename
    pub kind: String,      // claude-code | codex | opencode | …
    pub pane_id: String,   // welches Pane ihn besitzt
    pub args: Vec<String>, // an die Agent-CLI durchgereicht
    pub timeout_ms: Option<u64>,
}` } },
        { t: 'code', title: 'paperclip: Heartbeat-Policy statt Dauerlauf', lang: 'ts', file: 'docs/agents-runtime.md', url: GH_PP + 'docs/agents-runtime.md', code:
`// Agenten laufen NICHT dauerhaft — sie werden geweckt (Heartbeats).
// Wakeup-Quellen: timer | assignment | on_demand | automation
{
  enabled: true,
  intervalSec: 300,        // Timer-Intervall (0 = aus)
  wakeOnAssignment: true,  // wecke bei zugewiesener Arbeit
  wakeOnOnDemand: true,    // manuelle Weckrufe
  cwd: "/workspace",
  timeoutSec: 900,         // max. Laufzeit pro Heartbeat
}` },
        { t: 'procontra', title: 'Was geht, was nicht geht', cols: [
          { tone: 'goes', head: '✓ Was DockWeave kann', items: [
            'Echte Isolation: Container, cap-drop ALL, non-root, kein Docker-Socket.',
            'Secretless Credentials: Vault + Broker, Agent sieht nur ein Session-Token.',
            'Egress-Profile (offline / broker-only), fail closed.',
            'Audit mit Hash-Kette, stabile Agenten-Identität.'
          ]},
          { tone: 'noes', head: '✗ Was DockWeave nicht kann', items: [
            'Kein Harness, kein Task-/Org-Management, keine Budgets.',
            'Kein Terminal-Multiplexing (Detach/Reattach).',
            'Single-Host: braucht Docker + root-Daemon.'
          ]},
          { tone: 'goes', head: '✓ Was herdr kann', items: [
            'Detach/Reattach: Terminals laufen im Hintergrund weiter.',
            'Multi-Machine: lokale + SSH-Maschinen in einem Fenster.',
            'Agent-nativ: CLI + Socket-API, auf „blocked“ warten.'
          ]},
          { tone: 'noes', head: '✗ Was herdr nicht kann', items: [
            'Keine Isolation, keine Credential-Verwaltung, kein Audit.',
            'Kein Task-/Budget-/Governance-Management.',
            'Zustand wird über Screen-Detection erraten.'
          ]},
          { tone: 'goes', head: '✓ Was paperclip kann', items: [
            'Org-Chart, Rollen, Berichtslinien, Delegation.',
            'Tasks mit Heartbeats, Approvals, Budgets & Kostenkontrolle.',
            'Routines/Schedules, Multi-Org, Plugins, Mobile.'
          ]},
          { tone: 'noes', head: '✗ Was paperclip nicht kann', items: [
            'Lokale Adapter laufen unsandboxed (keine Container-Isolation).',
            'Kein secretless Broker — Secrets werden in Runs injiziert.',
            'Kein Terminal-Multiplexing; Herzschlag-Modell statt Dauerlauf.'
          ]}
        ]},
        { t: 'threeCols', title: 'Wo DockWeave nichts bringt — und wo doch', sub: 'Drei disjunkte Zonen: paperclip-only, Overlap und DockWeave-only.', cols: [
          { tone: 'pp-only', head: 'Nur paperclip — hier bringt DockWeave nichts', items: [
            'Menschliche Identität: SSO, RBAC, Rollen, Invites, responsible-user.',
            'Freigabe-Workflows: Approvals, Review-Stufen, Human-in-the-loop.',
            'Org-weite Budgets & Kosten-Dashboards (Schwellwerte, Hard-Stops).',
            'Tool-/Connection-Gateway: „Allowed / Ask first / Off“ pro Connector.',
            'Multi-Tenant-Org-Isolation (eine Instanz, mehrere Firmen).',
            'Business-Audit: wer hat was genehmigt, Entscheidungs-Tracking.',
            'Config-Revisionierung & Rollback.'
          ]},
          { tone: 'overlap', head: 'Overlap — beide, aber anders', items: [
            '<b>Secrets:</b> paperclip verschlüsselt & injiziert; DockWeave ist secretless (Key bleibt draußen).',
            '<b>Budgets:</b> paperclip org-weit; DockWeave nur Limits pro Broker-Grant.',
            '<b>Audit:</b> paperclip Business-Ebene; DockWeave technische Hash-Kette.',
            '<b>Sandbox:</b> paperclip via externe Provider; DockWeave lokal per Container.'
          ]},
          { tone: 'dw-only', head: 'Nur DockWeave — paperclips Lücke', items: [
            'Container-Sandbox für lokale Agenten (paperclip lokal unsandboxed).',
            'Netz-Egress: offline / broker-only, fail closed.',
            'Secretless Credentials: echter Key kommt nie in den Agenten.',
            'Mount-Sicherheit: Allowlist, TOCTOU, RO-Staging.',
            'SO_PEERCRED-Zugriff auf dem lokalen Daemon.'
          ]}
        ]},
        { t: 'note', text: '<strong>In Kombination:</strong> Die drei stapeln sich sauber — <strong>paperclip</strong> entscheidet oben, <em>wer was wann mit welchem Budget</em> macht (Tasks, Approvals, Kosten). <strong>herdr</strong> hält darunter die <em>Terminals/Sessions</em> am Leben (detach, Multi-Machine). <strong>DockWeave</strong> ganz unten erzwingt die <em>Grenzen</em> (Sandbox, secretless Credentials, Egress, Audit). paperclip dokumentiert selbst, dass lokale Adapter <strong>unsandboxed</strong> laufen — genau diese Lücke schließt DockWeave; und das Dauerlauf-Fenster, das paperclip nicht bietet, deckt herdr ab.' }
      ]
    },
    {
      id: 'm0', kicker: 'Modul 0', title: 'Denken: Agent ≠ Modell',
      sub: 'Das mentale Modell, bevor du baust', pages: [
        { t: 'code', title: 'Das mentale Modell', lang: 'text', file: 'Schichten eines Agenten-Systems', code:
`Nutzer / Operator
      │
      ▼
Steuerungsebene (CLI, API, UI)
      │  Identität, Policy, Audit
      ▼
Orchestrierung (Agenten, Ketten, Gates)
      │
      ├── Sandbox      (Container, Dateien, Netzwerk)
      ├── Harness      (Prompt, Tools, Loop)
      ├── Modell       (LLM-Provider)
      ├── Gedächtnis   (Memory, Knowledge, Skills)
      └── Credentials  (Vault, Broker, Sessions)` },
        { t: 'bullets', title: 'Merksätze', points: [
          'Das <b>Modell</b> erzeugt Text.',
          'Der <b>Harness</b> macht daraus Aktionen (Tools, Shell, Dateien).',
          'Die <b>Sandbox</b> begrenzt, was diese Aktionen erreichen können.',
          'Die <b>Steuerungsebene</b> entscheidet, wer was darf.',
          '<b>Credentials</b> gehören nicht in den Agenten.'
        ]},
        { t: 'code', title: 'Identität im echten Code', lang: 'go', file: 'internal/model/model.go', url: GH_DW + 'internal/model/model.go', code:
`type Agent struct {
    ID   string \`json:"id"\`   // ag_…, stabil über Container-Lebenszyklen
    Name string \`json:"name"\`
    Type string \`json:"type"\` // codex | opencode | claude | gemini | custom
    Image string \`json:"image"\`
    State AgentState \`json:"state"\`
    ContainerID string \`json:"container_id,omitempty"\` // wechselt — Identität nicht
}` },
        { t: 'quiz', q: 'Was ist ein Harness?', options: ['Das LLM selbst', 'Die Schleife und Werkzeuge um das Modell', 'Ein Docker-Container', 'Ein API-Key'], answer: 1, ok: 'Richtig. Der Harness steuert das Modell und führt Tools aus.', bad: 'Nicht ganz: Das Modell erzeugt Text; der Harness macht daraus Aktionen.' }
      ]
    },
    {
      id: 'm1', kicker: 'Modul 1', title: 'Der Agent-Loop',
      sub: 'planen → handeln → beobachten → wiederholen', pages: [
        { t: 'sim' },
        { t: 'code', title: 'Die Schleife', lang: 'go', file: 'Harness-Kern (Pseudocode)', code:
`for step := 0; step < maxSteps; step++ {
    resp := model.Chat(ctx, tools, history)
    if len(resp.ToolCalls) == 0 {
        return resp.Text
    }
    for _, call := range resp.ToolCalls {
        result := executeTool(ctx, call)
        history = append(history, toolResult(call, result))
    }
}
return errors.New("max steps exceeded")` },
        { t: 'bullets', title: 'Worauf achten', points: [
          '<b>Maximale Schritte</b> und <b>Timeouts</b> setzen.',
          'Tool-Aufrufe validieren, nicht blind ausführen.',
          'Ergebnisse begrenzen (keine riesigen Ausgaben in den Kontext).',
          'Abbruch durch den Nutzer ermöglichen.',
          'Jede Tool-Ausführung ist eine potenzielle Sicherheitsgrenze.'
        ]},
        { t: 'code', title: 'Ein Tool braucht einen Contract', lang: 'json', file: 'read_file.json', code:
`{
  "name": "read_file",
  "description": "Read a file",
  "inputSchema": {
    "type": "object",
    "properties": { "path": { "type": "string" } },
    "required": ["path"]
  }
}` },
        { t: 'note', tone: 'blue', text: '<strong>DockWeave bringt keinen eigenen Harness mit.</strong> OpenCode, Codex, Claude Code usw. sind die Harnesses. DockWeave liefert die Umgebung, Identität und Grenzen.' },
        { t: 'quiz', q: 'Warum braucht der Loop ein Max-Steps-Limit?', options: ['Damit das Modell schneller antwortet', 'Damit ein Agent nicht endlos Tools aufruft', 'Damit Docker schneller startet', 'Damit der API-Key gültig bleibt'], answer: 1, ok: 'Genau. Ohne Limit kann eine Schleife dauerhaft weiterlaufen.', bad: 'Es geht um Begrenzung: Endlosschleifen und unkontrollierte Tool-Aufrufe verhindern.' }
      ]
    },
    {
      id: 'm2', kicker: 'Modul 2', title: 'Sandbox',
      sub: 'Container, Rechte, Dateien, Ressourcen', pages: [
        { t: 'code', title: 'Minimaler Sandbox-Start', lang: 'bash', file: 'docker run', code:
`docker run --rm -it \
  --user 1000:1000 \
  --cap-drop ALL \
  --security-opt no-new-privileges \
  --network none \
  --memory 2g --cpus 1 \
  -v /srv/project:/workspace/project:ro \
  -v /var/lib/agents/<id>/memory:/memory \
  codex-image` },
        { t: 'bullets', title: 'Wichtige Regeln', points: [
          'Kein <code>--privileged</code>',
          'Kein <code>/var/run/docker.sock</code> im Agenten',
          'Kein <code>CAP_SYS_ADMIN</code>',
          'Non-root als Default',
          'Ressourcenlimits setzen',
          'Netzwerk explizit wählen (<code>none</code>, internes Netz, bridge)'
        ]},
        { t: 'bullets', title: 'Was ein Container NICHT automatisch isoliert', points: [
          'das <b>Netzwerk</b> (Bridge erlaubt oft Internet),',
          '<b>Secrets</b> in Umgebungsvariablen,',
          'andere <b>Container</b>,',
          'den <b>Host</b> über offene Ports.'
        ]},
        { t: 'note', text: 'Deshalb braucht es zusätzlich <strong>Policy und Egress-Kontrolle</strong> — ein Container allein ist keine vollständige Netzgrenze.' },
        { t: 'code', title: 'DockWeave im echten Code', lang: 'go', file: 'internal/dockerclient/dockerclient.go', url: GH_DW + 'internal/dockerclient/dockerclient.go', code:
`host := &container.HostConfig{
    Mounts: []mount.Mount{
        {Type: mount.TypeBind, Source: spec.AgentDir, Target: "/agent", ReadOnly: a.AgentRO},
        {Type: mount.TypeBind, Source: spec.MemoryDir, Target: "/memory"},
        {Type: mount.TypeBind, Source: spec.WorkspaceDir, Target: "/workspace",
         BindOptions: &mount.BindOptions{Propagation: mount.PropagationRSlave}},
    },
    CapDrop:     []string{"ALL"},
    Privileged:  false,
    SecurityOpt: []string{"no-new-privileges"},
    NetworkMode: container.NetworkMode(a.NetworkMode),
    User:        fmt.Sprintf("%d:%d", a.UID, a.GID),
}` },
        { t: 'quiz', q: 'Was gehört nicht in einen Agentencontainer?', options: ['Ein Projektverzeichnis', 'Der Docker-Socket', 'Ein Memory-Verzeichnis', 'Ein non-root User'], answer: 1, ok: 'Richtig. Der Docker-Socket würde die Sandbox aushebeln.', bad: 'Der Docker-Socket ist tabu: Damit könnte der Agent den Host kontrollieren.' }
      ]
    },
    {
      id: 'm3', kicker: 'Modul 3', title: 'Steuerungsebene',
      sub: 'Daemon, CLI, Unix-Socket, SO_PEERCRED', pages: [
        { t: 'code', title: 'Architektur', lang: 'text', file: 'Steuerungsebene', code:
`CLI/TUI/Web ──(Unix-Socket, SO_PEERCRED)──> dockweaved (root)
                                              │
                                              ├── Docker API
                                              ├── SQLite
                                              └── Mount-Manager` },
        { t: 'bullets', title: 'Warum', points: [
          'Privilegierte Operationen bleiben an <b>einer</b> Stelle.',
          'Die CLI ist ein <b>dünner Client</b>.',
          'Der Socket ist lokal und per <b>Peer-Credentials</b> geschützt.',
          'Rollen (<code>admin</code>, <code>monitor</code>) lassen sich zentral prüfen.'
        ]},
        { t: 'code', title: 'Minimaler API-Ausschnitt', lang: 'text', file: 'REST v1', code:
`GET    /api/v1/agents
POST   /api/v1/agents
POST   /api/v1/agents/{id}/start
POST   /api/v1/agents/{id}/stop
GET    /api/v1/workspaces
POST   /api/v1/grants` },
        { t: 'code', title: 'SO_PEERCRED im echten Code', lang: 'go', file: 'internal/api/auth.go', url: GH_DW + 'internal/api/auth.go', code:
`cred, _ := unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
role := policy.RoleOf(cred.Uid)
if role == "" {
    http.Error(w, "forbidden: peer uid not authorized", 403)
    return
}
if role == RoleMonitor && r.Method != "GET" && r.Method != "HEAD" {
    http.Error(w, "forbidden: monitor role is read-only", 403)
    return
}` },
        { t: 'code', title: 'Der Daemon lehnt Start ohne root ab', lang: 'go', file: 'cmd/dockweaved/main.go', url: GH_DW + 'cmd/dockweaved/main.go', code:
`if os.Geteuid() != 0 {
    return errors.New("dockweaved must run as root (CAP_SYS_ADMIN); refusing")
}
ln, err := net.Listen("unix", cfg.SocketPath)
handler := api.PeerAuthMiddleware(authPolicy, apiSrv)
srv := &http.Server{Handler: handler, ConnContext: api.PeerConnContext}` },
        { t: 'quiz', q: 'Warum läuft Docker nicht direkt in der CLI?', options: ['Weil die CLI sonst langsamer wäre', 'Weil Privilegien zentral und prüfbar bleiben müssen', 'Weil Docker nur root mag', 'Weil die CLI kein JSON kann'], answer: 1, ok: 'Genau. Eine zentrale Steuerungsebene macht Rechte und Audit prüfbar.', bad: 'Es geht um Privilegientrennung: nur der Daemon darf privilegiert handeln.' }
      ]
    },
    {
      id: 'm4', kicker: 'Modul 4', title: 'Identität & Persistenz',
      sub: 'stabile Agenten, SQLite, Migrationen', pages: [
        { t: 'code', title: 'Minimales Schema', lang: 'sql', file: 'agents', code:
`CREATE TABLE agents (
  id TEXT PRIMARY KEY,
  name TEXT UNIQUE NOT NULL,
  type TEXT NOT NULL,
  image TEXT NOT NULL,
  command TEXT,
  state TEXT NOT NULL
);` },
        { t: 'bullets', title: 'Weitere Tabellen', points: [
          '<code>workspaces</code> — erlaubte Host-Verzeichnisse',
          '<code>grants</code> — Agent × Workspace × Modus',
          '<code>events</code> — Audit',
          '<code>agent_mcp</code> / <code>agent_skills</code> — Werkzeuge pro Agent',
          '<code>agent_type_mcp</code> / <code>agent_type_skills</code> — Vorlagen pro Agententyp'
        ]},
        { t: 'bullets', title: 'Wichtig', points: [
          'Stabile IDs (<code>ag_…</code>), keine Container-IDs als Identität.',
          'Migrationen versionieren.',
          'Keine Secrets in normalen Metadaten speichern.'
        ]},
        { t: 'code', title: 'Stabile IDs im echten Code', lang: 'go', file: 'internal/model/model.go', url: GH_DW + 'internal/model/model.go', code:
`func NewID(prefix string) string {
    var b [12]byte
    rand.Read(b[:])
    return prefix + "_" + hex.EncodeToString(b[:])
}

const (
    AgentCreated AgentState = "created"
    AgentRunning AgentState = "running"
    AgentStopped AgentState = "stopped"
)` },
        { t: 'quiz', q: 'Was sollte die Agentenidentität überleben?', options: ['Nur den Container-Namen', 'Container-Neuerstellung und Neustarts', 'Nur den aktuellen Prozess', 'Den API-Key'], answer: 1, ok: 'Richtig. Identität ist unabhängig von Container-IDs.', bad: 'Die Identität muss eine Container-Neuerstellung überleben.' }
      ]
    },
    {
      id: 'm5', kicker: 'Modul 5', title: 'Workspaces & Mounts',
      sub: 'Grants, Allowlist, TOCTOU, RO-Staging', pages: [
        { t: 'code', title: 'Ablauf', lang: 'text', file: 'Grant → Mount', code:
`Workspace registrieren
  → Grant erstellen (Agent, Workspace, ro/rw)
    → Mount anwenden
      → Container sieht /workspace/<name>` },
        { t: 'note', tone: 'blue', text: 'Ein <strong>Grant ist eine Erlaubnis, kein Mount</strong>. Erst der Mount macht sie wirksam.' },
        { t: 'bullets', title: 'Worauf achten', points: [
          '<b>Allowlist</b>: Nur registrierte Host-Pfade sind mountbar.',
          '<b>Kanonisieren</b>: <code>filepath.EvalSymlinks</code>, danach erneut prüfen.',
          '<b>TOCTOU</b>: Quelle mit <code>openat2(RESOLVE_BENEATH|NO_MAGICLINKS)</code> öffnen und per <code>/proc/self/fd/&lt;n&gt;</code> mounten.',
          '<b>Read-only</b>: privat binden → <code>remount,ro</code> → in den Shared-Baum moven.',
          'Kein <code>umount -l</code> als Default: Bei <code>EBUSY</code> strukturiert fehlschlagen.'
        ]},
        { t: 'code', title: 'RO-Staging im echten Code', lang: 'go', file: 'internal/mounts/manager.go', url: GH_DW + 'internal/mounts/manager.go', code:
`// RO-Staging: privat binden → remount ro → in den Shared-Baum moven.
// Ein direktes -o ro propagiert nicht zuverlässig in Slave-Namespaces.
unix.Mount(src, staging, "none", unix.MS_BIND|unix.MS_REC, "")
unix.Mount("none", staging, "", unix.MS_REMOUNT|unix.MS_BIND|unix.MS_RDONLY|unix.MS_REC, "")
unix.Mount(staging, target, "", unix.MS_MOVE, "")` },
        { t: 'code', title: 'TOCTOU-Schutz im echten Code', lang: 'go', file: 'internal/mounts/pathutil.go', url: GH_DW + 'internal/mounts/pathutil.go', code:
`how := &unix.OpenHow{
    Flags:   unix.O_RDONLY | unix.O_DIRECTORY | unix.O_CLOEXEC,
    Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_MAGICLINKS,
}
fd, err := unix.Openat2(pfd, base, how)` },
        { t: 'quiz', q: 'Warum reicht ein direktes <code>mount -o ro</code> nicht?', options: ['Weil Docker das ignoriert', 'Weil Remount-Flags nicht zuverlässig in Slave-Namespaces propagieren', 'Weil es zu langsam ist', 'Weil es nur für Dateien geht'], answer: 1, ok: 'Genau. Deshalb wird privat gestaged und dann verschoben.', bad: 'Die RO-Flags propagieren nicht zuverlässig — deshalb das Staging.' }
      ]
    },
    {
      id: 'm6', kicker: 'Modul 6', title: 'Secrets & Broker',
      sub: 'secretless agents, Vault, Sessions', pages: [
        { t: 'bullets', title: 'Ein API-Key in einer Env-Variable ist sichtbar für', points: [
          'den Agenten und seine Tools,',
          '<code>docker inspect</code>,',
          '<code>/proc/&lt;pid&gt;/environ</code>,',
          'versehentliche Logs,',
          'Prompt Injection.'
        ]},
        { t: 'code', title: 'Besser: Secretless Agent', lang: 'text', file: 'Datenfluss', code:
`Agent ──(Session-Token)──> Broker ──(echter Key)──> Provider` },
        { t: 'bullets', title: 'Vault-Anforderungen', points: [
          'Argon2id für die Passphrase',
          'AES-256-GCM mit zufälliger Nonce',
          'AAD pro Eintrag, damit Ciphertexte nicht getauscht werden können',
          'atomare Writes, <code>0600</code>, Verzeichnis <code>0700</code>',
          'kein Klartext in SQLite, Logs, Docker-Metadaten'
        ]},
        { t: 'bullets', title: 'Broker-Anforderungen', points: [
          'feste Provider-Ziele (keine freie Zielwahl)',
          'Redirects nicht verfolgen',
          'Deny by default',
          'Session-Tokens nur als Hash speichern',
          'Ablauf und sofortiger Widerruf',
          'Rate-/Kostenlimits pro Grant'
        ]},
        { t: 'code', title: 'Argon2id + AES-256-GCM im echten Code', lang: 'go', file: 'internal/credentialvault/crypto.go', url: GH_DW + 'internal/credentialvault/crypto.go', code:
`func deriveKey(passphrase, salt []byte, p KDFParams) []byte {
    return argon2.IDKey(passphrase, salt, p.Time, p.Memory, p.Threads, p.KeyLen)
}
func seal(plaintext, key, aad []byte) (nonce, ciphertext []byte, err error) {
    block, _ := aes.NewCipher(key)
    gcm, _ := cipher.NewGCM(block)
    nonce, _ = randomBytes(gcm.NonceSize())
    ciphertext = gcm.Seal(nil, nonce, plaintext, aad)
    return nonce, ciphertext, nil
}` },
        { t: 'code', title: 'Nur der Hash wird gespeichert', lang: 'go', file: 'internal/credentialbroker/util.go', url: GH_DW + 'internal/credentialbroker/util.go', code:
`// Nur der SHA-256-Hash eines Session-Tokens wird gespeichert.
func hashToken(token string) string {
    h := sha256.Sum256([]byte(token))
    return hex.EncodeToString(h[:])
}` },
        { t: 'quiz', q: 'Was bekommt der Agent vom Broker?', options: ['Den echten Provider-Key', 'Ein kurzlebiges Session-Token', 'Den Vault-Master-Key', 'Gar nichts'], answer: 1, ok: 'Richtig. Der echte Key bleibt im Broker.', bad: 'Der Agent bekommt nur ein eingeschränktes Session-Token, nie den echten Key.' }
      ]
    },
    {
      id: 'm7', kicker: 'Modul 7', title: 'Egress',
      sub: 'Proxy ist keine Netzgrenze', pages: [
        { t: 'code', title: 'Network-Profile', lang: 'go', file: 'internal/model/model.go', url: GH_DW + 'internal/model/model.go', code:
`type NetworkProfile string

const (
    NetProfileOffline    NetworkProfile = "offline"
    NetProfileBrokerOnly NetworkProfile = "broker-only"
    NetProfileRestricted NetworkProfile = "restricted-development"
    NetProfileLegacy     NetworkProfile = "unrestricted-legacy"
)` },
        { t: 'bullets', title: 'Wichtig', points: [
          '<code>offline</code>: <code>--network none</code>',
          '<code>broker-only</code>: internes Docker-Netz, keine externe Route, Broker am Host-Gateway',
          '<code>restricted-development</code>: nicht implementiert → <b>fail closed</b>',
          'Netzwerkregeln müssen <b>außerhalb</b> des Agenten liegen.',
          'Der Host-Gateway bleibt eine Restgefahr; Portfilterung per nftables ist ein eigener Schritt.'
        ]},
        { t: 'quiz', q: 'Warum ist ein HTTP-Proxy allein keine Egress-Sicherheit?', options: ['Weil er zu langsam ist', 'Weil der Agent ihn umgehen kann, wenn er direkten Netzwerkzugriff hat', 'Weil er keine HTTPS kann', 'Weil er nur für Browser ist'], answer: 1, ok: 'Genau. Ohne erzwungene Netzgrenze ist der Proxy optional.', bad: 'Der Agent kann einen Proxy umgehen, wenn er direkt routen darf.' }
      ]
    },
    {
      id: 'm8', kicker: 'Modul 8', title: 'Audit & Monitoring',
      sub: 'Hash-Kette, Events, Konflikte', pages: [
        { t: 'code', title: 'Hash-Kette', lang: 'go', file: 'internal/model/model.go', url: GH_DW + 'internal/model/model.go', code:
`// Events bilden eine Hash-Kette (PrevHash/Hash), damit Manipulation
// an der gespeicherten Historie erkennbar wird.
type Event struct {
    Type     EventType \`json:"type"\`
    Actor    string    \`json:"actor"\`
    Message  string    \`json:"message"\`
    PrevHash string    \`json:"prev_hash"\`
    Hash     string    \`json:"hash"\`
}` },
        { t: 'bullets', title: 'Zusätzlich', points: [
          'Docker-Events und Prozessdaten (procfs) beobachten.',
          'Schreibkonflikte zwischen Agenten erkennen.',
          'Keine Secrets in Events.'
        ]},
        { t: 'quiz', q: 'Was macht eine Audit-Hash-Kette?', options: ['Sie verschlüsselt Events', 'Sie macht Manipulation an der Historie erkennbar', 'Sie löscht alte Events', 'Sie ersetzt Backups'], answer: 1, ok: 'Richtig. Jedes Event bindet an das vorherige.', bad: 'Die Kette dient der Manipulationserkennung, nicht der Verschlüsselung.' }
      ]
    },
    {
      id: 'm9', kicker: 'Modul 9', title: 'UI: CLI, TUI, Web',
      sub: 'Bedienung ohne Sicherheitsverlust', pages: [
        { t: 'numbered', title: 'Baue in dieser Reihenfolge', items: [
          '<b>CLI</b> — schnell testbar, skriptbar, ehrlich.',
          '<b>TUI</b> — interaktive Bedienung im Terminal.',
          '<b>Web</b> — Agentenliste, Chat/Terminal, Konfiguration, Analyse.'
        ]},
        { t: 'code', title: 'Ein Browser braucht ein Gateway', lang: 'text', file: 'Web → Daemon', code:
`Browser ──HTTP/WebSocket──> Gateway ──Unix-Socket──> Daemon` },
        { t: 'bullets', title: 'Wichtig', points: [
          'Token/Cookie für den Browser-Zugriff.',
          'Kein privilegiertes Verhalten im Gateway.',
          'Terminal über ein echtes PTY (z. B. xterm.js + WebSocket).',
          'Keine Secrets in der UI anzeigen.'
        ]},
        { t: 'quiz', q: 'Warum braucht eine Web-UI ein Gateway?', options: ['Weil Browser keinen Unix-Domain-Socket sprechen', 'Weil HTTP kein JSON kann', 'Weil Docker einen Browser braucht', 'Weil Tokens nur im Browser funktionieren'], answer: 0, ok: 'Genau. Das Gateway verbindet Browser HTTP/WS mit dem Unix-Socket.', bad: 'Browser können keine Unix-Sockets öffnen — daher das Gateway.' }
      ]
    },
    {
      id: 'm10', kicker: 'Modul 10', title: 'MCP & Skills',
      sub: 'Werkzeuge pro Agent konfigurierbar', pages: [
        { t: 'bullets', title: 'MCP-Punkte', points: [
          '<code>initialize</code> beschreibt Protokollversion und Capabilities.',
          '<code>tools/list</code> liefert Tools mit <code>inputSchema</code> (JSON Schema).',
          'Ein MCP-Server ist <b>kein</b> einzelnes Standard-JSON-Dokument.'
        ]},
        { t: 'bullets', title: 'Für ein Agenten-System', points: [
          'Skills/MCP pro Agententyp als Vorlage speichern.',
          'Beim Anlegen eines Agenten in eigene, editierbare Zeilen kopieren.',
          'Vor dem Start ins Memory-Home materialisieren.',
          'Env-Werte nicht speichern, nur Namen; Werte in den Vault.'
        ]},
        { t: 'code', title: 'AgentMCP im echten Code', lang: 'go', file: 'internal/model/model.go', url: GH_DW + 'internal/model/model.go', code:
`type AgentMCP struct {
    ID        string       \`json:"id"\`
    AgentID   string       \`json:"agent_id"\`
    Transport MCPTransport \`json:"transport"\` // stdio | http | sse
    Command   string       \`json:"command,omitempty"\`
    EnvKeys   []string     \`json:"env_keys,omitempty"\` // nur Namen, Werte im Vault
}` },
        { t: 'quiz', q: 'Beschreibt MCP einen ganzen Server als ein Standard-JSON?', options: ['Ja, genau ein Dokument', 'Nein: JSON-RPC-Methoden plus Schemas pro Tool', 'Nur für Claude', 'Nur für OpenAI'], answer: 1, ok: 'Richtig. Ein normalisierter Server-Contract ist eine Ergänzung, kein MCP-Standard.', bad: 'MCP definiert Methoden und Tool-Schemas, kein einzelnes Gesamt-JSON.' }
      ]
    },
    {
      id: 'm11', kicker: 'Modul 11', title: 'Tests & Threat Modeling',
      sub: 'Beweise statt Behauptungen', pages: [
        { t: 'bullets', title: 'Mindestens', points: [
          'Unit-Tests für Policy, Vault, Pfade.',
          'Race Detector.',
          'Integrationstests mit echten Mounts nur in isolierter Umgebung.',
          'Fake-Provider statt echter API-Keys.',
          'Angriffstests: Secret-Exfiltration, Pfad-Traversal, Redirect, Budget, Session.'
        ]},
        { t: 'numbered', title: 'Threat-Model-Fragen', items: [
          'Wer ist vertrauenswürdig? (Operator, Daemon, Broker, Host)',
          'Was ist unvertrauenswürdig? (Agent, Prompt-Injection, Netzwerk)',
          'Welche Grenze schützt welches Gut?',
          'Was passiert bei Fehlern? (fail closed?)',
          'Welche Restrisiken bleiben?'
        ]},
        { t: 'quiz', q: 'Wo laufen Integrationstests mit echten Mounts?', options: ['Direkt auf dem Entwickler-Host', 'Nur in einer isolierten, privilegierten Umgebung', 'In der CI ohne Docker', 'Nirgends, sie werden übersprungen'], answer: 1, ok: 'Richtig. Privilegierte Mount-Tests laufen nie direkt auf dem Host.', bad: 'Echte Mount-Tests brauchen CAP_SYS_ADMIN — nur isoliert, sonst t.Skip.' }
      ]
    },
    {
      id: 'm12', kicker: 'Modul 12', title: 'Bau-Reihenfolge',
      sub: 'Milestones mit Abnahmekriterium', pages: [
        { t: 'table', title: 'Milestones', head: ['Phase', 'Ziel', 'Abnahmekriterium'], rows: [
          ['M0', 'Konzepte', 'Du kannst Harness, Agent, Sandbox, Grant erklären'],
          ['M1', 'Sandbox', 'Container startet non-root, ohne Caps, ohne Docker-Socket'],
          ['M2', 'Steuerung', 'CLI → Daemon → Docker funktioniert'],
          ['M3', 'Identität', 'Agent überlebt Container-Neuerstellung'],
          ['M4', 'Workspaces', 'Grant ro/rw funktioniert, Allowlist greift'],
          ['M5', 'Mounts sicher', 'RO-Staging, TOCTOU-Schutz, kein Lazy-Unmount-Default'],
          ['M6', 'Secrets', 'Vault + Broker, Agent sieht nur Session-Token'],
          ['M7', 'Egress', '<code>offline</code>/<code>broker-only</code> erzwungen, Fail closed'],
          ['M8', 'Audit', 'Hash-Kette, keine Secrets in Events'],
          ['M9', 'UI', 'CLI/TUI/Web mit klarer Rechteanzeige'],
          ['M10', 'MCP/Skills', 'pro Agent konfigurierbar und materialisiert']
        ]},
        { t: 'note', text: 'Baue <strong>eine Phase nach der anderen</strong>. Ein halbfertiger Broker ohne Egress-Schutz ist keine Sicherheitslösung.' }
      ]
    },
    {
      id: 'm13', kicker: 'Modul 13', title: 'Häufige Fehler',
      sub: 'Die zehn Klassiker', pages: [
        { t: 'numbered', title: 'Die 10 häufigsten Fehler', items: [
          'API-Keys in Env-Variablen.',
          'Docker-Socket im Agenten.',
          '<code>--privileged</code>, weil es „einfacher“ ist.',
          'Mounts ohne Allowlist.',
          'Symlinks nicht auflösen.',
          '<code>umount -l</code> als Standard.',
          'Proxy als „Netzwerksicherheit“ verkaufen.',
          'Session-Tokens im Klartext speichern.',
          'Budgets ohne Reservierung bei parallelen Requests.',
          'Sicherheit behaupten, obwohl sie nicht erzwungen ist.'
        ]}
      ]
    },
    {
      id: 'm14', kicker: 'Modul 14', title: 'Sicherheits-Checkliste',
      sub: 'Was wirklich zählt', pages: [
        { t: 'checklist', title: 'Checkliste', items: [
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
        ]}
      ]
    },
    {
      id: 'm15', kicker: 'Modul 15', title: 'Referenz-Map',
      sub: 'Thema → DockWeave-Datei', pages: [
        { t: 'table', title: 'Thema → Datei', head: ['Thema', 'DockWeave-Datei'], rows: [
          ['Agentenmodell', '<code>internal/model/model.go</code>'],
          ['Persistenz/Migrationen', '<code>internal/persistence</code>'],
          ['Docker-Sandbox', '<code>internal/dockerclient</code>'],
          ['Agenten-Lifecycle', '<code>internal/agents</code>'],
          ['Workspace-Grants', '<code>internal/workspaces</code>'],
          ['Mount-Sicherheit', '<code>internal/mounts</code>'],
          ['Vault', '<code>internal/credentialvault</code>'],
          ['Broker', '<code>internal/credentialbroker</code>'],
          ['Egress', '<code>internal/agents</code>, <code>tests/integration/egress_network_test.go</code>'],
          ['UI-Gateway', '<code>internal/webui</code>'],
          ['Angular-UI', '<code>ui/web</code>'],
          ['TUI', '<code>ui/tui</code>'],
          ['Lernleitfaden', '<code>docs/learning/agent-harness.de.md</code>']
        ]}
      ]
    },
    {
      id: 'm16', kicker: 'Modul 16', title: 'Fazit',
      sub: 'Vertrauensgrenzen bewusst ziehen', pages: [
        { t: 'bullets', title: 'Die fünf Fragen', points: [
          'Was darf der Agent?',
          'Was darf er niemals sehen?',
          'Wer entscheidet?',
          'Was passiert bei Fehlern?',
          'Was ist tatsächlich erzwungen — und was nur dokumentiert?'
        ]},
        { t: 'note', text: 'Ein sicheres Agenten-System besteht nicht daraus, einen Container zu starten. Es besteht daraus, <strong>Vertrauensgrenzen bewusst zu ziehen</strong>. Der Rest ist Implementierung.' }
      ]
    }
  ];

  /* ================= engine ================= */
  const stage = document.getElementById('stage');
  const chapterLabel = document.getElementById('chapter-label');
  const pageCounter = document.getElementById('page-counter');
  const dots = document.getElementById('dots');
  const prevBtn = document.getElementById('prev-btn');
  const nextBtn = document.getElementById('next-btn');
  const progressFill = document.getElementById('progress-fill');
  const tocOverlay = document.getElementById('toc-overlay');
  const tocEl = document.getElementById('toc');
  const tocBtn = document.getElementById('toc-btn');
  const tocClose = document.getElementById('toc-close');

  let ci = 0, pi = 0, lastDir = 'next';

  function flat() {
    // global index across all pages for progress
    let total = 0;
    for (const c of chapters) total += c.pages.length;
    return total;
  }
  function globalIndex() {
    let n = 0;
    for (let i = 0; i < ci; i++) n += chapters[i].pages.length;
    return n + pi;
  }

  function persist() { try { localStorage.setItem(POS_KEY, JSON.stringify({ ci, pi })); } catch {} }
  function loadPos() {
    try { const s = JSON.parse(localStorage.getItem(POS_KEY) || '{}'); if (Number.isInteger(s.ci) && Number.isInteger(s.pi)) return s; } catch {}
    return null;
  }

  function goTo(nci, npi, dir) {
    if (nci < 0) nci = 0;
    if (nci >= chapters.length) { nci = chapters.length - 1; npi = chapters[nci].pages.length - 1; }
    const ch = chapters[nci];
    if (npi < 0) npi = 0;
    if (npi >= ch.pages.length) npi = ch.pages.length - 1;
    ci = nci; pi = npi;
    lastDir = dir || 'next';
    persist();
    render();
  }

  function next() {
    const ch = chapters[ci];
    if (pi < ch.pages.length - 1) goTo(ci, pi + 1, 'next');
    else if (ci < chapters.length - 1) goTo(ci + 1, 0, 'next');
  }
  function prev() {
    if (pi > 0) goTo(ci, pi - 1, 'prev');
    else if (ci > 0) goTo(ci - 1, chapters[ci - 1].pages.length - 1, 'prev');
  }

  function render() {
    const ch = chapters[ci];
    const page = ch.pages[pi];
    const html = `<div class="page anim-${lastDir}"> <div class="page-inner">` + renderPage(ch, page, pi, ch.pages.length) + `</div></div>`;
    stage.innerHTML = html;
    const pageEl = stage.querySelector('.page');
    void stage.offsetWidth;
    pageEl.classList.add('active');
    wirePage(pageEl);

    chapterLabel.textContent = `${ch.kicker} · ${ch.title}`;
    pageCounter.textContent = `${globalIndex() + 1} / ${flat()}`;
    prevBtn.disabled = (ci === 0 && pi === 0);
    const last = (ci === chapters.length - 1 && pi === ch.pages.length - 1);
    nextBtn.textContent = last ? 'Fertig ✓' : 'Weiter →';

    dots.innerHTML = ch.pages.map((_, i) => `<i class="${i === pi ? 'on' : ''}" data-dot="${i}"></i>`).join('');
    dots.querySelectorAll('[data-dot]').forEach(d => d.addEventListener('click', () => goTo(ci, Number(d.dataset.dot), pi > Number(d.dataset.dot) ? 'prev' : 'next')));

    progressFill.style.width = `${((globalIndex() + 1) / flat()) * 100}%`;
  }

  function wirePage(pageEl) {
    // quiz
    pageEl.querySelectorAll('.quiz[data-quiz]').forEach(quiz => {
      const answer = Number(quiz.dataset.answer);
      const fb = quiz.querySelector('.quiz-feedback');
      const okText = quiz.dataset.ok || '';
      const badText = quiz.dataset.bad || '';
      quiz.querySelectorAll('button[data-opt]').forEach(btn => {
        btn.addEventListener('click', () => {
          quiz.querySelectorAll('button[data-opt]').forEach(b => b.classList.remove('correct', 'wrong'));
          const v = Number(btn.dataset.opt);
          if (v === answer) {
            btn.classList.add('correct'); fb.className = 'quiz-feedback ok'; fb.textContent = '✓ ' + okText;
          } else {
            btn.classList.add('wrong'); fb.className = 'quiz-feedback bad'; fb.textContent = '✗ ' + badText;
          }
        });
      });
    });

    // layers
    const layersEl = pageEl.querySelector('[data-layers]');
    if (layersEl) {
      const layers = [
        ['Operator / UI', 'Identität, Policy, Audit', 'Der Operator entscheidet. Die UI ist nur ein Client; Rechte werden zentral geprüft.'],
        ['Orchestrierung', 'Agenten, Ketten, Gates', 'Startet Agenten, verbindet Ketten und deterministische Gates — ohne selbst privilegiert zu sein.'],
        ['Sandbox', 'Container, Dateien, Netz', 'Begrenzt Prozesse, Dateien und Netzwerk. Ein Container allein ist keine vollständige Netzgrenze.'],
        ['Harness', 'Prompt, Tools, Loop', 'Das Programm um das Modell: Tool-Calls, Kontext, Schleife, Fehlerbehandlung.'],
        ['Modell', 'LLM-Provider', 'Erzeugt Text. Der Modell-Provider sieht nur die Anfrage — nicht deine Secrets-Datei.'],
        ['Memory & Skills', 'Memory, Knowledge, MCP', 'Persistentes Wissen und Werkzeuge. Pro Agent konfigurierbar, vor dem Start materialisiert.'],
        ['Credentials', 'Vault, Broker, Sessions', 'Hält echte Keys außerhalb des Agenten und gibt nur kurzlebige Session-Tokens heraus.']
      ];
      const exp = pageEl.querySelector('[data-layer-exp]');
      layersEl.innerHTML = layers.map(([name, sub], i) => `<button class="layer" data-layer="${i}"><b>${name}</b><span>${sub}</span></button>`).join('');
      layersEl.querySelectorAll('[data-layer]').forEach(btn => {
        btn.addEventListener('click', () => {
          layersEl.querySelectorAll('.layer').forEach(b => b.classList.remove('active'));
          btn.classList.add('active');
          const [name, , text] = layers[Number(btn.dataset.layer)];
          exp.innerHTML = `<h4>${name}</h4><p>${text}</p>`;
        });
      });
    }

    // checklist
    const checkEl = pageEl.querySelector('[data-checklist]');
    if (checkEl) {
      let checks = [];
      try { checks = JSON.parse(localStorage.getItem(CHK_KEY) || '[]'); } catch {}
      checkEl.querySelectorAll('input[data-check]').forEach(input => {
        const i = Number(input.dataset.check);
        input.checked = checks.includes(i);
        input.addEventListener('change', () => {
          let cur = []; try { cur = JSON.parse(localStorage.getItem(CHK_KEY) || '[]'); } catch {}
          if (input.checked) { if (!cur.includes(i)) cur.push(i); }
          else cur = cur.filter(x => x !== i);
          try { localStorage.setItem(CHK_KEY, JSON.stringify(cur)); } catch {}
        });
      });
    }

    // simulator
    const logEl = pageEl.querySelector('[data-sim-log]');
    if (logEl) {
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
      let idx = 0;
      pageEl.querySelector('[data-sim-next]').addEventListener('click', () => {
        if (idx >= sim.length) return;
        const [kind, text] = sim[idx++];
        const line = document.createElement('div');
        line.className = 'sim-line ' + kind;
        line.textContent = text;
        logEl.appendChild(line);
        logEl.scrollTop = logEl.scrollHeight;
      });
      pageEl.querySelector('[data-sim-reset]').addEventListener('click', () => {
        idx = 0;
        logEl.innerHTML = '<div class="sim-line plan">[bereit] Klicke „Nächster Schritt“.</div>';
      });
    }
  }

  /* ================= TOC ================= */
  function buildToc() {
    tocEl.innerHTML = `<h2>Kapitelübersicht</h2>
      <div class="toc-grid">${chapters.map((c, i) => {
        const cur = i === ci ? 'style="border-color:var(--accent)"' : '';
        return `<button class="toc-item" data-toc="${i}" ${cur}><span class="n">${c.kicker}</span><b>${c.title}</b><span>${c.pages.length} Seiten</span></button>`;
      }).join('')}</div>`;
    tocEl.querySelectorAll('[data-toc]').forEach(btn => {
      btn.addEventListener('click', () => { goTo(Number(btn.dataset.toc), 0, 'next'); closeToc(); });
    });
  }
  function openToc() { buildToc(); tocOverlay.classList.add('open'); }
  function closeToc() { tocOverlay.classList.remove('open'); }

  /* ================= events ================= */
  nextBtn.addEventListener('click', () => {
    if (ci === chapters.length - 1 && pi === chapters[ci].pages.length - 1) { goTo(0, 0, 'prev'); return; }
    next();
  });
  prevBtn.addEventListener('click', prev);
  tocBtn.addEventListener('click', openToc);
  tocClose.addEventListener('click', closeToc);
  tocOverlay.addEventListener('click', e => { if (e.target === tocOverlay) closeToc(); });

  document.addEventListener('keydown', e => {
    if (tocOverlay.classList.contains('open')) {
      if (e.key === 'Escape') closeToc();
      return;
    }
    const t = e.target;
    const interactive = t && (t.tagName === 'BUTTON' || t.tagName === 'INPUT' || t.tagName === 'TEXTAREA' || t.tagName === 'A' || t.isContentEditable);
    if (e.key === 'ArrowRight' || e.key === 'PageDown') { e.preventDefault(); next(); }
    else if (e.key === 'ArrowLeft' || e.key === 'PageUp') { e.preventDefault(); prev(); }
    else if (e.key === ' ' && !interactive) { e.preventDefault(); next(); }
    else if (e.key === 'Home') { goTo(0, 0, 'prev'); }
    else if (e.key === 'End') { goTo(chapters.length - 1, chapters[chapters.length - 1].pages.length - 1, 'next'); }
    else if (e.key === 'Escape' || e.key.toLowerCase() === 't') { openToc(); }
  });

  // resume position
  const pos = loadPos();
  goTo(pos ? pos.ci : 0, pos ? pos.pi : 0, 'next');
})();
