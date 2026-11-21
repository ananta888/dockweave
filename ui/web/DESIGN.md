# DockWeave graph interface

## 1. Atmosphere and identity
Preserve the existing dense dark operator console and graph canvas. This change adds reliable save and synchronization feedback, not a redesign.

## 2. Color
Existing component palette: canvas #0b0e14, toolbar/surface #161b22, neutral borders #30363d, secondary text #8b949e and focus/action blue #58a6ff. Reuse existing notice, error, muted and button classes; do not add a new palette.

## 3. Typography
Inherit the application's system sans-serif and existing .mono code treatment. Retain existing toolbar and form text sizes. Status text must remain readable and wrap rather than truncate essential errors.

## 4. Spacing and layout
Retain the existing toolbar, canvas and scrollable editor panels. Notices form a vertical stack above the canvas. Actions reuse nearby button spacing and remain keyboard accessible. Canvas owns its pan/zoom; notices must not become an overlay obscuring controls.

## 5. Components
Reuse button, notice/error, editor form, pipeline badge and proceed edge label. Save notice states: loading, pending, saving, saved, conflict, failed. Conflict offers explicit server reload and local export; it never silently overwrites either draft. Sync retry is disabled while graph changes are pending. Status includes text, not color alone. Existing pipeline browser fixtures are the state harness.

## 6. Motion and interaction
No new animation. Preserve existing focus-visible treatment. Async feedback remains visible until confirmed; retry is explicit after conflict/error. A delayed response may not replace a newer draft or validation state.

## 7. Depth and surface
Reuse current neutral borders and tonal surfaces. Notices use existing classes, without introducing decorative accent stripes or new shadows/radii.

## 8. Accessibility constraints and existing limitations
New errors use role=alert, ordinary progress uses role=status, and every action is a native labeled button. Keyboard navigation and narrow viewport wrapping are checked with the browser fixtures. Existing graph canvas density and dark-only palette are retained; this work does not claim a general accessibility audit of unrelated graph controls.

The graph component contains its canvas, editors and controls in one existing component. Its production CSS is 14.32 kB (uncompressed). The component warning budget is 16 kB with the existing hard 20 kB limit retained; the whole initial bundle still has a separate 1.5 MB warning / 2 MB error budget. This bounded allowance avoids moving component CSS into global scope solely to bypass accounting. Future increases require a size comparison; this change does not claim a CSS reduction.
