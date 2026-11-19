# Crystal Run — DOC-9

A standalone desktop-browser 3D platform game. Collect five crystals across floating islands, avoid the moving red hazard, and enter the portal. Falling returns you to spawn without losing crystals; the clock keeps running. WASD/arrows move, Space jumps, Escape pauses. Hidden tabs pause automatically.

## Run

Requires Node.js 22.12+ and npm. From this directory:

```sh
npm ci
npm run dev
npm run build
npm test
npx playwright install chromium
npm run test:browser
```

Open the URL printed by Vite. Deploy `dist/` to static hosting. WebGL is required. Best times are local to this browser; unavailable storage does not prevent play. See [German documentation](README.de.md).

## Design and stack

TypeScript strict mode, Vite, Three.js, HTML/CSS, LocalStorage, Vitest and Playwright. Original procedural geometry avoids external asset dependencies. The MVP deliberately uses simple floor/overlap collision checks rather than a physics engine. Desktop keyboard controls are supported; touch controls and multiplayer are out of scope.

Lightweight DDD: `Run` is the aggregate for a single attempt. Its vocabulary comprises run status, crystal identity, active elapsed time and portal eligibility. It rejects unknown/duplicate crystals, freezes time on pause and prevents premature/repeated completion. Level IDs define the required crystal set. Rendering and movement are outside the domain.

SOLID: the aggregate owns rules, `Player` owns movement, `World` owns graphics and `Keyboard` owns input. `Game` depends on the small `BestTimeStore` port; LocalStorage is an adapter. `InputSource` defines the movement contract for future devices. Implementations must preserve storage and input semantics. Composition occurs in `main.ts`; the domain imports no browser or Three.js code. No interface-per-class scaffolding is needed.

## Delivery plan and acceptance

1. Foundation: strict TypeScript, independent project, build and test commands.
2. Playable slice: islands, camera, jumping, falls and moving hazard.
3. Rules: five distinct crystals, locked portal, pause and completion.
4. UI: start, restart, resume, result and resilient best-time storage.
5. Verification: domain/storage/movement tests, browser smoke test and production build.
6. Follow-up: full-route browser automation, cross-browser playtesting, accessibility review and performance measurement on a named reference device.

Steps 1–4 are implemented. A clean `npm ci`, production build and six unit tests pass. Browser verification is described below. Step 6 remains follow-up work. A planning estimate for a polished MVP is 5–8 developer days; this implementation is the initial playable version.

Acceptance: all five crystals can be reached, the portal only completes an active fully collected run, pause excludes elapsed time, restarts clear progress, and storage errors do not block completion. A 60 FPS target at 1080p requires measurement; it is not a verified performance claim.

## Limits

Simple axis-aligned floor checks do not model general rigid-body physics. There is one level, one moving hazard, no audio, no mobile controls and no backend. The browser smoke test does not complete the entire route. Real-device playtesting remains necessary to tune difficulty and camera behavior.

## Dependency notes

Versions and the lockfile are pinned. `.npmrc` uses legacy peer resolution to work around npm 10 optional-peer resolution crashes; the selected Vite/Vitest versions are compatible. `npm audit` currently reports four high findings from the same transitive `picomatch <=4.0.7` advisory (GHSA-fc5q-39xc-vjpf). The registry currently offers no newer picomatch release. This concerns development tooling, not the static game runtime. Recheck before exposing development tooling to untrusted inputs. The build also reports a roughly 503 kB JavaScript chunk (131 kB gzip), primarily Three.js.
