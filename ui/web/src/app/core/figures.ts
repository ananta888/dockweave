export interface BuiltinFigure {
  id: string;
  label: string;
  svg: string;
}

const head = (fill: string, extra = '') => `
  <circle cx="40" cy="24" r="16" fill="${fill}"></circle>
  <circle cx="34" cy="22" r="2" fill="#0b0e14"></circle>
  <circle cx="46" cy="22" r="2" fill="#0b0e14"></circle>
  <path d="M34 30 Q40 35 46 30" stroke="#0b0e14" stroke-width="2" fill="none"></path>
  ${extra}`;
const body = (fill: string) => `<path d="M12 106 C12 70 25 51 40 51 C55 51 68 70 68 106 Z" fill="${fill}"></path>`;
const figure = (id: string, label: string, color: string, accessory: string) => ({
  id, label,
  svg: `<svg viewBox="0 0 80 108" xmlns="http://www.w3.org/2000/svg">${body(color)}${head(color, accessory)}</svg>`,
});

export const FIGURES: BuiltinFigure[] = [
  figure('ceo', 'CEO', '#d29922', '<path d="M24 8 L32 16 L40 6 L48 16 L56 8 L54 22 L26 22 Z" fill="#f0c000"/>'),
  figure('cto', 'CTO', '#58a6ff', '<path d="M22 20 H30 V26 H22 Z M50 20 H58 V26 H50 Z M30 23 H50" stroke="#0b0e14" stroke-width="2" fill="none"/>'),
  figure('developer', 'Developer', '#3fb950', '<path d="M28 12 L22 18 L28 24 M52 12 L58 18 L52 24" stroke="#0b0e14" stroke-width="2" fill="none"/>'),
  figure('reviewer', 'Reviewer', '#bc8cff', '<circle cx="52" cy="30" r="8" fill="none" stroke="#0b0e14" stroke-width="2"/><path d="M58 36 L64 42" stroke="#0b0e14" stroke-width="3"/>'),
  figure('qa', 'QA', '#39c5cf', '<path d="M26 16 L30 20 L38 12" stroke="#0b0e14" stroke-width="2" fill="none"/><path d="M44 16 H56 M44 22 H54" stroke="#0b0e14" stroke-width="2"/>'),
  figure('devops', 'DevOps', '#f0883e', '<circle cx="40" cy="16" r="5" fill="none" stroke="#0b0e14" stroke-width="2"/><path d="M40 8 V11 M40 21 V24 M32 16 H35 M45 16 H48" stroke="#0b0e14" stroke-width="2"/>'),
  figure('researcher', 'Researcher', '#a371f7', '<path d="M25 12 H37 V26 H25 Z M39 12 H55 V26 H39 Z" fill="none" stroke="#0b0e14" stroke-width="2"/>'),
  figure('designer', 'Designer', '#db61a2', '<path d="M24 14 L36 26 L28 30 Z" fill="#0b0e14"/><circle cx="50" cy="16" r="5" fill="#0b0e14"/>'),
  figure('analyst', 'Analyst', '#2ea043', '<path d="M24 26 V14 M32 26 V18 M40 26 V10 M48 26 V20 M56 26 V16" stroke="#0b0e14" stroke-width="2"/>'),
  figure('operator', 'Operator', '#8b949e', '<path d="M24 14 Q40 2 56 14" fill="none" stroke="#0b0e14" stroke-width="3"/><rect x="22" y="14" width="7" height="10" rx="3" fill="#0b0e14"/><rect x="51" y="14" width="7" height="10" rx="3" fill="#0b0e14"/>'),
];

export function builtinFigure(id: string | undefined): BuiltinFigure | undefined {
  return FIGURES.find(f => f.id === id);
}

// sanitizeSvg keeps the figure inert: no scripts, event handlers, foreign
// objects, iframes or javascript: URLs.
export function sanitizeSvg(input: string): string {
  const svg = input.trim();
  if (!svg.toLowerCase().startsWith('<svg') || svg.length > 64 * 1024) return '';
  if (/<script/i.test(svg) || /<foreignObject/i.test(svg) || /<iframe/i.test(svg)) return '';
  if (/\son[a-z]+\s*=/i.test(svg) || /javascript:/i.test(svg)) return '';
  return svg;
}

export function figureSvg(avatar?: string, customSvg?: string): string {
  if (customSvg) {
    const clean = sanitizeSvg(customSvg);
    if (clean) return clean;
  }
  const builtin = builtinFigure(avatar);
  if (builtin) return builtin.svg;
  return FIGURES[2].svg;
}
