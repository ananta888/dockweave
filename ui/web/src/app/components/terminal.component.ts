import {
  AfterViewInit, Component, ElementRef, Input, OnChanges, OnDestroy,
  SimpleChanges, ViewChild, output, signal,
} from '@angular/core';
import { Terminal } from '@xterm/xterm';
import { FitAddon } from '@xterm/addon-fit';
import { FormsModule } from '@angular/forms';

@Component({
  selector: 'dw-terminal',
  standalone: true,
  imports: [FormsModule],
  template: `
    <div class="term-shell">
      <div class="term-bar">
        <span class="dot" [class.on]="running"></span>
        <strong>{{ agentName }}</strong>
        <span class="muted">{{ running ? 'live terminal / chat' : 'agent not running' }}</span>
        <span class="spacer"></span>
        <button type="button" (click)="reconnect()" [disabled]="!running">Reconnect</button>
        <button type="button" (click)="clear()">Clear</button>
      </div>
      <div class="term-host" #host></div>
      @if (!running) {
        <div class="term-overlay">
          <p>Agent is not running.</p>
          <button type="button" class="primary" (click)="start.emit()">Start agent</button>
        </div>
      }
      <form class="prompt" (ngSubmit)="sendPrompt()">
        <input [(ngModel)]="prompt" name="prompt" placeholder="Prompt an den Agenten senden … (wird in das Agenten-TUI geschrieben)"
               [disabled]="!running" autocomplete="off" />
        <button type="submit" class="primary" [disabled]="!running || !prompt.trim()">Send</button>
      </form>
      @if (status()) { <div class="term-status">{{ status() }}</div> }
    </div>
  `,
  styles: [`
    .term-shell { display:flex; flex-direction:column; height:100%; min-height:0; background:#0b0e14; }
    .term-bar { display:flex; align-items:center; gap:.6rem; padding:.45rem .8rem; background:#161b22; border-bottom:1px solid #30363d; font-size:.82rem; }
    .term-bar .dot { width:.55rem; height:.55rem; border-radius:50%; background:#f85149; }
    .term-bar .dot.on { background:#3fb950; }
    .term-bar .muted { color:#8b949e; }
    .term-bar .spacer { flex:1; }
    .term-host { flex:1; min-height:0; padding:.35rem .5rem; overflow:hidden; }
    .term-overlay { position:absolute; inset:3rem 0 4rem 0; display:flex; flex-direction:column; align-items:center; justify-content:center; gap:1rem; background:rgba(11,14,20,.72); }
    .prompt { display:flex; gap:.5rem; padding:.5rem .8rem; border-top:1px solid #30363d; background:#0f141b; }
    .prompt input { flex:1; }
    .term-status { padding:.25rem .8rem; color:#d29922; font-size:.78rem; background:#161b22; }
  `],
})
export class TerminalComponent implements AfterViewInit, OnChanges, OnDestroy {
  @Input({ required: true }) agentId = '';
  @Input({ required: true }) agentName = '';
  @Input() running = false;
  @ViewChild('host', { static: true }) host!: ElementRef<HTMLDivElement>;

  status = signal('');
  prompt = '';
  readonly start = output<void>();

  private term?: Terminal;
  private fit?: FitAddon;
  private ws?: WebSocket;
  private ro?: ResizeObserver;
  private initialized = false;

  ngAfterViewInit(): void {
    this.initTerm();
    this.initialized = true;
    if (this.running) this.connect();
  }

  ngOnChanges(changes: SimpleChanges): void {
    if (!this.initialized) return;
    if (changes['agentId']) {
      this.term?.clear();
      this.connect();
    } else if (changes['running']) {
      if (this.running) this.connect();
      else this.disconnect();
    }
  }

  ngOnDestroy(): void {
    this.disconnect();
    this.ro?.disconnect();
    this.term?.dispose();
  }

  private initTerm(): void {
    this.term = new Terminal({
      cursorBlink: true,
      fontSize: 13,
      fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Consolas, monospace',
      theme: {
        background: '#0b0e14', foreground: '#c9d1d9', cursor: '#58a6ff',
        selectionBackground: '#264f78', black: '#0b0e14', red: '#f85149',
        green: '#3fb950', yellow: '#d29922', blue: '#58a6ff', magenta: '#bc8cff',
        cyan: '#39c5cf', white: '#c9d1d9',
      },
      scrollback: 10000,
    });
    this.fit = new FitAddon();
    this.term.loadAddon(this.fit);
    this.term.open(this.host.nativeElement);
    this.fit.fit();
    this.term.onData(data => this.send({ type: 'input', data }));
    this.ro = new ResizeObserver(() => this.resize());
    this.ro.observe(this.host.nativeElement);
  }

  private resize(): void {
    if (!this.fit || !this.term) return;
    try { this.fit.fit(); } catch { /* hidden */ }
    this.send({ type: 'resize', rows: this.term.rows, cols: this.term.cols });
  }

  connect(): void {
    if (!this.initialized || !this.agentId || !this.running) return;
    this.disconnect();
    const proto = location.protocol === 'https:' ? 'wss' : 'ws';
    const url = `${proto}://${location.host}/ws/term?agent=${encodeURIComponent(this.agentId)}`;
    const ws = new WebSocket(url);
    ws.binaryType = 'arraybuffer';
    this.ws = ws;
    ws.onopen = () => {
      this.status.set('');
      this.term?.writeln(`\x1b[38;5;244mconnected to ${this.agentName}\x1b[0m`);
      setTimeout(() => this.resize(), 80);
    };
    ws.onmessage = ev => {
      if (typeof ev.data === 'string') {
        this.status.set(ev.data.trim());
        this.term?.writeln(`\x1b[31m${ev.data}\x1b[0m`);
        return;
      }
      this.term?.write(new Uint8Array(ev.data as ArrayBuffer));
    };
    ws.onerror = () => this.status.set('WebSocket error (is `dockweave web` running?)');
    ws.onclose = () => {
      this.status.set(this.running ? 'terminal closed' : 'agent stopped');
    };
  }

  disconnect(): void {
    if (this.ws) {
      this.ws.onclose = null;
      this.ws.close();
      this.ws = undefined;
    }
  }

  reconnect(): void { this.connect(); }

  clear(): void { this.term?.clear(); }

  sendPrompt(): void {
    const text = this.prompt.trim();
    if (!text) return;
    this.send({ type: 'input', data: text + '\r' });
    this.prompt = '';
  }

  private send(msg: Record<string, unknown>): void {
    if (this.ws?.readyState === WebSocket.OPEN) {
      this.ws.send(JSON.stringify(msg));
    }
  }
}
