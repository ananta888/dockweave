export interface BuiltinFigure {
  id: string;
  label: string;
  svg: string;
}

interface FigureStyle {
  id: string;
  label: string;
  bg1: string;
  bg2: string;
  skin1: string;
  skin2: string;
  hair: string;
  hairDark: string;
  outfit: string;
  outfitDark: string;
  accessory: string;
}

function makeFigure(s: FigureStyle): BuiltinFigure {
  const p = s.id;
  return {
    id: s.id,
    label: s.label,
    svg: `<svg viewBox="0 0 120 142" xmlns="http://www.w3.org/2000/svg">
  <defs>
    <linearGradient id="${p}-bg" x1="0" y1="0" x2="1" y2="1">
      <stop stop-color="${s.bg1}"/><stop offset="1" stop-color="${s.bg2}"/>
    </linearGradient>
    <linearGradient id="${p}-outfit" x1="0" y1="0" x2="0" y2="1">
      <stop stop-color="${s.outfit}"/><stop offset="1" stop-color="${s.outfitDark}"/>
    </linearGradient>
    <radialGradient id="${p}-skin" cx=".38" cy=".28" r=".85">
      <stop stop-color="${s.skin1}"/><stop offset="1" stop-color="${s.skin2}"/>
    </radialGradient>
    <linearGradient id="${p}-hair" x1="0" y1="0" x2="0" y2="1">
      <stop stop-color="${s.hair}"/><stop offset="1" stop-color="${s.hairDark}"/>
    </linearGradient>
    <filter id="${p}-shadow" x="-40%" y="-40%" width="180%" height="180%">
      <feDropShadow dx="0" dy="4" stdDeviation="3.5" flood-color="#000" flood-opacity=".38"/>
    </filter>
    <filter id="${p}-glow" x="-50%" y="-50%" width="200%" height="200%">
      <feGaussianBlur stdDeviation="2.2" result="b"/><feMerge><feMergeNode in="b"/><feMergeNode in="SourceGraphic"/></feMerge>
    </filter>
  </defs>
  <rect x="2" y="2" width="116" height="138" rx="24" fill="url(#${p}-bg)"/>
  <ellipse cx="60" cy="126" rx="31" ry="7" fill="#000" opacity=".28"/>
  <g filter="url(#${p}-shadow)">
    <rect x="45" y="103" width="11" height="21" rx="5.5" fill="${s.outfitDark}"/>
    <rect x="63" y="103" width="11" height="21" rx="5.5" fill="${s.outfitDark}"/>
    <ellipse cx="50" cy="124" rx="10" ry="4" fill="#1b1f24"/>
    <ellipse cx="70" cy="124" rx="10" ry="4" fill="#1b1f24"/>
    <path d="M30 74 Q60 56 90 74 L94 110 Q60 124 26 110 Z" fill="url(#${p}-outfit)"/>
    <path d="M30 74 Q45 66 60 65 L60 122 Q42 121 26 110 Z" fill="#fff" opacity=".10"/>
    <rect x="56" y="60" width="8" height="12" rx="4" fill="${s.skin2}"/>
    <circle cx="26" cy="88" r="9" fill="url(#${p}-skin)"/>
    <circle cx="94" cy="88" r="9" fill="url(#${p}-skin)"/>
    <circle cx="60" cy="45" r="26" fill="url(#${p}-skin)"/>
    <circle cx="35" cy="47" r="5" fill="${s.skin2}"/>
    <circle cx="85" cy="47" r="5" fill="${s.skin2}"/>
    <path d="M34 42 Q35 16 60 15 Q86 16 86 44 Q78 32 64 32 Q47 32 34 42 Z" fill="url(#${p}-hair)"/>
    <path d="M39 33 Q50 24 62 24 Q74 24 82 32 Q70 29 60 31 Q48 30 39 33 Z" fill="#fff" opacity=".18"/>
    <ellipse cx="51" cy="48" rx="3.1" ry="3.7" fill="#1b1f24"/>
    <ellipse cx="69" cy="48" rx="3.1" ry="3.7" fill="#1b1f24"/>
    <circle cx="52" cy="46.6" r="1.1" fill="#fff"/>
    <circle cx="70" cy="46.6" r="1.1" fill="#fff"/>
    <path d="M53 58 Q60 63.5 67 58" stroke="#7a3e1d" stroke-width="2.1" fill="none" stroke-linecap="round"/>
    <ellipse cx="42" cy="56" rx="5" ry="2.6" fill="#e86a7a" opacity=".38"/>
    <ellipse cx="78" cy="56" rx="5" ry="2.6" fill="#e86a7a" opacity=".38"/>
    ${s.accessory}
  </g>
</svg>`,
  };
}

