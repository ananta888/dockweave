import { Component, signal } from '@angular/core';
import { HerdrPanelComponent } from './herdr-panel.component';
import { PaperclipPanelComponent } from './paperclip-panel.component';

type IntegrationTab = 'herdr' | 'paperclip';

@Component({
  selector: 'dw-integrations',
  standalone: true,
  imports: [HerdrPanelComponent, PaperclipPanelComponent],
  template: `
    <div class="integrations">
      <div class="tabs">
        <button [class.active]="tab() === 'herdr'" (click)="tab.set('herdr')">Herdr</button>
        <button [class.active]="tab() === 'paperclip'" (click)="tab.set('paperclip')">Paperclip</button>
      </div>
      <div class="body">
        @if (tab() === 'herdr') { <dw-herdr-panel /> }
        @else { <dw-paperclip-panel /> }
      </div>
    </div>
  `,
  styles: [`
    :host { display:block; height:100%; min-height:0; }
    .integrations { height:100%; display:flex; flex-direction:column; min-height:0; }
    .tabs { display:flex; gap:.35rem; padding:.6rem 1.2rem 0; border-bottom:1px solid #21262d; }
    .tabs button { background:transparent; border:1px solid #30363d; border-bottom:0; color:#8b949e; padding:.4rem .9rem; border-radius:6px 6px 0 0; cursor:pointer; }
    .tabs button.active { background:#161b22; color:#58a6ff; border-color:#58a6ff; }
    .body { flex:1; min-height:0; overflow:hidden; }
  `],
})
export class IntegrationsComponent {
  readonly tab = signal<IntegrationTab>('herdr');
}
