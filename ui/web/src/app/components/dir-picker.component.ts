import { Component, Input, OnChanges, SimpleChanges, inject, output, signal } from '@angular/core';
import { ApiService } from '../core/api.service';
import { DirListing } from '../core/models';

@Component({
  selector: 'dw-dir-picker',
  standalone: true,
  template: `
    @if (open) {
      <div class="backdrop" (click)="closed.emit()">
        <div class="picker" (click)="$event.stopPropagation()">
          <header>
            <h2>{{ title }}</h2>
            <button class="ghost small" (click)="closed.emit()">✕</button>
          </header>

          <div class="pathbar">
            <button class="small" (click)="load('')">Roots</button>
            <button class="small" (click)="up()" [disabled]="!listing()?.parent">Up</button>
            <code>{{ listing()?.path || 'roots' }}</code>
            <span class="spacer"></span>
            <button class="primary small" (click)="use()" [disabled]="!listing()?.path">Use this directory</button>
          </div>

          @if (error()) { <div class="error">{{ error() }}</div> }
          @if (loading()) {
            <div class="muted pad">Loading…</div>
          } @else {
            <div class="list">
              @if (!listing()?.path) {
                @for (r of listing()?.roots || []; track r.path) {
                  <button class="dir" (click)="openPath(r.path)">
                    <span class="icon">◈</span><span class="mono">{{ r.path }}</span>
                  </button>
                }
              } @else {
                @for (e of listing()?.entries || []; track e.path) {
                  <button class="dir" (click)="openPath(e.path)">
                    <span class="icon">▸</span><span class="mono">{{ e.name }}/</span>
                  </button>
                }
                @if (!(listing()?.entries || []).length) {
                  <div class="muted pad">No subdirectories.</div>
                }
              }
            </div>
          }
        </div>
      </div>
    }
  `,
  styles: [`
    .backdrop { position:fixed; inset:0; background:#010409cc; display:flex; align-items:center; justify-content:center; z-index:80; }
    .picker { width:min(760px,94vw); max-height:80vh; display:flex; flex-direction:column; background:#161b22; border:1px solid #30363d; border-radius:10px; overflow:hidden; }
    header { display:flex; align-items:center; gap:.5rem; padding:.7rem .9rem; border-bottom:1px solid #30363d; }
    header h2 { margin:0; font-size:.95rem; flex:1; }
    .pathbar { display:flex; align-items:center; gap:.5rem; padding:.55rem .9rem; border-bottom:1px solid #21262d; background:#0f141b; }
    .pathbar code { overflow:hidden; text-overflow:ellipsis; white-space:nowrap; color:#79c0ff; }
    .list { overflow:auto; padding:.4rem; min-height:200px; }
    .dir { display:flex; align-items:center; gap:.5rem; width:100%; text-align:left; background:transparent; border:1px solid transparent; border-radius:6px; padding:.45rem .6rem; color:#c9d1d9; }
    .dir:hover { background:#1f6feb1a; border-color:#1f6feb; }
    .icon { color:#58a6ff; }
    .mono { font-family:ui-monospace, monospace; }
    .muted { color:#8b949e; }
    .pad { padding:1rem; text-align:center; }
    .error { color:#ff7b72; padding:.5rem .9rem; }
    .spacer { flex:1; }
    .small { padding:.2rem .5rem; font-size:.75rem; }
    .primary { background:#238636; border-color:#2ea043; color:#fff; }
    .ghost { background:transparent; border:1px solid #30363d; color:#c9d1d9; }
  `],
})
export class DirPickerComponent implements OnChanges {
  @Input() open = false;
  @Input() startPath = '';
  @Input() title = 'Select directory';
  readonly picked = output<string>();
  readonly closed = output<void>();

  listing = signal<DirListing | null>(null);
  loading = signal(false);
  error = signal('');

  private readonly api = inject(ApiService);

  ngOnChanges(changes: SimpleChanges): void {
    if (changes['open'] && this.open) {
      this.load(this.startPath || '');
    }
  }

  openPath(path: string): void {
    this.load(path);
  }

  up(): void {
    const p = this.listing()?.parent;
    if (p) this.load(p);
  }

  use(): void {
    const p = this.listing()?.path;
    if (p) this.picked.emit(p);
  }

  load(path: string): void {
    this.loading.set(true);
    this.error.set('');
    this.api.listDirs(path).subscribe({
      next: l => { this.listing.set(l); this.loading.set(false); },
      error: e => { this.error.set(e.message ?? String(e)); this.loading.set(false); },
    });
  }
}