export const FIGURES: BuiltinFigure[] = [
  makeFigure({
    id: 'ceo', label: 'CEO', bg1: '#3a2a08', bg2: '#17130a',
    skin1: '#ffd6ae', skin2: '#e3a374', hair: '#3b2a20', hairDark: '#1f1611',
    outfit: '#d8a62a', outfitDark: '#9b6c0a',
    accessory: `<path d="M39 19 L48 28 L60 15 L72 28 L81 19 L78 35 L42 35 Z" fill="#ffd34d" filter="url(#ceo-glow)"/><circle cx="60" cy="20" r="3" fill="#fff3b0"/><path d="M54 74 H66 L64 100 H56 Z" fill="#2c2317"/><path d="M60 77 L63 86 L60 95 L57 86 Z" fill="#f6d365"/>`,
  }),
  makeFigure({
    id: 'cto', label: 'CTO', bg1: '#0d2747', bg2: '#0a1524',
    skin1: '#f3c79e', skin2: '#d79a6b', hair: '#241b18', hairDark: '#120d0c',
    outfit: '#3f8cff', outfitDark: '#1c4fa8',
    accessory: `<circle cx="51" cy="48" r="7" fill="none" stroke="#0b0e14" stroke-width="2.2"/><circle cx="69" cy="48" r="7" fill="none" stroke="#0b0e14" stroke-width="2.2"/><path d="M58 48 H62" stroke="#0b0e14" stroke-width="2.2"/><path d="M46 88 H74 V110 H46 Z" fill="#0b0e14"/><path d="M49 91 H71 V104 H49 Z" fill="#39c5cf"/><path d="M53 107 H67" stroke="#8b949e" stroke-width="2"/>`,
  }),
  makeFigure({
    id: 'developer', label: 'Developer', bg1: '#0e3320', bg2: '#0a1710',
    skin1: '#eab98f', skin2: '#c9895c', hair: '#2b1d16', hairDark: '#150e0a',
    outfit: '#3fb950', outfitDark: '#1e6b2b',
    accessory: `<path d="M31 40 Q32 12 60 12 Q88 12 89 40 Q76 28 60 28 Q44 28 31 40 Z" fill="#244a2d"/><path d="M35 40 Q40 50 60 50 Q80 50 85 40" fill="none" stroke="#244a2d" stroke-width="5"/><rect x="42" y="84" width="36" height="22" rx="4" fill="#0b0e14"/><text x="60" y="99" font-family="ui-monospace, monospace" font-size="11" fill="#3fb950" text-anchor="middle">&lt;/&gt;</text>`,
  }),
  makeFigure({
    id: 'reviewer', label: 'Reviewer', bg1: '#2a1740', bg2: '#150b22',
    skin1: '#f4c9a2', skin2: '#d99a6a', hair: '#3a2432', hairDark: '#1d1119',
    outfit: '#bc8cff', outfitDark: '#7b47c9',
    accessory: `<circle cx="86" cy="78" r="13" fill="#d9e6ff" opacity=".25" stroke="#c9d1d9" stroke-width="3"/><path d="M96 88 L108 102" stroke="#c9d1d9" stroke-width="5" stroke-linecap="round"/><path d="M40 48 H62" stroke="#0b0e14" stroke-width="2" opacity=".25"/>`,
  }),
  makeFigure({
    id: 'qa', label: 'QA', bg1: '#083239', bg2: '#06181b',
    skin1: '#ffd8b2', skin2: '#e3a476', hair: '#46301f', hairDark: '#24170e',
    outfit: '#39c5cf', outfitDark: '#13747b',
    accessory: `<rect x="76" y="70" width="26" height="34" rx="4" fill="#f2f6fa"/><rect x="82" y="76" width="14" height="3" rx="1.5" fill="#8b949e"/><path d="M82 86 L86 90 L93 81" stroke="#238636" stroke-width="3" fill="none" stroke-linecap="round"/><path d="M82 96 H96" stroke="#8b949e" stroke-width="2"/>`,
  }),
  makeFigure({
    id: 'devops', label: 'DevOps', bg1: '#3a2208', bg2: '#1a0f05',
    skin1: '#e8b98d', skin2: '#c78a5b', hair: '#2c211b', hairDark: '#140e0b',
    outfit: '#f0883e', outfitDark: '#a94d0a',
    accessory: `<path d="M30 44 Q31 13 60 13 Q89 13 90 44" fill="none" stroke="#c9d1d9" stroke-width="4"/><rect x="25" y="40" width="10" height="17" rx="5" fill="#0b0e14"/><rect x="85" y="40" width="10" height="17" rx="5" fill="#0b0e14"/><circle cx="60" cy="102" r="10" fill="none" stroke="#c9d1d9" stroke-width="2.5"/><path d="M60 88 V92 M60 112 V116 M46 102 H50 M70 102 H74 M50 92 L53 95 M70 92 L67 95 M50 112 L53 109 M70 112 L67 109" stroke="#c9d1d9" stroke-width="2"/>`,
  }),
  makeFigure({
    id: 'researcher', label: 'Researcher', bg1: '#1d1a40', bg2: '#0f0d22',
    skin1: '#f0c7a4', skin2: '#d19970', hair: '#4a3626', hairDark: '#261a11',
    outfit: '#a371f7', outfitDark: '#6c3fc4',
    accessory: `<path d="M33 74 Q60 60 87 74 L90 110 Q60 121 30 110 Z" fill="#f5f7fa" opacity=".92"/><path d="M54 80 V96 L44 114 H76 L66 96 V80 Z" fill="#d9e6ff" opacity=".8" stroke="#8b949e" stroke-width="1.5"/><path d="M47 106 H73" stroke="#39c5cf" stroke-width="4"/>`,
  }),
  makeFigure({
    id: 'designer', label: 'Designer', bg1: '#3a1028', bg2: '#1c0813',
    skin1: '#f7cfae', skin2: '#dda279', hair: '#513025', hairDark: '#2a1710',
    outfit: '#db61a2', outfitDark: '#a12e6e',
    accessory: `<path d="M34 31 Q40 13 60 13 Q80 13 86 31 Q70 25 60 25 Q50 25 34 31 Z" fill="#2d1b23"/><circle cx="60" cy="17" r="4" fill="#db61a2"/><path d="M42 92 A10 10 0 1 0 62 92 A10 10 0 1 0 42 92" fill="#f2f6fa"/><circle cx="47" cy="89" r="2" fill="#db61a2"/><circle cx="54" cy="85" r="2" fill="#39c5cf"/><circle cx="58" cy="94" r="2" fill="#d29922"/><path d="M80 82 L92 106" stroke="#c9d1d9" stroke-width="4" stroke-linecap="round"/><path d="M77 80 L84 77 L86 84 Z" fill="#bc8cff"/>`,
  }),
  makeFigure({
    id: 'analyst', label: 'Analyst', bg1: '#08301c', bg2: '#06170d',
    skin1: '#e8bb92', skin2: '#c78d61', hair: '#2b241f', hairDark: '#14110e',
    outfit: '#2ea043', outfitDark: '#14632a',
    accessory: `<rect x="76" y="68" width="28" height="34" rx="4" fill="#0b0e14"/><path d="M82 94 V84 M88 94 V78 M94 94 V88 M100 94 V74" stroke="#3fb950" stroke-width="3" stroke-linecap="round"/>`,
  }),
  makeFigure({
    id: 'operator', label: 'Operator', bg1: '#2c2c36', bg2: '#15151c',
    skin1: '#f2c9a4', skin2: '#d69d70', hair: '#3a2a20', hairDark: '#1d150f',
    outfit: '#8b949e', outfitDark: '#4f5862',
    accessory: `<path d="M30 46 Q30 12 60 12 Q90 12 90 46" fill="none" stroke="#c9d1d9" stroke-width="4"/><rect x="24" y="42" width="11" height="18" rx="5" fill="#0b0e14"/><rect x="85" y="42" width="11" height="18" rx="5" fill="#0b0e14"/><path d="M87 55 Q96 64 90 71" fill="none" stroke="#c9d1d9" stroke-width="2"/><circle cx="89" cy="73" r="3" fill="#c9d1d9"/><rect x="42" y="86" width="36" height="20" rx="4" fill="#0b0e14"/><path d="M46 92 H74 M46 98 H68" stroke="#39c5cf" stroke-width="2"/>`,
  }),
  makeFigure({
    id: 'product-manager', label: 'Product', bg1: '#123a36', bg2: '#071c1a',
    skin1: '#f6cda6', skin2: '#d99d70', hair: '#33241c', hairDark: '#180f0a',
    outfit: '#2ec4b6', outfitDark: '#12766d',
    accessory: `<rect x="76" y="70" width="28" height="34" rx="4" fill="#f2f6fa"/><path d="M82 78 H98 M82 85 H96 M82 92 H94" stroke="#8b949e" stroke-width="2"/><path d="M84 98 L88 102 L96 93" stroke="#238636" stroke-width="3" fill="none" stroke-linecap="round"/>`,
  }),
  makeFigure({
    id: 'architect', label: 'Architect', bg1: '#152a44', bg2: '#08111e',
    skin1: '#ecc39a', skin2: '#cf9468', hair: '#2a211c', hairDark: '#140f0c',
    outfit: '#4c8dff', outfitDark: '#2350a8',
    accessory: `<path d="M42 78 L60 72 L78 78 L78 108 L60 112 L42 108 Z" fill="#dbeafe" opacity=".9"/><path d="M48 88 H72 M48 94 H68 M48 100 H74" stroke="#1f6feb" stroke-width="2"/><circle cx="52" cy="84" r="3" fill="#1f6feb"/>`,
  }),
  makeFigure({
    id: 'security', label: 'Security', bg1: '#3a1014', bg2: '#1a0709',
    skin1: '#f0c7a0', skin2: '#d1986a', hair: '#2b1d18', hairDark: '#150d0a',
    outfit: '#f85149', outfitDark: '#a51d1d',
    accessory: `<path d="M60 12 L78 20 L75 40 Q72 55 60 62 Q48 55 45 40 L42 20 Z" fill="#f2f6fa" opacity=".95" stroke="#8b949e" stroke-width="2"/><path d="M54 34 L59 39 L68 28" stroke="#238636" stroke-width="4" fill="none" stroke-linecap="round"/>`,
  }),
  makeFigure({
    id: 'data-engineer', label: 'Data', bg1: '#102f22', bg2: '#071a12',
    skin1: '#eec49c', skin2: '#d09467', hair: '#3a2a1f', hairDark: '#1b120c',
    outfit: '#2ea043', outfitDark: '#14632a',
    accessory: `<ellipse cx="60" cy="84" rx="16" ry="6" fill="#dbeafe"/><path d="M44 84 V104 A16 6 0 0 0 76 104 V84" fill="none" stroke="#dbeafe" stroke-width="3"/><path d="M44 94 A16 6 0 0 0 76 94" fill="none" stroke="#dbeafe" stroke-width="2"/>`,
  }),
  makeFigure({
    id: 'support', label: 'Support', bg1: '#3a1024', bg2: '#1a0710',
    skin1: '#f6cca8', skin2: '#dba077', hair: '#3b2920', hairDark: '#1c130e',
    outfit: '#db61a2', outfitDark: '#a12e6e',
    accessory: `<path d="M30 46 Q30 12 60 12 Q90 12 90 46" fill="none" stroke="#c9d1d9" stroke-width="4"/><rect x="24" y="42" width="11" height="18" rx="5" fill="#0b0e14"/><rect x="85" y="42" width="11" height="18" rx="5" fill="#0b0e14"/><path d="M86 56 Q96 64 90 72" fill="none" stroke="#c9d1d9" stroke-width="2"/><circle cx="89" cy="74" r="3" fill="#c9d1d9"/><path d="M76 78 H102 V94 H88 L82 100 V94 H76 Z" fill="#dbeafe" opacity=".9"/>`,
  }),
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
