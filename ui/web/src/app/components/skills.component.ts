import { Component, OnInit, inject, signal } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { ApiService } from '../core/api.service';
import { SkillCatalog, SkillCatalogEntry, SkillStore, SkillStoreEntry } from '../core/models';

@Component({
  selector: 'dw-skills',
  standalone: true,
  imports: [FormsModule],
  template: `
    <div class="skills-view">
      <div class="head">
        <div>
          <h2>Skills</h2>
          <p class="hint">Standard-Skill und kuratierte Stores (Claude, Codex, Cross-Agent). Installierte Skills lassen sich mit einem Klick als Default für einen Agententyp setzen; beim Start werden sie in dessen Home materialisiert.</p>
        </div>
        <label class="target">Ziel-Typ
          <select [(ngModel)]="targetType" name="targetType">
            @for (t of types; track t) { <option [value]="t">{{ t }}</option> }
          </select>
        </label>
      </div>
      @if (message()) { <div class="notice">{{ message() }}</div> }
      @if (error()) { <div class="notice error">{{ error() }}</div> }

      <section>
        <h3>Verfügbar / installiert</h3>
        @for (e of catalog().installed; track e.name) {
          <div class="row">
            <div class="grow">
              <span class="mono">{{ e.name }}</span>
              <span class="badge" [class.standard]="e.bundled">{{ e.bundled ? 'Standard' : e.store }}</span>
              <div class="muted">{{ e.description || e.path }}</div>
            </div>
            <span class="spacer"></span>
            <button class="small primary" (click)="assign(e)" [disabled]="busy()">→ {{ targetType }}</button>
            @if (!e.bundled) { <button class="small danger" (click)="remove(e)" [disabled]="busy()">delete</button> }
          </div>
        }
        @if (!catalog().installed.length) { <p class="muted">Noch keine Skills installiert.</p> }
      </section>

      <section>
        <h3>Stores</h3>
        @for (s of catalog().stores; track s.id) {
          <div class="store">
            <div class="store-head">
              <strong>{{ s.name }}</strong>
              <a [href]="s.url" target="_blank" rel="noopener">GitHub ↗</a>
              <span class="muted">{{ s.description }}</span>
            </div>
            @for (e of s.entries; track e.path) {
              <div class="row">
                <span class="mono grow">{{ e.name }}</span>
                <span class="muted grow">{{ e.description }}</span>
                <span class="spacer"></span>
                <button class="small" (click)="install(s, e)" [disabled]="busy()">install</button>
              </div>
            }
          </div>
        }
      </section>
    </div>
  `,
  styles: [`
    :host { display:block; height:100%; min-height:0; }
    .skills-view { padding:1rem 1.2rem; overflow:auto; height:100%; box-sizing:border-box; }
    .head { display:flex; align-items:flex-start; gap:1rem; margin-bottom:.6rem; }
    .head h2 { margin:0 0 .2rem; font-size:1.05rem; }
    .hint { color:#8b949e; font-size:.78rem; margin:.1rem 0 .6rem; max-width:70ch; }
    .target { display:flex; flex-direction:column; gap:.2rem; font-size:.72rem; color:#8b949e; }
    .target select { min-width:9rem; }
    section { background:#161b22; border:1px solid #30363d; border-radius:8px; padding:1rem; margin-bottom:1rem; }
    h3 { margin:0 0 .6rem; font-size:.9rem; color:#58a6ff; text-transform:uppercase; letter-spacing:.05em; }
    .row { display:flex; align-items:center; gap:.5rem; border-bottom:1px solid #21262d; padding:.4rem 0; font-size:.8rem; }
    .grow { flex:1; min-width:0; }
    .mono { font-family:ui-monospace, monospace; }
    .muted { color:#8b949e; font-size:.75rem; }
    .spacer { flex:0 0 .2rem; }
    .badge { display:inline-block; margin-left:.35rem; padding:.05rem .35rem; border-radius:4px; background:#30363d; color:#c9d1d9; font-size:.65rem; text-transform:uppercase; }
    .badge.standard { background:#1f6feb33; color:#58a6ff; border:1px solid #1f6feb; }
    .small { padding:.15rem .45rem; font-size:.72rem; }
    .danger { background:#da3633; border-color:#f85149; color:#fff; }
    .primary { background:#238636; border-color:#2ea043; color:#fff; }
    .store { border-top:1px solid #21262d; padding-top:.6rem; margin-top:.6rem; }
    .store:first-of-type { border-top:0; margin-top:0; padding-top:0; }
    .store-head { display:flex; align-items:baseline; gap:.6rem; flex-wrap:wrap; margin-bottom:.3rem; }
    .store-head a { color:#58a6ff; font-size:.75rem; }
    .notice { padding:.5rem .7rem; background:#11263b; border:1px solid #1f6feb; border-radius:6px; margin-bottom:.6rem; }
    .notice.error { background:#3d1114; border-color:#f85149; }
  `],
})
export class SkillsComponent implements OnInit {
  readonly types = ['opencode', 'claude', 'codex', 'gemini', 'antigravity', 'custom'];
  targetType = 'opencode';
  catalog = signal<SkillCatalog>({ bundled: [], installed: [], stores: [] });
  busy = signal(false);
  message = signal('');
  error = signal('');

  private readonly api = inject(ApiService);

  ngOnInit(): void {
    this.load();
  }

  load(): void {
    this.error.set('');
    this.api.getSkillCatalog().subscribe({
      next: c => this.catalog.set(c ?? { bundled: [], installed: [], stores: [] }),
      error: e => this.error.set(e.message ?? String(e)),
    });
  }

  assign(e: SkillCatalogEntry): void {
    this.busy.set(true);
    this.message.set('');
    this.error.set('');
    this.api.addTypeSkill(this.targetType, {
      name: e.name, description: e.description ?? '', source_path: e.path, enabled: true,
    }).subscribe({
      next: () => {
        this.busy.set(false);
        this.message.set(`${e.name} ist jetzt Default für ${this.targetType}; wird beim nächsten Agentenstart materialisiert.`);
      },
      error: err => { this.busy.set(false); this.error.set(err.message ?? String(err)); },
    });
  }

  install(s: SkillStore, e: SkillStoreEntry): void {
    this.busy.set(true);
    this.message.set('');
    this.error.set('');
    this.api.installCatalogSkill(s.repo, e.path, s.ref).subscribe({
      next: entry => {
        this.busy.set(false);
        this.message.set(`${entry.name} installiert.`);
        this.load();
      },
      error: err => { this.busy.set(false); this.error.set(err.message ?? String(err)); },
    });
  }

  remove(e: SkillCatalogEntry): void {
    if (!confirm(`Skill ${e.name} löschen? Bereits zugewiesene Typen verlieren ihn beim nächsten Start.`)) return;
    this.busy.set(true);
    this.api.deleteCatalogSkill(e.name).subscribe({
      next: () => { this.busy.set(false); this.message.set(`${e.name} entfernt.`); this.load(); },
      error: err => { this.busy.set(false); this.error.set(err.message ?? String(err)); },
    });
  }
}
