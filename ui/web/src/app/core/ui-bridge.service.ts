import { Injectable, OnDestroy } from '@angular/core';
import { Subject } from 'rxjs';

@Injectable({ providedIn: 'root' })
export class UiBridgeService implements OnDestroy {
  readonly commands = new Subject<Record<string, unknown>>();
  private ws?: WebSocket;
  private state: unknown = { connected: false };
  private retry?: ReturnType<typeof setTimeout>;

  connect(): void {
    if (this.ws && (this.ws.readyState === WebSocket.OPEN || this.ws.readyState === WebSocket.CONNECTING)) return;
    const proto = location.protocol === 'https:' ? 'wss' : 'ws';
    const ws = new WebSocket(`${proto}://${location.host}/ws/ui`);
    this.ws = ws;
    ws.onopen = () => this.sendState(this.state);
    ws.onmessage = ev => {
      try {
        const msg = JSON.parse(ev.data as string) as Record<string, unknown>;
        if (msg['type'] === 'command') this.commands.next(msg);
      } catch { /* ignore */ }
    };
    ws.onclose = () => {
      this.ws = undefined;
      clearTimeout(this.retry);
      this.retry = setTimeout(() => this.connect(), 2000);
    };
    ws.onerror = () => ws.close();
  }

  sendState(state: unknown): void {
    this.state = state;
    if (this.ws?.readyState === WebSocket.OPEN) {
      this.ws.send(JSON.stringify(state));
    }
  }

  ngOnDestroy(): void {
    clearTimeout(this.retry);
    this.ws?.close();
  }
}
