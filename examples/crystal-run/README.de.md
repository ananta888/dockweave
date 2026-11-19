# Crystal Run — DOC-9

Eigenständiges kleines Browser-3D-Spiel: Sammle fünf Kristalle auf schwebenden Inseln, weiche der roten Gefahr aus und erreiche das Portal. Ein Absturz setzt dich ohne Kristallverlust an den Anfang zurück; die Uhr läuft weiter. WASD/Pfeiltasten bewegen, Leertaste springt, Escape pausiert. Inaktive Tabs pausieren automatisch.

## Start und Prüfung

Voraussetzung: Node.js 22.12+ und npm. In diesem Verzeichnis:

```sh
npm ci
npm run dev
npm run build
npm test
npx playwright install chromium
npm run test:browser
```

Die von Vite ausgegebene URL öffnen. Für statisches Hosting den Inhalt von `dist/` veröffentlichen. WebGL ist erforderlich. Bestzeiten gelten lokal für diesen Browser. Gesperrter Speicher verhindert das Spielen nicht. [English documentation](README.md).

## Entwurf und Tech-Stack

TypeScript im Strict Mode, Vite, Three.js, HTML/CSS, LocalStorage, Vitest und Playwright. Prozedurale Formen benötigen keine externen Grafikassets. Einfache Boden- und Überlappungsprüfungen genügen für diese erste Version. Zielplattform sind Desktop-Browser mit Tastatur; Touch und Multiplayer sind nicht enthalten.

Leichtgewichtiges DDD: `Run` ist die Aggregatwurzel einer Runde. Fachbegriffe sind Rundenstatus, Kristallidentität, aktive Spielzeit und Portal-Freischaltung. Das Modell verhindert unbekannte oder doppelte Sammlungen und vorzeitige oder wiederholte Abschlüsse. Die Leveldefinition legt die benötigten Kristalle fest. Bewegung und Grafik liegen außerhalb der Domain.

SOLID: `Run` besitzt die Regeln, `Player` die Bewegung, `World` die Grafik und `Keyboard` die Eingabe. `Game` hängt vom kleinen Vertrag `BestTimeStore` ab; LocalStorage implementiert ihn. `InputSource` beschreibt die Eingabe für spätere Geräte. Austauschbare Adapter müssen dieselben Verträge erfüllen. `main.ts` verbindet die Komponenten. Die Domain importiert weder Browser-APIs noch Three.js. Interfaces werden nur an sinnvollen Grenzen eingeführt.

## Plan und Abnahme

1. Grundlage: eigenes Projekt, strikte Typprüfung, Build und Tests.
2. Spielbarer Ausschnitt: Inseln, Kamera, Sprünge, Abstürze und bewegliche Gefahr.
3. Regeln: fünf unterschiedliche Kristalle, Portal, Pause und Abschluss.
4. Oberfläche: Start, Neustart, Fortsetzen, Ergebnis und fehlertolerante Bestzeit.
5. Prüfung: Domain-, Speicher- und Bewegungstests, Browser-Smoke-Test und Produktionsbuild.
6. Nacharbeiten: vollständige Route im Browser automatisieren, weitere Browser und echte Geräte testen, Zugänglichkeit prüfen und Performance messen.

Schritte 1–4 sind implementiert. Ein sauberes `npm ci`, der Produktionsbuild und sechs Unit-Tests bestehen. Die Browserprüfung wird unten dokumentiert. Schritt 6 bleibt offen. Für ein ausgearbeitetes MVP sind grob fünf bis acht Entwicklungstage anzusetzen; dies ist die erste spielbare Umsetzung.

Abnahme: Alle Kristalle sind erreichbar; das Portal schließt ausschließlich aktive Runden mit vollständiger Sammlung ab. Pausen zählen nicht zur Spielzeit, Neustarts löschen den Fortschritt und Speicherfehler verhindern den Abschluss nicht. 60 FPS bei 1080p sind ein noch zu messendes Ziel.

## Grenzen

Die einfachen Bodenprüfungen ersetzen keine allgemeine Physik-Engine. Enthalten sind ein Level und eine bewegliche Gefahr; Audio, mobile Steuerung und Backend fehlen. Der Browser-Smoke-Test spielt noch nicht die gesamte Route. Schwierigkeit und Kameraverhalten benötigen weitere Spieltests auf echten Geräten.

## Abhängigkeiten

Versionen und Lockfile sind festgeschrieben. `.npmrc` verwendet Legacy-Peer-Auflösung als Umgehung eines npm-10-Absturzes bei optionalen Peers; die gewählten Vite-/Vitest-Versionen sind kompatibel. `npm audit` meldet derzeit vier hohe Befunde aus derselben transitiven `picomatch <=4.0.7`-Schwachstelle (GHSA-fc5q-39xc-vjpf). Die Registry bietet aktuell keine neuere picomatch-Version. Betroffen sind Entwicklungswerkzeuge, nicht die statische Spiellaufzeit. Vor Nutzung mit nicht vertrauenswürdigen Eingaben erneut prüfen. Der Build weist außerdem auf ein JavaScript-Bündel von etwa 503 kB (131 kB gzip) hin, hauptsächlich Three.js.
